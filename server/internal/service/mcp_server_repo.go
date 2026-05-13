package service

import (
	"context"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// MCPServerRepo — MCP server 快照数据访问
type MCPServerRepo struct {
	db *gorm.DB
}

// NewMCPServerRepo 创建 MCPServerRepo
func NewMCPServerRepo(db *gorm.DB) *MCPServerRepo {
	return &MCPServerRepo{db: db}
}

// UpsertSnapshot 创建或更新工具快照
func (r *MCPServerRepo) UpsertSnapshot(ctx context.Context, name, snapshotJSON string) error {
	var existing model.MCPServerRecord
	err := r.db.WithContext(ctx).Where("name = ?", name).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		return r.db.WithContext(ctx).Create(&model.MCPServerRecord{
			Name:          name,
			ToolsSnapshot: snapshotJSON,
			Enabled:       true,
		}).Error
	}
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&existing).Updates(map[string]interface{}{
		"tools_snapshot": snapshotJSON,
	}).Error
}

// FindByName 按名称获取
func (r *MCPServerRepo) FindByName(ctx context.Context, name string) (*model.MCPServerRecord, error) {
	var rec model.MCPServerRecord
	err := r.db.WithContext(ctx).Where("name = ? AND enabled = ?", name, true).First(&rec).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &rec, err
}

// ListEnabled 列出所有启用的 MCP server
func (r *MCPServerRepo) ListEnabled(ctx context.Context) ([]model.MCPServerRecord, error) {
	var list []model.MCPServerRecord
	err := r.db.WithContext(ctx).Where("enabled = ?", true).Find(&list).Error
	return list, err
}
