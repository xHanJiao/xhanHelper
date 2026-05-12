package tool

import (
	"context"
	"log"

	"xhan-helper/internal/model"
)

// ToolRepo 接口
type ToolRepo interface {
	ListTools(ctx context.Context, userID, status string, limit, offset int) ([]model.ToolRegistry, int64, error)
	CreateTool(ctx context.Context, t *model.ToolRegistry) error
}

// SeedBuiltinTools — 启动时 seed 内置工具到数据库 (placeholder)
func SeedBuiltinTools(ctx context.Context, toolRepo ToolRepo, userID string) error {
	log.Printf("[tool_seeder] placeholder implementation")
	return nil
}
