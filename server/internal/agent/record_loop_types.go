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
// 骨架照 facts 的 FactWorker（fact_worker.go:92/123/170/483），把单发决定换成 MiniReAct。

const (
	recordToolSearch = "record_search"
	recordToolMemory = "memory"
	recordToolLegacy = "legacy_note"

	recordLoopCaller = "record_loop"
)

// recordLoopSystemPrompt — 决策循环 system prompt。拼接 promptbuilder.MemoryDefinition，
// 避免「记忆是什么」在链路上出现第二份定义（见 promptbuilder/memory_definition.go）。
var recordLoopSystemPrompt = "你是记忆库的写入决策助手。\n\n" + promptbuilder.MemoryDefinition +
	"\n\n你的输入是一条候选记忆，和它在记忆库里检索到的近邻记录。"

// recordLoopFinalRoundPrompt — 强制收敛轮指令。默认那条要「最终结果」，这里没有最终结果要给谁看。
const recordLoopFinalRoundPrompt = `[系统] 本轮已达最大步数。请不要再调用任何工具。`

// recordNeighbor — 预检索/追加检索得到的一条近邻（id 已剥前缀）。
// 仅这两条来源会进近邻集合，也就是 target_id 的合法取值域。
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
	return &RecordDecisionLoop{deps: deps, cfg: cfg, source: source}
}
