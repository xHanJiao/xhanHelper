package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"xhan-helper/internal/config"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/model"
	"xhan-helper/internal/profilefield"
	"xhan-helper/internal/service"
)

// 画像每周整合重写（docs/design/user-profile-v2/design.md §4.5）。
//
// 增量（session_end）负责及时，重写负责收敛：把「事件 → 记忆 → 现有画像」重新压缩一遍，
// 让零散条目归并、让被后来的事实推翻的旧条目退出。触发条件写死常量，不加配置项：
// 距上次重写 ≥7 天，且近 7 天新增事件 ≥3 或新增会话 ≥5。
const (
	rewriteInterval     = 7 * 24 * time.Hour
	rewriteWindow       = 7 * 24 * time.Hour
	rewriteMinEvents    = 3
	rewriteMinSessions  = 5
	rewriteEventLimit   = 50
	rewriteMemoryLimit  = 30
	rewriteSystemPrompt = "你是用户画像重建助手。只输出 JSON。"
)

// rewriteSkipFields — 整字段跳过（LLM 输出被丢弃）：重要关系留给未来的事件/实体机制，
// 在那之前一个字节都不动。
var rewriteSkipFields = map[string]bool{"重要关系": true}

// RewriteOptions — 单次重写的行为开关。
type RewriteOptions struct {
	DryRun bool // 只算 diff，不落库（也不备份）
	Force  bool // 忽略"是否到期"判定（CLI 手工重建用）
}

// RewriteResult — 单次重写的结果（给日志与 CLI 展示）。
type RewriteResult struct {
	Skipped       string // 非空 = 本次未执行（原因）
	Diff          string // 字段级 diff（人看的）
	ChangedFields int    // 有变化的字段数
	AppliedFields int    // 实际落库的字段数
	BackupPath    string // 落库前的备份文件
	EventCount    int
	SessionCount  int
	FactCount     int // 落库后生效事实条数
}

// ProfileConsolidatorConfig — ProfileConsolidator 依赖配置。必需依赖为 nil 时 panic。
type ProfileConsolidatorConfig struct {
	Client    llm.LLMClient
	FactRepo  *service.UserProfileFactRepo
	EventRepo *service.EventRepo
	MemRepo   *service.UserMemoryRepo
	ConvRepo  *service.ConversationRepo
	UserRepo  *service.UserRepo
	StateRepo *service.ProfileRewriteStateRepo
	BackupDir string // 落库前备份目录（含真实个人数据）
	DBName    string // 备份文件里记录用
}

// ProfileConsolidator — 画像每周整合重写。
type ProfileConsolidator struct {
	client    llm.LLMClient
	factRepo  *service.UserProfileFactRepo
	eventRepo *service.EventRepo
	memRepo   *service.UserMemoryRepo
	convRepo  *service.ConversationRepo
	userRepo  *service.UserRepo
	stateRepo *service.ProfileRewriteStateRepo
	backupDir string
	dbName    string
}

// NewProfileConsolidator 创建重写器；缺依赖说明接线 bug，直接 panic 炸在明处。
func NewProfileConsolidator(cfg *ProfileConsolidatorConfig) *ProfileConsolidator {
	switch {
	case cfg.Client == nil:
		panic("profile consolidate: Client is nil (wiring bug)")
	case cfg.FactRepo == nil:
		panic("profile consolidate: FactRepo is nil (wiring bug)")
	case cfg.EventRepo == nil:
		panic("profile consolidate: EventRepo is nil (wiring bug)")
	case cfg.MemRepo == nil:
		panic("profile consolidate: MemRepo is nil (wiring bug)")
	case cfg.ConvRepo == nil:
		panic("profile consolidate: ConvRepo is nil (wiring bug)")
	case cfg.UserRepo == nil:
		panic("profile consolidate: UserRepo is nil (wiring bug)")
	case cfg.StateRepo == nil:
		panic("profile consolidate: StateRepo is nil (wiring bug)")
	}
	return &ProfileConsolidator{
		client:    cfg.Client,
		factRepo:  cfg.FactRepo,
		eventRepo: cfg.EventRepo,
		memRepo:   cfg.MemRepo,
		convRepo:  cfg.ConvRepo,
		userRepo:  cfg.UserRepo,
		stateRepo: cfg.StateRepo,
		backupDir: cfg.BackupDir,
		dbName:    cfg.DBName,
	}
}

