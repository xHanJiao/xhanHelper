package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

const themeDiscovererPrompt = `你是 xhanHelper 的「主题侦察员」。你的职责是：从用户最近的活动（聊天、笔记、记忆）中，发现用户可能长期关注的领域，提名为候选主题。

## 核心原则

1. 宁缺毋滥。一次最多提名 2 个候选。拿不准就不提。返回空结果是正常行为。
2. 关注「长期追踪价值」，不是「一次性好奇」。
3. 有理有据。每个提名必须附带理由——从哪些数据中看出了什么信号。
4. **只输出 JSON，不要输出任何解释、分析过程或前言**。

## 你能使用的检索工具

1. search_notes(query, top_k) — 向量检索笔记
2. search_memories(query, top_k) — 向量检索长期记忆

## 检索策略

query 必须是自然语言完整句子，禁止用空格分隔的关键词列表。例如「用户最近反复讨论的个人话题和兴趣方向」而非「话题 兴趣 最近 重复」。

## 判定标准：什么值得提名为主题

一个方向值得提名，需要同时满足：
- 重复出现：过去 7-14 天内至少出现了 2-3 次相关讨论或记录
- 用户主动表达关注：是用户发起的话题
- 可追踪：这个主题有持续变化的空间
- 非短期任务：不是完成即结束的 todo

## 判定标准：什么「不该」提名

以下情况一律不提名：
- 一次性闲聊
- 短期 todo（完成即结束）
- Agent 诱导的话题
- 太宽泛（如「生活」「工作」）
- 太窄（可追踪内容太少）
- 已有主题覆盖

## 输出格式

{
  "candidates": [
    {
      "name": "候选主题名，5 字以内",
      "description": "追踪什么，20 字以内",
      "rationale": "提名理由：从哪些数据中发现了什么信号",
      "signal_strength": "high | medium",
      "source_refs": ["数据原文引用1", "数据原文引用2"]
    }
  ],
  "rejected": [
    {"direction": "曾考虑的方向", "reject_reason": "一次性闲聊 / 短期 todo / 太宽泛 / 太窄 / 已有主题覆盖 / 信号不足"}
  ]
}

如果没发现合适的候选，返回：
{"candidates": [], "rejected": [{"direction": "搜索概况", "reject_reason": "最近7天数据无重复出现的关注方向"}]}`

// ThemeDiscoverer — 候选主题发现子 Agent（实现 tool.Tool 接口）
type ThemeDiscoverer struct {
	flashClient    llm.LLMClient
	retrievalTools map[string]tool.Tool
	themeStore     *ThemeStore
	embedder       *llm.Embedder
	vectorStore    VectorStorer
	dedupThreshold float64 // 余弦相似度阈值，默认 0.85
	maxIterations  int
}

func NewThemeDiscoverer(flashClient llm.LLMClient, retrievalTools map[string]tool.Tool, themeStore *ThemeStore, embedder *llm.Embedder, vectorStore VectorStorer, maxIterations int) *ThemeDiscoverer {
	return &ThemeDiscoverer{
		flashClient:    flashClient,
		retrievalTools: retrievalTools,
		themeStore:     themeStore,
		embedder:       embedder,
		vectorStore:    vectorStore,
		dedupThreshold: 0.85,
		maxIterations:  maxIterations,
	}
}

// VectorStorer — 向量存储接口（避免循环依赖，用于 theme embedding 和去重检索）
type VectorStorer interface {
	Upsert(id string, vec []float64, metadata map[string]string)
	VecSearch(query []float64, topK int, filter func(id string, vec []float64, metadata map[string]string) bool) []VectorSearchHit
}

// VectorSearchHit — 向量检索命中
type VectorSearchHit struct {
	ID    string
	Score float64
}

func (td *ThemeDiscoverer) Name() string { return "theme_discoverer" }
func (td *ThemeDiscoverer) Description() string {
	return "从用户最近活动中发现值得长期追踪的候选主题"
}

type discovererArgs struct {
	RecentMsgCount  int `json:"recent_msg_count"`
	RecentNoteCount int `json:"recent_note_count"`
	RecentMemCount  int `json:"recent_mem_count"`
}

func (td *ThemeDiscoverer) Functions() []tool.FuncSchema {
	return []tool.FuncSchema{{
		Name:        "discover",
		Description: "扫描用户最近活动，发现值得提名的候选追踪主题",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"recent_msg_count":  map[string]interface{}{"type": "integer", "description": "最近7天新增消息数"},
				"recent_note_count": map[string]interface{}{"type": "integer", "description": "最近7天新增笔记数"},
				"recent_mem_count":  map[string]interface{}{"type": "integer", "description": "最近7天新增记忆数"},
			},
		},
	}}
}

