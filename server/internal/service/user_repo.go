package service

import (
	"context"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/google/uuid"
	"xhan-helper/internal/model"
)

var ErrTokenExpired = errors.New("token expired")

// UserRepo — 用户数据访问
type UserRepo struct {
	db               *gorm.DB
	tokenTTLMinutes  int
	maxTokensPerUser int
}

func NewUserRepo(db *gorm.DB, tokenTTLMinutes, maxTokensPerUser int) *UserRepo {
	if maxTokensPerUser <= 0 {
		maxTokensPerUser = 4
	}
	return &UserRepo{db: db, tokenTTLMinutes: tokenTTLMinutes, maxTokensPerUser: maxTokensPerUser}
}

func (r *UserRepo) Create(ctx context.Context, username, password string) (*model.User, string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", err
	}
	user := &model.User{
		ID:           uuid.New().String(),
		Username:     username,
		PasswordHash: string(hash),
	}

	var token string
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		var issueErr error
		token, issueErr = issueTokenTx(tx, user.ID, r.tokenTTLMinutes)
		return issueErr
	})
	if err != nil {
		return nil, "", err
	}
	return user, token, nil
}

func (r *UserRepo) Login(ctx context.Context, username, password string) (*model.User, string, error) {
	var user model.User
	if err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error; err != nil {
		return nil, "", err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, "", err
	}

	var token string
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.UserToken{}).Where("user_id = ?", user.ID).Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(r.maxTokensPerUser) {
			deleteCount := int(count) - r.maxTokensPerUser + 1
			if err := tx.Where("user_id = ?", user.ID).
				Order("created_at ASC").
				Limit(deleteCount).
				Delete(&model.UserToken{}).Error; err != nil {
				return err
			}
		}
		var issueErr error
		token, issueErr = issueTokenTx(tx, user.ID, r.tokenTTLMinutes)
		return issueErr
	})
	if err != nil {
		return nil, "", err
	}
	return &user, token, nil
}

func (r *UserRepo) ChangePassword(ctx context.Context, userID, oldPassword, newPassword string) error {
	var user model.User
	if err := r.db.WithContext(ctx).Where("id = ?", userID).First(&user).Error; err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(oldPassword)); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&user).Update("password_hash", string(hash)).Error
}

func (r *UserRepo) Logout(ctx context.Context, token string) error {
	return r.db.WithContext(ctx).Where("token = ?", token).Delete(&model.UserToken{}).Error
}

// ListAll 列出所有用户
func (r *UserRepo) ListAll(ctx context.Context) ([]model.User, error) {
	var users []model.User
	err := r.db.WithContext(ctx).Find(&users).Error
	return users, err
}

// ListResult — 用户列表分页结果
type ListResult struct {
	Users      []model.User `json:"users"`
	TotalCount int64        `json:"total_count"`
}

// List 分页列出用户
func (r *UserRepo) List(ctx context.Context, offset, limit int) (*ListResult, error) {
	var users []model.User
	var total int64
	if err := r.db.WithContext(ctx).Model(&model.User{}).Count(&total).Error; err != nil {
		return nil, err
	}
	if err := r.db.WithContext(ctx).Order("created_at DESC").Offset(offset).Limit(limit).Find(&users).Error; err != nil {
		return nil, err
	}
	return &ListResult{Users: users, TotalCount: total}, nil
}

func (r *UserRepo) FindByToken(ctx context.Context, token string) (*model.User, error) {
	var ut model.UserToken
	if err := r.db.WithContext(ctx).Where("token = ?", token).First(&ut).Error; err != nil {
		return nil, err
	}
	if time.Now().After(ut.ExpiresAt) {
		return nil, ErrTokenExpired
	}
	var u model.User
	if err := r.db.WithContext(ctx).Where("id = ?", ut.UserID).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

func issueTokenTx(db *gorm.DB, userID string, ttlMinutes int) (string, error) {
	if ttlMinutes <= 0 {
		ttlMinutes = 5
	}
	ut := &model.UserToken{
		ID:        uuid.New().String(),
		UserID:    userID,
		Token:     uuid.New().String(),
		ExpiresAt: time.Now().Add(time.Duration(ttlMinutes) * time.Minute),
	}
	return ut.Token, db.Create(ut).Error
}

// RefreshToken 延长 token 过期时间，用于心跳续期。
// 仅当 token 存在且未过期时才续期；已过期的 token 返回 ErrTokenExpired。
func (r *UserRepo) RefreshToken(ctx context.Context, token string) error {
	var ut model.UserToken
	if err := r.db.WithContext(ctx).Where("token = ?", token).First(&ut).Error; err != nil {
		return err
	}
	if time.Now().After(ut.ExpiresAt) {
		return ErrTokenExpired
	}
	ttl := r.tokenTTLMinutes
	if ttl <= 0 {
		ttl = 5
	}
	return r.db.WithContext(ctx).Model(&ut).Update("expires_at", time.Now().Add(time.Duration(ttl)*time.Minute)).Error
}
