// profile-rebuild — 用户画像一次性重建（设计 docs/design/user-profile-v2/design.md §6.3）。
//
// 老画像里混着事件流水账（带日期/金额/地点），字段体系也已经换了一版。
// 本工具按「事件 → 记忆 → 旧画像」的取证顺序让 LLM 重写一份干净的画像，
// 默认只打印字段级 diff（dry-run），加 --write 才落库；落库前自动全量备份。
//
// 用法:
//
//	APP_ENV=prod ./profile-rebuild                    # dry-run：打印 diff + 备份文件路径
//	APP_ENV=prod ./profile-rebuild --write            # 备份 + 落库（prod 需 CONFIRM_PROD=yes）
//	APP_ENV=prod ./profile-rebuild --events 15 --memories 50
//
// 注意：「重要关系」整字段跳过（LLM 输出被丢弃），未来换事件/实体机制时再单独处理。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"xhan-helper/internal/app"
	"xhan-helper/internal/appinit"
	"xhan-helper/internal/config"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/model"
	"xhan-helper/internal/profilefield"
	"xhan-helper/internal/service"
)

// skipFields — 重建时整字段跳过，LLM 输出被丢弃。
var skipFields = map[string]bool{"重要关系": true}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	write := flag.Bool("write", false, "真正落库（默认只打印 diff；落库前自动备份）")
	userFlag := flag.String("user", "", "user_id（默认：本地单用户 / 库里唯一用户）")
	eventN := flag.Int("events", 10, "喂给 LLM 的近期事件条数")
	memoryN := flag.Int("memories", 30, "喂给 LLM 的近期记忆条数")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `profile-rebuild — 用户画像一次性重建（默认 dry-run，只打印 diff）

用法:
  profile-rebuild [flags]

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
示例:
  APP_ENV=prod profile-rebuild                 # 只看 diff + 备份
  APP_ENV=prod profile-rebuild --write         # 备份 + 落库（prod 需 CONFIRM_PROD=yes）
  APP_ENV=prod profile-rebuild --events 15 --memories 50
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
	factRepo := repos.UserProfileFact

	userID, err := resolveUser(ctx, cfg, repos, *userFlag)
	if err != nil {
		log.Fatalf("resolve user: %v", err)
	}
	if *write {
		config.GuardProdWrite(cfg.DB.Database)
	}

	all, err := factRepo.ListAllByUser(ctx, userID)
	if err != nil {
		log.Fatalf("list facts: %v", err)
	}
	backupPath, err := writeBackup(backupDir(cfg), cfg.DB.Database, userID, all)
	if err != nil {
		log.Fatalf("backup: %v", err)
	}
	log.Printf("[rebuild] db=%s user=%s facts=%d backup=%s", cfg.DB.Database, userID, len(all), backupPath)

	events, err := repos.Event.List(ctx, userID, false, *eventN, 0)
	if err != nil {
		log.Fatalf("list events: %v", err)
	}
	memories, _, err := repos.Mem.ListByUser(ctx, userID, "", "", "", *memoryN, 0)
	if err != nil {
		log.Fatalf("list memories: %v", err)
	}
	log.Printf("[rebuild] input: events=%d memories=%d", len(events), len(memories))

	llmClients := app.InitLLM(cfg, llm.NewClient, llm.NewStrongClient, llmcost.NewDBRecorder(repos.LlmCost))
	prompt := buildPrompt(all, events, memories)
	resp, err := llmClients.BgFlash.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: "你是用户画像重建助手。只输出 JSON。",
		Messages:     []llm.Message{{Role: "user", Content: prompt}},
		UserID:       userID,
	})
	if err != nil {
		log.Fatalf("llm: %v", err)
	}
	rebuilt, err := parseFactsJSON(resp.Content)
	if err != nil {
		log.Fatalf("parse LLM output: %v (raw=%s)", err, truncate(resp.Content, 300))
	}

	current := groupActive(all)
	printDiff(current, rebuilt)

	if !*write {
		log.Printf("[rebuild] dry-run 完成，未落库。备份：%s", backupPath)
		return
	}
	batchID := idgen.Next()
	applied := 0
	for field, values := range rebuilt {
		if skipFields[field] {
			log.Printf("[rebuild] skip field %s（保持原样）", field)
			continue
		}
		def := profilefield.ProfileFieldDesc(field)
		if def == nil {
			log.Printf("[rebuild] field %q not in whitelist, drop", field)
			continue
		}
		inputs := make([]service.FactInput, 0, len(values))
		for _, v := range values {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if utf8.RuneCountInString(v) > def.MaxLen {
				v = string([]rune(v)[:def.MaxLen])
			}
			inputs = append(inputs, service.FactInput{Value: v, UserStated: true})
			if len(inputs) >= def.MaxFacts {
				break
			}
		}
		if err := factRepo.ReconcileFieldFacts(ctx, userID, field, inputs, batchID); err != nil {
			log.Fatalf("apply field %s: %v", field, err)
		}
		applied++
	}
	if n, err := factRepo.MarkBatchSource(ctx, userID, batchID, model.FactSourceMigrated); err != nil {
		log.Fatalf("mark batch source: %v", err)
	} else {
		log.Printf("[rebuild] 已落库 %d 个字段，标记 source=migrated 的行 %d 条；备份：%s", applied, n, backupPath)
	}
}

// resolveUser 与 event-bootstrap 同口径：显式 --user → 本地单用户 → 库里唯一用户。
func resolveUser(ctx context.Context, cfg *config.Config, repos *app.Repos, flagID string) (string, error) {
	if flagID != "" {
		users, err := repos.User.ListAll(ctx)
		if err != nil {
			return "", err
		}
		for _, u := range users {
			if u.ID == flagID {
				return flagID, nil
			}
		}
		return "", fmt.Errorf("user %q 不存在", flagID)
	}
	if id, err := appinit.EnsureLocalUser(cfg, repos.User); err != nil {
		return "", err
	} else if id != "" {
		return id, nil
	}
	users, err := repos.User.ListAll(ctx)
	if err != nil {
		return "", err
	}
	if len(users) == 1 {
		return users[0].ID, nil
	}
	lines := make([]string, 0, len(users))
	for _, u := range users {
		lines = append(lines, fmt.Sprintf("  --user %s  (%s)", u.ID, u.Username))
	}
	return "", fmt.Errorf("库里有 %d 个用户，请用 --user 指定：\n%s", len(users), strings.Join(lines, "\n"))
}

// backupDir 决定备份落哪：SQLite 跟库同目录（本地版在 Application Support，不在仓库里）；
// MySQL 用 data/（仓库里 server/.gitignore 与根 .gitignore 都覆盖）。
func backupDir(cfg *config.Config) string {
	if cfg.DB.Driver == "sqlite" && cfg.DB.Database != "" {
		return filepath.Dir(cfg.DB.Database)
	}
	return "data"
}

// writeBackup 把全部事实（含历史批次）落成 JSON，权限 0600（含真实个人数据）。
func writeBackup(dir, dbName, userID string, facts []model.UserProfileFact) (string, error) {
	out := map[string]any{
		"db":          dbName,
		"user_id":     userID,
		"exported_at": time.Now().Format(time.RFC3339),
		"facts":       facts,
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "profile-backup-"+time.Now().Format("20060102-150405")+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		log.Printf("[rebuild] resolve backup path: %v（按相对路径继续）", err)
		return path, nil
	}
	return abs, nil
}

// buildPrompt 组装重建输入：事件（证据最强）→ 记忆 → 旧画像（可能混有事件）。
func buildPrompt(facts []model.UserProfileFact, events []model.Event, memories []model.UserMemory) string {
	var b strings.Builder
	b.WriteString("<task>\n重建用户画像。证据按强弱排列：events（近期事件聚合）→ memories（历史记忆）→ old_profile（旧画像，其中可能混有事件流水账）。\n")
	b.WriteString("请输出一份干净、稳定、可长期复用的画像 JSON。\n</task>\n\n")

	b.WriteString("<fields>\n")
	for _, def := range profilefield.ProfileFields {
		if skipFields[def.Field] {
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
		if f.Status != model.FactStatusActive || skipFields[f.Field] {
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

// parseFactsJSON 解析 LLM 输出，兼容"字段":["a"]与"字段":"a"两种形态。
func parseFactsJSON(content string) (map[string][]string, error) {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in output")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content[start:end+1]), &raw); err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(raw))
	for field, r := range raw {
		var s string
		if err := json.Unmarshal(r, &s); err == nil {
			out[field] = []string{s}
			continue
		}
		var arr []string
		if err := json.Unmarshal(r, &arr); err != nil {
			log.Printf("[rebuild] field %q value not string or []string, skip: %s", field, truncate(string(r), 80))
			continue
		}
		out[field] = arr
	}
	return out, nil
}

func groupActive(facts []model.UserProfileFact) map[string][]string {
	out := make(map[string][]string, len(profilefield.ProfileFields))
	for _, f := range facts {
		if f.Status != model.FactStatusActive {
			continue
		}
		out[f.Field] = append(out[f.Field], f.Value)
	}
	return out
}

// printDiff 按字段打印 [保留] / [新增] / [丢弃]，人工逐条审。
func printDiff(current, rebuilt map[string][]string) {
	fmt.Println("\n================ 画像重建 diff ================")
	changed := 0
	for _, def := range profilefield.ProfileFields {
		if skipFields[def.Field] {
			continue
		}
		cur := current[def.Field]
		next := rebuilt[def.Field]
		curSet := make(map[string]bool, len(cur))
		nextSet := make(map[string]bool, len(next))
		for _, v := range cur {
			curSet[v] = true
		}
		for _, v := range next {
			nextSet[v] = true
		}
		var lines []string
		for _, v := range next {
			if curSet[v] {
				lines = append(lines, "  [保留] "+v)
			} else {
				lines = append(lines, "  [新增] "+v)
			}
		}
		for _, v := range cur {
			if !nextSet[v] {
				lines = append(lines, "  [丢弃] "+v)
			}
		}
		if len(lines) == 0 {
			continue
		}
		changed++
		fmt.Printf("\n%s\n%s\n", def.Field, strings.Join(lines, "\n"))
	}
	fmt.Printf("\n=============================================\n共 %d 个字段有变化\n", changed)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
