// event-bootstrap — 事件系统一次性冷启动（设计 §8.5）。
//
// 只处理重构后的记忆（source IN post_chat/inline），按 created_at 正序分批喂给
// 与生产同一个 EventAssembler；游标落盘，断点可续。
//
// 冷启动期间必须停掉服务：session_end 的 EventWorker 会并发拼装同一批记忆。
//
// 用法:
//
//	APP_ENV=dev go run ./cmd/event-bootstrap --dry-run   # 只统计输入与现状
//	APP_ENV=dev go run ./cmd/event-bootstrap --limit 40  # 先试跑一批
//	APP_ENV=dev go run ./cmd/event-bootstrap             # 全量
//	APP_ENV=dev go run ./cmd/event-bootstrap --reset     # 清空该用户事件后重跑
//	APP_ENV=dev go run ./cmd/event-bootstrap --review    # 冷启动后做一次结构复盘（merge + 改名）
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/app"
	"xhan-helper/internal/appinit"
	"xhan-helper/internal/config"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

var bootstrapSources = []string{model.MemorySourcePostChat, model.MemorySourceInline}

type cursorFile struct {
	DB        string    `json:"db"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	MemoryID  string    `json:"memory_id"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	limit := flag.Int("limit", 0, "本次最多处理多少条记忆（0 = 全部）")
	reset := flag.Bool("reset", false, "清空该用户全部事件、记忆退回普通池，并删除游标后重跑")
	dryRun := flag.Bool("dry-run", false, "只统计输入与事件现状；不调 LLM、不改事件/记忆数据（AutoMigrate 仍会跑）")
	review := flag.Bool("review", false, "跑完后做一次结构复盘：Flash 看全部事件，输出 merge + 改名")
	pruneSingles := flag.Bool("prune-singles", false, "删除只含 0/1 条记忆的事件（记忆退回普通池）；可配 --dry-run 先看清单")
	userFlag := flag.String("user", "", "user_id（默认：本地单用户 / 库里唯一用户）")
	cursorPath := flag.String("cursor", filepath.Join("data", "event-bootstrap.cursor"), "游标文件路径")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `event-bootstrap — 事件系统一次性冷启动

用法:
  event-bootstrap [flags]

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
示例:
  APP_ENV=dev event-bootstrap --dry-run
  APP_ENV=dev event-bootstrap --limit 40
  APP_ENV=dev event-bootstrap
  APP_ENV=dev event-bootstrap --reset
  APP_ENV=dev event-bootstrap --review
  APP_ENV=dev event-bootstrap --prune-singles --dry-run
  APP_ENV=dev event-bootstrap --prune-singles
`)
	}
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := idgen.Init(cfg.Snowflake.NodeID); err != nil {
		log.Fatalf("init idgen: %v", err)
	}
	ctx := context.Background()
	db := app.InitDB(cfg)
	repos := app.InitRepos(cfg, db)

	userID, err := resolveUser(ctx, cfg, repos, *userFlag)
	if err != nil {
		log.Fatalf("resolve user: %v", err)
	}
	log.Printf("[bootstrap] db=%s user=%s sources=%v", cfg.DB.Database, userID, bootstrapSources)
	if *reset && *dryRun {
		log.Fatal("--reset 与 --dry-run 不能同时使用")
	}
	if *review && *dryRun {
		log.Fatal("--review 与 --dry-run 不能同时使用")
	}
	if !*dryRun {
		config.GuardProdWrite(cfg.DB.Database)
	}
	if *pruneSingles {
		if *reset || *review || *limit > 0 {
			log.Fatal("--prune-singles 只能单独使用（不能与 --reset / --review / --limit 同时）")
		}
		if err := pruneSingleMemoryEvents(ctx, repos, userID, *dryRun); err != nil {
			log.Fatalf("prune singles: %v", err)
		}
		return
	}

	var cur *cursorFile
	if *reset {
		events, memories, err := repos.Event.ResetUser(ctx, userID)
		if err != nil {
			log.Fatalf("reset events: %v", err)
		}
		if err := os.Remove(*cursorPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Fatalf("remove cursor: %v", err)
		}
		log.Printf("[bootstrap] reset done: events=%d memories=%d cursor removed", events, memories)
	} else if !*dryRun {
		// dry-run 只看现状，不依赖（也不校验）历史游标。
		cur, err = loadCursor(*cursorPath, cfg.DB.Database, userID)
		if err != nil {
			log.Fatalf("load cursor: %v", err)
		}
	}

	total, err := repos.Mem.CountBySources(ctx, userID, bootstrapSources)
	if err != nil {
		log.Fatalf("count memories: %v", err)
	}
	if cur != nil {
		log.Printf("[bootstrap] resume from cursor: created_at=%s id=%s", cur.CreatedAt.Format(time.RFC3339), cur.MemoryID)
	}
	if *dryRun {
		printReport(ctx, repos, userID, total)
		return
	}

	llmClients := app.InitLLM(cfg, llm.NewClient, llm.NewStrongClient, llmcost.NewDBRecorder(repos.LlmCost))
	assembler := initAssembler(ctx, cfg, repos, llmClients)

	batchSize := cfg.EventAssembly.BatchSize
	if batchSize <= 0 {
		batchSize = 40
	}
	processed := 0
	for {
		var after *time.Time
		afterID := ""
		if cur != nil {
			after = &cur.CreatedAt
			afterID = cur.MemoryID
		}
		batch, err := repos.Mem.ListBySourcesAfter(ctx, userID, bootstrapSources, after, afterID, batchSize)
		if err != nil {
			log.Fatalf("list memories: %v", err)
		}
		if len(batch) == 0 {
			break
		}
		if *limit > 0 && processed+len(batch) > *limit {
			batch = batch[:*limit-processed]
		}
		stats, err := assembler.Assemble(ctx, userID, batch)
		if err != nil {
			log.Fatalf("assemble (processed=%d): %v", processed, err)
		}
		last := batch[len(batch)-1]
		if stats.Unparsed > 0 || stats.Failed > 0 {
			// 游标停在上一批：这批没产出可用计划或写库失败，重跑会重新处理它。
			log.Fatalf("[bootstrap] batch ending %s produced no usable plan (unparsed=%d failed=%d); cursor kept, rerun to retry",
				last.CreatedAt.Format("2006-01-02 15:04:05"), stats.Unparsed, stats.Failed)
		}
		cur = &cursorFile{DB: cfg.DB.Database, UserID: userID, CreatedAt: last.CreatedAt, MemoryID: last.ID}
		if err := saveCursor(*cursorPath, cur); err != nil {
			log.Fatalf("save cursor: %v", err)
		}
		processed += len(batch)
		log.Printf("[bootstrap] progress %d/%d: created=%d assigned=%d updated=%d merged=%d rejected=%d failed=%d",
			processed, total, stats.Created, stats.Assigned, stats.Updated, stats.Merged, stats.Rejected, stats.Failed)
		if *limit > 0 && processed >= *limit {
			break
		}
	}
	log.Printf("[bootstrap] done: processed %d/%d memories", processed, total)
	printReport(ctx, repos, userID, total)
	if *review {
		if err := runReview(ctx, repos, llmClients.BgFlash, userID); err != nil {
			log.Fatalf("structure review: %v", err)
		}
		printReport(ctx, repos, userID, total)
	}
}

