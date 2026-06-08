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

// SearchNotesV2Tool — chunk 级向量检索笔记
type SearchNotesV2Tool struct {
	vectorStore app.VectorStore
	noteRepo    *service.NoteRepo
	chunkRepo   *service.EmbeddingChunkRepo
	embedder    *llm.Embedder
}

func NewSearchNotesV2Tool(vs app.VectorStore, noteRepo *service.NoteRepo, chunkRepo *service.EmbeddingChunkRepo, embedder *llm.Embedder) *SearchNotesV2Tool {
	return &SearchNotesV2Tool{vectorStore: vs, noteRepo: noteRepo, chunkRepo: chunkRepo, embedder: embedder}
}

func (t *SearchNotesV2Tool) Name() string { return "search_notes_v2" }
func (t *SearchNotesV2Tool) Description() string {
	return "向量检索笔记（chunk 级，返回匹配片段+全文）"
}

func (t *SearchNotesV2Tool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "search_notes_v2",
		Display:     "向量搜笔记",
		Description: "用自然语言完整句子向量检索笔记。命中后返回去重的笔记全文+匹配的句子片段。query 必须是自然语言完整句子，禁止关键词列表",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string", "description": "自然语言检索句子"},
				"top_k": map[string]interface{}{"type": "integer", "description": "返回的去重笔记数，默认 8"},
			},
			"required": []string{"query"},
		},
	}}
}

func (t *SearchNotesV2Tool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
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

	parentIDs := searchChunks(ec.Ctx, t.vectorStore, t.embedder, ec.UserID, a.Query, "note", a.TopK)
	if len(parentIDs) == 0 {
		return emptyResult("（未找到相关笔记）\n")
	}

	notes, err := t.noteRepo.GetByIDsAndUser(ec.Ctx, parentIDs, ec.UserID)
	if err != nil {
		log.Printf("[search_notes_v2] GetByIDs error: %v", err)
	}

	var sb strings.Builder
	count := 0
	for _, n := range notes {
		sb.WriteString(fmt.Sprintf("- [note:%s] %s: %s\n", n.ID, n.Topic, n.Content))
		count++
	}

	return marshalResult(sb.String(), count)
}
