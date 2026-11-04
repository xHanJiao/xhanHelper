package retrieval

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/util"
)

// chunkRepo is the minimal DB interface needed by EntitySource for Fetch.
type chunkRepo[T model.ChunkEntity] interface {
	GetByIDs(ctx context.Context, ids []string) ([]T, error)
}

// EntityConfig holds the per-entity-type configuration for EntitySource.
type EntityConfig[T model.ChunkEntity] struct {
	EntityType   string                                                  // model.EntityTypeMemory / EntityTypeNote
	FullType     string                                                  // "mem" / "note" (vector store metadata type for full-embedding entries)
	IDPrefix     string                                                  // "mem:" / "note:"
	ConfigPicker func(SearchOptions) SourceConfig                        // opts.Memory / opts.Note
	ListAll      func(context.Context, time.Time, int, int) ([]T, error) // for Warmup (since=zero 表示不过滤)
	SinceDays    int                                                     // >0 时 warmup 只加载最近 N 天
	Filter       func(T) bool                                            // 可选：注入前过滤实体（memory 用 event_id IS NULL）
}

// EntitySource is a generic chunk+full-embedding retrieval source.
// T must satisfy model.ChunkEntity (UserMemory, Note, etc).
type EntitySource[T model.ChunkEntity] struct {
	name          string
	entityType    string
	fullType      string
	idPrefix      string
	vs            VectorStore
	decayer       *TimeDecay
	repo          chunkRepo[T]
	embeddingRepo *service.EmbeddingRepo
	chunkRepo     *service.EmbeddingChunkRepo
	chunkBuilder  app.ChunkEmbedder
	configPicker  func(SearchOptions) SourceConfig
	listAll       func(context.Context, time.Time, int, int) ([]T, error)
	sinceDays     int
	filter        func(T) bool
}

// NewEntitySource creates a generic chunk+full-embedding retrieval source.
func NewEntitySource[T model.ChunkEntity](
	name string,
	vs VectorStore,
	repo chunkRepo[T],
	embeddingRepo *service.EmbeddingRepo,
	chunkRepo *service.EmbeddingChunkRepo,
	chunkBuilder app.ChunkEmbedder,
	decayer *TimeDecay,
	cfg EntityConfig[T],
) *EntitySource[T] {
	if decayer == nil {
		decayer = NewTimeDecay(0)
	}
	return &EntitySource[T]{
		name:          name,
		entityType:    cfg.EntityType,
		fullType:      cfg.FullType,
		idPrefix:      cfg.IDPrefix,
		vs:            vs,
		decayer:       decayer,
		repo:          repo,
		embeddingRepo: embeddingRepo,
		chunkRepo:     chunkRepo,
		chunkBuilder:  chunkBuilder,
		configPicker:  cfg.ConfigPicker,
		listAll:       cfg.ListAll,
		sinceDays:     cfg.SinceDays,
		filter:        cfg.Filter,
	}
}

func (s *EntitySource[T]) Name() string { return s.name }

// ===== Fetch =====

