package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

type ReminderRepo struct {
	db *gorm.DB
}

func NewReminderRepo(db *gorm.DB) *ReminderRepo {
	return &ReminderRepo{db: db}
}

func (r *ReminderRepo) Create(ctx context.Context, rem *model.Reminder) error {
	return r.db.WithContext(ctx).Create(rem).Error
}

func (r *ReminderRepo) FindByID(ctx context.Context, userID, id string) (*model.Reminder, error) {
	var rem model.Reminder
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		First(&rem).Error
	if err != nil {
		return nil, err
	}
	return &rem, nil
}

func (r *ReminderRepo) List(ctx context.Context, userID, status string, limit, offset int) ([]model.Reminder, int64, error) {
	var list []model.Reminder
	var total int64

	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if status != "" {
		q = q.Where("status = ?", status)
	}

	if err := q.Model(&model.Reminder{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}

	return list, total, nil
}

func (r *ReminderRepo) Ack(ctx context.Context, userID, id, msgID string) error {
	// 空 id 时 GORM 丢弃 PK → 退化成该用户全部提醒置为已确认。挡调用方 typo。
	if id == "" {
		return errors.New("reminder id 为空")
	}
	return r.db.WithContext(ctx).Model(&model.Reminder{ID: id}).
		Where("user_id = ?", userID).
		Updates(map[string]interface{}{
			"status":                 model.ReminderStatusAcknowledged,
			"acknowledged_at":        time.Now(),
			"trigger_message_msg_id": msgID,
		}).Error
}

func (r *ReminderRepo) Cancel(ctx context.Context, userID, id string) error {
	return r.db.WithContext(ctx).Model(&model.Reminder{}).
		Where("id = ? AND user_id = ? AND status = ?", id, userID, model.ReminderStatusPending).
		Update("status", model.ReminderStatusCancelled).Error
}

func (r *ReminderRepo) UpdateGeneratedContent(ctx context.Context, id, body, triggerMsg string) error {
	return r.db.WithContext(ctx).Model(&model.Reminder{}).
		Where("id = ? AND status = ?", id, model.ReminderStatusGenerating).
		Updates(map[string]interface{}{
			"status":            model.ReminderStatusPending,
			"notification_body": body,
			"trigger_message":   triggerMsg,
		}).Error
}

func (r *ReminderRepo) Search(ctx context.Context, userID, query, status string, limit, offset int) ([]model.Reminder, int64, error) {
	var list []model.Reminder
	var total int64

	q := r.db.WithContext(ctx).
		Where("user_id = ? AND content LIKE ?", userID, "%"+query+"%")
	if status != "" {
		q = q.Where("status = ?", status)
	}

	if err := q.Model(&model.Reminder{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}

	return list, total, nil
}
