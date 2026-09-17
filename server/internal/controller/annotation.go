package controller

import (
	"log"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

const (
	// maxAnnotationQuoteLen 单条划线原文上限（字）
	maxAnnotationQuoteLen = 500
	// maxAnnotationNoteLen 单条批注上限（字）
	maxAnnotationNoteLen = 2000
	// maxAnnotationsPerDoc 单文档批注条数上限
	maxAnnotationsPerDoc = 500
	// maxAnchorHintLen 客户端传来的消歧前后文单侧上限（字），与 service.anchorContext 对齐
	maxAnchorHintLen = 60
)

// AnnotationHandler — 文档划线批注 API handler
type AnnotationHandler struct {
	annoRepo *service.DocumentAnnotationRepo
	docRepo  *service.DocumentRepo
}

func NewAnnotationHandler(annoRepo *service.DocumentAnnotationRepo, docRepo *service.DocumentRepo) *AnnotationHandler {
	return &AnnotationHandler{annoRepo: annoRepo, docRepo: docRepo}
}

// ListAnnotations GET /api/documents/:id/annotations?limit=&offset=
func (h *AnnotationHandler) ListAnnotations(c *gin.Context) {
	userID := c.GetString("user_id")
	docID := c.Param("id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	list, total, err := h.annoRepo.List(c.Request.Context(), docID, userID, limit, offset)
	if err != nil {
		log.Printf("ListAnnotations: list doc=%s user=%s: %v", docID, userID, err)
		view.JSONError(c, http.StatusInternalServerError, "查询划线与批注失败")
		return
	}
	if list == nil {
		list = []model.DocumentAnnotation{}
	}
	view.JSONOK(c, gin.H{"annotations": list, "total": total})
}

// CreateAnnotation POST /api/documents/:id/annotations
// 客户端传 quote（DOM 里的选中文本，不含 Markdown 标记）+ note + 可选的 prefix/suffix
// （DOM 选区的前后文纯文本）。锚点与前后文快照由服务端在源文上算；
// prefix/suffix 只在消歧时当提示用 —— 短句在文中重复时，没它就只能锚到第一处。
func (h *AnnotationHandler) CreateAnnotation(c *gin.Context) {
	userID := c.GetString("user_id")
	docID := c.Param("id")

	var req struct {
		Quote  string `json:"quote"`
		Note   string `json:"note"`
		Prefix string `json:"prefix"`
		Suffix string `json:"suffix"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, http.StatusBadRequest, "参数格式错误")
		return
	}
	req.Quote = normalizeQuote(req.Quote)
	req.Prefix = capHintTail(req.Prefix)
	req.Suffix = capHintHead(req.Suffix)
	if req.Quote == "" {
		view.JSONError(c, http.StatusBadRequest, "quote 不能为空")
		return
	}
	if utf8.RuneCountInString(req.Quote) > maxAnnotationQuoteLen {
		view.JSONError(c, http.StatusBadRequest, "划线原文过长")
		return
	}
	if utf8.RuneCountInString(req.Note) > maxAnnotationNoteLen {
		view.JSONError(c, http.StatusBadRequest, "批注过长")
		return
	}

	doc, err := h.docRepo.GetByID(c.Request.Context(), docID, userID)
	if err != nil {
		view.JSONError(c, http.StatusNotFound, "文档不存在")
		return
	}

	n, err := h.annoRepo.CountByDocument(c.Request.Context(), docID, userID)
	if err != nil {
		log.Printf("CreateAnnotation: count doc=%s user=%s: %v", docID, userID, err)
		view.JSONError(c, http.StatusInternalServerError, "创建失败")
		return
	}
	if n >= maxAnnotationsPerDoc {
		view.JSONError(c, http.StatusBadRequest, "单篇文档划线批注数量已达上限")
		return
	}

	ax := service.NewAnchorer(doc.Content)
	sp, ok := ax.Locate(req.Quote, req.Prefix, req.Suffix)

	a := &model.DocumentAnnotation{
		ID:         uuid.New().String(),
		UserID:     userID,
		DocumentID: docID,
		Quote:      req.Quote,
		Note:       req.Note,
		Orphaned:   !ok,
	}
	if ok {
		a.Quote = sp.Quote
		a.StartOffset, a.EndOffset = sp.Start, sp.End
		a.Prefix, a.Suffix = ax.Context(sp)
		// 夹逼会把 quote 改写成投影文本（动了用户数据），必须留痕
		a.QuoteRewritten = sp.Fuzzy
	}

	if err := h.annoRepo.Create(c.Request.Context(), a); err != nil {
		log.Printf("CreateAnnotation: create doc=%s user=%s: %v", docID, userID, err)
		view.JSONError(c, http.StatusInternalServerError, "创建失败")
		return
	}
	view.JSONOK(c, a)
}

// UpdateAnnotation PATCH /api/annotations/:id — 只改 note，不允许改锚点
func (h *AnnotationHandler) UpdateAnnotation(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	var req struct {
		Note *string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, http.StatusBadRequest, "参数格式错误")
		return
	}
	if req.Note == nil {
		view.JSONError(c, http.StatusBadRequest, "note 必填")
		return
	}
	if utf8.RuneCountInString(*req.Note) > maxAnnotationNoteLen {
		view.JSONError(c, http.StatusBadRequest, "批注过长")
		return
	}

	rows, err := h.annoRepo.UpdateNote(c.Request.Context(), id, userID, *req.Note)
	if err != nil {
		log.Printf("UpdateAnnotation: update id=%s user=%s: %v", id, userID, err)
		view.JSONError(c, http.StatusInternalServerError, "更新失败")
		return
	}
	if rows == 0 {
		view.JSONError(c, http.StatusNotFound, "划线与批注不存在")
		return
	}
	view.JSONOK(c, gin.H{"id": id, "note": *req.Note})
}

// DeleteAnnotation DELETE /api/annotations/:id
func (h *AnnotationHandler) DeleteAnnotation(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	rows, err := h.annoRepo.Delete(c.Request.Context(), id, userID)
	if err != nil {
		log.Printf("DeleteAnnotation: delete id=%s user=%s: %v", id, userID, err)
		view.JSONError(c, http.StatusInternalServerError, "删除失败")
		return
	}
	if rows == 0 {
		view.JSONError(c, http.StatusNotFound, "划线与批注不存在")
		return
	}
	view.JSONOK(c, gin.H{"deleted": true})
}

// RelocateAnnotation POST /api/annotations/:id/relocate — 抽屉「重新定位」：
// 客户端让用户重选一段文字（同样可带 DOM 前后文消歧），服务端重挂锚点并解除 orphan。
func (h *AnnotationHandler) RelocateAnnotation(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	var req struct {
		Quote  string `json:"quote"`
		Prefix string `json:"prefix"`
		Suffix string `json:"suffix"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, http.StatusBadRequest, "参数格式错误")
		return
	}
	req.Quote = normalizeQuote(req.Quote)
	req.Prefix = capHintTail(req.Prefix)
	req.Suffix = capHintHead(req.Suffix)
	if req.Quote == "" {
		view.JSONError(c, http.StatusBadRequest, "quote 不能为空")
		return
	}
	if utf8.RuneCountInString(req.Quote) > maxAnnotationQuoteLen {
		view.JSONError(c, http.StatusBadRequest, "划线原文过长")
		return
	}

	a, err := h.annoRepo.GetByID(c.Request.Context(), id, userID)
	if err != nil {
		view.JSONError(c, http.StatusNotFound, "划线与批注不存在")
		return
	}
	doc, err := h.docRepo.GetByID(c.Request.Context(), a.DocumentID, userID)
	if err != nil {
		view.JSONError(c, http.StatusNotFound, "文档不存在")
		return
	}

	ax := service.NewAnchorer(doc.Content)
	sp, ok := ax.Locate(req.Quote, req.Prefix, req.Suffix)
	if !ok {
		view.JSONError(c, http.StatusBadRequest, "选中的文字不在文档正文里")
		return
	}
	pfx, sfx := ax.Context(sp)
	anchor := service.Anchor{Quote: sp.Quote, Prefix: pfx, Suffix: sfx, Start: sp.Start}
	rows, err := h.annoRepo.UpdateAnchor(c.Request.Context(), id, userID, anchor, sp.End)
	if err != nil {
		log.Printf("RelocateAnnotation: update id=%s user=%s: %v", id, userID, err)
		view.JSONError(c, http.StatusInternalServerError, "重新定位失败")
		return
	}
	if rows == 0 {
		view.JSONError(c, http.StatusNotFound, "划线与批注不存在")
		return
	}

	a.Quote, a.Prefix, a.Suffix = sp.Quote, pfx, sfx
	a.StartOffset, a.EndOffset, a.Orphaned = sp.Start, sp.End, false
	a.QuoteRewritten = sp.Fuzzy
	view.JSONOK(c, a)
}

// normalizeQuote 归一化选中文本：折叠连续空白为单个空格并去首尾，
// 与 service 侧投影的空白折叠保持一致（DOM 里的换行/缩进不能影响锚定）。
func normalizeQuote(s string) string {
	out := make([]rune, 0, len(s))
	space := false
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == '\u3000' {
			space = true
			continue
		}
		if space && len(out) > 0 {
			out = append(out, ' ')
		}
		space = false
		out = append(out, r)
	}
	return string(out)
}

// capHintTail 截断前文提示，保留**靠近选区**的尾部
func capHintTail(s string) string {
	r := []rune(s)
	if len(r) <= maxAnchorHintLen {
		return s
	}
	return string(r[len(r)-maxAnchorHintLen:])
}

// capHintHead 截断后文提示，保留**靠近选区**的头部
func capHintHead(s string) string {
	r := []rune(s)
	if len(r) <= maxAnchorHintLen {
		return s
	}
	return string(r[:maxAnchorHintLen])
}
