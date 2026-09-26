package controller

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/activity"
	"xhan-helper/internal/view"
)

// eventsBatch POST /api/events/batch — 客户端埋点整段上报（实施技术方案 5.5）。
// 鉴权走外层 auth 中间件；体积上限 1 MiB、单段 ≤ MaxBatchEvents；幂等靠 event_id（INSERT OR IGNORE）。
func eventsBatch(store *activity.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			view.JSONError(c, http.StatusServiceUnavailable, "activity disabled")
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		var req struct {
			SegmentID string                `json:"segment_id"`
			Events    []activity.BatchEvent `json:"events"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			view.JSONError(c, http.StatusBadRequest, "invalid body: "+err.Error())
			return
		}
		if len(req.Events) == 0 {
			view.JSONError(c, http.StatusBadRequest, "events required")
			return
		}
		if len(req.Events) > activity.MaxBatchEvents {
			view.JSONError(c, http.StatusBadRequest, "too many events (max 500)")
			return
		}
		res, err := store.IngestBatch(req.SegmentID, req.Events)
		if err != nil {
			if errors.Is(err, activity.ErrInvalidBatch) {
				view.JSONError(c, http.StatusBadRequest, err.Error())
				return
			}
			log.Printf("[events] batch ingest failed (segment=%s, n=%d): %v", req.SegmentID, len(req.Events), err)
			view.JSONError(c, http.StatusInternalServerError, "ingest failed")
			return
		}
		view.JSONOK(c, gin.H{
			"accepted":       res.Accepted,
			"duplicate":      res.Duplicate,
			"watermark":      res.Watermark,
			"watermark_kind": res.WatermarkKind,
		})
	}
}