// Run 遍历全部用户，跑到期的重写。单用户失败只记日志，不影响其它用户与 cron 其它钩子。
func (c *ProfileConsolidator) Run(ctx context.Context) {
	users, err := c.userRepo.ListAll(ctx)
	if err != nil {
		log.Printf("[profile-rewrite] list users error: %v", err)
		return
	}
	for _, u := range users {
		res, err := c.Rewrite(ctx, u.ID, RewriteOptions{})
		if err != nil {
			log.Printf("[profile-rewrite] user=%s failed: %v", shortID(u.ID), err)
			continue
		}
		if res.Skipped != "" {
			log.Printf("[profile-rewrite] user=%s skipped: %s", shortID(u.ID), res.Skipped)
			continue
		}
		log.Printf("[profile-rewrite] user=%s done: fields=%d changed=%d facts=%d events=%d sessions=%d backup=%s",
			shortID(u.ID), res.AppliedFields, res.ChangedFields, res.FactCount, res.EventCount, res.SessionCount, res.BackupPath)
	}
}

// Rewrite 对单个用户跑一次重写（dry-run 时只返回 diff）。
//
// 到期判定用状态表里的 last_run_at；失败时记 last_error 但不推进 last_run_at，
// 下次节拍自动重试。source=user 行与 rejected 行由 ReconcileFieldFacts 保护，这里不重复判断。
func (c *ProfileConsolidator) Rewrite(ctx context.Context, userID string, opts RewriteOptions) (RewriteResult, error) {
	var res RewriteResult

	all, err := c.factRepo.ListAllByUser(ctx, userID)
	if err != nil {
		return res, fmt.Errorf("profile rewrite: list facts: %w", err)
	}
	if countActiveFacts(all) == 0 {
		res.Skipped = "没有生效的画像事实"
		return res, nil
	}

	now := time.Now().In(config.BJ)
	state, err := c.stateRepo.Get(ctx, userID)
	if err != nil {
		return res, fmt.Errorf("profile rewrite: get state: %w", err)
	}
	var lastRun time.Time
	if state != nil && state.LastRunAt != nil {
		lastRun = state.LastRunAt.In(config.BJ)
	}

	// 首次运行（没有状态行）只记基线、不重写：避免上线/重启那一刻静默改写画像，
	// 让人先有一次手工 dry-run / --write 确认的机会。手工 --write 会把 last_run_at 推到 now，
	// 之后满 7 天再由本 worker 接管。
	if !opts.Force && state == nil {
		runAt := now
		c.saveState(ctx, &model.ProfileRewriteState{
			UserID:        userID,
			LastRunAt:     &runAt,
			LastFactCount: countActiveFacts(all),
			LastStatus:    model.ProfileRewriteStatusOK,
		})
		res.FactCount = countActiveFacts(all)
		res.Skipped = "首次运行：只记录基线，未重写（先手工 make profile-rebuild --write 确认）"
		return res, nil
	}

	in, err := c.collectInput(ctx, userID, now, lastRun, opts.Force)
	if err != nil {
		return res, err
	}
	res.EventCount = len(in.events)
	res.SessionCount = int(in.sessions)
	if in.skip != "" {
		res.Skipped = in.skip
		return res, nil
	}

	rebuilt, err := c.callLLM(ctx, userID, all, in.events, in.memories)
	if err != nil {
		c.recordError(ctx, userID, state, err)
		return res, err
	}

	current := groupActiveFacts(all)
	res.Diff, res.ChangedFields = buildRewriteDiff(current, rebuilt)

	if opts.DryRun {
		return res, nil
	}

	backupPath, err := c.writeBackup(userID, all)
	if err != nil {
		// 没备份就不落库：重写是破坏性写入，宁可这一轮不跑。
		c.recordError(ctx, userID, state, err)
		return res, fmt.Errorf("profile rewrite: backup before write: %w", err)
	}
	res.BackupPath = backupPath

	// 逐字段对账，每个字段各自一个事务：中途某字段失败会停在「半新半旧」，靠下次节拍重跑收敛
	// （重写是幂等压缩，不需要跨字段的强一致）。要整轮原子性得给 repo 加带 *gorm.DB 的变体，
	// 收益不成比例。
	batchID := idgen.Next()
	applied := 0
	for _, def := range profilefield.ProfileFields {
		if rewriteSkipFields[def.Field] {
			continue
		}
		values, ok := rebuilt[def.Field]
		if !ok {
			continue // 模型没输出该字段 → 不动，不当作清空
		}
		inputs := factInputsFromValues(values, &def)
		// 重写输出视为"已被证实"（证据来自事件与记忆），不受增量提取的证据门槛限制。
		if err := c.factRepo.ReconcileFieldFacts(ctx, userID, def.Field, inputs, batchID); err != nil {
			c.recordError(ctx, userID, state, err)
			return res, fmt.Errorf("profile rewrite: apply field %s: %w", def.Field, err)
		}
		applied++
	}
	res.AppliedFields = applied

	if _, err := c.factRepo.MarkBatchSource(ctx, userID, batchID, model.FactSourceRewrite); err != nil {
		c.recordError(ctx, userID, state, err)
		return res, fmt.Errorf("profile rewrite: mark batch source: %w", err)
	}
	if n, err := c.factRepo.CleanupOutdated(ctx, userID, cleanOutdatedKeepBatches); err != nil {
		log.Printf("[profile-rewrite] cleanup outdated error: %v", err)
	} else if n > 0 {
		log.Printf("[profile-rewrite] cleaned %d outdated facts", n)
	}

	after, err := c.factRepo.ListAllByUser(ctx, userID)
	if err != nil {
		log.Printf("[profile-rewrite] recount facts error: %v（last_fact_count 按重写前记录）", err)
		res.FactCount = countActiveFacts(all)
	} else {
		res.FactCount = countActiveFacts(after)
	}
	c.recordSuccess(ctx, userID, res)
	return res, nil
}

