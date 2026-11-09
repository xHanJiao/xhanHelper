package retrieval

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// 事件注入形态（见 docs/design/event-threads/injection-rewrite.md）。
const (
	EventModeFull    = "full"    // 用户钉住（event.enable）：summary + 最近 20 条记忆
	EventModeHit     = "hit"     // 本轮命中：summary + 命中的记忆 top 10
	EventModeSummary = "summary" // 最近窗口内有活动：summary + 最近 3 条记忆
	EventModeIndex   = "index"   // 超过窗口：只有标题
)

// EventSourceName — EventSource 的名字，同时用作检索 trace 的 provider 名。
// 必须与 ctxflow.TypeEvent（资源区块类型）保持一致，否则 orchestrator 匹配不到
// trace、会误走「携带上一轮」路径。
const EventSourceName = "event"

// EventSourceConfig — 事件注入的静态配额（来自配置）。
type EventSourceConfig struct {
	PinnedMax        int // pinned 上限
	HitMax           int // 命中事件上限
	HitMemories      int // 命中事件附带记忆条数
	RecentMemories   int // 最近窗口事件附带记忆条数
	RecentWindowDays int // 摘要窗口天数
	MemoriesPerEvent int // pinned 附带记忆条数
}

// EventSource — 事件上下文源。
//
// 事件**全量**进入资源区（不再靠向量检索决定哪些事件出现）；检索只决定给谁附记忆：
// 命中事件带命中的记忆，pinned 带最近 20 条，最近窗口内的带最近 3 条，更早的只有标题。
type EventSource struct {
	vs        VectorStore
	memRepo   *service.UserMemoryRepo
	eventRepo *service.EventRepo
	decayer   *TimeDecay
	cfg       EventSourceConfig
}

func NewEventSource(
	vs VectorStore,
	memRepo *service.UserMemoryRepo,
	eventRepo *service.EventRepo,
	cfg EventSourceConfig,
	decayer *TimeDecay,
) *EventSource {
	if decayer == nil {
		decayer = NewTimeDecay(0)
	}
	if cfg.PinnedMax <= 0 {
		cfg.PinnedMax = 3
	}
	if cfg.HitMax <= 0 {
		cfg.HitMax = 3
	}
	if cfg.HitMemories <= 0 {
		cfg.HitMemories = 10
	}
	if cfg.RecentMemories <= 0 {
		cfg.RecentMemories = 3
	}
	if cfg.RecentWindowDays <= 0 {
		cfg.RecentWindowDays = 14
	}
	if cfg.MemoriesPerEvent <= 0 {
		cfg.MemoriesPerEvent = 20
	}
	return &EventSource{vs: vs, memRepo: memRepo, eventRepo: eventRepo, decayer: decayer, cfg: cfg}
}

func (s *EventSource) Name() string { return EventSourceName }

// Fetch 主对话注入路径（EventsAll=true）与 unified_search 语义检索路径（EventsAll=false）共用。
func (s *EventSource) Fetch(ctx context.Context, opts SearchOptions) ([]ContextPacket, []NearMissItem, error) {
	threshold := opts.Event.Threshold
	if threshold <= 0 {
		threshold = 0.5
	}
	limit := opts.Event.Limit
	if limit <= 0 {
		limit = 20
	}
	return s.build(ctx, opts.UserID, opts.Query, opts.Embedding, threshold, limit, opts.PinnedEventIDs, opts.EventsAll)
}

// ListForInjection 不跑向量检索，只出全量列表（短输入 / embedding 失败 / 事件源报错时的兜底）。
// 字面命中（消息里直接点名事件）仍然生效。
func (s *EventSource) ListForInjection(ctx context.Context, userID, query string, pinnedIDs []string) ([]ContextPacket, error) {
	pkts, _, err := s.build(ctx, userID, query, nil, 0.5, 20, pinnedIDs, true)
	return pkts, err
}

