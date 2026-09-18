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

// ListDocuments GET /api/documents?tag=&keyword=&fields=title,tag,content&limit=&offset=
func (h *DocumentHandler) ListDocuments(c *gin.Context) {
	userID := c.GetString("user_id")
	tag := c.Query("tag")
	keyword := c.Query("keyword")
	fields := parseDocFields(c.Query("fields"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	list, total, err := h.docRepo.List(c.Request.Context(), userID, tag, keyword, fields, limit, offset)
	if err != nil {
		log.Printf("ListDocuments: list user=%s tag=%q keyword=%q fields=%v: %v", userID, tag, keyword, fields, err)
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

// ListDocumentTags GET /api/documents/tags — 词云用的标签及出现次数
func (h *DocumentHandler) ListDocumentTags(c *gin.Context) {
	userID := c.GetString("user_id")

	tags, err := h.docRepo.ListTagCounts(c.Request.Context(), userID)
	if err != nil {
		log.Printf("ListDocumentTags: user=%s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if tags == nil {
		tags = []service.TagCount{}
	}

	view.JSONOK(c, gin.H{"tags": tags})
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
