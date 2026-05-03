package controller

import (
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

// ListNotes GET /api/notes
func (h *NoteHandler) ListNotes(c *gin.Context) {
	userID := c.GetString("user_id")
	notes, err := h.noteRepo.List(c.Request.Context(), userID,
		c.Query("topic"), c.Query("keyword"))
	if err != nil {
		view.JSONError(c, 500, "query notes failed")
		return
	}
	view.JSONOK(c, notes)
}

// DeleteNote DELETE /api/notes/:id
func (h *NoteHandler) DeleteNote(c *gin.Context) {
	if err := h.noteRepo.Delete(c.Request.Context(), c.Param("id")); err != nil {
		view.JSONError(c, 500, "delete note failed")
		return
	}
	view.JSONOK(c, gin.H{"deleted": true})
}
