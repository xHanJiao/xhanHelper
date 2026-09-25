package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
	"xhan-helper/internal/util"
)

// newMemory 组装一条待写记忆。topic 复用是确定性逻辑，留在这里，不交给 LLM。
func (s *recordLoopState) newMemory(topic, summary string, facts []string) *model.UserMemory {
	topic = s.reuseTopic(context.Background(), topic)
	return &model.UserMemory{
		ID:              uuid.New().String(),
		UserID:          s.userID,
		MemoryKey:       topic,
		MemoryValue:     summary,
		Topic:           topic,
		Facts:           factsPtr(facts),
		Source:          s.source,
		ConfirmCount:    1,
		LastConfirmedAt: timeNow(),
		ContentHash:     util.MD5Hex(summary),
	}
}

// persist 落库并同步向量/概念/分块。isNew=false 表示 update（走 Update + 重嵌入）。
func (s *recordLoopState) persist(ctx context.Context, m *model.UserMemory, isNew bool) error {
	if isNew {
		if err := s.deps.MemRepo.Create(ctx, m); err != nil {
			return fmt.Errorf("create memory: %w", err)
		}
	} else {
		if err := s.deps.MemRepo.Update(ctx, m.ID, m.Topic, m.MemoryValue, m.Facts); err != nil {
			return fmt.Errorf("update memory: %w", err)
		}
	}
	s.deps.ConceptEmbed.EnsureTopicEmbedded(ctx, m.UserID, m.Topic, model.EntityTypeMemory, m.ID)
	vec, err := s.deps.Embedder.Embed(ctx, m.MemoryValue)
	if err != nil {
		log.Printf("[record_loop] embed memory %.8s error: %v", m.ID, err)
		return nil
	}
	s.deps.VectorStore.Upsert(model.VectorPrefixMemory+m.ID, vec, map[string]string{
		"type": model.VectorTypeMemory, "user_id": m.UserID, "topic": m.Topic, "content": m.MemoryValue,
	})
	embStr := llm.EmbeddingToString(vec)
	if err := s.deps.EmbeddingRepo.Upsert(ctx, &model.Embedding{
		ID: m.ID, EntityType: model.EntityTypeMemory, EntityID: m.ID,
		UserID: m.UserID, Model: s.deps.Embedder.ModelName(), Embedding: &embStr,
	}); err != nil {
		log.Printf("[record_loop] upsert embedding %.8s error: %v", m.ID, err)
	}
	if s.deps.ChunkBuilder != nil {
		if _, err := s.deps.ChunkBuilder.BuildChunks(ctx, m.UserID, model.EntityTypeMemory, m.ID, m.MemoryValue, map[string]string{"topic": m.Topic}); err != nil {
			log.Printf("[record_loop] chunk memory %.8s error: %v", m.ID, err)
		}
	}
	return nil
}

// purge 清理单条记忆/笔记的派生数据（向量行/chunk/版本快照/内存向量），失败只记日志、不翻转结果。
func (s *recordLoopState) purge(ctx context.Context, typ, id string) {
	if s.deps.EntityPurger == nil {
		return
	}
	if err := s.deps.EntityPurger.Purge(ctx, typ, id); err != nil {
		log.Printf("[record_loop] purge %s %.8s error: %v", typ, id, err)
	}
}

func actionJSON(action, id string) json.RawMessage {
	b, err := json.Marshal(map[string]string{"action": action, "id": id})
	if err != nil {
		log.Printf("[record_loop] marshal action error: %v", err)
		return json.RawMessage(`{}`)
	}
	return b
}

func errResult(err error) *tool.Result {
	return &tool.Result{Success: false, Error: err.Error()}
}

// reuseTopic topic 复用：≥ 阈值就用最相似的已有 topic，否则用新的。确定性逻辑，不交给 LLM。
// 候选集 = 该用户在用的 topic（notes ∪ memories 的 distinct），天然排除已死的孤儿概念。
// 向量优先读 concept 缓存（GetOrEmbed），emb/查询失败一律降级为「用新 topic」并记日志。
func (s *recordLoopState) reuseTopic(ctx context.Context, topic string) string {
	topic = strings.TrimSpace(topic)
	if topic == "" || s.cfg.TopicReuseThresh <= 0 {
		return topic
	}
	candidates, err := s.deps.distinctTopics(ctx, s.userID)
	if err != nil {
		log.Printf("[record_loop] list distinct topics error: %v (using new topic)", err)
		return topic
	}
	candidates = slices.DeleteFunc(candidates, func(t string) bool { return t == topic })
	if len(candidates) == 0 {
		return topic
	}
	vec, err := s.deps.ConceptEmbed.GetOrEmbed(ctx, "topic", topic, s.userID)
	if err != nil || len(vec) == 0 {
		log.Printf("[record_loop] embed topic %q error: %v (using new topic)", topic, err)
		return topic
	}

	best, bestScore := "", 0.0
	for _, c := range candidates {
		cv, err := s.deps.ConceptEmbed.GetOrEmbed(ctx, "topic", c, s.userID)
		if err != nil || len(cv) == 0 {
			log.Printf("[record_loop] embed existing topic %q error: %v (skip)", c, err)
			continue
		}
		if score := util.CosineSimilarity(vec, cv); score > bestScore {
			best, bestScore = c, score
		}
	}
	if best != "" && bestScore >= s.cfg.TopicReuseThresh {
		log.Printf("[record_loop] topic reuse: %q → %q (score=%.3f)", topic, best, bestScore)
		return best
	}
	return topic
}

// distinctTopics 该用户在用的 topic = notes ∪ memories 的 distinct。
func (d *RecordDeps) distinctTopics(ctx context.Context, userID string) ([]string, error) {
	topics, err := d.distinctTopicsWithCount(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(topics))
	for _, t := range topics {
		out = append(out, t.Topic)
	}
	return out, nil
}

// topicRef 一个已有 topic 及其引用次数。
type topicRef struct {
	Topic string
	Count int64
}

// distinctTopicsWithCount 该用户在用的 topic 及引用次数（notes ∪ memories），按引用次数降序。
func (d *RecordDeps) distinctTopicsWithCount(ctx context.Context, userID string) ([]topicRef, error) {
	notes, err := d.NoteRepo.DistinctTopicsWithCount(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("note distinct topics: %w", err)
	}
	mems, err := d.MemRepo.DistinctTopics(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("memory distinct topics: %w", err)
	}
	agg := make(map[string]int64, len(notes)+len(mems))
	for _, n := range notes {
		if n.Topic != "" {
			agg[n.Topic] += n.Count
		}
	}
	for _, m := range mems {
		if m.Topic != "" {
			agg[m.Topic] += m.Count
		}
	}
	out := make([]topicRef, 0, len(agg))
	for t, c := range agg {
		out = append(out, topicRef{Topic: t, Count: c})
	}
	slices.SortFunc(out, func(a, b topicRef) int {
		if a.Count != b.Count {
			return int(b.Count - a.Count)
		}
		return strings.Compare(a.Topic, b.Topic)
	})
	return out, nil
}
