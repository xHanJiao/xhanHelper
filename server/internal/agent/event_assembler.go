package agent

import (
	"context"
	"fmt"
	"html"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// EventAssembler — 把一批新记忆归到事件（session_end 的 EventWorker 与冷启动共用）。
//
// 循环（MiniReAct）全程只读，所有写库都在最终 JSON 解析之后一次性做：
// 循环失败 / 超轮次 = 什么都不发生，记忆留在普通池，下个 session 自然重试。
type EventAssembler struct {
	flashClient   llm.LLMClient
	registry      *tool.Registry
	eventRepo     *service.EventRepo
	memRepo       *service.UserMemoryRepo
	embeddingRepo *service.EmbeddingRepo
	embedder      app.Embedder

	candidateTop  int
	dedupeSim     float64
	maxIterations int
	batchSize     int

	// event summary 向量缓存：创建前查重需要与现有事件 summary 比余弦，
	// 事件数少且 summary 变化不频繁，缓存在内存里避免每批重复调用 embedding。
	cacheMu  sync.Mutex
	vecCache map[string]cachedEventVec
}

type cachedEventVec struct {
	text string
	vec  []float64
}

// EventAssemblerConfig — EventAssembler 依赖与阈值。
type EventAssemblerConfig struct {
	FlashClient   llm.LLMClient
	Registry      *tool.Registry
	EventRepo     *service.EventRepo
	MemRepo       *service.UserMemoryRepo
	EmbeddingRepo *service.EmbeddingRepo
	Embedder      app.Embedder

	CandidateTop  int
	DedupeSim     float64
	MaxIterations int
	BatchSize     int
}

// NewEventAssembler 创建拼装器。必需依赖为 nil 时 panic（接线 bug 暴露在明处）。
func NewEventAssembler(cfg *EventAssemblerConfig) *EventAssembler {
	if cfg.FlashClient == nil || cfg.Registry == nil || cfg.EventRepo == nil ||
		cfg.MemRepo == nil || cfg.EmbeddingRepo == nil || cfg.Embedder == nil {
		panic("event assembler: required dependency nil (wiring bug)")
	}
	if cfg.CandidateTop <= 0 {
		cfg.CandidateTop = 10
	}
	if cfg.DedupeSim <= 0 {
		cfg.DedupeSim = 0.88
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 6
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 40
	}
	return &EventAssembler{
		flashClient:   cfg.FlashClient,
		registry:      cfg.Registry,
		eventRepo:     cfg.EventRepo,
		memRepo:       cfg.MemRepo,
		embeddingRepo: cfg.EmbeddingRepo,
		embedder:      cfg.Embedder,
		candidateTop:  cfg.CandidateTop,
		dedupeSim:     cfg.DedupeSim,
		maxIterations: cfg.MaxIterations,
		batchSize:     cfg.BatchSize,
		vecCache:      make(map[string]cachedEventVec),
	}
}

// AssemblyStats — 一次拼装的计数（日志 / 冷启动指标）。
type AssemblyStats struct {
	Memories int
	Batches  int
	Created  int
	Assigned int
	Updated  int
	Merged   int
	Rejected int // 被创建校验拒绝的新事件数
}

// Assemble 处理一批记忆（内部按 batchSize 分批，全部处理）。
func (a *EventAssembler) Assemble(ctx context.Context, userID string, memories []model.UserMemory) (AssemblyStats, error) {
	stats := AssemblyStats{Memories: len(memories)}
	if len(memories) == 0 {
		return stats, nil
	}
	for start := 0; start < len(memories); start += a.batchSize {
		end := min(start+a.batchSize, len(memories))
		batch := memories[start:end]
		stats.Batches++
		st, err := a.assembleBatch(ctx, userID, batch)
		stats.Created += st.Created
		stats.Assigned += st.Assigned
		stats.Updated += st.Updated
		stats.Merged += st.Merged
		stats.Rejected += st.Rejected
		if err != nil {
			return stats, fmt.Errorf("event assemble batch %d/%d: %w", stats.Batches, (len(memories)+a.batchSize-1)/a.batchSize, err)
		}
	}
	return stats, nil
}

// assembleBatch 单批：加载候选 → ReAct → 解析 → 写库。
func (a *EventAssembler) assembleBatch(ctx context.Context, userID string, batch []model.UserMemory) (AssemblyStats, error) {
	active, err := a.loadActiveEvents(ctx, userID)
	if err != nil {
		return AssemblyStats{}, err
	}
	candidates, err := a.buildCandidates(ctx, userID, batch, active)
	if err != nil {
		return AssemblyStats{}, err
	}

	userMsg := buildAssemblyUserMessage(batch, candidates)
	react := NewMiniReAct(&MiniReActConfig{
		Client:           a.flashClient,
		Tools:            a.registry.GetLLMToolsFiltered([]string{"unified_search"}),
		ToolRegistry:     a.registry,
		SystemPrompt:     eventAssemblySystemPrompt,
		FinalRoundPrompt: eventAssemblyFinalRoundPrompt,
		UserID:           userID,
		MaxIterations:    a.maxIterations,
		MaxToolResult:    2000,
		Caller:           "event-assemble",
		// 拼装循环必须只读：即使 LLM 幻觉出别的工具名，也在这里被白名单拦掉。
		SubAgent: SubAgentExt{AllowedTools: map[string][]string{"unified_search": nil}},
	})
	result, err := react.Run(ctx, userMsg)
	if err != nil {
		return AssemblyStats{}, err
	}
	if result.TerminatedEarly || strings.TrimSpace(result.FinalContent) == "" {
		log.Printf("[event] assemble: no final JSON (iterations=%d), skip batch", result.Iterations)
		return AssemblyStats{}, nil
	}
	var plan assemblyPlan
	if _, err := ParseFlashJSON(result.FinalContent, &plan); err != nil {
		log.Printf("[event] assemble: final JSON parse error: %v, raw=%s", err, truncateStr(result.FinalContent, 200))
		return AssemblyStats{}, nil // 不写库，记忆留在普通池
	}

	batchByID := make(map[string]model.UserMemory, len(batch))
	for _, m := range batch {
		batchByID[m.ID] = m
	}
	return a.applyPlan(ctx, userID, &plan, batchByID, active), nil
}

// loadActiveEvents 加载全部未删除事件（含已删的不要，创建校验只对比存活事件）。
func (a *EventAssembler) loadActiveEvents(ctx context.Context, userID string) (map[string]*model.Event, error) {
	list, err := a.eventRepo.List(ctx, userID, false, 200, 0)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	out := make(map[string]*model.Event, len(list))
	for i := range list {
		out[list[i].ID] = &list[i]
	}
	return out, nil
}

// buildCandidates 计算注入给 LLM 的候选事件：
// 新记忆向量 × 事件记忆向量的 max 余弦 top N，再补「最近 7 天有更新」的事件。
func (a *EventAssembler) buildCandidates(ctx context.Context, userID string, batch []model.UserMemory, active map[string]*model.Event) ([]assemblyCandidate, error) {
	if len(active) == 0 {
		return nil, nil
	}
	eventIDs := make([]string, 0, len(active))
	for id := range active {
		eventIDs = append(eventIDs, id)
	}
	memories, err := a.memRepo.ListByEventIDs(ctx, userID, eventIDs)
	if err != nil {
		return nil, fmt.Errorf("list event memories: %w", err)
	}
	byEvent := make(map[string][]string, len(active))
	allMemIDs := make([]string, 0, len(memories))
	for _, m := range memories {
		if m.EventID != nil {
			byEvent[*m.EventID] = append(byEvent[*m.EventID], m.ID)
			allMemIDs = append(allMemIDs, m.ID)
		}
	}

	newIDs := make([]string, 0, len(batch))
	for _, m := range batch {
		newIDs = append(newIDs, m.ID)
	}
	newVecs, err := a.loadVectors(ctx, newIDs)
	if err != nil {
		log.Printf("[event] load new memory vectors error (fallback to recency): %v", err)
	}
	eventVecs, err := a.loadVectors(ctx, allMemIDs)
	if err != nil {
		log.Printf("[event] load event memory vectors error (fallback to recency): %v", err)
		eventVecs = nil
	}

	recentCutoff := time.Now().Add(-7 * 24 * time.Hour)
	candidates := make([]assemblyCandidate, 0, len(active))
	for id, e := range active {
		c := assemblyCandidate{Event: e, MemoryCount: len(byEvent[id])}
		if e.LastMemoryAt != nil && e.LastMemoryAt.After(recentCutoff) {
			c.Recent = true
		}
		for _, newVec := range newVecs {
			for _, memID := range byEvent[id] {
				if ev := eventVecs[memID]; ev != nil {
					if sim := cosineSimilarity(newVec, ev); sim > c.Sim {
						c.Sim = sim
					}
				}
			}
		}
		candidates = append(candidates, c)
	}

	// 相似度优先；同分按最近记忆时间倒序。
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Sim != candidates[j].Sim {
			return candidates[i].Sim > candidates[j].Sim
		}
		ti, tj := lastMemoryTime(candidates[i].Event), lastMemoryTime(candidates[j].Event)
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return candidates[i].Event.ID < candidates[j].Event.ID
	})
	top := candidates
	if len(top) > a.candidateTop {
		top = top[:a.candidateTop]
	}

	// 补最近 7 天有更新但没进 top 的事件（按 last_memory_at 倒序追加）。
	inTop := make(map[string]bool, len(top))
	for _, c := range top {
		inTop[c.Event.ID] = true
	}
	recent := make([]assemblyCandidate, 0)
	for _, c := range candidates {
		if c.Recent && !inTop[c.Event.ID] {
			recent = append(recent, c)
		}
	}
	sort.Slice(recent, func(i, j int) bool {
		return lastMemoryTime(recent[i].Event).After(lastMemoryTime(recent[j].Event))
	})
	return append(top, recent...), nil
}

