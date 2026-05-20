package tool

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"
)

// SearchMemoriesTool — 向量检索长期记忆
type SearchMemoriesTool struct {
	vectorStore app.VectorStore
	memRepo     *service.UserMemoryRepo
	embedder    *llm.Embedder
}

func NewSearchMemoriesTool(vs app.VectorStore, memRepo *service.UserMemoryRepo, embedder *llm.Embedder) *SearchMemoriesTool {
	return &SearchMemoriesTool{vectorStore: vs, memRepo: memRepo, embedder: embedder}
}

func (t *SearchMemoriesTool) Name() string { return "search_memories" }
func (t *SearchMemoriesTool) Description() string {
	return "向量检索长期记忆，返回最相关的记忆条目"
}

func (t *SearchMemoriesTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "search_memories",
		Description: "用自然语言查询向量检索用户长期记忆，返回最相关的记忆条目。每条结果带引用 ID（mem:xxx）方便后续溯源",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string", "description": "自然语言检索词"},
				"top_k": map[string]interface{}{"type": "integer", "description": "返回条数，默认 8"},
			},
			"required": []string{"query"},
		},
	}}
}

func (t *SearchMemoriesTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	var a struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.Query == "" {
		return &Result{Success: false, Error: "query 不能为空"}
	}
	if a.TopK <= 0 {
		a.TopK = 8
	}

	vec, err := t.embedder.Embed(ec.Ctx, a.Query)
	if err != nil {
		log.Printf("[search_memories] embed error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("向量化失败: %v", err)}
	}

	hits := t.vectorStore.Search(vec, a.TopK, func(id string, _ []float64, metadata map[string]string) bool {
		return metadata["type"] == "mem" && metadata["user_id"] == ec.UserID
	})

	var memIDs []string
	for _, hit := range hits {
		memIDs = append(memIDs, strings.TrimPrefix(hit.ID, "mem:"))
	}

	var sb strings.Builder
	if len(memIDs) == 0 {
		sb.WriteString("（未找到相关记忆）\n")
	} else {
		memories, err := t.memRepo.GetByIDsAndUser(ec.Ctx, memIDs, ec.UserID)
		if err != nil {
			log.Printf("[search_memories] GetByIDs error: %v", err)
		}
		count := 0
		for _, m := range memories {
			sb.WriteString(fmt.Sprintf("- [mem:%s] %s\n", m.ID, m.MemoryValue))
			count++
		}
		if count == 0 {
			sb.WriteString("（未找到相关记忆）\n")
		}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"results": sb.String(),
	})

	return &Result{Success: true, Data: data}
}
