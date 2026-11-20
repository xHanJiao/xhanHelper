package controller

import (
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xhan-helper/internal/profilefield"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// ProfileHandler — 用户画像 REST API（设置页「关于我」）。
// 只暴露 active 事实：pending（观察中）与 rejected（用户删掉）都不出现在这里。
type ProfileHandler struct {
	factRepo *service.UserProfileFactRepo
}

// NewProfileHandler 创建 ProfileHandler。
func NewProfileHandler(factRepo *service.UserProfileFactRepo) *ProfileHandler {
	return &ProfileHandler{factRepo: factRepo}
}

type profileItemDTO struct {
	ID            string     `json:"id"`
	Value         string     `json:"value"`
	Source        string     `json:"source"`
	EvidenceCount int        `json:"evidence_count"`
	ObservedAt    *time.Time `json:"observed_at,omitempty"`
	LastSeenAt    *time.Time `json:"last_seen_at,omitempty"`
}

type profileFieldDTO struct {
	Field      string           `json:"field"`
	Kind       string           `json:"kind"`
	InjectDesc string           `json:"inject_desc"`
	MaxLen     int              `json:"max_len"`
	MaxFacts   int              `json:"max_facts"`
	Items      []profileItemDTO `json:"items"`
}

type profileLayerDTO struct {
	Layer  int               `json:"layer"`
	Name   string            `json:"name"`
	Fields []profileFieldDTO `json:"fields"`
}

// GetProfile GET /api/profile — 按层分组返回全部白名单字段（空字段也返回，供客户端新增）。
func (h *ProfileHandler) GetProfile(c *gin.Context) {
	userID := c.GetString("user_id")
	facts, err := h.factRepo.GetActiveByUser(c.Request.Context(), userID)
	if err != nil {
		log.Printf("[profile] GetActiveByUser error: %v", err)
		view.JSONError(c, 500, "query profile failed")
		return
	}
	byField := make(map[string][]profileItemDTO, len(facts))
	for _, f := range facts {
		if !profilefield.InProfileWhitelist(f.Field) {
			continue
		}
		byField[f.Field] = append(byField[f.Field], profileItemDTO{
			ID:            f.ID,
			Value:         f.Value,
			Source:        f.Source,
			EvidenceCount: f.EvidenceCount,
			ObservedAt:    f.ObservedAt,
			LastSeenAt:    f.LastSeenAt,
		})
	}

	groups := make([]profileLayerDTO, 0, 6)
	for _, def := range profilefield.ProfileFields {
		if n := len(groups); n == 0 || groups[n-1].Layer != def.Layer {
			groups = append(groups, profileLayerDTO{
				Layer:  def.Layer,
				Name:   profilefield.LayerName(def.Layer),
				Fields: make([]profileFieldDTO, 0, 5),
			})
		}
		items := byField[def.Field]
		if items == nil {
			items = []profileItemDTO{} // Swift 侧 [Item] 解码不接受 null
		}
		groups[len(groups)-1].Fields = append(groups[len(groups)-1].Fields, profileFieldDTO{
			Field:      def.Field,
			Kind:       def.Kind,
			InjectDesc: def.InjectDesc,
			MaxLen:     def.MaxLen,
			MaxFacts:   def.MaxFacts,
			Items:      items,
		})
	}
	view.JSONOK(c, gin.H{"groups": groups})
}

// CreateFact POST /api/profile/facts — 用户手动新增一条事实（source=user，LLM 不得覆盖）。
func (h *ProfileHandler) CreateFact(c *gin.Context) {
	userID := c.GetString("user_id")
	var req struct {
		Field string `json:"field"`
		Value string `json:"value"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "invalid body")
		return
	}
	def := profilefield.ProfileFieldDesc(strings.TrimSpace(req.Field))
	if def == nil {
		view.JSONError(c, 400, "未知画像字段")
		return
	}
	value := strings.TrimSpace(req.Value)
	if value == "" {
		view.JSONError(c, 400, "内容不能为空")
		return
	}
	if n := len([]rune(value)); n > def.MaxLen {
		view.JSONError(c, 400, "内容超出该字段上限（"+strconv.Itoa(def.MaxLen)+" 字）")
		return
	}
	// 同内容已在 → 直接告诉用户，别报成"条数超限"
	existing, err := h.factRepo.GetActiveByField(c.Request.Context(), userID, def.Field)
	if err != nil {
		log.Printf("[profile] GetActiveByField error: %v", err)
		view.JSONError(c, 500, "add fact failed")
		return
	}
	for _, f := range existing {
		if strings.TrimSpace(f.Value) == value {
			view.JSONError(c, 400, "该条目已存在")
			return
		}
	}
	count, err := h.factRepo.CountActiveByField(c.Request.Context(), userID, def.Field)
	if err != nil {
		log.Printf("[profile] CountActiveByField error: %v", err)
		view.JSONError(c, 500, "add fact failed")
		return
	}
	if count >= int64(def.MaxFacts) {
		view.JSONError(c, 400, "该字段最多 "+strconv.Itoa(def.MaxFacts)+" 条，请先删一条")
		return
	}
	fact, err := h.factRepo.CreateUserFact(c.Request.Context(), userID, def.Field, value)
	if err != nil {
		log.Printf("[profile] CreateUserFact error: %v", err)
		view.JSONError(c, 500, "add fact failed")
		return
	}
	view.JSONOK(c, profileItemDTO{
		ID:            fact.ID,
		Value:         fact.Value,
		Source:        fact.Source,
		EvidenceCount: fact.EvidenceCount,
		ObservedAt:    fact.ObservedAt,
		LastSeenAt:    fact.LastSeenAt,
	})
}

// UpdateFact PATCH /api/profile/facts/:id — 改一条事实（改后归用户，LLM 不得覆盖）。
func (h *ProfileHandler) UpdateFact(c *gin.Context) {
	userID := c.GetString("user_id")
	var req struct {
		Value string `json:"value"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "invalid body")
		return
	}
	value := strings.TrimSpace(req.Value)
	if value == "" {
		view.JSONError(c, 400, "内容不能为空")
		return
	}
	fact, err := h.factRepo.GetByID(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			view.JSONError(c, 404, "fact not found")
			return
		}
		log.Printf("[profile] GetByID error: %v", err)
		view.JSONError(c, 500, "update fact failed")
		return
	}
	def := profilefield.ProfileFieldDesc(fact.Field)
	if def == nil {
		view.JSONError(c, 400, "该字段已下线，无法编辑")
		return
	}
	if n := len([]rune(value)); n > def.MaxLen {
		view.JSONError(c, 400, "内容超出该字段上限（"+strconv.Itoa(def.MaxLen)+" 字）")
		return
	}
	if err := h.factRepo.UpdateUserFactValue(c.Request.Context(), userID, fact.ID, value, def.MaxLen); err != nil {
		log.Printf("[profile] UpdateUserFactValue error: %v", err)
		view.JSONError(c, 500, "update fact failed")
		return
	}
	view.JSONOK(c, gin.H{"id": fact.ID})
}

// DeleteFact DELETE /api/profile/facts/:id — 用户删除（置 rejected，LLM 永不复活）。
func (h *ProfileHandler) DeleteFact(c *gin.Context) {
	userID := c.GetString("user_id")
	if err := h.factRepo.RejectFact(c.Request.Context(), userID, c.Param("id")); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			view.JSONError(c, 404, "fact not found")
			return
		}
		log.Printf("[profile] RejectFact error: %v", err)
		view.JSONError(c, 500, "delete fact failed")
		return
	}
	view.JSONOK(c, gin.H{"deleted": true})
}
