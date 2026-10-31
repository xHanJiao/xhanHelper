package agent

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"strings"
	"time"

	"xhan-helper/internal/agent/promptbuilder"
	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// memoryCandidate — 抽取段产出的一条候选记忆。不落库，交给决策循环处理。
// 无 Topic 字段：topic 由决策循环前的 topic_namer 单独一次 LLM 调用定稿（见 topic_namer.go）。
type memoryCandidate struct {
	Summary string `json:"summary"`
	// Facts — 用户原话的逐字片段（可整句截断，不得改写/拼接）。由 ExtractCandidates
	// 用用户消息原文硬校验，非逐字的片段当场丢弃（见 verifyVerbatimFacts）。
	Facts []string `json:"facts,omitempty"`
	// Reason — 为什么它一个月后还有用（抽取段自问自答）。说不出理由的候选直接在
	// ExtractCandidates 里丢掉：过程性噪声的共性就是「说不出留着干嘛」（见设计文档 §3.4）。
	Reason string `json:"reason,omitempty"`
}

// memoryExtractResult — 抽取段 LLM 的输出结构（一个键 memories，数组）。
// 一次输出多条，逐条过决策循环；不再退化成「一句话 + 一串细节」。
type memoryExtractResult struct {
	Extracted bool              `json:"extracted"`
	Memories  []memoryCandidate `json:"memories"`
}

// memoryInstructionXML — 记忆提取指令消息的 XML 结构。
// 固定部分（task/rules/output_format）在文档前部，构成同 worker 内可命中的缓存前缀；
// 变化部分 current_time 放文档尾部，miss 的只有 tail。
type memoryInstructionXML struct {
	XMLName      struct{} `xml:"memory_extraction"`
	Task         string   `xml:"task"`
	Rules        []string `xml:"rules>rule"`
	OutputFormat string   `xml:"output_format"`
	CurrentTime  string   `xml:"current_time,omitempty"`
}

// buildMemoryInstructionXML 构建记忆提取 worker 的指令 user 消息。
// task 里的「什么算记忆」用 promptbuilder.MemoryDefinition（全链路唯一定义），
// 抽取段只负责「从对话里按这个定义捞候选」，不再另写一份定义。
func buildMemoryInstructionXML(now string, maxCandidates int) string {
	doc := memoryInstructionXML{
		Task: fmt.Sprintf("从以下对话中找出将来聊天还会用到的长期信息，最多 %d 条。这是**查漏**，不是整理纪要：宁缺勿滥，宁可少提也不要凑数。\n\n记忆的定义：\n%s\n\n对话里可能有一部分内容此前已经记过——重复的不要提。不编造、不推测；没有值得留的就一条都不提。", maxCandidates, promptbuilder.MemoryDefinition),
		Rules: []string{
			"只依据用户的消息。助手说的不构成记忆来源，除非用户确认",
			fmt.Sprintf("facts 必须是用户消息里的**逐字原文片段**（最多 %d 条）：可整句截断，不得改写、不得拼接两句、不得补字；助手说的话不算。不逐字的片段会被系统丢弃", memoryFactsMaxCount),
			"只提定义之内的长期信息：本轮任务怎么推进（产出形式/顺序/临时口径）、已写进文档的内容、单笔账目的录入与归类细节，都不提",
			"每条必须给出 reason：为什么它一个月后还有用（一句话）。说不出用途的会被丢弃",
			"合并同类信息，不要逐条复述。不编造、不推测",
			"没有值得提取的内容时返回 {\"extracted\":false}",
			"将\"今天\"、\"昨天\"等相对时间转为绝对日期（如 2026-05-15）——**只作用于 summary**；facts 是原话，一个字都不许改",
		},
		OutputFormat: `{"extracted":true,"memories":[{"summary":"一句话概括核心信息","facts":["用户消息里的原话片段（逐字照抄）"],"reason":"为什么一个月后还有用"}]}
一条记忆 = 一条独立的、自足的事件或结论，各占 memories 数组的一项。没有值得提取的内容时返回 {"extracted":false}
summary 是你自己的概括；facts 不是概括，是用户的**原话**——两者分工不要混。
reason 为空或说不出用途的候选会被丢弃；facts 里不逐字的片段会被系统丢弃（facts 允许为空）。不要输出 topic——主题词由后续步骤单独归类，你只负责把候选内容提出来。`,
		CurrentTime: now,
	}

	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		log.Printf("[memory] encode instruction XML error: %v", err)
		return ""
	}
	return buf.String()
}