// rewriteInput — 一次重写的输入证据（skip 非空表示判定后不该跑，证据不完整）。
type rewriteInput struct {
	events   []model.Event
	memories []model.UserMemory
	sessions int64
	skip     string
}

// collectInput 做到期判定并取输入证据。force 时忽略判定，直接拿最近 N 条事件/记忆。
func (c *ProfileConsolidator) collectInput(ctx context.Context, userID string, now, lastRun time.Time, force bool) (rewriteInput, error) {
	if force {
		events, err := c.eventRepo.List(ctx, userID, false, rewriteEventLimit, 0)
		if err != nil {
			return rewriteInput{}, fmt.Errorf("profile rewrite: list events: %w", err)
		}
		memories, _, err := c.memRepo.ListByUser(ctx, userID, "", "", "", rewriteMemoryLimit, 0)
		if err != nil {
			return rewriteInput{}, fmt.Errorf("profile rewrite: list memories: %w", err)
		}
		return rewriteInput{events: events, memories: memories}, nil
	}

	if !lastRun.IsZero() && now.Sub(lastRun) < rewriteInterval {
		return rewriteInput{skip: fmt.Sprintf("距上次重写不足 %d 天", int(rewriteInterval.Hours()/24))}, nil
	}

	// 只统计「上次重写之后」的活动，避免同一批证据被反复计入。
	// 当前 interval == window（都是 7 天），所以走完上面的闸门后 lastRun 恒 ≤ now-7d，
	// 这个 if 实际上不会命中；留着是为了将来把 interval 调得比 window 长时仍然正确。
	since := now.Add(-rewriteWindow)
	if !lastRun.IsZero() && lastRun.After(since) {
		since = lastRun
	}
	events, err := c.eventRepo.ListUpdatedSince(ctx, userID, since, rewriteEventLimit)
	if err != nil {
		return rewriteInput{}, fmt.Errorf("profile rewrite: list events since %s: %w", since.Format(time.RFC3339), err)
	}
	sessions, err := c.convRepo.CountTouchedSince(ctx, userID, since)
	if err != nil {
		return rewriteInput{}, fmt.Errorf("profile rewrite: count sessions since %s: %w", since.Format(time.RFC3339), err)
	}
	in := rewriteInput{events: events, sessions: sessions}
	if len(events) < rewriteMinEvents && sessions < rewriteMinSessions {
		in.skip = fmt.Sprintf("近 7 天活动不足（事件 %d < %d，会话 %d < %d）",
			len(events), rewriteMinEvents, sessions, rewriteMinSessions)
		return in, nil
	}

	memories, err := c.memRepo.ListUpdatedSince(ctx, userID, since)
	if err != nil {
		return rewriteInput{}, fmt.Errorf("profile rewrite: list memories since %s: %w", since.Format(time.RFC3339), err)
	}
	if len(memories) > rewriteMemoryLimit {
		memories = memories[len(memories)-rewriteMemoryLimit:] // 升序，留最新
	}
	in.memories = memories
	return in, nil
}

