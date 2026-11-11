package model

import "time"

// User — 用户
type User struct {
	ID           string    `gorm:"primaryKey;size:36" json:"id"`
	Username     string    `gorm:"size:50;uniqueIndex;not null" json:"username"`
	PasswordHash string    `gorm:"size:255;not null" json:"-"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// UserToken — 登录凭证（Bearer Token）
// TokenHash 存 SHA-256(token) 的 64 位 hex，不是明文 token；列名沿用 token。
type UserToken struct {
	ID        string `gorm:"primaryKey;size:36"`
	UserID    string `gorm:"size:36;not null;index"`
	TokenHash string `gorm:"column:token;size:64;uniqueIndex;not null"`
	// DeviceID 由客户端生成并持久化，用于「同设备重登替换旧 token」；空串=老客户端，行为不变。
	DeviceID  string    `gorm:"size:64"`
	ExpiresAt time.Time `gorm:"not null"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}
