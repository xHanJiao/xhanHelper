package main

import (
	"context"
	"time"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/mcp"
	"xhan-helper/internal/service"
)

// SnapshotProvider 提供监控看板所需的各项快照数据
type SnapshotProvider struct {
	vectorStore    *agent.VectorStore
	mcpManager     *mcp.Manager
	convRepo       *service.ConversationRepo
	themeStateRepo *service.ThemeStateRepo
}

// VectorStore 向量存储统计
func (p *SnapshotProvider) VectorStore() any {
	if p.vectorStore == nil {
		return nil
	}
	return p.vectorStore.Stats()
}

// MCP MCP 服务状态
func (p *SnapshotProvider) MCP() any {
	if p.mcpManager == nil {
		return nil
	}
	return p.mcpManager.Stats()
}

// ActiveConversations 最近1小时活跃会话数
func (p *SnapshotProvider) ActiveConversations() any {
	if p.convRepo == nil {
		return 0
	}
	return p.convRepo.CountActive(context.Background(), time.Hour)
}

// Themes 主题追踪统计
func (p *SnapshotProvider) Themes() any {
	if p.themeStateRepo == nil {
		return nil
	}
	stats, err := p.themeStateRepo.GlobalStats(context.Background())
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return stats
}
