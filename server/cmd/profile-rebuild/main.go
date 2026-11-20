// profile-rebuild — 用户画像一次性重建（docs/design/user-profile-v2/design.md §6.3 / §4.5）。
//
// 老画像里混着事件流水账（带日期/金额/地点），字段体系也已经换了一版。
// 本工具按「事件 → 记忆 → 现有画像」的取证顺序让 LLM 重写一份干净的画像，
// 默认只打印字段级 diff（dry-run），加 --write 才落库；落库前自动全量备份。
//
// 与后台每周重写共用同一份实现（agent.ProfileConsolidator）：区别只在本工具 --force
// 忽略「距上次 ≥7 天 且 有新活动」的到期判定，随时可手工跑。
//
// 用法:
//
//	APP_ENV=prod ./profile-rebuild                    # dry-run：打印 diff
//	APP_ENV=prod ./profile-rebuild --write            # 备份 + 落库（prod 需 CONFIRM_PROD=yes）
//
// 注意：「重要关系」整字段跳过（LLM 输出被丢弃），未来换事件/实体机制时再单独处理。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/app"
	"xhan-helper/internal/appinit"
	"xhan-helper/internal/config"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	write := flag.Bool("write", false, "真正落库（默认只打印 diff；落库前自动备份）")
	userFlag := flag.String("user", "", "user_id（默认：本地单用户 / 库里唯一用户）")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `profile-rebuild — 用户画像一次性重建（默认 dry-run，只打印 diff）

用法:
  profile-rebuild [flags]

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
示例:
  APP_ENV=prod profile-rebuild                 # 只看 diff
  APP_ENV=prod profile-rebuild --write         # 备份 + 落库（prod 需 CONFIRM_PROD=yes）
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
	if *write {
		config.GuardProdWrite(cfg.DB.Database)
	}

	llmClients := app.InitLLM(cfg, llm.NewClient, llm.NewStrongClient, llmcost.NewDBRecorder(repos.LlmCost))
	rewriter := agent.NewProfileConsolidator(&agent.ProfileConsolidatorConfig{
		Client:    llmClients.BgFlash,
		FactRepo:  repos.UserProfileFact,
		EventRepo: repos.Event,
		MemRepo:   repos.Mem,
		ConvRepo:  repos.Conversation,
		UserRepo:  repos.User,
		StateRepo: repos.ProfileRewriteState,
		BackupDir: agent.ProfileBackupDir(cfg),
		DBName:    cfg.DB.Database,
	})

	res, err := rewriter.Rewrite(ctx, userID, agent.RewriteOptions{DryRun: !*write, Force: true})
	if err != nil {
		log.Fatalf("rewrite: %v", err)
	}
	if res.Skipped != "" {
		log.Fatalf("rewrite skipped: %s", res.Skipped)
	}

	fmt.Printf("\n================ 画像重建 diff ================\n")
	if strings.TrimSpace(res.Diff) == "" {
		fmt.Println("\n（没有变化）")
	} else {
		fmt.Println(res.Diff)
	}
	fmt.Printf("\n=============================================\n共 %d 个字段有变化（输入：事件 %d 条）\n",
		res.ChangedFields, res.EventCount)

	if !*write {
		log.Printf("[rebuild] dry-run 完成，未落库")
		return
	}
	log.Printf("[rebuild] 已落库 %d 个字段，生效事实 %d 条；备份：%s", res.AppliedFields, res.FactCount, res.BackupPath)
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
