package main

import (
	"context"
	"log"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xhan-helper/internal/activity"
	"xhan-helper/internal/agent"
	"xhan-helper/internal/app"
	"xhan-helper/internal/appinit"
	"xhan-helper/internal/async"
	"xhan-helper/internal/backup"
	"xhan-helper/internal/config"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/monitor"
	"xhan-helper/internal/util"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.Load()
	if err != nil {
		log.Fatal("load config:", err)
	}

	if err := idgen.Init(cfg.Snowflake.NodeID); err != nil {
		log.Fatal("init idgen:", err)
	}

	util.PanicHandler = monitor.RecordPanic
	gin.SetMode(cfg.Server.Mode)

	// --- 基础设施 ---
	db := app.InitDB(cfg)
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}()

	// --- 活动埋点 events.db（仅本机 sqlite 且开关打开）---
	activityStore := setupActivity(cfg)
	if activityStore != nil {
		defer func() {
			if err := activityStore.Close(); err != nil {
				log.Printf("[activity] close events.db: %v", err)
			}
		}()
	}
	// 系统变更事件（doc.* / memory.* / bill.created / reminder.*）：GORM 写回调统一收口。
	activity.RegisterChangeCallbacks(db, activityStore)

	embedder := llm.NewEmbedder(cfg.Embedding.APIKey, cfg.Embedding.BaseURL, cfg.Embedding.Model)
	vs := agent.NewVectorStore()
	vsAdapter := agent.NewVectorSearchAdapter(vs)
	infra := app.InitInfra(cfg, db, func(apiKey, baseURL, model string) *llm.Embedder { return embedder }, vsAdapter)

	repos := app.InitRepos(cfg, db)
	localUserID, err := appinit.EnsureLocalUser(cfg, repos.User)
	if err != nil {
		log.Fatalf("[local] %v", err)
	}
	costRecorder := llmcost.NewDBRecorder(repos.LlmCost)

	llmClients := app.InitLLM(cfg, llm.NewClient, llm.NewStrongClient, costRecorder)
	llmClients.Flash = llmcost.WrapIOAudit(llmClients.Flash, repos.LLMCallIO)
	llmClients.Strong = llmcost.WrapIOAudit(llmClients.Strong, repos.LLMCallIO)
	llmClients.BgFlash = llmcost.WrapIOAudit(llmClients.BgFlash, repos.LLMCallIO)
	llmClients.BgStrong = llmcost.WrapIOAudit(llmClients.BgStrong, repos.LLMCallIO)

	// 活动埋点：llm.call.started/ended + llm.first_token（store 为 nil 时原样返回）
	llmClients.Flash = activity.WrapLLMClient(llmClients.Flash, activityStore, "main_flash")
	llmClients.Strong = activity.WrapLLMClient(llmClients.Strong, activityStore, "main_strong")
	llmClients.BgFlash = activity.WrapLLMClient(llmClients.BgFlash, activityStore, "bg_flash")
	llmClients.BgStrong = activity.WrapLLMClient(llmClients.BgStrong, activityStore, "bg_strong")

	// BatchDelete 走显式 emit（批量删除拿不到逐行 PK，是变更回调的唯一例外）。
	repos.Mem.SetActivity(activityStore)

	// 图片描述后处理：上传后异步用多模态模型生成一句描述（后台任务关闭时不接，避免隐藏的外呼）。
	if !cfg.Server.BackgroundTasksDisabled() {
		infra.ImageStore.SetDescriber(agent.NewImageDescriber(llmClients.BgFlash))
	}

	// --- 工具 & Agent 系统 ---
	tools := appinit.InitTools(cfg, repos, infra, llmClients, activityStore)
	agents := appinit.InitAgents(cfg, repos, infra, llmClients, tools, vs, activityStore)

	// LLM 审计保留清理：每日删 7 天前记录
	if !cfg.Server.BackgroundTasksDisabled() {
		cleanup := func(ctx context.Context) {
			cutoff := time.Now().Add(-7 * 24 * time.Hour)
			for {
				n, err := repos.LLMCallIO.CleanupBefore(ctx, cutoff, 500)
				if err != nil {
					log.Printf("[llm_audit] retention cleanup error: %v", err)
					return
				}
				if n < 500 {
					return
				}
			}
		}
		go async.NewIntervalRunner("llm_io_retention", 24*time.Hour, cleanup).Start(agents.BGCtx)
	}

	// --- 监控 ---
	appinit.RegisterMonitorSnapshots(vs, tools.MCPManager, repos.Conversation)
	tools.MCPManager.StartIdleReaper(agents.BGCtx)

	// --- 本地数据备份（仅本地模式；每小时检查、到期才跑）---
	backupSvc := setupLocalBackup(cfg, db, agents.BGCtx)

	// --- 路由 & 服务 ---
	router := appinit.InitRouter(cfg, repos, infra, llmClients, agents, tools, tools.MCPManager, vs, localUserID, backupSvc, activityStore)
	appinit.Serve(cfg, router, agents.BGCancel, tools.MCPManager, agents.BGCtx)
}

