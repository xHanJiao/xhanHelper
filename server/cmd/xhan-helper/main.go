package main

import (
	"context"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

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

	// --- 工具 & Agent 系统 ---
	tools := appinit.InitTools(cfg, repos, infra, llmClients)
	agents := appinit.InitAgents(cfg, repos, infra, llmClients, tools, vs)

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
	appinit.RegisterMonitorSnapshots(vs, tools.MCPManager, repos.Conversation, repos.ThemeState)
	tools.MCPManager.StartIdleReaper(agents.BGCtx)

	// --- 本地数据备份（仅本地模式；每小时检查、到期才跑）---
	backupSvc := setupLocalBackup(cfg, db, agents.BGCtx)

	// --- 路由 & 服务 ---
	router := appinit.InitRouter(cfg, repos, infra, llmClients, agents, tools, tools.MCPManager, vs, localUserID, backupSvc)
	appinit.Serve(cfg, router, agents.BGCancel, tools.MCPManager, agents.BGCtx)
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
