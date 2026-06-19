package service

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// MessageShareRepo 消息分享仓库
type MessageShareRepo struct {
	db      *gorm.DB
	msgRepo *MessageRepo
}

// NewMessageShareRepo 创建消息分享仓库
func NewMessageShareRepo(db *gorm.DB, msgRepo *MessageRepo) *MessageShareRepo {
	return &MessageShareRepo{db: db, msgRepo: msgRepo}
}

// PublicMessageView 公开分享视图中展示的消息（不含 user_id）
type PublicMessageView struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Create 创建消息分享。验证 userMsgIDs 归属 → 展开完整 round → 生成 token → 写入。
func (r *MessageShareRepo) Create(ctx context.Context, convID, userID string, userMsgIDs []string) (*model.MessageShare, error) {
	// 验证 userMsgIDs 属于当前用户且属于该对话
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("id IN ? AND user_id = ? AND conversation_id = ? AND role = 'user' AND status = ?",
			userMsgIDs, userID, convID, model.MsgStatusNormal).
		Count(&count).Error; err != nil {
		return nil, err
	}
	if int(count) != len(userMsgIDs) {
		return nil, errors.New("部分消息不属于当前用户或对话")
	}

	// 展开 rounds：复用 MessageRepo.GetRoundsByUserMsgIDs
	rounds, err := r.msgRepo.GetRoundsByUserMsgIDs(ctx, userMsgIDs)
	if err != nil {
		return nil, err
	}

	// 收集所有消息 ID（仅 user + assistant，过滤 tool 和 IsInternal）
	var allMsgIDs []string
	for _, round := range rounds {
		for _, msg := range round {
			if msg.Role == "tool" || msg.IsInternal {
				continue
			}
			allMsgIDs = append(allMsgIDs, msg.ID)
		}
	}
	if len(allMsgIDs) == 0 {
		return nil, errors.New("没有可分享的消息")
	}

	msgIDsJSON, err := json.Marshal(allMsgIDs)
	if err != nil {
		return nil, err
	}

	share := &model.MessageShare{
		ID:             uuid.New().String(),
		Token:          uuid.New().String(),
		ConversationID: convID,
		MessageIDs:     string(msgIDsJSON),
		CreatedBy:      userID,
		ExpiresAt:      time.Now().Add(7 * 24 * time.Hour),
	}

	if err := r.db.WithContext(ctx).Create(share).Error; err != nil {
		return nil, err
	}

	return share, nil
}

// GetByToken 根据 token 获取分享信息，同时验证过期
func (r *MessageShareRepo) GetByToken(ctx context.Context, token string) (*model.MessageShare, error) {
	var share model.MessageShare
	if err := r.db.WithContext(ctx).Where("token = ?", token).First(&share).Error; err != nil {
		return nil, err
	}
	if time.Now().After(share.ExpiresAt) {
		return &share, errors.New("分享已过期")
	}
	return &share, nil
}

// GetMessagesByToken 根据 token 获取分享的消息列表（扁平，仅 user+assistant，按时间正序）
func (r *MessageShareRepo) GetMessagesByToken(ctx context.Context, token string) ([]PublicMessageView, error) {
	share, err := r.GetByToken(ctx, token)
	if err != nil {
		return nil, err
	}

	var msgIDs []string
	if err := json.Unmarshal([]byte(share.MessageIDs), &msgIDs); err != nil {
		log.Printf("[share] unmarshal message_ids failed: token=%s err=%v", token, err)
		return nil, errors.New("分享数据异常")
	}

	if len(msgIDs) == 0 {
		return []PublicMessageView{}, nil
	}

	// 批量获取消息，按时间正序
	var msgs []model.Message
	if err := r.db.WithContext(ctx).
		Where("id IN ? AND status = ?", msgIDs, model.MsgStatusNormal).
		Order("created_at ASC").
		Find(&msgs).Error; err != nil {
		return nil, err
	}

	// 转为公开视图
	result := make([]PublicMessageView, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, PublicMessageView{
			ID:        m.ID,
			Role:      m.Role,
			Content:   m.Content,
			CreatedAt: m.CreatedAt,
		})
	}
	return result, nil
}

// IncrementVisitCount 递增分享链接的访问次数
func (r *MessageShareRepo) IncrementVisitCount(ctx context.Context, token string) {
	if err := r.db.WithContext(ctx).Model(&model.MessageShare{}).Where("token = ?", token).UpdateColumn("visit_count", gorm.Expr("visit_count + 1")).Error; err != nil {
		log.Printf("[share] increment visit count failed: token=%s err=%v", token, err)
	}
}