func (s *EventSource) build(
	ctx context.Context,
	userID, query string,
	embedding []float64,
	threshold float64,
	limit int,
	pinnedIDs []string,
	all bool,
) ([]ContextPacket, []NearMissItem, error) {
	active, err := s.loadActiveEvents(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("list events: %w", err)
	}
	if len(active) == 0 {
		return nil, nil, nil
	}

	// 1. 记忆向量命中 → 事件得分（组内最高）+ 每条命中记忆的分数
	hitScore := make(map[string]float64)
	hitMemScore := make(map[string]map[string]float64)
	if len(embedding) > 0 {
		scores := s.searchMemoryScores(embedding, userID, limit)
		memories, err := s.loadHitMemories(ctx, userID, scores)
		if err != nil {
			return nil, nil, err
		}
		for _, m := range memories {
			if m.EventID == nil {
				continue
			}
			eid := *m.EventID
			if active[eid] == nil {
				// 记忆的 event_id 指向已删/不存在的事件（merge/delete 的防御性缺口）
				continue
			}
			sc := scores[m.ID] * s.decayer.Freshness(m.UpdatedAt)
			if sc > hitScore[eid] {
				hitScore[eid] = sc
			}
			if hitMemScore[eid] == nil {
				hitMemScore[eid] = make(map[string]float64)
			}
			hitMemScore[eid][m.ID] = sc
		}
	}
	// 字面命中：消息里直接点名事件（中文没有空格，用事件名子串判断）。
	if q := strings.TrimSpace(query); q != "" {
		for id, e := range active {
			name := strings.TrimSpace(e.Name)
			if len([]rune(name)) >= 2 && strings.Contains(q, name) {
				if sc := threshold + 0.1; sc > hitScore[id] {
					hitScore[id] = sc
				}
			}
		}
	}

	// 2. 阈值过滤
	var nearMiss []NearMissItem
	for id, sc := range hitScore {
		if sc < threshold {
			if e := active[id]; e != nil {
				nearMiss = append(nearMiss, NearMissItem{
					ID: id, Text: e.Name, VectorScore: sc, Reason: "below_threshold",
				})
			}
			delete(hitScore, id)
		}
	}

	// 3. pinned（只在注入路径生效；检索工具不返回 pinned）
	pinned := make(map[string]bool)
	if all {
		for _, id := range pinnedIDs {
			if len(pinned) >= s.cfg.PinnedMax {
				break
			}
			if active[id] == nil || pinned[id] {
				continue
			}
			pinned[id] = true
		}
	}

	// 命中事件：按事件分数降序。注入路径受 HitMax 限制；检索工具按 top_k（limit）返回。
	hitCap := s.cfg.HitMax
	if !all {
		hitCap = limit
	}
	hitIDs := make([]string, 0, len(hitScore))
	for id := range hitScore {
		if !pinned[id] {
			hitIDs = append(hitIDs, id)
		}
	}
	sort.Slice(hitIDs, func(i, j int) bool {
		a, b := hitIDs[i], hitIDs[j]
		if hitScore[a] != hitScore[b] {
			return hitScore[a] > hitScore[b]
		}
		return eventNewer(active[a], active[b])
	})
	if len(hitIDs) > hitCap {
		hitIDs = hitIDs[:hitCap]
	}
	hitSet := make(map[string]bool, len(hitIDs))
	for _, id := range hitIDs {
		hitSet[id] = true
	}

	// 5. 形态分配
	cutoff := time.Now().AddDate(0, 0, -s.cfg.RecentWindowDays)
	modeOf := func(id string, e *model.Event) string {
		switch {
		case pinned[id]:
			return EventModeFull
		case hitSet[id]:
			return EventModeHit
		case !eventTime(e).Before(cutoff):
			return EventModeSummary
		default:
			return EventModeIndex
		}
	}

	// 检索工具只要命中事件；注入路径要全量
	ids := make([]string, 0, len(active))
	if all {
		for id := range active {
			ids = append(ids, id)
		}
	} else {
		ids = append(ids, hitIDs...)
	}
	if len(ids) == 0 {
		return nil, nearMiss, nil
	}

	// 6. 一次性取回所有事件记忆（MemoryCount 需要全量计数），按 updated_at DESC 排
	allMems, err := s.memRepo.ListByEventIDs(ctx, userID, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("list event memories: %w", err)
	}
	memoriesByEvent := make(map[string][]model.UserMemory, len(ids))
	for _, m := range allMems {
		if m.EventID == nil {
			continue
		}
		memoriesByEvent[*m.EventID] = append(memoriesByEvent[*m.EventID], m)
	}
	for id := range memoriesByEvent {
		sort.Slice(memoriesByEvent[id], func(i, j int) bool {
			a, b := memoriesByEvent[id][i], memoriesByEvent[id][j]
			if !a.UpdatedAt.Equal(b.UpdatedAt) {
				return a.UpdatedAt.After(b.UpdatedAt)
			}
			return a.ID < b.ID
		})
	}

	// 7. 输出：pinned → hit → summary → index；组内按新近度（hit 按分数）
	sort.Slice(ids, func(i, j int) bool {
		a, b := ids[i], ids[j]
		ma, mb := modeOf(a, active[a]), modeOf(b, active[b])
		if ra, rb := modeRank(ma), modeRank(mb); ra != rb {
			return ra < rb
		}
		if ma == EventModeHit && hitScore[a] != hitScore[b] {
			return hitScore[a] > hitScore[b]
		}
		return eventNewer(active[a], active[b])
	})

	packets := make([]ContextPacket, 0, len(ids))
	for _, id := range ids {
		e := active[id]
		if e == nil {
			continue
		}
		mode := modeOf(id, e)
		mems := memoriesByEvent[id]
		switch mode {
		case EventModeFull:
			mems = firstN(mems, s.cfg.MemoriesPerEvent)
		case EventModeHit:
			mems = topByScore(mems, hitMemScore[id], s.cfg.HitMemories)
		case EventModeSummary:
			mems = firstN(mems, s.cfg.RecentMemories)
		default:
			mems = nil
		}
		score := 0.0
		switch mode {
		case EventModeFull:
			score = math.Max(hitScore[id], 1)
		case EventModeHit:
			score = hitScore[id]
		}
		packets = append(packets, ContextPacket{
			ID:    e.ID,
			Type:  EventSourceName,
			Text:  e.Summary,
			Score: math.Round(score*10000) / 10000,
			Meta: EventMeta{
				EventID:     e.ID,
				Title:       e.Name,
				Summary:     e.Summary,
				Mode:        mode,
				Score:       score,
				UpdatedAt:   eventTime(e),
				Pinned:      pinned[id],
				Memories:    mems,
				MemoryCount: len(memoriesByEvent[id]),
			},
		})
	}

	log.Printf("[event] inject: user=%s active=%d hit=%d selected=%d (pinned/hit/summary/index=%d/%d/%d/%d)",
		userID, len(active), len(hitIDs), len(packets),
		countMode(packets, EventModeFull), countMode(packets, EventModeHit),
		countMode(packets, EventModeSummary), countMode(packets, EventModeIndex))
	return packets, nearMiss, nil
}

