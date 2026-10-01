package controller

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// DocumentHandler — 文档 API handler
type DocumentHandler struct {
	docRepo *service.DocumentRepo
}

func NewDocumentHandler(docRepo *service.DocumentRepo) *DocumentHandler {
	return &DocumentHandler{docRepo: docRepo}
}

// ListDocuments GET /api/documents?tag=&tags=&keyword=&fields=title,tag,content&audience=&limit=&offset=
// tags 为逗号分隔的多标签**交集**（文档须同时含全部标签；标签名内不含逗号）；tag 为旧的单标签筛选，二者叠加。
// audience 控制读者层级：缺省 finished（列表默认只给成品）；internal=只看材料；all=不过滤。
// 响应 total 为当前筛选命中的总数（客户端三态按钮只显示当前档，无需额外计数）。
func (h *DocumentHandler) ListDocuments(c *gin.Context) {
	userID := c.GetString("user_id")
	keyword := c.Query("keyword")
	fields := parseDocFields(c.Query("fields"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	audience, ok := model.ParseDocumentAudienceFilter(c.Query("audience"))
	if !ok {
		view.JSONError(c, http.StatusBadRequest, "audience 必须为 finished / internal / all")
		return
	}

	tags := parseDocTags(c.Query("tags"))
	if tag := strings.TrimSpace(c.Query("tag")); tag != "" {
		tags = append([]string{tag}, tags...)
	}

	list, total, err := h.docRepo.ListByAllTags(c.Request.Context(), userID, tags, keyword, fields, audience, limit, offset)
	if err != nil {
		log.Printf("ListDocuments: list user=%s tags=%v keyword=%q fields=%v audience=%q: %v", userID, tags, keyword, fields, audience, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if list == nil {
		list = []model.Document{}
	}

	// total 即当前 audience 档的命中数，客户端三态按钮只显示当前档，无需额外 count。
	view.JSONOK(c, gin.H{
		"documents": list,
		"total":     total,
	})
}

// parseDocTags 解析 tags=A,C,D：按逗号切分、去空格、去空项、保序去重。
func parseDocTags(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.Split(raw, ",") {
		if t = strings.TrimSpace(t); t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// parseDocFields 解析 fields=title,tag,content，忽略未知项；空/非法返回 nil（repo 回退 title+content）。
func parseDocFields(raw string) []string {
	var out []string
	for _, f := range strings.Split(raw, ",") {
		switch f = strings.TrimSpace(f); f {
		case "title", "tag", "content":
			out = append(out, f)
		}
	}
	return out
}

// ListDocumentTags GET /api/documents/tags?audience= — 词云用的标签及出现次数 + 共现边
// audience 与列表同口径：缺省 finished（只统计成品），all=不过滤，internal=只看材料。
func (h *DocumentHandler) ListDocumentTags(c *gin.Context) {
	userID := c.GetString("user_id")
	audience, ok := model.ParseDocumentAudienceFilter(c.Query("audience"))
	if !ok {
		view.JSONError(c, http.StatusBadRequest, "audience 必须为 finished / internal / all")
		return
	}

	tags, edges, err := h.docRepo.TagGraph(c.Request.Context(), userID, audience)
	if err != nil {
		log.Printf("ListDocumentTags: user=%s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

// GetDocument GET /api/documents/:id
func (h *DocumentHandler) GetDocument(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	doc, err := h.docRepo.GetByID(c.Request.Context(), id, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
		return
	}

	view.JSONOK(c, doc)
}

// DeleteDocument DELETE /api/documents/:id — 软删（移入回收站，7 天内可恢复）
func (h *DocumentHandler) DeleteDocument(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	rows, err := h.docRepo.Delete(c.Request.Context(), id, userID)
	if err != nil {
		log.Printf("DeleteDocument: delete id=%s user=%s: %v", id, userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
		return
	}

	// 批注随文档一起留到恢复期结束（PurgeExpired 物理清理时一并删），此处不动
	view.JSONOK(c, gin.H{"deleted": true})
}

// ListTrashDocuments GET /api/documents/trash?limit=&offset= — 回收站列表
func (h *DocumentHandler) ListTrashDocuments(c *gin.Context) {
	userID := c.GetString("user_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	list, total, err := h.docRepo.ListTrash(c.Request.Context(), userID, limit, offset)
	if err != nil {
		log.Printf("ListTrashDocuments: user=%s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if list == nil {
		list = []model.Document{}
	}

	view.JSONOK(c, gin.H{
		"documents": list,
		"total":     total,
	})
}

// RestoreDocument POST /api/documents/:id/restore — 从回收站恢复
func (h *DocumentHandler) RestoreDocument(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	rows, err := h.docRepo.Restore(c.Request.Context(), id, userID)
	if err != nil {
		log.Printf("RestoreDocument: restore id=%s user=%s: %v", id, userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "document not found in trash"})
		return
	}

	view.JSONOK(c, gin.H{"restored": true})
}

// RestoreAllTrashDocuments POST /api/documents/trash/restore — 全部恢复
func (h *DocumentHandler) RestoreAllTrashDocuments(c *gin.Context) {
	userID := c.GetString("user_id")

	n, err := h.docRepo.RestoreAll(c.Request.Context(), userID)
	if err != nil {
		log.Printf("RestoreAllTrashDocuments: user=%s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	view.JSONOK(c, gin.H{"restored": n})
}

// PurgeAllTrashDocuments DELETE /api/documents/trash — 清空回收站（永久删除）
func (h *DocumentHandler) PurgeAllTrashDocuments(c *gin.Context) {
	userID := c.GetString("user_id")

	n, err := h.docRepo.PurgeAll(c.Request.Context(), userID)
	if err != nil {
		log.Printf("PurgeAllTrashDocuments: user=%s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	view.JSONOK(c, gin.H{"purged": n})
}
