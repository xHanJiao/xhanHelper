package service

import (
	"context"
	"time"

	"xhan-helper/internal/model"
)

// MailboxService — 信箱组装服务（分页拉取 + 批量加载依赖 + 逐条打包）
type MailboxService struct {
	mailboxRepo *MailboxRepo
	themeRepo   *ThemeStateRepo
}

// NewMailboxService 创建信箱组装服务
func NewMailboxService(mailboxRepo *MailboxRepo, themeRepo *ThemeStateRepo) *MailboxService {
	return &MailboxService{mailboxRepo: mailboxRepo, themeRepo: themeRepo}
}

// ListPacked 分页拉取信箱列表，批量加载依赖，逐条打包后返回
func (s *MailboxService) ListPacked(ctx context.Context, userID, itemType string, isRead *bool, limit, offset int) ([]model.PackedMailboxItem, int64, error) {
	// 1. 分页拉取信箱原始列表
	items, total, err := s.mailboxRepo.ListByUser(ctx, userID, itemType, isRead, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	if len(items) == 0 {
		return nil, total, nil
	}

	// 2. 收集需要批量加载的数据
	themeIDs := make([]string, 0)
	for _, item := range items {
		if item.EntityType == "theme" && item.EntityID != "" {
			themeIDs = append(themeIDs, item.EntityID)
		}
	}

	// 3. 批量拉取主题信息
	var themeMap map[string]*model.ThemeState
	if len(themeIDs) > 0 {
		themeMap, err = s.themeRepo.BatchGetByIDs(ctx, themeIDs, userID)
		if err != nil {
			return nil, 0, err
		}
	}

	// 4. 逐条打包
	packed := make([]model.PackedMailboxItem, 0, len(items))
	for _, item := range items {
		packed = append(packed, packItem(item, themeMap))
	}

	return packed, total, nil
}

// packItem 打包单条信箱消息，计算 tags
func packItem(item model.MailboxItem, _ map[string]*model.ThemeState) model.PackedMailboxItem {
	p := model.PackedMailboxItem{
		ID:         item.ID,
		UserID:     item.UserID,
		Title:      item.Title,
		Content:    item.Content,
		Greeting:   item.Greeting,
		Type:       item.Type,
		Source:     item.Source,
		EntityType: item.EntityType,
		EntityID:   item.EntityID,
		IsRead:     item.IsRead,
		IsArchived: item.IsArchived,
		CreatedAt:  item.CreatedAt.Format(time.RFC3339),
	}
	return p
}