// callLLM 组装输入并调用模型，返回字段 → 事实列表。配额在 factInputsFromValues 里按上限截断。
func (c *ProfileConsolidator) callLLM(ctx context.Context, userID string, facts []model.UserProfileFact, events []model.Event, memories []model.UserMemory) (map[string][]string, error) {
	ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
		Category:    llmcost.CategoryPostChat,
		SubCategory: llmcost.SubProfileRewrite,
		UserID:      userID,
	})
	prompt := buildRewritePrompt(facts, events, memories)
	resp, err := c.client.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: rewriteSystemPrompt,
		Messages:     []llm.Message{{Role: "user", Content: prompt}},
		UserID:       userID,
	})
	if err != nil {
		return nil, fmt.Errorf("profile rewrite: chat: %w", err)
	}
	rebuilt, err := parseExtractedFacts(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("profile rewrite: parse output: %w (raw=%s)", err, truncateRunes(resp.Content, 200))
	}
	if len(rebuilt) == 0 {
		// 压缩输出的契约是「所有字段都出现」；空对象等于模型没干活，按失败处理而不是静默当无事发生。
		return nil, fmt.Errorf("profile rewrite: LLM 输出没有可解析字段（raw=%s）", truncateRunes(resp.Content, 200))
	}
	return rebuilt, nil
}

// recordSuccess / recordError 落一行运行状态。成功推进 last_run_at；失败只记原因，
// last_run_at 保持不动 → 下次节拍自动重试。状态写失败属于可降级，记录后继续。
func (c *ProfileConsolidator) recordSuccess(ctx context.Context, userID string, res RewriteResult) {
	now := time.Now().In(config.BJ)
	c.saveState(ctx, &model.ProfileRewriteState{
		UserID:           userID,
		LastRunAt:        &now,
		LastEventCount:   res.EventCount,
		LastSessionCount: res.SessionCount,
		LastFactCount:    res.FactCount,
		LastStatus:       model.ProfileRewriteStatusOK,
	})
}