// pruneSingleMemoryEvents 删除只含 0/1 条记忆的事件：记忆退回普通池、事件软删。
// dryRun 只打印清单。用于清理冷启动留下的「单记忆事件」。
func pruneSingleMemoryEvents(ctx context.Context, repos *app.Repos, userID string, dryRun bool) error {
	stats, err := loadEventStats(ctx, repos, userID)
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}
	pruned, kept := 0, 0
	for _, st := range stats {
		if st.Count >= 2 {
			kept++
			continue
		}
		if dryRun {
			log.Printf("[prune] would delete %q (%s): %d memories", st.Event.Name, st.Event.ID, st.Count)
			pruned++
			continue
		}
		if err := repos.Event.Delete(ctx, userID, st.Event.ID); err != nil {
			return fmt.Errorf("delete event %s: %w", st.Event.ID, err)
		}
		log.Printf("[prune] deleted %q (%s): %d memories", st.Event.Name, st.Event.ID, st.Count)
		pruned++
	}
	log.Printf("[prune] done: pruned=%d kept=%d dry_run=%v", pruned, kept, dryRun)
	return nil
}

// resolveUser 依次尝试 --user、本地单用户、库里唯一用户。
func resolveUser(ctx context.Context, cfg *config.Config, repos *app.Repos, userFlag string) (string, error) {
	if userFlag != "" {
		users, err := repos.User.ListAll(ctx)
		if err != nil {
			return "", err
		}
		for _, u := range users {
			if u.ID == userFlag {
				return userFlag, nil
			}
		}
		return "", fmt.Errorf("user %q 不存在", userFlag)
	}
	id, err := appinit.EnsureLocalUser(cfg, repos.User)
	if err != nil {
		return "", err
	}
	if id != "" {
		return id, nil
	}
	users, err := repos.User.ListAll(ctx)
	if err != nil {
		return "", err
	}
	if len(users) == 1 {
		return users[0].ID, nil
	}
	ids := make([]string, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID+"("+u.Username+")")
	}
	return "", fmt.Errorf("无法自动确定用户（当前 %d 个：%v），请用 --user 指定", len(users), ids)
}