// loadVectors 批量读取记忆向量（缺失/解析失败的单条跳过并记日志）。
func (a *EventAssembler) loadVectors(ctx context.Context, memoryIDs []string) (map[string][]float64, error) {
	out := make(map[string][]float64, len(memoryIDs))
	if len(memoryIDs) == 0 {
		return out, nil
	}
	rows, err := a.embeddingRepo.ListByEntities(ctx, model.EntityTypeMemory, memoryIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.Embedding == nil {
			continue
		}
		vec, err := llm.StringToEmbedding(*row.Embedding)
		if err != nil {
			log.Printf("[event] parse memory vector %s error: %v", row.EntityID, err)
			continue
		}
		out[row.EntityID] = vec
	}
	return out, nil
}

func lastMemoryTime(e *model.Event) time.Time {
	if e.LastMemoryAt != nil {
		return *e.LastMemoryAt
	}
	return time.Time{}
}

// ==================== 输出结构 ====================

type assemblyCandidate struct {
	Event       *model.Event
	MemoryCount int
	Recent      bool
	Sim         float64
}

type assemblyAssignment struct {
	MemoryID string `json:"memory_id"`
	EventID  string `json:"event_id"`
}

type assemblyNewEvent struct {
	Name      string   `json:"name"`
	Summary   string   `json:"summary"`
	MemoryIDs []string `json:"memory_ids"`
}