func (c *ProfileConsolidator) recordError(ctx context.Context, userID string, prev *model.ProfileRewriteState, cause error) {
	s := &model.ProfileRewriteState{
		UserID:     userID,
		LastStatus: model.ProfileRewriteStatusError,
		LastError:  truncateRunes(cause.Error(), 500),
	}
	if prev != nil {
		s.LastRunAt = prev.LastRunAt
		s.LastEventCount = prev.LastEventCount
		s.LastSessionCount = prev.LastSessionCount
		s.LastFactCount = prev.LastFactCount
	}
	c.saveState(ctx, s)
}

func (c *ProfileConsolidator) saveState(ctx context.Context, s *model.ProfileRewriteState) {
	if err := c.stateRepo.Save(ctx, s); err != nil {
		log.Printf("[profile-rewrite] save state (user=%s, status=%s) error: %v", shortID(s.UserID), s.LastStatus, err)
	}
}

// writeBackup 落库前把全部事实（含历史批次）写成 JSON，权限 0600（含真实个人数据）。
func (c *ProfileConsolidator) writeBackup(userID string, facts []model.UserProfileFact) (string, error) {
	out := map[string]any{
		"db":          c.dbName,
		"user_id":     userID,
		"exported_at": time.Now().In(config.BJ).Format(time.RFC3339),
		"facts":       facts,
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal backup: %w", err)
	}
	if err := os.MkdirAll(c.backupDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", c.backupDir, err)
	}
	// 文件名带 user 与纳秒：多用户连续重写时同一秒内不会互相覆盖（那是唯一的还原点）。
	stamp := time.Now().In(config.BJ).Format("20060102-150405.000000000")
	path := filepath.Join(c.backupDir, "profile-backup-"+shortID(userID)+"-"+stamp+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		log.Printf("[profile-rewrite] resolve backup path: %v（按相对路径继续）", err)
		return path, nil
	}
	return abs, nil
}

// ProfileBackupDir 决定备份落哪：SQLite 跟库同目录（本地版在 Application Support，不在仓库里）；
// MySQL 用 data/（仓库里 .gitignore 已覆盖）。
func ProfileBackupDir(cfg *config.Config) string {
	if cfg.DB.Driver == "sqlite" && cfg.DB.Database != "" {
		return filepath.Dir(cfg.DB.Database)
	}
	return "data"
}

// ==================== 纯函数（可单测） ====================

// countActiveFacts 统计生效事实条数。
func countActiveFacts(facts []model.UserProfileFact) int {
	n := 0
	for _, f := range facts {
		if f.Status == model.FactStatusActive {
			n++
		}
	}
	return n
}

// groupActiveFacts 按字段聚合生效事实（diff 用）。
func groupActiveFacts(facts []model.UserProfileFact) map[string][]string {
	out := make(map[string][]string, len(profilefield.ProfileFields))
	for _, f := range facts {
		if f.Status != model.FactStatusActive || rewriteSkipFields[f.Field] {
			continue
		}
		out[f.Field] = append(out[f.Field], f.Value)
	}
	return out
}

// factInputsFromValues 把 LLM 输出转成对账输入：去空、截断超长、按 MaxFacts 截条数。
func factInputsFromValues(values []string, def *profilefield.ProfileFieldDef) []service.FactInput {
	inputs := make([]service.FactInput, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || profilePlaceholders[v] {
			continue
		}
		if utf8.RuneCountInString(v) > def.MaxLen {
			log.Printf("[profile-rewrite] fact too long (field=%s, %d>%d), truncate: %s",
				def.Field, utf8.RuneCountInString(v), def.MaxLen, truncateRunes(v, 60))
			v = truncateRunes(v, def.MaxLen)
		}
		inputs = append(inputs, service.FactInput{Value: v, UserStated: true})
		if len(inputs) >= def.MaxFacts {
			break
		}
	}
	return inputs
}

