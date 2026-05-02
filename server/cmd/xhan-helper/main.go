package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"xhan-helper/internal/agent"
	"xhan-helper/internal/config"
	"xhan-helper/internal/controller"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("load config:", err)
	}

	gin.SetMode(cfg.Server.Mode)

	db := service.NewDB(cfg.DB)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	db.AutoMigrate(
		&model.Device{},
		&model.Conversation{},
		&model.Message{},
		&model.Bill{},
		&model.UserMemory{},
		&model.ScheduledTask{},
	)

	// LLM 客户端
	llmClient, err := llm.NewClient(cfg.LLM)
	if err != nil {
		log.Fatal("create llm client:", err)
	}

	// Repos
	devRepo := service.NewDeviceRepo(db)
	convRepo := service.NewConversationRepo(db)
	msgRepo := service.NewMessageRepo(db)
	billRepo := service.NewBillRepo(db)
	memRepo := service.NewUserMemoryRepo(db)

	// Tool Registry + Tools
	reg := tool.NewRegistry()
	reg.Register(tool.NewBookkeepingTool(billRepo))
	reg.Register(tool.NewDatetimeTool())
	reg.Register(tool.NewUUIDTool())
	reg.Register(tool.NewCalculatorTool())

	// Memory Store
	memStore := agent.NewLLMMemoryStore(llmClient, memRepo)

	// Orchestrator
	orch := agent.New(llmClient, reg, memStore)

	// Memory Scheduler (后台异步)
	memExtractor := agent.NewMemoryExtractor(llmClient, memStore, msgRepo, convRepo)
	memScheduler := agent.NewMemoryScheduler(memExtractor, convRepo)
	go memScheduler.Start(context.Background())

	// Handlers
	billHandler := controller.NewBillHandler(billRepo, memExtractor)

	// Router
	router := controller.NewRouter(cfg, orch, devRepo, convRepo, msgRepo, billHandler)

	addr := ":" + strconv.Itoa(cfg.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	go func() {
		log.Printf("server starting on %s (mode: %s)", addr, cfg.Server.Mode)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("server error:", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	log.Println("server stopped")
}