func (td *ThemeDiscoverer) Execute(ec *tool.ExecuteContext, funcName string, args json.RawMessage) *tool.Result {
	var a discovererArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &tool.Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}

	// 获取活跃主题列表
	activeThemes, err := td.themeStore.ListActive(ec.Ctx, ec.UserID)
	if err != nil {
		log.Printf("[theme_discoverer] ListActive error: %v", err)
	}

	// 拼装输入消息
	var sb strings.Builder
	sb.WriteString("## 当前活跃主题（避免重复提名）\n")
	if len(activeThemes) == 0 {
		sb.WriteString("（无活跃主题）\n")
	} else {
		for _, t := range activeThemes {
			sb.WriteString(fmt.Sprintf("- %s：%s\n", t.Name, t.Description))
		}
	}
	sb.WriteString(fmt.Sprintf("\n## 最近 7 天活动概览\n"))
	sb.WriteString(fmt.Sprintf("- 新增消息：%d 条（含 user role）\n", a.RecentMsgCount))
	sb.WriteString(fmt.Sprintf("- 新增笔记：%d 条\n", a.RecentNoteCount))
	sb.WriteString(fmt.Sprintf("- 新增记忆：%d 条\n", a.RecentMemCount))
	sb.WriteString("\n请检索并判断是否有值得提名的候选主题。\n")

	// 创建轻量 ReAct 循环（仅检索工具，限 2 轮）
	loop := NewReactLoop(td.flashClient, td.retrievalTools, themeDiscovererPrompt, td.maxIterations, ec.UserID, "ThemeDiscoverer", CtxTraceRepo(ec.Ctx), CtxTraceID(ec.Ctx))

	result, err := loop.Run(ec.Ctx, sb.String())
	if err != nil {
		log.Printf("[theme_discoverer] ReactLoop error: %v", err)
		return &tool.Result{Success: false, Error: fmt.Sprintf("发现失败: %v", err)}
	}

	// 解析输出
	var output struct {
		Candidates []map[string]interface{} `json:"candidates"`
		Rejected   []map[string]interface{} `json:"rejected"`
	}
	if _, parseErr := ParseFlashJSON(result.FinalResponse, &output); parseErr != nil {
		log.Printf("[theme_discoverer] parse error: %v", parseErr)
		return &tool.Result{Success: false, Error: fmt.Sprintf("解析发现结果失败: %v", parseErr)}
	}

	// 向量去重：embed 候选人，与已有主题做余弦相似度对比
	if td.embedder != nil && td.vectorStore != nil && len(output.Candidates) > 0 {
		output.Candidates, output.Rejected = td.dedupBySimilarity(ec.Ctx, ec.UserID, output.Candidates, output.Rejected)
	}

	data, _ := json.Marshal(output)

	log.Printf("[theme_discoverer] found %d candidates (after dedup), %d rejected", len(output.Candidates), len(output.Rejected))
	return &tool.Result{Success: true, Data: data}
}

// dedupBySimilarity 对候选主题做向量去重，过滤与已有主题太相似的候选
func (td *ThemeDiscoverer) dedupBySimilarity(ctx context.Context, userID string, candidates, rejected []map[string]interface{}) ([]map[string]interface{}, []map[string]interface{}) {
	kept := make([]map[string]interface{}, 0, len(candidates))
	for _, c := range candidates {
		name, _ := c["name"].(string)
		desc, _ := c["description"].(string)
		text := name + " " + desc

		vec, err := td.embedder.Embed(ctx, text)
		if err != nil {
			log.Printf("[theme_discoverer] embed candidate %q error: %v", name, err)
			kept = append(kept, c) // embed 失败不阻塞，保留候选
			continue
		}

		// 搜索已有主题向量（type=theme）
		hits := td.vectorStore.VecSearch(vec, 5, func(id string, _ []float64, metadata map[string]string) bool {
			return metadata["type"] == "theme" && metadata["user_id"] == userID
		})

		tooSimilar := false
		for _, hit := range hits {
			if hit.Score >= td.dedupThreshold {
				log.Printf("[theme_discoverer] candidate %q filtered: too similar to existing theme %s (score=%.4f > %.2f)",
					name, hit.ID, hit.Score, td.dedupThreshold)
				rejected = append(rejected, map[string]interface{}{
					"direction":     name,
					"reject_reason": fmt.Sprintf("向量去重：与已有主题 %s 相似度 %.2f > %.2f", hit.ID, hit.Score, td.dedupThreshold),
				})
				tooSimilar = true
				break
			}
		}
		if !tooSimilar {
			kept = append(kept, c)
		}
	}
	return kept, rejected
}
