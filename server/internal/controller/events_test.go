package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/activity"
)

// POST /api/events/batch：整段接收、accepted/duplicate/watermark、幂等重投、400 校验。
func TestEventsBatchHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := activity.Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open activity: %v", err)
	}
	defer store.Close()

	r := gin.New()
	r.POST("/api/events/batch", eventsBatch(store))

	post := func(body string) (int, map[string]any) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/events/batch", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		var env struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return w.Code, env.Data
	}

	body := `{"segment_id":"s1","events":[` +
		`{"event_id":"e1","type":"client.app.active","occurred_at":"2026-09-26T10:00:00.000Z","actor":"user"},` +
		`{"event_id":"e2","type":"client.app.inactive","occurred_at":"2026-09-26T10:05:00.000Z","actor":"user"}]}`

	code, m := post(body)
	if code != 200 {
		t.Fatalf("code=%d body=%v", code, m)
	}
	if m["accepted"].(float64) != 2 || m["duplicate"].(float64) != 0 {
		t.Errorf("first = %v", m)
	}
	if m["watermark"] != "2026-09-26T10:05:00.000Z" {
		t.Errorf("watermark = %v", m["watermark"])
	}
	if m["watermark_kind"] != "time" {
		t.Errorf("watermark_kind = %v, want time", m["watermark_kind"])
	}

	// 重投同段 → 无副作用，只计 duplicate
	if code, m = post(body); code != 200 || m["accepted"].(float64) != 0 || m["duplicate"].(float64) != 2 {
		t.Errorf("replay = %d %v", code, m)
	}

	// 校验：空 events、缺 event_id → 400
	if code, _ := post(`{"events":[]}`); code != 400 {
		t.Errorf("empty events code=%d, want 400", code)
	}
	if code, _ := post(`{"events":[{"type":"x"}]}`); code != 400 {
		t.Errorf("missing event_id code=%d, want 400", code)
	}
}

// segment 模式：水位回显 segment_id；缺 segment_id → 400。
func TestEventsBatchHandler_SegmentMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := activity.Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open activity: %v", err)
	}
	defer store.Close()
	store.SetWatermarkMode(activity.WatermarkSegment)

	r := gin.New()
	r.POST("/api/events/batch", eventsBatch(store))
	post := func(body string) (int, map[string]any) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/events/batch", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		var env struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return w.Code, env.Data
	}

	code, m := post(`{"segment_id":"seg-42","events":[{"event_id":"e1","type":"client.idle.ended"}]}`)
	if code != 200 || m["watermark"] != "seg-42" || m["watermark_kind"] != "segment" {
		t.Errorf("segment resp = %d %v", code, m)
	}
	// 缺 segment_id → 400
	if code, _ := post(`{"events":[{"event_id":"e2","type":"client.idle.ended"}]}`); code != 400 {
		t.Errorf("缺 segment_id code=%d, want 400", code)
	}
}

// 采集关闭（store=nil）→ 503，客户端据此保留本地段。
func TestEventsBatchHandler_Disabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/events/batch", eventsBatch(nil))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/events/batch", bytes.NewBufferString(`{"events":[{"event_id":"e","type":"client.idle.ended"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("code=%d, want 503", w.Code)
	}
}
