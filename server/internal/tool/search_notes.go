package tool

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"
)

// VectorSearcher — 向量检索接口（避免循环依赖 agent 包）
type VectorSearcher interface {
	Search(query []float64, topK int, filter func(id string, vec []float64, metadata map[string]string) bool) []VectorHit
	GetVector(id string) ([]float64, bool)
	Size() int
}

// VectorHit — 向量检索命中
type VectorHit struct {
	ID    string
	Score float64
}

// SearchNotesTool — 向量检索笔记
type SearchNotesTool struct {
	vectorStore VectorSearcher
	noteRepo    *service.NoteRepo
	embedder    *llm.Embedder
}

func NewSearchNotesTool(vs VectorSearcher, noteRepo *service.NoteRepo, embedder *llm.Embedder) *SearchNotesTool {
	return &SearchNotesTool{vectorStore: vs, noteRepo: noteRepo, embedder: embedder}
}

func (t *SearchNotesTool) Name() string { return "search_notes" }
func (t *SearchNotesTool) Description() string {
	return "向量检索笔记，返回最相关的笔记片段"
}

func (t *SearchNotesTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "search_notes",
		Description: "用自然语言查询向量检索笔记，返回最相关的笔记内容。每条结果带引用 ID（note:xxx）方便后续溯源",
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

func (t *SearchNotesTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
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
		log.Printf("[search_notes] embed error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("向量化失败: %v", err)}
	}

	hits := t.vectorStore.Search(vec, a.TopK, func(id string, _ []float64, metadata map[string]string) bool {
		return metadata["type"] == "note" && metadata["user_id"] == ec.UserID
	})

	var noteIDs []string
	for _, hit := range hits {
		noteIDs = append(noteIDs, strings.TrimPrefix(hit.ID, "note:"))
	}

	var sb strings.Builder
	if len(noteIDs) == 0 {
		sb.WriteString("（未找到相关笔记）\n")
	} else {
		notes, err := t.noteRepo.GetByIDsAndUser(ec.Ctx, noteIDs, ec.UserID)
		if err != nil {
			log.Printf("[search_notes] GetByIDs error: %v", err)
		}
		count := 0
		for _, n := range notes {
			sb.WriteString(fmt.Sprintf("- [note:%s] %s: %s\n", n.ID, n.Topic, n.Content))
			count++
		}
		if count == 0 {
			sb.WriteString("（未找到相关笔记）\n")
		}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"results": sb.String(),
	})

	return &Result{Success: true, Data: data}
}