// MemoryExtractor — 从对话中提取记忆，向量去重。
// 由 MemoryWorker（PostChatWorker）调用；输入为 session 快照中的 user 消息。
type MemoryExtractor struct {
	llmClient     llm.LLMClient
	memRepo       *service.UserMemoryRepo
	vectorStore   app.VectorStore
	embedder      app.Embedder
	embeddingRepo *service.EmbeddingRepo
	dupeThreshold float64
	maxCandidates int
	chunkBuilder  *ChunkBuilder
	conceptEmbed  *ConceptEmbedService
}

// MemoryExtractorConfig — 记忆提取器依赖配置
type MemoryExtractorConfig struct {
	LLMClient     llm.LLMClient
	MemRepo       *service.UserMemoryRepo
	VectorStore   app.VectorStore
	Embedder      app.Embedder
	EmbeddingRepo *service.EmbeddingRepo
	DupeThreshold float64
	MaxCandidates int // 单次会话最多新增几条记忆（抽取段上限）；0 → 默认 5
	ChunkBuilder  *ChunkBuilder
	ConceptEmbed  *ConceptEmbedService
}

// NewMemoryExtractor 创建提取器
func NewMemoryExtractor(cfg *MemoryExtractorConfig) *MemoryExtractor {
	if cfg.DupeThreshold <= 0 {
		cfg.DupeThreshold = 0.85
	}
	if cfg.MaxCandidates <= 0 {
		cfg.MaxCandidates = 2
	}
	return &MemoryExtractor{
		llmClient:     cfg.LLMClient,
		memRepo:       cfg.MemRepo,
		vectorStore:   cfg.VectorStore,
		embedder:      cfg.Embedder,
		embeddingRepo: cfg.EmbeddingRepo,
		dupeThreshold: cfg.DupeThreshold,
		maxCandidates: cfg.MaxCandidates,
		chunkBuilder:  cfg.ChunkBuilder,
		conceptEmbed:  cfg.ConceptEmbed,
	}
}

