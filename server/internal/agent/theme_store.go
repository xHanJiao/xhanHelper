package agent

import (
	"context"
	"log"

	"xhan-helper/internal/service"
)

// ThemeStore — 主题状态页读写的业务层封装
type ThemeStore struct {
	repo *service.ThemeStateRepo
}

func NewThemeStore(repo *service.ThemeStateRepo) *ThemeStore {
	return &ThemeStore{repo: repo}
}

// SaveState 保存或更新主题状态页
func (s *ThemeStore) SaveState(ctx context.Context, themeID, userID, stateText string) error {
	return s.repo.UpdateState(ctx, themeID, userID, stateText)
}

// LoadState 加载主题状态页文本
func (s *ThemeStore) LoadState(ctx context.Context, themeID, userID string) string {
	t, err := s.repo.GetByID(ctx, themeID, userID)
	if err != nil {
		log.Printf("[theme_store] GetByID error: %v", err)
		return ""
	}
	if t == nil {
		return ""
	}
	return t.StateText
}

// ListActive 列出用户所有活跃主题
func (s *ThemeStore) ListActive(ctx context.Context, userID string) ([]ThemeStateInfo, error) {
	list, err := s.repo.ListActive(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := make([]ThemeStateInfo, len(list))
	for i, t := range list {
		result[i] = ThemeStateInfo{
			ID:          t.ID,
			Name:        t.Name,
			Description: t.Description,
			Status:      t.Status,
			StateText:   t.StateText,
		}
	}
	return result, nil
}

// ThemeStateInfo — 主题状态摘要（不含原始状态页全文）
type ThemeStateInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	StateText   string `json:"state_text"`
}
