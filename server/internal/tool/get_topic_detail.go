package tool

import (
	"encoding/json"
	"fmt"
	"log"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// GetTopicDetailTool — 按 topic 拉取完整 notes + memories
type GetTopicDetailTool struct {
	noteRepo *service.NoteRepo
	memRepo  *service.UserMemoryRepo
}

func NewGetTopicDetailTool(noteRepo *service.NoteRepo, memRepo *service.UserMemoryRepo) *GetTopicDetailTool {
	return &GetTopicDetailTool{noteRepo: noteRepo, memRepo: memRepo}
}

func (t *GetTopicDetailTool) Name() string { return "get_topic_detail" }
func (t *GetTopicDetailTool) Description() string {
	return "获取指定 topic 下所有 notes 和 memories 的全文"
}

func (t *GetTopicDetailTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "get_topic_detail",
		Display:     "主题详情",
		Description: "获取指定 topic（及其聚类相关 topic）下所有 notes 和 memories 的全文，按时间排序",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"topic":  map[string]interface{}{"type": "string", "description": "主 topic 名"},
				"topics": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "聚类关联的所有 topic 名列表"},
			},
			"required": []string{"topic"},
		},
	}}
}

func (t *GetTopicDetailTool) ReadOnlyFuncs() map[string]bool { return AllReadOnly(t) }

type topicDetailArgs struct {
	Topic  string   `json:"topic"`
	Topics []string `json:"topics"`
}

func (t *GetTopicDetailTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	var a topicDetailArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if a.Topic == "" {
		return &Result{Success: false, Error: "topic 不能为空"}
	}

	topics := a.Topics
	if len(topics) == 0 {
		topics = []string{a.Topic}
	}

	var items []map[string]interface{}

	// 查 notes
	for _, tp := range topics {
		notes, _, err := t.noteRepo.List(ec.Ctx, ec.UserID, tp, "", 200, 0)
		if err != nil {
			log.Printf("[get_topic_detail] list notes for %s error: %v", tp, err)
			continue
		}
		for _, n := range notes {
			items = append(items, map[string]interface{}{
				"type":       model.EntityTypeNote,
				"id":         n.ID,
				"topic":      n.Topic,
				"content":    n.Content,
				"tags":       n.Tags,
				"created_at": n.CreatedAt,
			})
		}
	}

	// 查 memories
	memories, _, err := t.memRepo.ListByUser(ec.Ctx, ec.UserID, "", "", "", 500, 0)
	if err != nil {
		log.Printf("[get_topic_detail] list memories error: %v", err)
	} else {
		for _, m := range memories {
			if !contains(topics, m.Topic) {
				continue
			}
			items = append(items, map[string]interface{}{
				"type":       model.EntityTypeMemory,
				"id":         m.ID,
				"topic":      m.Topic,
				"content":    m.MemoryValue,
				"tags":       m.Tags,
				"created_at": m.CreatedAt,
			})
		}
	}

	data, err := json.Marshal(map[string]interface{}{
		"topic":   a.Topic,
		"cluster": topics,
		"items":   items,
		"count":   len(items),
	})
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("marshal error: %v", err)}
	}
	return &Result{Success: true, Data: data}
}
