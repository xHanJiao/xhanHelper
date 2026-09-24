package main

import (
	"context"
	"log"
	"time"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/app"
	"xhan-helper/internal/appinit"
	"xhan-helper/internal/async"
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

	// --- 路由 & 服务 ---
	router := appinit.InitRouter(cfg, repos, infra, llmClients, agents, tools, tools.MCPManager, vs, localUserID)
	appinit.Serve(cfg, router, agents.BGCancel, tools.MCPManager, agents.BGCtx)
}