// initAssembler 构建与生产同源的拼装器，并预热记忆/事件向量（语义搜索与补建缺失向量）。
func initAssembler(ctx context.Context, cfg *config.Config, repos *app.Repos, llmClients *app.LLMClients) *agent.EventAssembler {
	vs := agent.NewVectorStore()
	vsAdapter := agent.NewVectorSearchAdapter(vs)
	embedder := llm.NewEmbedder(cfg.Embedding.APIKey, cfg.Embedding.BaseURL, cfg.Embedding.Model)
	infra := app.InitInfra(cfg, repos.DB, func(apiKey, baseURL, model string) *llm.Embedder { return embedder }, vsAdapter)

	chunkBuilder := agent.NewChunkBuilder(infra, repos.Chunk, repos.Embedding, cfg.Vector.ChunkDisabledParents)
	memSource := retrieval.NewMemorySource(vs, repos.Mem, repos.Embedding, repos.Chunk, chunkBuilder,
		retrieval.NewTimeDecay(cfg.Retrieval.Memory.FreshnessDecay))
	eventSource := retrieval.NewEventSource(vs, repos.Mem, repos.Event, retrieval.EventSourceConfig{
		QuotaFull:        cfg.Retrieval.ResourceZone.QuotaEventFull,
		QuotaCompact:     cfg.Retrieval.ResourceZone.QuotaEventCompact,
		QuotaSummary:     cfg.Retrieval.ResourceZone.QuotaEventSummary,
		QuotaIndex:       cfg.Retrieval.ResourceZone.QuotaEventIndex,
		PinnedMax:        cfg.Retrieval.ResourceZone.QuotaEventPinned,
		MemoriesPerEvent: cfg.Retrieval.ResourceZone.EventMemories,
	}, retrieval.NewTimeDecay(cfg.Retrieval.Event.FreshnessDecay))

	log.Printf("[bootstrap] warming memory vectors...")
	memSource.Warmup(ctx, embedder)
	agent.WarmupAll(ctx, vsAdapter, repos.Embedding, cfg.Vector.ChunkDisabledParents)

	unifiedSearch := tool.NewUnifiedSearchTool(
		map[string]retrieval.ContextSource{"memory": memSource, "event": eventSource},
		infra.Embedder, repos.Message, repos.Mem, repos.Note, repos.ConversationSummary, repos.Event,
	)
	eventTool := tool.NewEventTool(repos.Event, repos.Mem, repos.Conversation, infra.Embedder, eventSource,
		cfg.Retrieval.ResourceZone.QuotaEventPinned)
	return appinit.BuildEventAssembler(cfg, repos, infra, llmClients, unifiedSearch, eventTool)
}

// eventStat — 一个事件及其记忆条数 / 首末日期（报告与结构复盘共用）。
type eventStat struct {
	Event *model.Event
	Count int
	First time.Time
	Last  time.Time
}

