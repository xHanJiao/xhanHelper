package retrieval

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// 事件注入形态（设计 §6.3）。
const (
	EventModeFull    = "full"
	EventModeCompact = "compact"
	EventModeSummary = "summary"
	EventModeIndex   = "index"
)

// EventSourceName — EventSource 的名字，同时用作检索 trace 的 provider 名。
// 必须与 ctxflow.TypeEvent（资源区块类型）保持一致，否则 orchestrator 匹配不到
// trace、会误走「携带上一轮」路径。
const EventSourceName = "event"

// EventRef — 上一轮注入过的事件引用（从 XML 解析，不落库）。
//
// Pinned 仅供 XML 展示/调试，不参与注入决策：权威来源是 SearchOptions.PinnedEventIDs
// （Conversation.Extra.EventFocus.Enabled）。不能消费 prev 里的 pinned——用户 event.disable
// 之后 Extra 已移除，但上一轮 XML 仍是 pinned="1"，消费会让 disable 永远不生效（自我续命）。
type EventRef struct {
	ID     string
	Mode   string
	Miss   int
	Pinned bool
}

// EventSourceConfig — 事件注入的静态配额（来自配置，见设计 §6.7）。
type EventSourceConfig struct {
	QuotaFull        int // full 上限（含 pinned）
	QuotaCompact     int // compact 上限
	QuotaSummary     int // summary 上限，超出降为 index
	QuotaIndex       int // index 上限，超出移出资源区
	PinnedMax        int // pinned 上限
	MemoriesPerEvent int // full 形态每个事件附带的记忆条数（updated_at DESC）
}

// EventSource — 事件上下文源：复用记忆向量，按 event_id 聚合注入。
//
// 不另建事件向量索引：搜到记忆后按 event_id 分组，组内最高分即事件得分。
// 已归属事件的记忆只经这里注入；MemorySource 只查 event_id IS NULL，两者不重复。
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
	if cfg.QuotaFull <= 0 {
		cfg.QuotaFull = 4
	}
	if cfg.QuotaCompact <= 0 {
		cfg.QuotaCompact = 6
	}
	if cfg.QuotaSummary <= 0 {
		cfg.QuotaSummary = 12
	}
	if cfg.QuotaIndex <= 0 {
		cfg.QuotaIndex = 25
	}
	if cfg.PinnedMax <= 0 {
		cfg.PinnedMax = 3
	}
	if cfg.MemoriesPerEvent <= 0 {
		cfg.MemoriesPerEvent = 20
	}
	return &EventSource{vs: vs, memRepo: memRepo, eventRepo: eventRepo, decayer: decayer, cfg: cfg}
}

func (s *EventSource) Name() string { return EventSourceName }

// eventState — 单个事件在本轮的注入决策。
type eventState struct {
	ev      *model.Event
	mode    string
	score   float64
	miss    int
	pinned  bool
	carried bool
}