// setupActivity 打开 events.db；禁用时返回 nil（不建库）。
// 路径：activity.db_path 显式指定；否则主库为 sqlite 时与主库同目录。主库非 sqlite 且未指定 → fatal（禁止静默写当前目录）。
// 打开失败按可降级处理：埋点坏了不该拖垮主服务，就地记录后继续。
func setupActivity(cfg *config.Config) *activity.Store {
	if !cfg.Activity.IsEnabled() {
		log.Printf("[activity] 未启用（activity.enabled=false）")
		return nil
	}
	path := cfg.Activity.DBPath
	if path == "" {
		if cfg.DB.Driver != "sqlite" {
			log.Fatalf("[activity] 已启用但未配置 activity.db_path，且主库非 sqlite（driver=%s）：请显式指定 events.db 路径", cfg.DB.Driver)
		}
		path = activity.DBPath(cfg.DB.Database)
	}
	// 相对路径解析成绝对路径并打印，避免跑在不同 CWD（如改 systemd）时 events.db 静默落到别处。
	absPath, err := filepath.Abs(path)
	if err != nil {
		log.Fatalf("[activity] 解析 events.db 路径 %q: %v", path, err)
	}
	st, err := activity.Open(absPath, true)
	if err != nil {
		log.Printf("[activity] 打开 events.db 失败，埋点停用：%v", err)
		return nil
	}
	st.SetWatermarkMode(cfg.Activity.WatermarkModeNormalized())
	// validate 已保证 day_timezone 可加载；这里再兜一层，绝不静默。
	if loc, err := cfg.Activity.DayLocation(); err != nil {
		log.Printf("[activity] day_timezone 解析失败，沿用主机 Local：%v", err)
	} else {
		activity.SetDayLocation(loc)
	}
	log.Printf("[activity] events.db=%s enabled=true watermark_mode=%s day_timezone=%s",
		absPath, st.WatermarkMode(), dayTimezoneLabel(cfg.Activity.DayTimezone))
	return st
}

// dayTimezoneLabel 返回日志用的时区标签（空 = 主机 Local）。
func dayTimezoneLabel(tz string) string {
	if tz == "" {
		return "Local"
	}
	return tz
}

// setupLocalBackup 本地模式下创建备份服务并按需挂调度器；非本地模式返回 nil（不注册路由）。
func setupLocalBackup(cfg *config.Config, db *gorm.DB, bgCtx context.Context) *backup.Service {
	if !cfg.Server.LocalSingleUser || cfg.DB.Driver != "sqlite" {
		return nil
	}
	svc := backup.New(db)
	if !cfg.Backup.Enabled {
		log.Printf("[backup] 未启用（可在设置页开启并选择备份位置）")
		return svc
	}
	opts := backup.OptionsFromConfig(cfg)
	log.Printf("[backup] 已启用：dest=%s interval=%d天 保留=%d+%d",
		opts.Dest, opts.IntervalDays, opts.DailyKeep, opts.WeeklyKeep)
	fn := func(ctx context.Context) {
		// IntervalRunner 不 recover：数据安全任务 panic 不能让它静默停摆。
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[backup] panic: %v", r)
			}
		}()
		ar, err := svc.RunIfDue(ctx, opts)
		if err != nil {
			log.Printf("[backup] 定时备份失败：%v", err)
			return
		}
		if ar != nil {
			log.Printf("[backup] 定时备份完成：%s", ar.Name)
		}
	}
	go async.NewIntervalRunner("local_backup", time.Hour, fn).RunImmediately().Start(bgCtx)
	monitor.Register("local_backup", svc.Stats)
	return svc
}
