package service

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"xhan-helper/internal/model"
)

// DeviceRepo — 设备数据访问
type DeviceRepo struct {
	db *gorm.DB
}

// NewDeviceRepo 创建 DeviceRepo
func NewDeviceRepo(db *gorm.DB) *DeviceRepo {
	return &DeviceRepo{db: db}
}

// Create 创建新设备
func (r *DeviceRepo) Create(ctx context.Context, d *model.Device) error {
	return r.db.WithContext(ctx).Create(d).Error
}

// FindByToken 根据 device_token 查找设备
func (r *DeviceRepo) FindByToken(ctx context.Context, token string) (*model.Device, error) {
	var d model.Device
	err := r.db.WithContext(ctx).Where("token = ?", token).First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &d, err
}
