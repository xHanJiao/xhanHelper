package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"
	"xhan-helper/internal/util"
)

// GetTopicStatsTool — topic 统计工具（聚类 + 7 维度）
type GetTopicStatsTool struct {
	conceptRepo      *service.ConceptEmbeddingRepo
	noteRepo         *service.NoteRepo
	memRepo          *service.UserMemoryRepo
	embedder         *llm.Embedder
	vectorStore      app.VectorStore
	topicEmbedder    app.ConceptEmbedder
	clusterThreshold float64
}

func NewGetTopicStatsTool(conceptRepo *service.ConceptEmbeddingRepo, noteRepo *service.NoteRepo, memRepo *service.UserMemoryRepo, embedder *llm.Embedder, vs app.VectorStore, topicEmbedder app.ConceptEmbedder, clusterThreshold float64) *GetTopicStatsTool {
	return &GetTopicStatsTool{conceptRepo: conceptRepo, noteRepo: noteRepo, memRepo: memRepo, embedder: embedder, vectorStore: vs, topicEmbedder: topicEmbedder, clusterThreshold: clusterThreshold}
}

func (t *GetTopicStatsTool) Name() string { return "get_topic_stats" }
func (t *GetTopicStatsTool) Description() string {
	return "获取 topic 聚类统计摘要，帮助发现值得追踪的主题"
}

func (t *GetTopicStatsTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "get_topic_stats",
		Description: "获取所有 topic 的聚类统计摘要：topic 分布、时间跨度、关联会话、tags、每周活跃度等",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}}
}

func (t *GetTopicStatsTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	// 1. 读取所有 concept（topic + tag）
	concepts, err := t.conceptRepo.ListAllByUser(ec.Ctx, ec.UserID)
	if err != nil {
		log.Printf("[get_topic_stats] list concepts error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("读取概念失败: %v", err)}
	}

	// 2. 从向量库取缓存的 topic 向量
	topicVecs := make(map[string][]float64)
	for _, c := range concepts {
		if c.Type != "topic" {
			continue
		}
		vec, ok := t.vectorStore.GetVector("concept:" + c.ID)
		if ok {
			topicVecs[c.Value] = vec
			continue
		}
		// 兜底：走统一嵌入入口（写 concept 表 + embeddings 表 + vectorStore）
		if t.topicEmbedder != nil {
			t.topicEmbedder.EnsureTopicEmbedded(ec.Ctx, ec.UserID, c.Value, "stats", "fallback")
		}
		// 重新从 vectorStore 读取
		vec, ok = t.vectorStore.GetVector("concept:" + c.ID)
		if ok {
			topicVecs[c.Value] = vec
		}
	}

	// 3. 聚类合并
	clusterList := util.ClusterVectors(topicVecs, t.clusterThreshold)

	// 4. 对每个 cluster 做统计
	var result []map[string]interface{}
	for _, cl := range clusterList {
		stats := t.buildClusterStats(ec.Ctx, ec.UserID, cl)
		if stats != nil {
			result = append(result, stats)
		}
	}

	if len(result) == 0 {
		data, _ := json.Marshal(map[string]interface{}{
			"clusters":       []interface{}{},
			"total_topics":   len(topicVecs),
			"total_clusters": 0,
		})
		return &Result{Success: true, Data: data}
	}

	data, err := json.Marshal(map[string]interface{}{
		"clusters":       result,
		"total_topics":   len(topicVecs),
		"total_clusters": len(result),
	})
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("marshal error: %v", err)}
	}

	log.Printf("[get_topic_stats] %d topics → %d clusters", len(topicVecs), len(result))
	return &Result{Success: true, Data: data}
}

func (t *GetTopicStatsTool) buildClusterStats(ctx context.Context, userID string, cl util.Cluster) map[string]interface{} {
	stats := map[string]interface{}{
		"topic":   cl.MainTopic,
		"cluster": cl.Members,
	}

	// 从 notes 聚合
	notes, _, _ := t.noteRepo.List(ctx, userID, "", "", 500, 0)
	var noteCount int
	var firstTime, lastTime string
	tags := make(map[string]bool)
	type weekCount struct{ w int }
	weekly := make(map[int]int)

	for _, n := range notes {
		if !contains(cl.Members, n.Topic) {
			continue
		}
		noteCount++
		ts := n.CreatedAt.In(config.BJ).Format("2006-01-02")
		if firstTime == "" || ts < firstTime {
			firstTime = ts
		}
		if ts > lastTime {
			lastTime = ts
		}
		for _, tag := range splitTags(n.Tags) {
			tags[tag] = true
		}
		// 简单按周分组
		_, week := n.CreatedAt.ISOWeek()
		weekly[week]++
	}

	// 从 memories 聚合
	memories, _, _ := t.memRepo.ListByUser(ctx, userID, "", "", 500, 0)
	var memCount int
	for _, m := range memories {
		if !contains(cl.Members, m.Topic) {
			continue
		}
		memCount++
		ts := m.CreatedAt.In(config.BJ).Format("2006-01-02")
		if firstTime == "" || ts < firstTime {
			firstTime = ts
		}
		if ts > lastTime {
			lastTime = ts
		}
		for _, tag := range splitTags(m.Tags) {
			tags[tag] = true
		}
		_, week := m.CreatedAt.ISOWeek()
		weekly[week]++
	}

	stats["source"] = map[string]int{"notes": noteCount, "memories": memCount}
	if firstTime != "" {
		stats["time_range"] = firstTime + " → " + lastTime
	}
	tagList := make([]string, 0, len(tags))
	for tag := range tags {
		tagList = append(tagList, tag)
	}
	stats["tags"] = tagList

	weekList := make([]int, 0, len(weekly))
	for _, v := range weekly {
		weekList = append(weekList, v)
	}
	stats["weekly_distribution"] = weekList
	stats["conversation_count"] = 0 // 简化：后续通过 chunk 命中判断

	return stats
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func splitTags(tags string) []string {
	if tags == "" {
		return nil
	}
	var result []string
	for _, t := range strings.Split(tags, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			result = append(result, t)
		}
	}
	return result
}