// printReport 输出设计 §10.2 的冷启动指标（名称相似度需人工抽查，未计算）。
func printReport(ctx context.Context, repos *app.Repos, userID string, total int64) {
	stats, err := loadEventStats(ctx, repos, userID)
	if err != nil {
		log.Printf("[bootstrap] report: %v", err)
		return
	}
	assigned := 0
	counts := make([]int, 0, len(stats))
	over20, over3Months := 0, 0
	log.Printf("[bootstrap] 事件 %d 个；输入记忆 %d 条", len(stats), total)
	for _, st := range stats {
		assigned += st.Count
		counts = append(counts, st.Count)
		if st.Count > 20 {
			over20++
		}
		if st.Last.Sub(st.First) > 90*24*time.Hour {
			over3Months++
		}
		log.Printf("[bootstrap]   %-30s %3d 条  %s ~ %s", st.Event.Name, st.Count,
			st.First.Format("2006-01-02"), st.Last.Format("2006-01-02"))
	}
	log.Printf("[bootstrap] 已分配 %d 条，未分配 %d 条", assigned, total-int64(assigned))
	if len(counts) > 0 {
		sort.Ints(counts)
		log.Printf("[bootstrap] 每事件记忆数 p50=%d p90=%d max=%d；超 20 条 %d 个；时间跨度 >3 个月 %d 个",
			counts[len(counts)/2], counts[(len(counts)*9)/10], counts[len(counts)-1], over20, over3Months)
	}
	simPairs := 0
	for i := 0; i < len(stats); i++ {
		for j := i + 1; j < len(stats); j++ {
			if diceSimilarity(stats[i].Event.Name, stats[j].Event.Name) > 0.9 {
				simPairs++
				log.Printf("[bootstrap]   名称相似 >0.9: %q / %q", stats[i].Event.Name, stats[j].Event.Name)
			}
		}
	}
	log.Printf("[bootstrap] 事件名两两相似 >0.9 的对数：%d", simPairs)
}

// diceSimilarity 字符二元组 Dice 系数；中文事件名用字符级即可，无需额外 embedding。
func diceSimilarity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) < 2 || len(rb) < 2 {
		return 0
	}
	counts := make(map[string]int, len(ra)-1)
	for i := 0; i < len(ra)-1; i++ {
		counts[string(ra[i:i+2])]++
	}
	inter := 0
	for i := 0; i < len(rb)-1; i++ {
		k := string(rb[i : i+2])
		if counts[k] > 0 {
			counts[k]--
			inter++
		}
	}
	return float64(2*inter) / float64((len(ra)-1)+(len(rb)-1))
}

