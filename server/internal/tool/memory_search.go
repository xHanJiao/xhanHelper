package tool

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"
)

// MemorySearchTool — LLM 可主动调用的记忆检索工具
type MemorySearchTool struct {
	vectorStore app.VectorStore
	memRepo     *service.UserMemoryRepo
	embedder    *llm.Embedder
	decayRate   float64
}

func NewMemorySearchTool(vs app.VectorStore, memRepo *service.UserMemoryRepo, embedder *llm.Embedder, decayRate float64) *MemorySearchTool {
	if decayRate <= 0 {
		decayRate = 0.02
	}
	return &MemorySearchTool{vectorStore: vs, memRepo: memRepo, embedder: embedder, decayRate: decayRate}
}

func (t *MemorySearchTool) Name() string { return "memory_search" }
func (t *MemorySearchTool) Description() string {
	return "在用户的长期记忆中搜索相关信息。当需要了解用户的偏好、习惯、过往决定、行为模式时调用，返回最相关的记忆条目"
}

func (t *MemorySearchTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "search",
		Description: "在用户记忆中搜索相关信息。当需要了解用户的偏好、习惯、过往决定时调用。返回带相关度排序的记忆条目列表",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query":       map[string]interface{}{"type": "string", "description": "搜索关键词"},
				"top_k":       map[string]interface{}{"type": "integer", "description": "返回条数，默认5，最大10"},
				"exclude_ids": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "排除的记忆 ID 列表，避免重复返回已看到的记忆"},
			},
			"required": []string{"query"},
		},
	}}
}

func (t *MemorySearchTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	var a struct {
		Query      string   `json:"query"`
		TopK       int      `json:"top_k"`
		ExcludeIDs []string `json:"exclude_ids"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.Query == "" {
		return &Result{Success: false, Error: "query 不能为空"}
	}
	if a.TopK <= 0 {
		a.TopK = 5
	}
	if a.TopK > 10 {
		a.TopK = 10
	}

	vec, err := t.embedder.Embed(ec.Ctx, a.Query)
	if err != nil {
		log.Printf("[memory_search] embed error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("向量化失败: %v", err)}
	}

	// 取 2 倍候选给时间衰减重排序留空间
	hits := t.vectorStore.Search(vec, a.TopK*2, func(id string, _ []float64, metadata map[string]string) bool {
		return metadata["type"] == "mem" && metadata["user_id"] == ec.UserID
	})

	skipIDs := make(map[string]bool, len(a.ExcludeIDs))
	for _, id := range a.ExcludeIDs {
		skipIDs[id] = true
	}

	var memIDs []string
	scoreMap := make(map[string]float64)
	for _, hit := range hits {
		id := strings.TrimPrefix(hit.ID, "mem:")
		if skipIDs[id] {
			continue
		}
		memIDs = append(memIDs, id)
		scoreMap[id] = hit.Score
	}

	if len(memIDs) == 0 {
		data, _ := json.Marshal(map[string]interface{}{
			"results": "（未找到相关记忆）\n",
		})
		return &Result{Success: true, Data: data}
	}

	memories, err := t.memRepo.GetByIDsAndUser(ec.Ctx, memIDs, ec.UserID)
	if err != nil {
		log.Printf("[memory_search] GetByIDs error: %v", err)
	}

	type scored struct {
		id         string
		content    string
		topic      string
		createdAt  time.Time
		score      float64
		freshness  float64
		finalScore float64
	}
	var list []scored
	now := time.Now()
	queryBigrams := bigramSet(a.Query)
	for _, m := range memories {
		vs := scoreMap[m.ID]
		daysAgo := now.Sub(m.CreatedAt).Hours() / 24
		freshness := 1.0 / (1.0 + daysAgo*t.decayRate)
		ks := bigramOverlap(queryBigrams, m.MemoryValue)
		finalScore := vs*0.5 + ks*0.3 + freshness*0.2
		list = append(list, scored{
			id: m.ID, content: m.MemoryValue, topic: m.Topic, createdAt: m.CreatedAt,
			score: vs, freshness: freshness, finalScore: finalScore,
		})
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].finalScore > list[j].finalScore
	})

	if a.TopK < len(list) {
		list = list[:a.TopK]
	}

	var sb strings.Builder
	sb.WriteString("### 记忆检索结果\n")
	for _, sc := range list {
		topicPart := ""
		if sc.topic != "" {
			topicPart = fmt.Sprintf("[%s] ", sc.topic)
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s%s (相关度 %.0f%%)\n",
			sc.createdAt.In(config.BJ).Format("2006-01-02"),
			topicPart,
			sc.content,
			math.Round(sc.finalScore*100),
		))
	}

	data, _ := json.Marshal(map[string]interface{}{
		"results": sb.String(),
	})

	return &Result{Success: true, Data: data}
}

// bigramSet 将文本切分为 bigram 集合，用于中文关键词匹配
func bigramSet(text string) map[string]bool {
	runes := []rune(text)
	set := make(map[string]bool, len(runes))
	for i := 0; i < len(runes)-1; i++ {
		set[string(runes[i:i+2])] = true
	}
	return set
}

// bigramOverlap 计算 query bigram 与目标文本的重叠比例
func bigramOverlap(queryBigrams map[string]bool, target string) float64 {
	if len(queryBigrams) == 0 {
		return 0
	}
	runes := []rune(target)
	if len(runes) < 2 {
		return 0
	}
	var overlap int
	for i := 0; i < len(runes)-1; i++ {
		if queryBigrams[string(runes[i:i+2])] {
			overlap++
		}
	}
	return float64(overlap) / float64(len(queryBigrams))
}
