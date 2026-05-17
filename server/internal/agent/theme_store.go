package agent

import (
	"context"
	"log"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// ThemeStore — 主题状态页读写的业务层封装
type ThemeStore struct {
	repo *service.ThemeStateRepo
}

func NewThemeStore(repo *service.ThemeStateRepo) *ThemeStore {
	return &ThemeStore{repo: repo}
}

// Create 创建主题
func (s *ThemeStore) Create(ctx context.Context, t *model.ThemeState) error {
	return s.repo.Create(ctx, t)
}

// Expire 清理过期的 candidate/draft 主题
func (s *ThemeStore) Expire(ctx context.Context, userID string, days int) error {
	return s.repo.ExpireCandidates(ctx, userID, days)
}

// GetByName 按名称查找主题
func (s *ThemeStore) GetByName(ctx context.Context, userID, name string) (*ThemeStateInfo, error) {
	t, err := s.repo.GetByName(ctx, userID, name)
	if err != nil || t == nil {
		return nil, err
	}
	return &ThemeStateInfo{
		ID:          t.ID,
		Name:        t.Name,
		Description: t.Description,
		Status:      t.Status,
		StateText:   t.StateText,
	}, nil
}

// ListByStatus 列出指定状态的主题
func (s *ThemeStore) ListByStatus(ctx context.Context, userID, status string) ([]ThemeStateInfo, error) {
	list, _, err := s.repo.List(ctx, userID, status, 200, 0)
	if err != nil {
		return nil, err
	}
	return toThemeStateInfo(list), nil
}

// UpdateStatus 更新主题状态
func (s *ThemeStore) UpdateStatus(ctx context.Context, id, userID, status string) error {
	return s.repo.UpdateStatus(ctx, id, userID, status)
}

// LoadMetadata 加载主题 metadata
func (s *ThemeStore) LoadMetadata(ctx context.Context, themeID, userID string) string {
	t, err := s.repo.GetByID(ctx, themeID, userID)
	if err != nil || t == nil || t.Metadata == nil {
		return ""
	}
	return *t.Metadata
}

// SaveState 保存或更新主题状态页
func (s *ThemeStore) SaveState(ctx context.Context, themeID, userID, stateText string) error {
	return s.repo.UpdateState(ctx, themeID, userID, stateText)
}

// GetName 获取主题名称
func (s *ThemeStore) GetName(ctx context.Context, themeID, userID string) string {
	t, err := s.repo.GetByID(ctx, themeID, userID)
	if err != nil || t == nil {
		return ""
	}
	return t.Name
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
// ListCandidates 列出候选和活跃主题（去重用）
func (s *ThemeStore) ListCandidates(ctx context.Context, userID string) ([]ThemeStateInfo, error) {
	list, err := s.repo.ListByStatus(ctx, userID, []string{"candidate", "active"})
	if err != nil {
		return nil, err
	}
	return toThemeStateInfo(list), nil
}

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
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Status      string  `json:"status"`
	StateText   string  `json:"state_text"`
	Metadata    *string `json:"metadata,omitempty"`
}

func toThemeStateInfo(list []model.ThemeState) []ThemeStateInfo {
	result := make([]ThemeStateInfo, len(list))
	for i, t := range list {
		result[i] = ThemeStateInfo{
			ID:          t.ID,
			Name:        t.Name,
			Description: t.Description,
			Status:      t.Status,
			StateText:   t.StateText,
			Metadata:    t.Metadata,
		}
	}
	return result
}