func loadEventStats(ctx context.Context, repos *app.Repos, userID string) ([]eventStat, error) {
	events, err := listAllEvents(ctx, repos.Event, userID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(events))
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	memories, err := repos.Mem.ListByEventIDs(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	stats := make([]eventStat, len(events))
	byID := make(map[string]*eventStat, len(events))
	for i := range events {
		stats[i] = eventStat{Event: &events[i]}
		byID[events[i].ID] = &stats[i]
	}
	for _, m := range memories {
		if m.EventID == nil {
			continue
		}
		st := byID[*m.EventID]
		if st == nil {
			continue
		}
		if st.Count == 0 || m.CreatedAt.Before(st.First) {
			st.First = m.CreatedAt
		}
		if m.CreatedAt.After(st.Last) {
			st.Last = m.CreatedAt
		}
		st.Count++
	}
	return stats, nil
}

const eventReviewPrompt = `你是「事件结构复盘」助手。下面是冷启动后生成的全部事件（id / 名称 / 记忆数 / 首末日期 / summary）。

请找出两类问题并输出修改方案：
1. 两个事件其实在说同一件事 → merge（into 保留名字更准、记忆更多的那一边）；
2. 事件名是拿最早那条记忆起的，已经不符合现在的内容 → 改名。

不要因为同属一个大项目就合并；只在「用户会认为是同一件事」时合并。同一批里不要写链式合并（A→B 又 B→C）。
只输出纯 JSON，不要 markdown 代码块：
{"merge":[{"from":"id","into":"id"}],"event_updates":[{"event_id":"id","name":"新名字"}]}
没有要改的就输出 {"merge":[],"event_updates":[]}。`

type reviewPlan struct {
	Merge []struct {
		From string `json:"from"`
		Into string `json:"into"`
	} `json:"merge"`
	EventUpdates []struct {
		EventID string `json:"event_id"`
		Name    string `json:"name"`
	} `json:"event_updates"`
}

// runReview 结构复盘：Flash 看全部事件，输出 merge + 改名并落库（设计 §8.5）。
func runReview(ctx context.Context, repos *app.Repos, client llm.LLMClient, userID string) error {
	stats, err := loadEventStats(ctx, repos, userID)
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}
	var b strings.Builder
	b.WriteString("<events>\n")
	for _, st := range stats {
		fmt.Fprintf(&b, `<event id="%s" name="%s" memories="%d" first="%s" last="%s">%s</event>`+"\n",
			st.Event.ID, st.Event.Name, st.Count, st.First.Format("2006-01-02"), st.Last.Format("2006-01-02"),
			truncateRunes(st.Event.Summary, 300))
	}
	b.WriteString("</events>\n请输出复盘 JSON。")

	msg, err := client.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: eventReviewPrompt,
		Messages:     []llm.Message{{Role: "user", Content: b.String()}},
	})
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}
	var plan reviewPlan
	if _, err := agent.ParseFlashJSON(msg.Content, &plan); err != nil {
		return fmt.Errorf("parse review JSON: %w (raw=%s)", err, truncateRunes(msg.Content, 200))
	}

	active := make(map[string]*model.Event, len(stats))
	for _, st := range stats {
		active[st.Event.ID] = st.Event
	}
	// 禁止链式合并：source 同时是 target 时整组拒绝（与拼装写库口径一致）。
	targets := make(map[string]bool, len(plan.Merge))
	for _, m := range plan.Merge {
		targets[m.Into] = true
	}
	merged, renamed, skipped := 0, 0, 0
	for _, m := range plan.Merge {
		if targets[m.From] {
			log.Printf("[review] merge rejected: chain (%s is also a target), skip all merges", m.From)
			plan.Merge = nil
			skipped++
			break
		}
	}
	for _, m := range plan.Merge {
		if active[m.From] == nil || active[m.Into] == nil || m.From == m.Into {
			log.Printf("[review] merge skipped (unknown/self): %s → %s", m.From, m.Into)
			skipped++
			continue
		}
		if err := repos.Event.Merge(ctx, userID, m.From, m.Into); err != nil {
			log.Printf("[review] merge %s → %s error: %v", m.From, m.Into, err)
			skipped++
			continue
		}
		delete(active, m.From)
		merged++
	}
	for _, u := range plan.EventUpdates {
		name := strings.TrimSpace(u.Name)
		if name == "" || active[u.EventID] == nil {
			continue
		}
		dup := false
		for id, e := range active {
			if id != u.EventID && strings.EqualFold(strings.TrimSpace(e.Name), name) {
				dup = true
				break
			}
		}
		if dup {
			log.Printf("[review] rename %s → %q skipped: duplicate name", u.EventID, name)
			skipped++
			continue
		}
		if err := repos.Event.UpdateSummary(ctx, userID, u.EventID, name, ""); err != nil {
			log.Printf("[review] rename %s error: %v", u.EventID, err)
			skipped++
			continue
		}
		active[u.EventID].Name = name
		renamed++
	}
	log.Printf("[review] done: merged=%d renamed=%d skipped=%d", merged, renamed, skipped)
	if skipped > 0 {
		return fmt.Errorf("%d 项复盘操作未应用，见上方日志", skipped)
	}
	return nil
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func listAllEvents(ctx context.Context, repo *service.EventRepo, userID string) ([]model.Event, error) {
	const pageSize = 200
	var out []model.Event
	for offset := 0; ; offset += pageSize {
		page, err := repo.List(ctx, userID, false, pageSize, offset)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < pageSize {
			return out, nil
		}
	}
}

func loadCursor(path, dbName, userID string) (*cursorFile, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c cursorFile
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.DB != dbName || c.UserID != userID {
		return nil, fmt.Errorf("游标属于 db=%s user=%s，与当前 db=%s user=%s 不一致；确认后用 --reset 重跑或删除 %s",
			c.DB, c.UserID, dbName, userID, path)
	}
	return &c, nil
}

func saveCursor(path string, c *cursorFile) error {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
