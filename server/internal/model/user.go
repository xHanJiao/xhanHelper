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
type UserToken struct {
	ID        string    `gorm:"primaryKey;size:36"`
	UserID    string    `gorm:"size:36;not null;index"`
	Token     string    `gorm:"size:36;uniqueIndex;not null"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}
