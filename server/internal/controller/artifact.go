package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// maxBatchDelete — 单次批量删除上限。灵阅一页 30 条，100 足够，同时挡住"误传整表"。
const maxBatchDelete = 100

// ArtifactHandler — HTML 产物的唯一正文出口。
type ArtifactHandler struct {
	repo *service.ArtifactRepo
}

func NewArtifactHandler(repo *service.ArtifactRepo) *ArtifactHandler {
	return &ArtifactHandler{repo: repo}
}

// GetArtifact GET /api/artifacts/:id — 返回单条（含正文，JSON）。
// 消息列表永远不带正文；裸 HTML + ETag 的出口见 ServeArtifactHTML（§4.1 / §9.8）。
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
		"tags":       a.Tags,
		"html":       a.HTML,
		"created_at": a.CreatedAt,
	})
}

// artifactCSP — 产物页的兜底 CSP（公开分享与裸 HTML 出口共用）。
// sandbox 把页面放进不透明源；图片只允许 data:（产物已内联）、样式只允许内联。
const artifactCSP = "sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:"

// writeHTML — 直接写字符串，避免 c.Data 把整页再复制一份（8MB 级）。
// 写失败（客户端断开）只记日志：响应已经发出去了，没有能补救的动作。
func writeHTML(c *gin.Context, html string) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Content-Length", strconv.Itoa(len(html)))
	c.Status(http.StatusOK)
	if _, err := c.Writer.WriteString(html); err != nil {
		log.Printf("[artifact] write html bytes=%d: %v", len(html), err)
	}
}

// ServeArtifactHTML GET /api/artifacts/:id/html — 裸 HTML 出口，给客户端正文缓存用（§9.8）。
//
// 与 GetArtifact 的区别：不包 JSON、带 ETag。客户端带 If-None-Match 命中时回 304，
// 避免每次重开都拉整页（带图页面能到几 MB）。no-store 是不让 URLSession/代理再存一份；
// 缓存由客户端自己按 ETag 管。
func (h *ArtifactHandler) ServeArtifactHTML(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")
	a, err := h.repo.GetHTML(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			view.JSONError(c, http.StatusNotFound, "artifact not found")
			return
		}
		log.Printf("[artifact] ServeHTML id=%s user=%s: %v", id, userID, err)
		view.JSONError(c, http.StatusInternalServerError, "query artifact failed")
		return
	}

	etag := artifactETag(a.HTML)
	c.Header("ETag", etag)
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", artifactCSP)
	if etagMatches(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		return
	}
	writeHTML(c, a.HTML)
}

// artifactETag — 正文哈希。artifacts 会原地覆盖（改版不换 id），所以不能用 id 当版本。
func artifactETag(html string) string {
	sum := sha256.Sum256([]byte(html))
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// etagMatches — 按 RFC 的列表形式逐项比较（`"a", W/"b"`），不做子串匹配：
// 误判 304 会让客户端一直停在旧页。If-None-Match 用弱比较，所以去掉 W/ 前缀再比。
func etagMatches(header, etag string) bool {
	if header == "*" {
		return true
	}
	for _, part := range strings.Split(header, ",") {
		candidate := strings.TrimSpace(part)
		if strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}

// ListArtifacts GET /api/artifacts?limit=30&offset=0&tags=a,b&keyword= — 灵阅列表（按时间倒序，不含正文）。
// 直接返回 model.Artifact：HTML 字段是 json:"-"、DeletedAt 也不序列化，
// 所以这里不需要再定义一个 DTO 去防漏（见 docs/design/html-artifact §9.5）。
// limit 默认 30 与客户端 ArtifactViewModel.pageSize 一致；limit=0 表示只取 total（词云「查看 N 篇」用）。
// tags 是逗号分隔的标签名，多个按**交集**过滤（与文档的标签筛选同口径）。
func (h *ArtifactHandler) ListArtifacts(c *gin.Context) {
	userID := c.GetString("user_id")
	// limit=0 是「只取总数」的哨兵；参数非法一律 400，不能把 Atoi 的失败当成 0
	limit := 30
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 100 {
			view.JSONError(c, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}
	offset := 0
	if raw := c.Query("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			view.JSONError(c, http.StatusBadRequest, "invalid offset")
			return
		}
		offset = n
	}
	tags := parseTagQuery(c.Query("tags"))
	if len(tags) > 10 {
		view.JSONError(c, http.StatusBadRequest, "too many tags")
		return
	}

	items, total, err := h.repo.ListByTags(c.Request.Context(), userID, tags, strings.TrimSpace(c.Query("keyword")), limit, offset)
	if err != nil {
		log.Printf("[artifact] ListArtifacts user=%s: %v", userID, err)
		view.JSONError(c, http.StatusInternalServerError, "query artifacts failed")
		return
	}
	view.JSONOK(c, gin.H{"items": items, "total": total})
}

// parseTagQuery — 逗号分隔的标签参数 → 去空去重的切片。
func parseTagQuery(raw string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, 4)
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// ListArtifactTags GET /api/artifacts/tags — 词云用的标签频次 + 共现边（与文档 /documents/tags 同构）。
func (h *ArtifactHandler) ListArtifactTags(c *gin.Context) {
	userID := c.GetString("user_id")
	tags, edges, err := h.repo.TagGraph(c.Request.Context(), userID)
	if err != nil {
		log.Printf("[artifact] ListArtifactTags user=%s: %v", userID, err)
		view.JSONError(c, http.StatusInternalServerError, "query artifact tags failed")
		return
	}
	if tags == nil {
		tags = []service.TagCount{}
	}
	if edges == nil {
		edges = []service.TagEdge{}
	}
	view.JSONOK(c, gin.H{"tags": tags, "edges": edges})
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

// BatchDeleteArtifacts POST /api/artifacts/batch-delete — 灵阅多选删除（软删，见 design §9.7）。
// 只删属于当前用户的；越权 id 静默跳过（返回实际删除行数），不报 404——
// 批量删除里一条失效 id 不该让整次操作失败。
func (h *ArtifactHandler) BatchDeleteArtifacts(c *gin.Context) {
	userID := c.GetString("user_id")
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, http.StatusBadRequest, "invalid body")
		return
	}
	// 去重 + 上限：一次误传整个列表不该把库里所有页面删光
	seen := make(map[string]struct{}, len(req.IDs))
	ids := make([]string, 0, len(req.IDs))
	for _, id := range req.IDs {
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		view.JSONError(c, http.StatusBadRequest, "ids is empty")
		return
	}
	if len(ids) > maxBatchDelete {
		view.JSONError(c, http.StatusBadRequest, "too many ids")
		return
	}
	rows, err := h.repo.DeleteMany(c.Request.Context(), userID, ids)
	if err != nil {
		log.Printf("[artifact] BatchDelete user=%s n=%d: %v", userID, len(ids), err)
		view.JSONError(c, http.StatusInternalServerError, "delete artifacts failed")
		return
	}
	view.JSONOK(c, gin.H{"deleted": rows})
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
	token, err := h.repo.GetShareToken(c.Request.Context(), userID, id)
	if err != nil {
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
	// revoked=false：本来就没分享。客户端据此换文案，别让「已撤销」成了假话。
	view.JSONOK(c, gin.H{"shared": false, "revoked": token != ""})
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
	c.Header("Content-Security-Policy", artifactCSP)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Cache-Control", "no-store")
	writeHTML(c, a.HTML)
}