// ExtractCandidates 从给定会话消息中抽取候选记忆。
// 只做抽取，不做去重、不落库、不嵌入——那些是决策循环的职责（它能看到近邻，判定才准）。
// 不做游标/进度更新（session-window 驱动，天然增量，无 LastExtractedMsgID 概念）。
// 上限 e.maxCandidates 条（prompt 明示 + 解析后截断，来自 config memory_max_per_session）。
// 函数本身不限制 role；编排层（MemoryWorker）现在只喂 user 消息——assistant 的结论是幻觉主源。
// 形状对齐 fact_extractor：对话渲染成 <对话> 块，拼在指令后，作为单条 user 消息发出。
func (e *MemoryExtractor) ExtractCandidates(ctx context.Context, userID string, msgs []model.Message) ([]memoryCandidate, error) {
	now := time.Now().In(config.BJ).Format("2006-01-02 15:04:05 Monday")
	prompt := buildMemoryInstructionXML(now, e.maxCandidates)

	dialog, err := renderDialog(msgs)
	if err != nil {
		return nil, fmt.Errorf("render memory dialog: %w", err)
	}
	if dialog != "" {
		prompt = prompt + "\n\n" + dialog
	}

	msg, err := e.llmClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: postChatDialogSystemPrompt,
		Messages:     []llm.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return nil, fmt.Errorf("extract memory candidates: %w", err)
	}

	result, err := parseExtractResult(msg.Content)
	if err != nil {
		return nil, err
	}
	if !result.Extracted {
		// 必须记一条：否则「模型判定无可记」与「抽取压根没跑」在日志里长得一样，
		// 想区分只能去翻 llm_call_ios 的审计（本次排查就吃了这个亏）。
		log.Printf("[memory] extracted: 模型判定无可记内容（extracted=false）")
		return nil, nil
	}

	// facts 的校验基准是**用户消息原文**——助手说的话不算（见 memory_facts.go）。
	userContents := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "user" {
			userContents = append(userContents, m.Content)
		}
	}

	cands := make([]memoryCandidate, 0, len(result.Memories))
	droppedNoReason, droppedEmpty, droppedFacts, truncatedFacts := 0, 0, 0, 0
	for _, c := range result.Memories {
		if strings.TrimSpace(c.Summary) == "" {
			droppedEmpty++
			continue
		}
		// 说不出「为什么一个月后还有用」的候选直接丢——过程性噪声的共性就是它。
		if strings.TrimSpace(c.Reason) == "" {
			droppedNoReason++
			continue
		}
		// facts 只留用户原话。丢片段不丢候选：summary 才是主内容，facts 允许为空。
		var nonVerbatim int
		c.Facts, nonVerbatim = verifyVerbatimFacts(c.Facts, userContents)
		droppedFacts += nonVerbatim
		// 顺手截到工具侧上限：超出的条数会被决策循环的工具打回，白烧一轮它的轮数预算。
		if len(c.Facts) > memoryFactsMaxCount {
			truncatedFacts += len(c.Facts) - memoryFactsMaxCount
			c.Facts = c.Facts[:memoryFactsMaxCount]
		}
		cands = append(cands, c)
		if len(cands) >= e.maxCandidates {
			break
		}
	}
	log.Printf("[memory] extracted candidates: %d (dropped: %d no-reason, %d empty-summary)",
		len(cands), droppedNoReason, droppedEmpty)
	// facts 的丢弃率就是 facts 的「扭曲率」：模型写的是它自己的话、不是用户的。
	if droppedFacts > 0 {
		log.Printf("[memory] facts 逐字校验：丢弃 %d 条非用户原话片段", droppedFacts)
	}
	if truncatedFacts > 0 {
		log.Printf("[memory] facts 超上限：截掉 %d 条（上限 %d）", truncatedFacts, memoryFactsMaxCount)
	}
	// 全丢是个危险指纹：模型输出了候选、却一个都没留——通常是 prompt/schema 漂移
	// （字段改名、退回旧格式）。这条链路是"失败即降级、不报错"的，所以要故意炸出声音。
	if len(result.Memories) > 0 && len(cands) == 0 {
		log.Printf("[memory] WARN 全部 %d 条候选被丢弃（无 reason 或 summary 为空）——prompt/schema 漂移？",
			len(result.Memories))
	}
	return cands, nil
}

// parseExtractResult 从 LLM 回复中解析 JSON。解析失败表示「本轮没抽到任何记忆」，
// 必须报错让调用方感知（照 fact_extractor.go:186 处理），不能静默当成空结果。
func parseExtractResult(text string) (*memoryExtractResult, error) {
	// ParseFlashJSON 对「整段没有 {」的输入返回 ("", nil)，此处显式当失败：
	// 没有 JSON 结构 = 模型没按格式输出，不能误判成「本轮无记忆」。
	if !strings.Contains(text, "{") {
		log.Printf("[memory] extract parse failed: no JSON object in output (raw %.120s)", text)
		return nil, fmt.Errorf("parse memory extract result: no JSON object in output")
	}
	var result memoryExtractResult
	if _, err := ParseFlashJSON(text, &result); err != nil {
		log.Printf("[memory] extract parse failed: %v (raw %.120s)", err, text)
		return nil, fmt.Errorf("parse memory extract result: %w", err)
	}
	return &result, nil
}
