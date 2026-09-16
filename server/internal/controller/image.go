package controller

import (
	"errors"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// ImageHandler — 图床 HTTP 层：上传 / 读取 / 管理。
//
// 读取分两条路：
//   - 公开 GET /images/:file —— 无鉴权，仅服务 Visibility=public 的资源（文档配图，供分享页/PDF 用）
//   - 私有 GET /api/images/:id/file —— 鉴权 + owner 校验（聊天图片输入）
type ImageHandler struct {
	store *service.ImageStore
}

// NewImageHandler 创建 ImageHandler
func NewImageHandler(store *service.ImageStore) *ImageHandler {
	if store == nil {
		panic("image handler: nil store (wiring bug)")
	}
	return &ImageHandler{store: store}
}

// Store 暴露图床（chat 控制器解析附件时复用同一实例）
func (h *ImageHandler) Store() *service.ImageStore { return h.store }

// uploadMaxMemory — multipart 解析时内存缓冲上限，超出部分落临时文件
const uploadMaxMemory = 8 << 20

// Upload POST /api/images/upload（鉴权）
// 请求：multipart/form-data，字段 file；或裸字节（Content-Type 为图片类型时按裸字节处理）
// 返回：{image_id, url, mime, bytes}
func (h *ImageHandler) Upload(c *gin.Context) {
	userID := c.GetString("user_id")

	data, err := readUpload(c)
	if err != nil {
		log.Printf("[image] upload read error (user=%s): %v", userID, err)
		view.JSONError(c, 400, err.Error())
		return
	}

	asset, err := h.store.Upload(c.Request.Context(), userID, model.ImageSourceChat, model.ImageVisibilityPrivate, data)
	if err != nil {
		if errors.Is(err, service.ErrImageTooLarge) || errors.Is(err, service.ErrImageUnsupported) {
			log.Printf("[image] upload rejected (user=%s): %v", userID, err)
			view.JSONError(c, 400, err.Error())
			return
		}
		log.Printf("[image] upload failed (user=%s, %d bytes): %v", userID, len(data), err)
		view.JSONError(c, 500, "图片保存失败")
		return
	}

	// 立刻上传到 LLM 侧（best-effort）：失败也不影响入库，对话时懒补
	if _, err := h.store.EnsureFileID(c.Request.Context(), asset); err != nil {
		log.Printf("[image] pre-upload to llm skipped (id=%s): %v", asset.ID[:8], err)
	}

	view.JSONOK(c, gin.H{
		"image_id": asset.ID,
		"url":      "/api/images/" + asset.ID + "/file",
		"mime":     asset.Mime,
		"bytes":    asset.Bytes,
	})
}

// ServePrivate GET /api/images/:id/file（鉴权 + owner）
func (h *ImageHandler) ServePrivate(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	asset, err := h.store.Repo().GetByIDAndUser(c.Request.Context(), id, userID)
	if err != nil {
		view.JSONError(c, 404, "图片不存在")
		return
	}
	h.serve(c, asset, "private, max-age=86400")
}

// ServePublic GET /images/:file（无鉴权）
// 文件名形如 <uuid>.<ext>；只有 Visibility=public 的记录可读，其余一律 404（不泄露存在性）。
func (h *ImageHandler) ServePublic(c *gin.Context) {
	name := c.Param("file")
	stem := strings.TrimSuffix(name, path.Ext(name))
	if _, err := uuid.Parse(stem); err != nil {
		c.Status(http.StatusNotFound)
		return
	}

	asset, err := h.store.Repo().GetByID(c.Request.Context(), stem)
	if err != nil || asset.Visibility != model.ImageVisibilityPublic {
		c.Status(http.StatusNotFound)
		return
	}
	h.serve(c, asset, "public, max-age=31536000, immutable")
}

// serve 输出图片字节。文件名不可变，缓存头由调用方按可见性决定。
func (h *ImageHandler) serve(c *gin.Context, asset *model.ImageAsset, cacheControl string) {
	f, err := h.store.Open(asset)
	if err != nil {
		log.Printf("[image] open %s failed: %v", asset.ID[:8], err)
		c.Status(http.StatusNotFound)
		return
	}
	defer f.Close()

	c.Header("Cache-Control", cacheControl)
	c.DataFromReader(http.StatusOK, asset.Bytes, asset.Mime, f, nil)
}

// List GET /api/images?limit=&offset=（鉴权，仅本人）
func (h *ImageHandler) List(c *gin.Context) {
	userID := c.GetString("user_id")
	limit := clampLimit(c.Query("limit"), 50, 200)
	offset, _ := strconv.Atoi(c.Query("offset"))
	if offset < 0 {
		offset = 0
	}

	items, total, err := h.store.Repo().ListByUser(c.Request.Context(), userID, limit, offset)
	if err != nil {
		log.Printf("[image] list error (user=%s): %v", userID, err)
		view.JSONError(c, 500, "查询图片列表失败")
		return
	}

	out := make([]gin.H, 0, len(items))
	for i := range items {
		a := &items[i]
		out = append(out, gin.H{
			"id":           a.ID,
			"url":          "/api/images/" + a.ID + "/file",
			"mime":         a.Mime,
			"bytes":        a.Bytes,
			"source":       a.Source,
			"referenced":   a.Referenced,
			"has_file_id":  a.DeepSeekFileID != "",
			"last_used_at": a.LastUsedAt,
			"created_at":   a.CreatedAt,
		})
	}
	view.JSONOK(c, gin.H{"items": out, "total": total})
}

// Delete DELETE /api/images/:id（鉴权 + owner）
func (h *ImageHandler) Delete(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	asset, err := h.store.Repo().GetByIDAndUser(c.Request.Context(), id, userID)
	if err != nil {
		view.JSONError(c, 404, "图片不存在")
		return
	}
	if err := h.store.Delete(c.Request.Context(), asset); err != nil {
		log.Printf("[image] delete %s failed: %v", id, err)
		view.JSONError(c, 500, "删除图片失败")
		return
	}
	view.JSONOK(c, gin.H{"deleted": true})
}

// readUpload 兼容两种提交方式：multipart 的 file 字段，或裸字节 body
func readUpload(c *gin.Context) ([]byte, error) {
	ct := c.ContentType()
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := c.Request.ParseMultipartForm(uploadMaxMemory); err != nil {
			return nil, errors.New("解析上传内容失败")
		}
		fh, err := c.FormFile("file")
		if err != nil {
			return nil, errors.New("缺少 file 字段")
		}
		f, err := fh.Open()
		if err != nil {
			return nil, errors.New("读取上传文件失败")
		}
		defer f.Close()
		return io.ReadAll(f)
	}
	if ct == "" || ct == "application/octet-stream" || strings.HasPrefix(ct, "image/") {
		return io.ReadAll(c.Request.Body)
	}
	return nil, errors.New("不支持的提交方式（请用 multipart 的 file 字段）")
}

// clampLimit 解析并夹取分页大小
func clampLimit(raw string, def, max int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
