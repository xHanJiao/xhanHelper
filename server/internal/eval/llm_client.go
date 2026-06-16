package eval

import (
	"context"

	"xhan-helper/internal/llm"
)

// ChatLLM is a narrow interface wrapping the LLM client for eval label use.
type ChatLLM interface {
	ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error)
	ModelName() string
}

// Embedder provides text-to-vector conversion for the eval module.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
	ModelName() string
}
