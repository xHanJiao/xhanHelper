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
	tokenTTLHours    int
	maxTokensPerUser int
}

func NewUserRepo(db *gorm.DB, tokenTTLHours, maxTokensPerUser int) *UserRepo {
	if maxTokensPerUser <= 0 {
		maxTokensPerUser = 4
	}
	return &UserRepo{db: db, tokenTTLHours: tokenTTLHours, maxTokensPerUser: maxTokensPerUser}
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
		token, issueErr = issueTokenTx(tx, user.ID, r.tokenTTLHours)
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
		token, issueErr = issueTokenTx(tx, user.ID, r.tokenTTLHours)
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

func issueTokenTx(db *gorm.DB, userID string, ttlHours int) (string, error) {
	if ttlHours <= 0 {
		ttlHours = 720
	}
	ut := &model.UserToken{
		ID:        uuid.New().String(),
		UserID:    userID,
		Token:     uuid.New().String(),
		ExpiresAt: time.Now().Add(time.Duration(ttlHours) * time.Hour),
	}
	return ut.Token, db.Create(ut).Error
}
