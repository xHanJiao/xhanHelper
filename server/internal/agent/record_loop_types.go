package agent

import (
	"xhan-helper/internal/agent/promptbuilder"
	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// 本文件实现「两段式」里的第二段：逐候选的**决策循环**。
// 抽取段（ExtractCandidates）产出 N 条候选，每条候选在这里跑一遍 ReAct 循环，
// 循环里模型可追加检索、并选一个写入工具（save / update / delete / delete_note）。
// 骨架照两段式记忆的决策段（把单发决定换成 MiniReAct）。

const (
	recordToolSearch = "record_search"
	recordToolMemory = "memory"
	recordToolLegacy = "legacy_note"

	recordLoopCaller = "record_loop"
)

// recordLoopInstruction — 决策循环的指令正文。
// 设计定稿见 docs/design/memory-note-rework/record-loop-prompt.md §2，该文件里的这段是**逐字镜像**，
// 由 TestRecordLoopInstructionMirrorsDoc 强制一致（改这里必须同步那份 md）。
// 放 system prompt 而非 user 消息：它是稳定常量，构成可命中的缓存前缀；变化部分
// （<candidate> / <neighbors> / <topic_records>）才进 user 消息。
const recordLoopInstruction = `你要为这条候选选一个动作：新开一条、改一条、删一条，或者什么都不做。

## 输入

<candidate>：待写入的候选，字段 topic / summary / facts。
<neighbors>：按 summary 向量相似度从记忆库取回的记忆与旧笔记近邻；相似度低于 memory_neighbor_threshold（默认 0.5）的不入此块，可能一条都不相关。
<topic_records>：该候选 topic 下已有的**记忆**（按时间倒序，最多 TopicNeighborLimit 条），属性 count 是该主题下的记忆条数（旧笔记不计入——它们只可能出现在 <neighbors> 里）。
每个记录写成 <record id="…" type="memory|note" topic="…">正文</record>，id 是它的唯一标识。
type=note 的是旧笔记（过渡期遗留，见下）。

## 记忆的构成

一条记忆 = topic + summary + facts：
- topic：一个主题词（≤5 字）。同一件事必须用同一个 topic，不要造近义词。
- summary：一句话概括核心信息，自足成句（主体写进句子里），≤150 字。
  只有 summary 会被检索到，所以它必须自足——不能靠 facts 才读得懂。
- facts：支持这条 summary 的具体细节，0~10 条、每条 ≤100 字。它跟随 summary 一起返回，不单独检索。

## 动作

- save_memory：新开一条。库里没有这件事。
- update_memory：近邻里有一条讲的是同一件事，而它变了、更全了、或需要修正 → 覆盖它。target_id 取自输入的 id。
  summary 写改写后的完整内容；facts 只写新增的细节——会与已有 facts 自动去重合并（只增不减、不逐出）。
- delete_memory：近邻里有一条被明确否定或已经作废。
- 什么都不调：近邻里已有一条同义记录、内容没变。**这是合法且常见的终局**——不要为了"有输出"而硬写一条。

## 检索工具（只读）

<neighbors> 与 <topic_records> 可能漏掉你要找的那条。关键词检索擅长字面量（人名、专有名词、日期、金额），相似度搜索恰恰不擅长这些。需要时补一轮；够了就别反复搜。

## 判据

同一件事 = 同一主体 ＋ 同一件事本身。
事实性内容变了（改期、变更计划、数据修正）→ update_memory；只是补充细节 → update_memory；多了一件无关的新事 → save_memory。
观点/看法变了 → 新增一条，不要覆盖：旧观点是当时的真实记录，也有价值。这里的「观点」指**立场、偏好、判断**这类因人而变的东西；同一个客观事实的版本变化（行程改了、数字更正了、计划更新了）不是观点，一律走 update_memory，不要当成"新的一件观点"存下来。
候选与某个近邻只是"同一个大主题"（都属某次旅行、某个项目），但讲的是不同的点，就不是同一件事。
<topic_records> 的用途是让你看见这个主题下已经有多少条：若候选讲的是已有某条的版本变化，用 update_memory 覆盖那条，**不要新增**——同一个主题下挂一堆只差一点的记录，用户看到的就是重复。

## delete 是最后手段

删除最重、不可逆，只有用户明确推翻时才用：
- 用：用户明确否定或作废（「那笔债不用还了」否定「计划还债」）。
- 不用：「新说法更全面」「措辞不一样」「看起来过时」「我想换个写法」——这些一律走 update_memory。
- 判不准就不动，或走 update_memory。宁可留着重复，也不要误删。

## 旧笔记（过渡期）

库里还留着一批旧笔记（type=note），是历史遗留，正在逐步清理：
- 不要新增或修改旧笔记。
- 如果发现某条旧笔记与候选讲的是同一件事，删掉那条旧笔记（delete_note）——内容已由记忆承接。
- 拿不准就留着，不要为了清理而删。

## 硬限制

- topic ≤ 5 字；summary ≤ 150 字；单条 fact ≤ 100 字。单次传入 facts ≤ 10 条（update 合并后不设上限）。超限会被工具拒绝。
- 一条候选最多新增 1 条记忆。上限由上游的抽取段控制，你不要试图拆分候选绕过它。

## 输出

- 用 tool call 表达，不要输出 JSON 文本。
- target_id 只能取自输入里的 id（<neighbors> 或 <topic_records>），不许自造。
- update_memory 的 summary 必须写改写后的完整内容（自足成句、主体写进句子），不是只写变化的那半句。
- 相对时间（今天、昨天）在候选里若还是相对说法，落库前转成绝对日期（如 2026-09-15）。`

// recordLoopSystemPrompt — 决策循环 system prompt。拼接 promptbuilder.MemoryDefinition，
// 避免「记忆是什么」在链路上出现第二份定义（见 promptbuilder/memory_definition.go）；
// 指令正文见 recordLoopInstruction（必须进 prompt 才能让模型知道「什么都不调」是合法终局）。
var recordLoopSystemPrompt = "你是记忆库的写入决策助手。\n\n" + promptbuilder.MemoryDefinition +
	"\n\n" + recordLoopInstruction

// recordLoopFinalRoundPrompt — 强制收敛轮指令。默认那条要「最终结果」，这里没有最终结果要给谁看。
const recordLoopFinalRoundPrompt = `[系统] 本轮已达最大步数。请不要再调用任何工具。`

// recordNeighbor — 进入近邻集合的一条记录（id 已剥前缀）。
// 三条来源：预检索、循环内追加检索、同 topic 已有记录（sameTopicRecords）。
// 它们共同构成 target_id 的合法取值域。
type recordNeighbor struct {
	ID      string
	Type    string // memory | note
	Topic   string
	Content string
	Score   float64
}

// RecordDeps — 决策循环的全部依赖。必需依赖不做 nil 检查：为 nil 是接线 bug，应炸在明处。
type RecordDeps struct {
	FlashLLM      llm.LLMClient
	LLMBG         llm.LLMClient // 后台入口用；nil 回退 FlashLLM
	MemRepo       *service.UserMemoryRepo
	NoteRepo      *service.NoteRepo
	VectorStore   *VectorStore
	Embedder      app.Embedder
	EmbeddingRepo *service.EmbeddingRepo
	ChunkRepo     *service.EmbeddingChunkRepo
	ChunkBuilder  *ChunkBuilder
	ConceptEmbed  *ConceptEmbedService
	EntityPurger  *service.EntityPurger

	// 语义检索源（泛型参数不同，无法放进同一个字段，故各留一个）。
	MemSource  *retrieval.EntitySource[model.UserMemory]
	NoteSource *retrieval.EntitySource[model.Note]
}

// RecordLoopConfig — 决策循环的可调参数（来自 server 配置段）。
type RecordLoopConfig struct {
	MaxIterations    int
	NeighborTopK     int
	NeighborThresh   float64
	ConfirmThresh    float64
	TopicReuseThresh float64
	// TopicNeighborLimit — 决策时一并喂给模型的「同 topic 已有记录」条数上限。
	// 不进 yaml：它只影响 prompt 视野，不是需要运维调的旋钮（见设计文档 §3.5）。
	TopicNeighborLimit int
}

// RecordLoopConfigFromServer 从 server 配置段构造循环参数。
func RecordLoopConfigFromServer(cfg *config.Config) RecordLoopConfig {
	return RecordLoopConfig{
		MaxIterations:    cfg.Server.RecordLoopMaxIterations,
		NeighborTopK:     cfg.Server.MemoryNeighborTopK,
		NeighborThresh:   cfg.Server.MemoryNeighborThreshold,
		ConfirmThresh:    cfg.Server.MemoryConfirmThreshold,
		TopicReuseThresh: cfg.Server.MemoryTopicReuseThreshold,
	}
}

// recordCandidate — 一条待决策的候选（抽取段产出 / inline 入口传入）。
type recordCandidate struct {
	Summary string
	Facts   []string
	Topic   string
}

// recordLoopResult — 一条候选的决策结果，供入口记日志/透传。
type recordLoopResult struct {
	Action string // add | update | delete | confirm | none
	ID     string
	Source string
}

// RecordDecisionLoop — 逐候选决策循环。
type RecordDecisionLoop struct {
	deps   *RecordDeps
	cfg    RecordLoopConfig
	source string // inline | post_chat，落库用
}

// NewRecordDecisionLoop 创建决策循环。
func NewRecordDecisionLoop(deps *RecordDeps, cfg RecordLoopConfig, source string) *RecordDecisionLoop {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 8 // 与 config.yaml / config 默认值对齐（record_loop_max_iterations）
	}
	if cfg.NeighborTopK <= 0 {
		cfg.NeighborTopK = 5
	}
	if cfg.TopicNeighborLimit <= 0 {
		cfg.TopicNeighborLimit = 8 // 同 topic 记录只看最近 8 条；count 属性给总数
	}
	return &RecordDecisionLoop{deps: deps, cfg: cfg, source: source}
}