// buildRewriteDiff 按字段生成 [保留]/[新增]/[丢弃]，返回文本与有变化的字段数。
func buildRewriteDiff(current, rebuilt map[string][]string) (string, int) {
	var b strings.Builder
	changed := 0
	for _, def := range profilefield.ProfileFields {
		if rewriteSkipFields[def.Field] {
			continue
		}
		cur, next := current[def.Field], rebuilt[def.Field]
		curSet := make(map[string]bool, len(cur))
		nextSet := make(map[string]bool, len(next))
		for _, v := range cur {
			curSet[v] = true
		}
		for _, v := range next {
			nextSet[v] = true
		}
		var lines []string
		hasChange := false
		for _, v := range next {
			if curSet[v] {
				lines = append(lines, "  [保留] "+v)
			} else {
				lines = append(lines, "  [新增] "+v)
				hasChange = true
			}
		}
		for _, v := range cur {
			if !nextSet[v] {
				lines = append(lines, "  [丢弃] "+v)
				hasChange = true
			}
		}
		if len(lines) == 0 {
			continue
		}
		if hasChange {
			changed++
		}
		fmt.Fprintf(&b, "\n%s\n%s\n", def.Field, strings.Join(lines, "\n"))
	}
	return b.String(), changed
}

// buildRewritePrompt 组装重写输入：事件（证据最强）→ 记忆 → 旧画像（可能混有事件流水账）。
func buildRewritePrompt(facts []model.UserProfileFact, events []model.Event, memories []model.UserMemory) string {
	var b strings.Builder
	b.WriteString("<task>\n重建用户画像。证据按强弱排列：events（近期事件聚合）→ memories（历史记忆）→ old_profile（旧画像，其中可能混有事件流水账）。\n")
	b.WriteString("请输出一份干净、稳定、可长期复用的画像 JSON。\n</task>\n\n")

	b.WriteString("<fields>\n")
	for _, def := range profilefield.ProfileFields {
		if rewriteSkipFields[def.Field] {
			continue
		}
		fmt.Fprintf(&b, "- %s(≤%d字，上限%d条): %s\n", def.Field, def.MaxLen, def.MaxFacts, def.BriefDesc)
	}
	b.WriteString("</fields>\n\n")

	b.WriteString("<events>\n")
	for _, e := range events {
		fmt.Fprintf(&b, "<event name=%q>%s</event>\n", e.Name, e.Summary)
	}
	b.WriteString("</events>\n\n")

	b.WriteString("<memories>\n")
	for _, m := range memories {
		fmt.Fprintf(&b, "<m topic=%q>%s</m>\n", m.Topic, m.MemoryValue)
	}
	b.WriteString("</memories>\n\n")

	b.WriteString("<old_profile>\n")
	for _, f := range facts {
		if f.Status != model.FactStatusActive || rewriteSkipFields[f.Field] {
			continue
		}
		fmt.Fprintf(&b, "<field key=%q>%s</field>\n", f.Field, f.Value)
	}
	b.WriteString("</old_profile>\n\n")

	b.WriteString(`<rules>
- 只写稳定属性：条目不出现具体日期、金额、地点组合（出现即说明是事件，整条丢弃）
- 每条必须能脱离上下文独立成立：不写"以前""上周""某天"，不写代词
- 同字段内语义互斥时只保留最新一条（如"已戒酒"覆盖"喝酒"）
- 旅行、出差、看房、聚会这类有具体起止时间的安排属于事件，不属于画像
- 协作偏好只收"对助手的要求"（能读成"助手应该…"）；用户自己为人处事的原则不算，除非它要求助手照做
- 旧画像里的条目：稳定的保留，事件流水账丢弃，不确定的丢弃
- 每条事实尽量短，符合上面给出的字数上限与条数上限
- 没把握的字段输出空数组，不要猜
</rules>

<output_format>
纯 JSON（不要 markdown 代码块）：{"字段名":["事实1","事实2"],...}
所有字段都要出现；没有内容的字段输出 []。
</output_format>`)
	return b.String()
}