func (s *EntitySource[T]) Fetch(ctx context.Context, opts SearchOptions) ([]ContextPacket, []NearMissItem, error) {
	queryVec := opts.Embedding
	userID := opts.UserID
	cfg := s.configPicker(opts)
	threshold := cfg.Threshold
	limit := cfg.Limit

	var nearMiss []NearMissItem

	// 1. Chunk 级向量检索，按 entity ID 去重取最高分
	chunkCandidates := make(map[string]float64)

	chunkResults := s.vs.Search(queryVec, limit*8, func(e Entry) bool {
		return e.Metadata["type"] == model.VectorTypeChunk &&
			e.Metadata["parent_type"] == s.name &&
			e.Metadata["user_id"] == userID
	})
	for _, res := range chunkResults {
		_, id := model.ParseChunkVectorKey(res.ID)
		if id == "" {
			nearMiss = append(nearMiss, NearMissItem{ID: res.ID, Text: res.ID, VectorScore: res.Score, Reason: "bad_chunk_id"})
			continue
		}
		if old, ok := chunkCandidates[id]; !ok || res.Score > old {
			chunkCandidates[id] = res.Score
		}
	}

	// 2. 全量嵌入搜索，排除已在 chunk 中命中的
	fullCandidates := make(map[string]float64)
	fullResults := s.vs.Search(queryVec, limit*2, func(e Entry) bool {
		return e.Metadata["type"] == s.fullType && e.Metadata["user_id"] == userID
	})
	for _, res := range fullResults {
		if !strings.HasPrefix(res.ID, s.idPrefix) {
			nearMiss = append(nearMiss, NearMissItem{ID: res.ID, Text: res.ID, VectorScore: res.Score, Reason: "wrong_prefix"})
			continue
		}
		id := strings.TrimPrefix(res.ID, s.idPrefix)
		if _, exists := chunkCandidates[id]; exists {
			continue
		}
		if _, exists := fullCandidates[id]; !exists {
			fullCandidates[id] = res.Score
		}
	}

	if len(chunkCandidates) == 0 && len(fullCandidates) == 0 {
		return nil, nearMiss, nil
	}

	// 3. 分组排序、裁剪（各组取 top limit*2 进入新鲜度评定）
	chunkHits := mapToSortedHits(chunkCandidates, limit*2)
	fullHits := mapToSortedHits(fullCandidates, limit*2)

	// 4. 加载所有 entity
	ids := make([]string, 0, len(chunkHits)+len(fullHits))
	scoreMap := make(map[string]float64, len(chunkHits)+len(fullHits))
	fromChunk := make(map[string]bool, len(chunkHits))
	for _, h := range chunkHits {
		ids = append(ids, h.id)
		scoreMap[h.id] = h.score
		fromChunk[h.id] = true
	}
	for _, h := range fullHits {
		ids = append(ids, h.id)
		scoreMap[h.id] = h.score
	}

	entities, err := s.repo.GetByIDs(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	if len(entities) == 0 {
		return nil, nil, fmt.Errorf("all hit %s ids not found, ids: %v", s.name, ids)
	}

	// 5. 新鲜度过滤：V×F >= threshold，分组记分
	type scoredHit struct {
		ent      T
		vecScore float64
	}
	var chunkScored []scoredHit
	var fullScored []scoredHit
	for _, e := range entities {
		if s.filter != nil && !s.filter(e) {
			continue
		}
		vs := scoreMap[e.GetID()]
		fresh := s.decayer.Freshness(e.GetCreatedAt())
		if vs*fresh >= threshold {
			sh := scoredHit{ent: e, vecScore: vs}
			if fromChunk[e.GetID()] {
				chunkScored = append(chunkScored, sh)
			} else {
				fullScored = append(fullScored, sh)
			}
		} else {
			nearMiss = append(nearMiss, NearMissItem{
				ID: e.GetID(), Text: e.GetContent(), VectorScore: vs,
				Reason: "below_threshold",
			})
		}
	}

	// 组内按向量分降序
	sort.Slice(chunkScored, func(i, j int) bool { return chunkScored[i].vecScore > chunkScored[j].vecScore })
	sort.Slice(fullScored, func(i, j int) bool { return fullScored[i].vecScore > fullScored[j].vecScore })

	// 合并：chunk 组在前
	scored := append(chunkScored, fullScored...)
	if len(scored) > limit {
		for _, s := range scored[limit:] {
			nearMiss = append(nearMiss, NearMissItem{
				ID:          s.ent.GetID(),
				Text:        s.ent.GetContent(),
				VectorScore: s.vecScore,
				Reason:      "over_limit",
			})
		}
		scored = scored[:limit]
	}

	packets := make([]ContextPacket, 0, len(scored))
	for _, sc := range scored {
		content := sc.ent.GetContent()
		if s.name == model.EntityTypeNote {
			// 长笔记注入中间截断（保留头尾），LLM 可调 get_note 获取全文；Meta.Content 保留全文
			content = util.TruncateMiddle(content, noteInjectedHead, noteInjectedTail)
			if content != sc.ent.GetContent() {
				content += "\n（内容过长已截断，如需全文请调用 get_note）"
			}
		}
		text := formatEntityPacket(sc.ent.GetTopic(), content, sc.vecScore)
		packets = append(packets, ContextPacket{
			ID:    sc.ent.GetID(),
			Type:  s.name,
			Text:  text,
			Score: sc.vecScore,
			Meta: EntityMeta{
				Content:   sc.ent.GetContent(),
				Topic:     sc.ent.GetTopic(),
				CreatedAt: sc.ent.GetCreatedAt(),
			},
		})
	}
	return packets, nearMiss, nil
}

// ===== Warmup =====

func (s *EntitySource[T]) Warmup(ctx context.Context, embedder *llm.Embedder) {
	if s.vs == nil || embedder == nil || s.embeddingRepo == nil {
		log.Printf("[vector] %s warmup: skipped (no config)", s.name)
		return
	}
	log.Printf("[vector] %s warmup: loading existing entries...", s.name)

	const pageSize = 100
	loaded, embedded, chunked := 0, 0, 0
	var since time.Time
	if s.sinceDays > 0 {
		since = time.Now().AddDate(0, 0, -s.sinceDays)
	}

	// 加载持久化的全量嵌入
	for offset := 0; ; offset += pageSize {
		var embeddings []model.Embedding
		var err error
		if since.IsZero() {
			embeddings, err = s.embeddingRepo.ListByType(ctx, s.entityType, pageSize, offset)
		} else {
			embeddings, err = s.embeddingRepo.ListByTypeSince(ctx, s.entityType, since, pageSize, offset)
		}
		if err != nil {
			log.Printf("[vector] %s warmup: list embeddings error: %v", s.name, err)
			break
		}
		if len(embeddings) == 0 {
			break
		}
		for _, emb := range embeddings {
			if emb.Embedding != nil {
				if vec, err := llm.StringToEmbedding(*emb.Embedding); err == nil && len(vec) > 0 {
					s.vs.Upsert(s.idPrefix+emb.EntityID, vec, map[string]string{"type": s.fullType, "user_id": emb.UserID})
					loaded++
				}
			}
		}
		if len(embeddings) < pageSize {
			break
		}
	}

	// 补建全量嵌入 + chunk
	for offset := 0; ; offset += pageSize {
		entities, err := s.listAll(ctx, since, pageSize, offset)
		if err != nil {
			log.Printf("[vector] %s warmup: list error: %v", s.name, err)
			break
		}
		if len(entities) == 0 {
			break
		}

		ids := make([]string, len(entities))
		for i, e := range entities {
			ids[i] = e.GetID()
		}

		existingChunks, _ := s.chunkRepo.BatchGetByParentIDs(ctx, s.entityType, ids)
		hasChunks := make(map[string]bool)
		for _, c := range existingChunks {
			hasChunks[c.ParentID] = true
		}

		for _, e := range entities {
			existingEmb, err := s.embeddingRepo.GetByEntity(ctx, s.entityType, e.GetID())
			if err != nil {
				log.Printf("[vector] %s warmup: GetByEntity error for %.8s: %v", s.name, e.GetID(), err)
			}
			if existingEmb == nil || existingEmb.Embedding == nil {
				vec, err := embedder.Embed(ctx, e.GetContent())
				if err != nil {
					log.Printf("[vector] %s warmup: embed %.8s error: %v", s.name, e.GetID()[:8], err)
					continue
				}
				s.vs.Upsert(s.idPrefix+e.GetID(), vec, map[string]string{"type": s.fullType, "user_id": e.GetUserID(), "topic": e.GetTopic()})
				embStr := llm.EmbeddingToString(vec)
				s.embeddingRepo.Upsert(ctx, &model.Embedding{
					ID: e.GetID(), EntityType: s.entityType, EntityID: e.GetID(),
					UserID: e.GetUserID(), Model: embedder.ModelName(), Embedding: &embStr,
				})
				embedded++
			}

			if !hasChunks[e.GetID()] && s.chunkBuilder != nil {
				if cnt, err := s.chunkBuilder.BuildChunks(ctx, e.GetUserID(), s.entityType, e.GetID(), e.GetContent(), map[string]string{"topic": e.GetTopic()}); err != nil {
					log.Printf("[vector] %s warmup: chunk build %.8s error: %v", s.name, e.GetID()[:8], err)
				} else if cnt > 0 {
					chunked++
				}
			}
		}
		if len(entities) < pageSize {
			break
		}
	}

	log.Printf("[vector] %s warmup: %d from persisted, %d new, %d chunked, total=%d in store",
		s.name, loaded, embedded, chunked, s.vs.Size())
}

// ===== helpers =====

type hit struct {
	id    string
	score float64
}

func mapToSortedHits(candidates map[string]float64, max int) []hit {
	hits := make([]hit, 0, len(candidates))
	for id, score := range candidates {
		hits = append(hits, hit{id: id, score: score})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > max {
		hits = hits[:max]
	}
	return hits
}

func formatEntityPacket(topic, content string, score float64) string {
	if topic != "" {
		topic = fmt.Sprintf("[%s] ", topic)
	}
	return fmt.Sprintf("- %s%s (相关度 %.2f)\n", topic, content, score)
}

// 长笔记注入截断参数：单条正文超过 150 字时中间截断，保留头部 100 字与尾部 50 字。
const (
	noteInjectedHead = 100
	noteInjectedTail = 50
)