func (s *EventSource) Fetch(ctx context.Context, opts SearchOptions) ([]ContextPacket, []NearMissItem, error) {
	userID := opts.UserID
	threshold := opts.Event.Threshold
	limit := opts.Event.Limit
	if limit <= 0 {
		limit = 20
	}

	active, err := s.loadActiveEvents(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("list events: %w", err)
	}
	if len(active) == 0 {
		return nil, nil, nil
	}

	// 1. 记忆向量命中 → 事件得分（组内最高）
	scores := s.searchMemoryScores(opts.Embedding, userID, limit)
	memories, err := s.loadHitMemories(ctx, userID, scores)
	if err != nil {
		return nil, nil, err
	}
	fresh := make(map[string]float64, len(memories))
	for _, m := range memories {
		if m.EventID == nil {
			continue
		}
		eid := *m.EventID
		score := scores[m.ID] * s.decayer.Freshness(m.UpdatedAt)
		if score > fresh[eid] {
			fresh[eid] = score
		}
	}
	// 字面命中：消息里直接点名事件（中文没有空格，用事件名子串判断）。
	for id, e := range active {
		name := strings.TrimSpace(e.Name)
		if len([]rune(name)) >= 2 && strings.Contains(opts.Query, name) {
			if sc := threshold + 0.1; sc > fresh[id] {
				fresh[id] = sc
			}
		}
	}

	var nearMiss []NearMissItem
	for id, sc := range fresh {
		if sc < threshold {
			if e := active[id]; e != nil {
				nearMiss = append(nearMiss, NearMissItem{
					ID: id, Text: e.Name, VectorScore: sc, Reason: "below_threshold",
				})
			}
			delete(fresh, id)
		}
	}

	states := make(map[string]*eventState)

	// 2. pinned 无条件 full（阶段 4 才有写入方，这里先支持）
	pinned := 0
	for _, id := range opts.PinnedEventIDs {
		if pinned >= s.cfg.PinnedMax {
			break
		}
		if states[id] != nil || active[id] == nil {
			continue
		}
		states[id] = &eventState{
			ev: active[id], mode: EventModeFull, pinned: true,
			score: math.Max(fresh[id], 1),
		}
		pinned++
	}

	// 3. fresh 命中：先按 full 放，超配额再降级
	freshIDs := make([]string, 0, len(fresh))
	for id := range fresh {
		if states[id] == nil {
			freshIDs = append(freshIDs, id)
		}
	}
	sort.Slice(freshIDs, func(i, j int) bool {
		a, b := active[freshIDs[i]], active[freshIDs[j]]
		if fresh[freshIDs[i]] != fresh[freshIDs[j]] {
			return fresh[freshIDs[i]] > fresh[freshIDs[j]]
		}
		return eventNewer(a, b)
	})
	for _, id := range freshIDs {
		e := active[id]
		if e == nil {
			// 记忆的 event_id 指向已删/不存在的事件（阶段 4 merge/delete 的防御性缺口）
			log.Printf("[event] skip stale event_id: %s", id)
			continue
		}
		states[id] = &eventState{ev: e, mode: EventModeFull, score: fresh[id]}
	}

	// 4. 上一轮注入过、本轮没命中：同窗口粘滞 / 跨窗口 carried
	// 注意：ref.Pinned 不参与决策（见 EventRef 注释），pinned 只认 PinnedEventIDs。
	for _, ref := range opts.PrevEventRefs {
		if states[ref.ID] != nil {
			continue
		}
		e := active[ref.ID]
		if e == nil {
			continue // 已合并/删除：直接跳过，不做别名解析
		}
		st := &eventState{ev: e, score: 0}
		if opts.IsNewSession {
			st.mode = EventModeSummary
			st.carried = true
		} else {
			switch ref.Mode {
			case EventModeFull:
				st.miss = ref.Miss + 1
				if st.miss >= 2 {
					st.mode = EventModeCompact
				} else {
					st.mode = EventModeFull
				}
			case EventModeCompact:
				st.mode = EventModeCompact
			case EventModeIndex:
				st.mode = EventModeIndex
			default:
				st.mode = EventModeSummary
			}
		}
		states[ref.ID] = st
	}

	// 5. 配额：full → compact → summary → index → 移出（pinned 不降级）
	s.degrade(states, EventModeFull, EventModeCompact, s.cfg.QuotaFull)
	s.degrade(states, EventModeCompact, EventModeSummary, s.cfg.QuotaCompact)
	s.degrade(states, EventModeSummary, EventModeIndex, s.cfg.QuotaSummary)
	s.degrade(states, EventModeIndex, "", s.cfg.QuotaIndex)

	if len(states) == 0 {
		return nil, nearMiss, nil
	}

	// 6. 一次性取回所有事件的记忆，按 updated_at DESC 排（full/compact 用）
	ids := make([]string, 0, len(states))
	for id := range states {
		ids = append(ids, id)
	}
	allMems, err := s.memRepo.ListByEventIDs(ctx, userID, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("list event memories: %w", err)
	}
	memoriesByEvent := make(map[string][]model.UserMemory, len(states))
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

	// 7. 输出顺序：pinned → full → compact → summary → index，组内分数降序
	out := make([]*eventState, 0, len(states))
	for _, st := range states {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return eventBetter(out[i], out[j]) })

	packets := make([]ContextPacket, 0, len(out))
	for _, st := range out {
		mems := memoriesByEvent[st.ev.ID]
		limitMems := 0
		switch st.mode {
		case EventModeFull:
			limitMems = s.cfg.MemoriesPerEvent
		case EventModeCompact:
			limitMems = 2
		}
		if limitMems > 0 && len(mems) > limitMems {
			mems = mems[:limitMems]
		}
		if limitMems == 0 {
			mems = nil
		}
		updated := st.ev.UpdatedAt
		if st.ev.LastMemoryAt != nil {
			updated = *st.ev.LastMemoryAt
		}
		packets = append(packets, ContextPacket{
			ID:    st.ev.ID,
			Type:  EventSourceName,
			Text:  st.ev.Summary,
			Score: math.Round(st.score*10000) / 10000,
			Meta: EventMeta{
				EventID:     st.ev.ID,
				Title:       st.ev.Name,
				Summary:     st.ev.Summary,
				Mode:        st.mode,
				Score:       st.score,
				UpdatedAt:   updated,
				Pinned:      st.pinned,
				Carried:     st.carried,
				Miss:        st.miss,
				Memories:    mems,
				MemoryCount: len(memoriesByEvent[st.ev.ID]),
			},
		})
	}

	log.Printf("[event] inject: user=%s active=%d fresh=%d selected=%d (full/compact/summary/index=%d/%d/%d/%d)",
		userID, len(active), len(fresh), len(packets),
		countMode(states, EventModeFull), countMode(states, EventModeCompact),
		countMode(states, EventModeSummary), countMode(states, EventModeIndex))
	return packets, nearMiss, nil
}