// LoadActiveEvents 分页加载用户全部未删除事件（不静默截断）。
// 事件注入（EventSource）与事件拼装（agent.EventAssembler）共用这一份实现。
func LoadActiveEvents(ctx context.Context, repo *service.EventRepo, userID string) (map[string]*model.Event, error) {
	const pageSize = 200
	out := make(map[string]*model.Event)
	for offset := 0; ; offset += pageSize {
		page, err := repo.List(ctx, userID, false, pageSize, offset)
		if err != nil {
			return nil, fmt.Errorf("list events (offset %d): %w", offset, err)
		}
		for i := range page {
			out[page[i].ID] = &page[i]
		}
		if len(page) < pageSize {
			break
		}
	}
	return out, nil
}

func (s *EventSource) loadActiveEvents(ctx context.Context, userID string) (map[string]*model.Event, error) {
	return LoadActiveEvents(ctx, s.eventRepo, userID)
}

// searchMemoryScores 复用记忆向量（chunk + 全量），返回 memoryID → 最高分。
func (s *EventSource) searchMemoryScores(query []float64, userID string, limit int) map[string]float64 {
	out := make(map[string]float64)
	if len(query) == 0 || s.vs == nil {
		return out
	}
	for _, res := range s.vs.Search(query, limit*8, func(e Entry) bool {
		return e.Metadata["type"] == model.VectorTypeChunk &&
			e.Metadata["parent_type"] == model.EntityTypeMemory &&
			e.Metadata["user_id"] == userID
	}) {
		_, id := model.ParseChunkVectorKey(res.ID)
		if id != "" && res.Score > out[id] {
			out[id] = res.Score
		}
	}
	for _, res := range s.vs.Search(query, limit*2, func(e Entry) bool {
		return e.Metadata["type"] == model.VectorTypeMemory && e.Metadata["user_id"] == userID
	}) {
		if !strings.HasPrefix(res.ID, model.VectorPrefixMemory) {
			continue
		}
		id := strings.TrimPrefix(res.ID, model.VectorPrefixMemory)
		if res.Score > out[id] {
			out[id] = res.Score
		}
	}
	return out
}

