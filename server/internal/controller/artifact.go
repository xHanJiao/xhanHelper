package controller

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// ArtifactHandler — HTML 产物的唯一正文出口。
type ArtifactHandler struct {
	repo *service.ArtifactRepo
}

func NewArtifactHandler(repo *service.ArtifactRepo) *ArtifactHandler {
	return &ArtifactHandler{repo: repo}
}

// GetArtifact GET /api/artifacts/:id — 返回单条（含正文）。
// 消息列表永远不带正文，只有这个接口带（见 docs/design/html-artifact §4.1）。
func (h *ArtifactHandler) GetArtifact(c *gin.Context) {
	userID := c.GetString("user_id")
	a, err := h.repo.GetByID(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			view.JSONError(c, http.StatusNotFound, "artifact not found")
			return
		}
		log.Printf("[artifact] GetArtifact id=%s user=%s: %v", c.Param("id"), userID, err)
		view.JSONError(c, http.StatusInternalServerError, "query artifact failed")
		return
	}
	view.JSONOK(c, gin.H{
		"id":         a.ID,
		"title":      a.Title,
		"summary":    a.Summary,
		"html":       a.HTML,
		"created_at": a.CreatedAt,
	})
}

// ListArtifacts GET /api/artifacts?limit=30&offset=0 — 灵阅列表（按时间倒序，不含正文）。
// 直接返回 model.Artifact：HTML 字段是 json:"-"、DeletedAt 也不序列化，
// 所以这里不需要再定义一个 DTO 去防漏（见 docs/design/html-artifact §9.5）。
// limit 默认 30 与客户端 ArtifactViewModel.pageSize 一致；两边改一处时记得对齐。
func (h *ArtifactHandler) ListArtifacts(c *gin.Context) {
	userID := c.GetString("user_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	// limit 超界/非数字回默认值，不让一次误传把整表拉出来
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}

	items, total, err := h.repo.ListByUser(c.Request.Context(), userID, limit, offset)
	if err != nil {
		log.Printf("[artifact] ListArtifacts user=%s: %v", userID, err)
		view.JSONError(c, http.StatusInternalServerError, "query artifacts failed")
		return
	}
	view.JSONOK(c, gin.H{"items": items, "total": total})
}

// DeleteArtifact DELETE /api/artifacts/:id — 软删（无回收站，见 design §9.7）。
// 分享 token 在同一行，删掉即失效。
func (h *ArtifactHandler) DeleteArtifact(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")
	rows, err := h.repo.Delete(c.Request.Context(), userID, id)
	if err != nil {
		log.Printf("[artifact] DeleteArtifact id=%s user=%s: %v", id, userID, err)
		view.JSONError(c, http.StatusInternalServerError, "delete artifact failed")
		return
	}
	if rows == 0 {
		view.JSONError(c, http.StatusNotFound, "artifact not found")
		return
	}
	view.JSONOK(c, gin.H{"deleted": true})
}

// CreateArtifactShare POST /api/artifacts/:id/share — 生成或复用分享 token（幂等）。
// 幂等是必须的：重复点「复制链接」不能把已经发出去的旧链接作废（见 design §9.6）。
func (h *ArtifactHandler) CreateArtifactShare(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	token, err := h.repo.GetShareToken(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			view.JSONError(c, http.StatusNotFound, "artifact not found")
			return
		}
		log.Printf("[artifact] CreateShare get id=%s user=%s: %v", id, userID, err)
		view.JSONError(c, http.StatusInternalServerError, "query artifact failed")
		return
	}
	if token == "" {
		token = uuid.NewString()
		rows, err := h.repo.SetShareToken(c.Request.Context(), userID, id, token)
		if err != nil {
			log.Printf("[artifact] CreateShare set id=%s user=%s: %v", id, userID, err)
			view.JSONError(c, http.StatusInternalServerError, "share artifact failed")
			return
		}
		if rows == 0 {
			view.JSONError(c, http.StatusNotFound, "artifact not found")
			return
		}
	}
	view.JSONOK(c, gin.H{"token": token, "url": "/api/share/artifacts/" + token})
}

// DeleteArtifactShare DELETE /api/artifacts/:id/share — 撤销分享，旧链接立即 404。
func (h *ArtifactHandler) DeleteArtifactShare(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	// 先确认行存在：Update 影响 0 行既可能是「本来就没分享」也可能是「不存在」，得区分。
	if _, err := h.repo.GetShareToken(c.Request.Context(), userID, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			view.JSONError(c, http.StatusNotFound, "artifact not found")
			return
		}
		log.Printf("[artifact] RevokeShare get id=%s user=%s: %v", id, userID, err)
		view.JSONError(c, http.StatusInternalServerError, "query artifact failed")
		return
	}
	if _, err := h.repo.ClearShareToken(c.Request.Context(), userID, id); err != nil {
		log.Printf("[artifact] RevokeShare clear id=%s user=%s: %v", id, userID, err)
		view.JSONError(c, http.StatusInternalServerError, "revoke share failed")
		return
	}
	view.JSONOK(c, gin.H{"shared": false})
}

// ViewSharedArtifact GET /api/share/artifacts/:token — 公开只读，直接吐 HTML（见 design §9.6）。
//
// 安全靠两层：入库时已清洗（script/iframe/on* 全删、图片内联 data:），这里再用响应头压一层。
// `sandbox` 把页面放进不透明源 —— 万一清洗漏了，脚本也摸不到同域 Web 端 localStorage 里的 token。
// `no-store` 是「可撤销」的前提：允许缓存就等于撤销不彻底。
func (h *ArtifactHandler) ViewSharedArtifact(c *gin.Context) {
	token := c.Param("token")
	if token == "" {
		view.JSONError(c, http.StatusNotFound, "分享链接无效")
		return
	}
	a, err := h.repo.GetByShareToken(c.Request.Context(), token)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			view.JSONError(c, http.StatusNotFound, "分享链接无效或已撤销")
			return
		}
		log.Printf("[artifact] ViewShared token=%s: %v", token, err)
		view.JSONError(c, http.StatusInternalServerError, "query artifact failed")
		return
	}
	c.Header("Content-Security-Policy",
		"sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(a.HTML))
}
