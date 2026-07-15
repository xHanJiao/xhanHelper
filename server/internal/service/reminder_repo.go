package service

import (
	"context"
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

func (r *ReminderRepo) FindByDeadline(ctx context.Context, userID string, deadline time.Time) (*model.Reminder, error) {
	var rem model.Reminder
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status = ? AND deadline = ?", userID, "pending", deadline).
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

func (r *ReminderRepo) ListExpiredUnmentioned(ctx context.Context, userID string, now time.Time, limit int) ([]model.Reminder, error) {
	var list []model.Reminder
	zeroTime := time.Time{}
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status = ? AND deadline < ? AND mentioned_at = ?", userID, "pending", now, zeroTime).
		Order("deadline DESC").
		Limit(limit).
		Find(&list).Error
	return list, err
}

func (r *ReminderRepo) ListUpcoming(ctx context.Context, userID string, now time.Time, limit int) ([]model.Reminder, error) {
	var list []model.Reminder
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status = ? AND deadline >= ?", userID, "pending", now).
		Order("deadline ASC").
		Limit(limit).
		Find(&list).Error
	return list, err
}

func (r *ReminderRepo) Ack(ctx context.Context, userID, id, msgID string) error {
	return r.db.WithContext(ctx).Model(&model.Reminder{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]interface{}{
			"status":                 "acknowledged",
			"acknowledged_at":        time.Now(),
			"trigger_message_msg_id": msgID,
		}).Error
}

func (r *ReminderRepo) Cancel(ctx context.Context, userID, id string) error {
	return r.db.WithContext(ctx).Model(&model.Reminder{}).
		Where("id = ? AND user_id = ? AND status = ?", id, userID, "pending").
		Update("status", "cancelled").Error
}

func (r *ReminderRepo) MarkMentioned(ctx context.Context, userID, id string) error {
	return r.db.WithContext(ctx).Model(&model.Reminder{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("mentioned_at", time.Now()).Error
}

func (r *ReminderRepo) UpdateGeneratedContent(ctx context.Context, id, title, body, triggerMsg string) error {
	return r.db.WithContext(ctx).Model(&model.Reminder{}).
		Where("id = ? AND status = ?", id, "generating").
		Updates(map[string]interface{}{
			"status":             "pending",
			"notification_title": title,
			"notification_body":  body,
			"trigger_message":    triggerMsg,
		}).Error
}
