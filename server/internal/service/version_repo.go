package service

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// VersionRepo — 通用版本快照仓库
type VersionRepo struct {
	db *gorm.DB
}

func NewVersionRepo(db *gorm.DB) *VersionRepo {
	return &VersionRepo{db: db}
}

// CreateSnapshot 为指定实体创建版本快照，自动递增 version 号
func (r *VersionRepo) CreateSnapshot(ctx context.Context, entityType, entityID string, data interface{}) error {
	snapshot, err := json.Marshal(data)
	if err != nil {
		return err
	}

	var maxVer int
	r.db.WithContext(ctx).Model(&model.ContentVersion{}).
		Where("entity_type = ? AND entity_id = ?", entityType, entityID).
		Select("COALESCE(MAX(version), 0)").
		Scan(&maxVer)

	v := &model.ContentVersion{
		ID:         uuid.New().String(),
		EntityType: entityType,
		EntityID:   entityID,
		Version:    maxVer + 1,
		Snapshot:   string(snapshot),
	}
	return r.db.WithContext(ctx).Create(v).Error
}

// GetVersions 获取指定实体的所有版本快照（按版本号倒序）
func (r *VersionRepo) GetVersions(ctx context.Context, entityType, entityID string) ([]model.ContentVersion, error) {
	var list []model.ContentVersion
	err := r.db.WithContext(ctx).
		Where("entity_type = ? AND entity_id = ?", entityType, entityID).
		Order("version DESC").
		Find(&list).Error
	return list, err
}

// GetSnapshot 获取指定实体的某个版本快照
func (r *VersionRepo) GetSnapshot(ctx context.Context, entityType, entityID string, version int) (*model.ContentVersion, error) {
	var v model.ContentVersion
	err := r.db.WithContext(ctx).
		Where("entity_type = ? AND entity_id = ? AND version = ?", entityType, entityID, version).
		First(&v).Error
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// DeleteByEntity 删除指定实体的所有版本快照（实体删除时调用）
func (r *VersionRepo) DeleteByEntity(ctx context.Context, entityType, entityID string) error {
	return r.db.WithContext(ctx).
		Where("entity_type = ? AND entity_id = ?", entityType, entityID).
		Delete(&model.ContentVersion{}).Error
}
