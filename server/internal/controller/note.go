package controller

import (
	"log"
	"strconv"

	"github.com/gin-gonic/gin"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// NoteHandler — 笔记 REST API
type NoteHandler struct {
	noteRepo *service.NoteRepo
}

// NewNoteHandler 创建 NoteHandler
func NewNoteHandler(noteRepo *service.NoteRepo) *NoteHandler {
	return &NoteHandler{noteRepo: noteRepo}
}

// ListNotes GET /api/notes?limit=50&offset=0
func (h *NoteHandler) ListNotes(c *gin.Context) {
	userID := c.GetString("user_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	notes, total, err := h.noteRepo.List(c.Request.Context(), userID,
		c.Query("topic"), c.Query("keyword"), limit, offset)
	if err != nil {
		log.Printf("[note] List error: %v", err)
		view.JSONError(c, 500, "query notes failed")
		return
	}
	view.JSONOK(c, gin.H{"notes": notes, "total": total})
}

// BatchGetNotes POST /api/notes/batch
func (h *NoteHandler) BatchGetNotes(c *gin.Context) {
	userID := c.GetString("user_id")

	var body struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.IDs) == 0 {
		view.JSONError(c, 400, "ids required")
		return
	}
	notes, err := h.noteRepo.GetByIDsAndUser(c.Request.Context(), body.IDs, userID)
	if err != nil {
		log.Printf("[note] BatchGet error: %v", err)
		view.JSONError(c, 500, "query notes failed")
		return
	}
	view.JSONOK(c, notes)
}

// DeleteNote DELETE /api/notes/:id
func (h *NoteHandler) DeleteNote(c *gin.Context) {
	userID := c.GetString("user_id")
	if err := h.noteRepo.SoftDelete(c.Request.Context(), c.Param("id"), userID); err != nil {
		log.Printf("[note] SoftDelete error: %v", err)
		view.JSONError(c, 500, "delete note failed")
		return
	}
	view.JSONOK(c, gin.H{"deleted": true})
}
