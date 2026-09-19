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
	docRepo  *service.DocumentRepo
	annoRepo *service.DocumentAnnotationRepo
}

func NewDocumentHandler(docRepo *service.DocumentRepo, annoRepo *service.DocumentAnnotationRepo) *DocumentHandler {
	return &DocumentHandler{docRepo: docRepo, annoRepo: annoRepo}
}

// ListDocuments GET /api/documents?tag=&tags=&keyword=&fields=title,tag,content&limit=&offset=
// tags 为逗号分隔的多标签**交集**（文档须同时含全部标签；标签名内不含逗号）；tag 为旧的单标签筛选，二者叠加。
func (h *DocumentHandler) ListDocuments(c *gin.Context) {
	userID := c.GetString("user_id")
	keyword := c.Query("keyword")
	fields := parseDocFields(c.Query("fields"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	tags := parseDocTags(c.Query("tags"))
	if tag := strings.TrimSpace(c.Query("tag")); tag != "" {
		tags = append([]string{tag}, tags...)
	}

	list, total, err := h.docRepo.ListByAllTags(c.Request.Context(), userID, tags, keyword, fields, limit, offset)
	if err != nil {
		log.Printf("ListDocuments: list user=%s tags=%v keyword=%q fields=%v: %v", userID, tags, keyword, fields, err)
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

// ListDocumentTags GET /api/documents/tags — 词云用的标签及出现次数 + 共现边
func (h *DocumentHandler) ListDocumentTags(c *gin.Context) {
	userID := c.GetString("user_id")

	tags, edges, err := h.docRepo.TagGraph(c.Request.Context(), userID)
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

// DeleteDocument DELETE /api/documents/:id
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

	// 批注跟着文档走，没有独立生命周期；删不干净只会留孤儿行，所以失败也记日志继续
	if _, err := h.annoRepo.DeleteByDocument(c.Request.Context(), id, userID); err != nil {
		log.Printf("DeleteDocument: delete annotations of doc=%s user=%s: %v", id, userID, err)
	}

	view.JSONOK(c, gin.H{"deleted": true})
}