type assemblyEventUpdate struct {
	EventID string `json:"event_id"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

type assemblyMerge struct {
	From string `json:"from"`
	Into string `json:"into"`
}

type assemblyPlan struct {
	Assignments  []assemblyAssignment  `json:"assignments"`
	NewEvents    []assemblyNewEvent    `json:"new_events"`
	EventUpdates []assemblyEventUpdate `json:"event_updates"`
	Merge        []assemblyMerge       `json:"merge"`
}

// ==================== 写库 ====================

// applyPlan 按固定顺序写库：merge → new_events → assignments → event_updates。
// merge 产生的旧 ID 在本批内重映射到新 ID（不留持久别名）。
func (a *EventAssembler) applyPlan(ctx context.Context, userID string, plan *assemblyPlan, batch map[string]model.UserMemory, active map[string]*model.Event) AssemblyStats {
	var stats AssemblyStats
	aliases := a.applyMerges(ctx, userID, plan.Merge, active, &stats)
	resolve := func(id string) string {
		for i := 0; i < 10; i++ {
			next, ok := aliases[id]
			if !ok {
				break
			}
			id = next
		}
		return id
	}

	a.applyNewEvents(ctx, userID, plan.NewEvents, batch, active, &stats)
	a.applyAssignments(ctx, userID, plan.Assignments, batch, active, resolve, &stats)
	a.applyEventUpdates(ctx, userID, plan.EventUpdates, active, resolve, &stats)
	return stats
}

func (a *EventAssembler) applyMerges(ctx context.Context, userID string, merges []assemblyMerge, active map[string]*model.Event, stats *AssemblyStats) map[string]string {
	aliases := make(map[string]string)
	// 同一批里 target 不能再当 source（禁止链式合并）。
	targets := make(map[string]bool, len(merges))
	for _, m := range merges {
		if id := strings.TrimSpace(m.Into); id != "" {
			targets[id] = true
		}
	}
	for _, m := range merges {
		from, into := strings.TrimSpace(m.From), strings.TrimSpace(m.Into)
		if from == "" || into == "" || from == into {
			continue
		}
		if targets[from] {
			log.Printf("[event] merge rejected (chain): %s → %s", from, into)
			continue
		}
		if active[from] == nil || active[into] == nil {
			log.Printf("[event] merge skipped (unknown event): %s → %s", from, into)
			continue
		}
		if err := a.eventRepo.Merge(ctx, userID, from, into); err != nil {
			log.Printf("[event] merge %s → %s error: %v", from, into, err)
			continue
		}
		delete(active, from)
		aliases[from] = into
		stats.Merged++
	}
	return aliases
}

func (a *EventAssembler) applyNewEvents(ctx context.Context, userID string, newEvents []assemblyNewEvent, batch map[string]model.UserMemory, active map[string]*model.Event, stats *AssemblyStats) {
	names := make(map[string]bool, len(active))
	for _, e := range active {
		names[normalizeEventName(e.Name)] = true
	}
	for _, ne := range newEvents {
		name := strings.TrimSpace(ne.Name)
		if name == "" {
			continue
		}
		var ids []string
		for _, id := range ne.MemoryIDs {
			if _, ok := batch[id]; ok {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			log.Printf("[event] new event %q rejected: no batch memory", name)
			stats.Rejected++
			continue
		}
		norm := normalizeEventName(name)
		if names[norm] {
			log.Printf("[event] new event %q rejected: duplicate name", name)
			stats.Rejected++
			continue
		}
		dup, vec, embedErr := a.duplicateBySummary(ctx, name, ne.Summary, active)
		if embedErr != nil {
			log.Printf("[event] new event %q dedupe embed error (skip embed check): %v", name, embedErr)
		} else if dup != "" {
			log.Printf("[event] new event %q rejected: summary similar to %q", name, active[dup].Name)
			stats.Rejected++
			continue
		}

		e := &model.Event{UserID: userID, Name: name, Summary: truncateRunes(strings.TrimSpace(ne.Summary), eventSummaryMaxRunes)}
		if err := a.eventRepo.CreateWithMemories(ctx, e, ids); err != nil {
			log.Printf("[event] create event %q error: %v", name, err)
			continue
		}
		active[e.ID] = e
		names[norm] = true
		if embedErr == nil && vec != nil {
			a.cacheVec(e.ID, e.Name+"\n"+e.Summary, vec)
		}
		stats.Created++
	}
}

func (a *EventAssembler) applyAssignments(ctx context.Context, userID string, assignments []assemblyAssignment, batch map[string]model.UserMemory, active map[string]*model.Event, resolve func(string) string, stats *AssemblyStats) {
	grouped := make(map[string][]string)
	seen := make(map[string]bool, len(assignments))
	for _, as := range assignments {
		memID := strings.TrimSpace(as.MemoryID)
		if _, ok := batch[memID]; !ok {
			continue
		}
		// 一条记忆只归一个事件：同一批里重复出现时以第一条为准（避免 map 遍历顺序决定归属）。
		if seen[memID] {
			continue
		}
		eid := resolve(strings.TrimSpace(as.EventID))
		if active[eid] == nil {
			log.Printf("[event] assignment skipped: memory %s → unknown event %s", memID, as.EventID)
			continue
		}
		seen[memID] = true
		grouped[eid] = append(grouped[eid], memID)
	}
	for eid, ids := range grouped {
		if err := a.eventRepo.AssignMemories(ctx, userID, eid, ids); err != nil {
			log.Printf("[event] assign %d memories → %s error: %v", len(ids), eid, err)
			continue
		}
		stats.Assigned += len(ids)
	}
}

func (a *EventAssembler) applyEventUpdates(ctx context.Context, userID string, updates []assemblyEventUpdate, active map[string]*model.Event, resolve func(string) string, stats *AssemblyStats) {
	for _, up := range updates {
		eid := resolve(strings.TrimSpace(up.EventID))
		e := active[eid]
		if e == nil {
			continue
		}
		if err := a.eventRepo.UpdateSummary(ctx, userID, eid, up.Name, truncateRunes(up.Summary, eventSummaryMaxRunes)); err != nil {
			log.Printf("[event] update summary %s error: %v", eid, err)
			continue
		}
		if n := strings.TrimSpace(up.Name); n != "" {
			e.Name = n
		}
		if strings.TrimSpace(up.Summary) != "" {
			e.Summary = up.Summary
		}
		a.invalidateCachedVec(eid)
		stats.Updated++
	}
}

// duplicateBySummary 新事件 name+summary 与现有事件 summary 的余弦 ≥ dedupeSim 时返回重复事件 ID。
// 同时返回新事件文本的向量，供创建成功后写入缓存（同批后续新事件直接复用）。
// embedding 失败只降级为「不做语义查重」（名称精确查重仍然生效），不阻断拼装。
func (a *EventAssembler) duplicateBySummary(ctx context.Context, name, summary string, active map[string]*model.Event) (string, []float64, error) {
	text := strings.TrimSpace(name + "\n" + summary)
	vec, err := a.embedder.Embed(ctx, text)
	if err != nil {
		return "", nil, err
	}
	var bestID string
	var best float64
	for id, e := range active {
		ev, err := a.eventVec(ctx, e)
		if err != nil {
			log.Printf("[event] dedupe embed event %s error: %v", id, err)
			continue
		}
		if sim := cosineSimilarity(vec, ev); sim > best {
			best, bestID = sim, id
		}
	}
	if best >= a.dedupeSim {
		return bestID, vec, nil
	}
	return "", vec, nil
}

func (a *EventAssembler) eventVec(ctx context.Context, e *model.Event) ([]float64, error) {
	text := e.Name + "\n" + e.Summary
	a.cacheMu.Lock()
	c, ok := a.vecCache[e.ID]
	a.cacheMu.Unlock()
	if ok && c.text == text {
		return c.vec, nil
	}
	vec, err := a.embedder.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	a.cacheMu.Lock()
	a.vecCache[e.ID] = cachedEventVec{text: text, vec: vec}
	a.cacheMu.Unlock()
	return vec, nil
}

func (a *EventAssembler) cacheVec(eventID, text string, vec []float64) {
	a.cacheMu.Lock()
	a.vecCache[eventID] = cachedEventVec{text: text, vec: vec}
	a.cacheMu.Unlock()
}

func (a *EventAssembler) invalidateCachedVec(eventID string) {
	a.cacheMu.Lock()
	delete(a.vecCache, eventID)
	a.cacheMu.Unlock()
}

// normalizeEventName 归一化事件名用于精确查重：小写、去掉空白与标点/符号。
func normalizeEventName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// eventSummaryMaxRunes — summary 硬上限（设计 §4.4：≤200 字）。
const eventSummaryMaxRunes = 200

// ==================== Prompt ====================

const eventAssemblySystemPrompt = `你是「事件拼装」助手。把本批新记忆归到现实里同一件事的事件下；必要时新建事件或合并重复事件。

—————— 什么是事件 ——————
- 现实里持续存在、用户主动跟进的一件事（如「回西安准备」「驾照考取」「求职」）。
- 不建：习惯（睡眠/记账/健身打卡）、兴趣、一次性任务、任务过程中的碎念、纯情绪。
- 事件名用用户自己的说法，尽量具体；不写目标、完成条件、参与方。

—————— 工具 ——————
unified_search（只读）：候选里没有、但怀疑某条记忆属于别的事件时，用 semantic_search/keyword_search 搜记忆。
候选里已有 sim ≥ 0.75 且明显是同一件事 → 直接分配，不要搜。
只有「像事件但候选都不匹配」或「怀疑两个事件重复」才允许搜。最多 6 轮。

—————— 输出 ——————
信息足够后，输出纯 JSON（不要 markdown 代码块）：
{"assignments":[{"memory_id":"...","event_id":"..."}],
 "new_events":[{"name":"...","summary":"...","memory_ids":["..."]}],
 "event_updates":[{"event_id":"...","name":"...","summary":"..."}],
 "merge":[{"from":"...","into":"..."}]}

规则：
- 一条记忆最多归一个事件；归不进且不是事件性质的，不要分配，留在普通记忆池。
- new_events 只放真实事件，memory_ids 必须来自本批新记忆；没有合适记忆就不要建。
- event_updates 用来重写被改动事件的 summary（≤200 字，写当前状态，未定的事写在里面）；只改名时 name 必填、summary 留空。
- 记忆的 current_event_id 非空表示它已属于该事件；内容有变化时把该事件放进 event_updates。
- merge 只在两个事件确实重复时用；同一批里不要写链式合并（A→B 又 B→C）。
- 没有需要做的操作就输出 {"assignments":[],"new_events":[],"event_updates":[],"merge":[]}。`

const eventAssemblyFinalRoundPrompt = `[系统] 本轮已达最大步数限制。请不要再调用任何工具，直接根据已掌握的信息输出拼装 JSON。
格式：{"assignments":[],"new_events":[],"event_updates":[],"merge":[]}。`

func buildAssemblyUserMessage(batch []model.UserMemory, candidates []assemblyCandidate) string {
	var b strings.Builder
	b.WriteString("<task>把下面这批新记忆归到合适的事件。候选优先；确实不属于任何已有事件、且是真实事件时才新建；发现重复事件才 merge。</task>\n")
	b.WriteString("<candidates>\n")
	if len(candidates) == 0 {
		b.WriteString("（暂无已有事件）\n")
	}
	for _, c := range candidates {
		last := ""
		if c.Event.LastMemoryAt != nil {
			last = c.Event.LastMemoryAt.Format("2006-01-02")
		}
		fmt.Fprintf(&b, `<event id="%s" name="%s" memory_count="%d" last_memory_at="%s" sim="%.2f"%s>`+"\n",
			html.EscapeString(c.Event.ID), html.EscapeString(c.Event.Name), c.MemoryCount, last, c.Sim,
			recentAttr(c.Recent))
		b.WriteString(html.EscapeString(truncateStr(c.Event.Summary, 300)))
		b.WriteString("\n</event>\n")
	}
	b.WriteString("</candidates>\n<new_memories>\n")
	for _, m := range batch {
		cur := ""
		if m.EventID != nil {
			cur = *m.EventID
		}
		fmt.Fprintf(&b, `<memory id="%s" date="%s" topic="%s" current_event_id="%s">%s</memory>`+"\n",
			html.EscapeString(m.ID), m.CreatedAt.Format("2006-01-02"), html.EscapeString(m.Topic), html.EscapeString(cur),
			html.EscapeString(m.MemoryValue))
	}
	b.WriteString("</new_memories>\n请输出拼装 JSON。")
	return b.String()
}

func recentAttr(recent bool) string {
	if recent {
		return ` recent="true"`
	}
	return ""
}