// loadHitMemories 按 id 取记忆并过滤到本用户（GetByIDs 不带 user 条件）。
func (s *EventSource) loadHitMemories(ctx context.Context, userID string, scores map[string]float64) ([]model.UserMemory, error) {
	if len(scores) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	list, err := s.memRepo.GetByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load hit memories: %w", err)
	}
	out := list[:0]
	for _, m := range list {
		if m.UserID == userID {
			out = append(out, m)
		}
	}
	return out, nil
}

// firstN 取前 n 条（调用方已按 updated_at DESC 排好）。
func firstN(mems []model.UserMemory, n int) []model.UserMemory {
	if n <= 0 {
		return nil
	}
	if len(mems) > n {
		return mems[:n]
	}
	return mems
}

// topByScore 取命中记忆里分数最高的 n 条（同分按 ID，保证确定性）。
// 字面命中（消息里直接点名事件）没有记忆级分数，回退为最近 n 条。
func topByScore(mems []model.UserMemory, scores map[string]float64, n int) []model.UserMemory {
	if n <= 0 {
		return nil
	}
	if len(scores) == 0 {
		return firstN(mems, n)
	}
	out := make([]model.UserMemory, 0, len(mems))
	for _, m := range mems {
		if _, ok := scores[m.ID]; ok {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		si, sj := scores[out[i].ID], scores[out[j].ID]
		if si != sj {
			return si > sj
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// countMode 统计各形态条数（日志用）。
func countMode(packets []ContextPacket, mode string) int {
	n := 0
	for _, p := range packets {
		if meta, ok := p.Meta.(EventMeta); ok && meta.Mode == mode {
			n++
		}
	}
	return n
}

// eventTime 取事件的「最近活动时间」：last_memory_at 优先，缺失回退 updated_at。
func eventTime(e *model.Event) time.Time {
	if e == nil {
		return time.Time{}
	}
	if e.LastMemoryAt != nil {
		return *e.LastMemoryAt
	}
	return e.UpdatedAt
}

// eventNewer 供排序：最后记忆时间更近的在前，平局按 ID（确定性）。
func eventNewer(a, b *model.Event) bool {
	if a == nil || b == nil {
		return a != nil
	}
	ta, tb := eventTime(a), eventTime(b)
	if !ta.Equal(tb) {
		return ta.After(tb)
	}
	return a.ID < b.ID
}

// modeRank 输出顺序：pinned(full) > hit > summary > index。
func modeRank(mode string) int {
	switch mode {
	case EventModeFull:
		return 0
	case EventModeHit:
		return 1
	case EventModeSummary:
		return 2
	case EventModeIndex:
		return 3
	}
	return 4
}

func (s *EventSource) Warmup(ctx context.Context, embedder *llm.Embedder) {
	// 复用记忆向量，由 MemorySource 预热，这里无需额外动作。
}
