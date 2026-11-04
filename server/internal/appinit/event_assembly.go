package appinit

import (
	"log"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/tool"
)

// BuildEventAssembler 构建事件拼装器及其只读工具集。
//
// 生产（session_end 的 EventWorker）与 cmd/event-bootstrap 共用这里，保证冷启动和
// 真实对话走同一套 prompt、阈值与只读边界（设计 §8.5）。
// unifiedSearch / eventTool 由调用方提供：两边各自持有已预热的数据源。
func BuildEventAssembler(
	cfg *config.Config,
	repos *app.Repos,
	infra *app.Infra,
	llmClients *app.LLMClients,
	unifiedSearch tool.Tool,
	eventTool *tool.EventTool,
) *agent.EventAssembler {
	reg := tool.NewRegistry()
	reg.Register(tool.NewLoggingWrapper(unifiedSearch, log.Default()))
	reg.Register(tool.NewLoggingWrapper(eventTool.ReadOnly(), log.Default()))
	reg.ValidateNativeFC()
	reg.ValidateReadOnlyFuncs()
	reg.ValidateCatalog()

	return agent.NewEventAssembler(&agent.EventAssemblerConfig{
		FlashClient:   llmClients.BgFlash,
		Registry:      reg,
		EventRepo:     repos.Event,
		MemRepo:       repos.Mem,
		EmbeddingRepo: repos.Embedding,
		Embedder:      infra.Embedder,
		CandidateTop:  cfg.EventAssembly.CandidateTop,
		DedupeSim:     cfg.EventAssembly.DedupeSim,
		MaxIterations: cfg.EventAssembly.MaxIterations,
		BatchSize:     cfg.EventAssembly.BatchSize,
	})
}
