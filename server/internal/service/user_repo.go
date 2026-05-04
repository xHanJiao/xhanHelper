package service

import (
	"context"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/google/uuid"
	"xhan-helper/internal/model"
)

// UserRepo — 用户数据访问
type UserRepo struct {
	db *gorm.DB
}

func NewUserRepo(db *gorm.DB) *UserRepo {
	return &UserRepo{db: db}
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
		token, issueErr = issueTokenTx(tx, user.ID)
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
		var issueErr error
		token, issueErr = issueTokenTx(tx, user.ID)
		return issueErr
	})
	if err != nil {
		return nil, "", err
	}
	return &user, token, nil
}

func (r *UserRepo) Logout(ctx context.Context, token string) error {
	return r.db.WithContext(ctx).Where("token = ?", token).Delete(&model.UserToken{}).Error
}

func (r *UserRepo) FindByToken(ctx context.Context, token string) (*model.User, error) {
	var ut model.UserToken
	if err := r.db.WithContext(ctx).Where("token = ?", token).First(&ut).Error; err != nil {
		return nil, err
	}
	var u model.User
	if err := r.db.WithContext(ctx).Where("id = ?", ut.UserID).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

func issueTokenTx(db *gorm.DB, userID string) (string, error) {
	ut := &model.UserToken{
		ID:     uuid.New().String(),
		UserID: userID,
		Token:  uuid.New().String(),
	}
	return ut.Token, db.Create(ut).Error
}