// degrade 把超配额的最低优先级事件降为 to；to 为空表示移出资源区。
func (s *EventSource) degrade(states map[string]*eventState, from, to string, quota int) {
	var list []*eventState
	for _, st := range states {
		if st.mode == from {
			list = append(list, st)
		}
	}
	if len(list) <= quota {
		return
	}
	// 最低优先级先降：分数低 → 最后记忆更旧 → ID
	sort.Slice(list, func(i, j int) bool { return eventWorse(list[i], list[j]) })
	for i := 0; i < len(list)-quota; i++ {
		if list[i].pinned {
			continue
		}
		if to == "" {
			delete(states, list[i].ev.ID)
			continue
		}
		list[i].mode = to
	}
}

// loadActiveEvents 分页加载全部未删除事件（不静默截断）。
func (s *EventSource) loadActiveEvents(ctx context.Context, userID string) (map[string]*model.Event, error) {
	const pageSize = 200
	out := make(map[string]*model.Event)
	for offset := 0; ; offset += pageSize {
		page, err := s.eventRepo.List(ctx, userID, false, pageSize, offset)
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

func countMode(states map[string]*eventState, mode string) int {
	n := 0
	for _, st := range states {
		if st.mode == mode {
			n++
		}
	}
	return n
}

// eventNewer 供排序：最后记忆时间更近的在前，平局按 ID（确定性）。
func eventNewer(a, b *model.Event) bool {
	if a == nil || b == nil {
		return a != nil
	}
	ta, tb := a.UpdatedAt, b.UpdatedAt
	if a.LastMemoryAt != nil {
		ta = *a.LastMemoryAt
	}
	if b.LastMemoryAt != nil {
		tb = *b.LastMemoryAt
	}
	if !ta.Equal(tb) {
		return ta.After(tb)
	}
	return a.ID < b.ID
}

// eventBetter 输出顺序：pinned > full > compact > summary > index，再按分数/新近度。
func eventBetter(a, b *eventState) bool {
	if a.pinned != b.pinned {
		return a.pinned
	}
	ra, rb := modeRank(a.mode), modeRank(b.mode)
	if ra != rb {
		return ra < rb
	}
	if a.score != b.score {
		return a.score > b.score
	}
	return eventNewer(a.ev, b.ev)
}

// eventWorse 降级顺序：与 eventBetter 相反。
func eventWorse(a, b *eventState) bool {
	if a.pinned != b.pinned {
		return !a.pinned
	}
	ra, rb := modeRank(a.mode), modeRank(b.mode)
	if ra != rb {
		return ra > rb
	}
	if a.score != b.score {
		return a.score < b.score
	}
	return !eventNewer(a.ev, b.ev)
}

func modeRank(mode string) int {
	switch mode {
	case EventModeFull:
		return 0
	case EventModeCompact:
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
