package main

import (
	"log"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/app"
	"xhan-helper/internal/appinit"
	"xhan-helper/internal/config"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
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

	llmClients := app.InitLLM(cfg, llm.NewClient, llm.NewStrongClient)
	repos := app.InitRepos(cfg, db)

	// --- 工具 & Agent 系统 ---
	tools := appinit.InitTools(cfg, repos, infra, llmClients)
	agents := appinit.InitAgents(cfg, repos, infra, llmClients, tools, vs)

	// --- 监控 ---
	appinit.RegisterMonitorSnapshots(vs, tools.MCPManager, repos.Conversation, repos.ThemeState, cfg, repos.SearchUsage)
	tools.MCPManager.StartIdleReaper(agents.BGCtx)

	// --- 路由 & 服务 ---
	router := appinit.InitRouter(cfg, repos, infra, llmClients.Flash, agents, tools, tools.MCPManager, vs)
	appinit.Serve(cfg, router, agents.BGCancel, tools.MCPManager, agents.BGCtx)
}
