package controller

import (
	"log"
	"net/http"
	"strconv"

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

// ListDocuments GET /api/documents?tag=&keyword=&limit=&offset=
func (h *DocumentHandler) ListDocuments(c *gin.Context) {
	userID := c.GetString("user_id")
	tag := c.Query("tag")
	keyword := c.Query("keyword")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "24"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	list, total, err := h.docRepo.List(c.Request.Context(), userID, tag, keyword, limit, offset)
	if err != nil {
		log.Printf("ListDocuments: list user=%s tag=%q keyword=%q: %v", userID, tag, keyword, err)
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

	if err := h.docRepo.Delete(c.Request.Context(), id, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	view.JSONOK(c, gin.H{"deleted": true})
}
