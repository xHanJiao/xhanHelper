package model

import "time"

// Bill — 账单（记账工具核心数据，P1 启用）
type Bill struct {
	ID          string    `gorm:"primaryKey;size:36" json:"id"`
	DeviceID    string    `gorm:"size:36;not null;index:idx_device_date;index:idx_device_category" json:"device_id"`
	Amount      float64   `gorm:"type:decimal(10,2);not null" json:"amount"`
	Category    string    `gorm:"size:50;not null;index:idx_device_category" json:"category"`
	Description string    `gorm:"size:500" json:"description"`
	BillDate    string    `gorm:"type:date;not null;index:idx_device_date" json:"bill_date"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
}
