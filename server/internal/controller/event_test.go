package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// /api/event-threads 列表与详情契约：记忆数、updated_at 倒序、facts 解析、别人的事件 404。
func TestEventHandler_ListAndGet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Event{}, &model.UserMemory{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	eventRepo := service.NewEventRepo(db)
	memRepo := service.NewUserMemoryRepo(db)
	h := NewEventHandler(eventRepo, memRepo)
	ctx := t.Context()

	mustCreateEvent(t, eventRepo, "e1", "回西安准备", "打包中")
	mustCreateEvent(t, eventRepo, "e2", "驾照考取", "科三待补考")
	facts := `["原话一","原话二"]`
	for _, m := range []*model.UserMemory{
		{ID: "m1", UserID: "u1", MemoryKey: "k", ContentHash: "h1", MemoryValue: "买了火车票", Topic: "行程", EventID: ptr("e1"), Facts: &facts},
		{ID: "m2", UserID: "u1", MemoryKey: "k", ContentHash: "h2", MemoryValue: "还没收拾行李", Topic: "行程", EventID: ptr("e1")},
		{ID: "m3", UserID: "u1", MemoryKey: "k", ContentHash: "h3", MemoryValue: "普通记忆"},
	} {
		if err := memRepo.Create(ctx, m); err != nil {
			t.Fatalf("seed memory %s: %v", m.ID, err)
		}
	}

	r := gin.New()
	r.GET("/api/event-threads", func(c *gin.Context) {
		c.Set("user_id", "u1")
		h.ListEvents(c)
	})
	r.GET("/api/event-threads/:id", func(c *gin.Context) {
		c.Set("user_id", c.Query("as"))
		h.GetEvent(c)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/event-threads", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	var listEnv struct {
		Data struct {
			Items []eventListItem `json:"items"`
			Total int             `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listEnv); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if listEnv.Data.Total != 2 || len(listEnv.Data.Items) != 2 {
		t.Fatalf("list = %+v", listEnv.Data)
	}
	byID := map[string]eventListItem{}
	for _, it := range listEnv.Data.Items {
		byID[it.ID] = it
	}
	if byID["e1"].MemoryCount != 2 || byID["e2"].MemoryCount != 0 {
		t.Fatalf("memory_count = %+v", byID)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/event-threads/e1?as=u1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", w.Code, w.Body.String())
	}
	var gotEnv struct {
		Data struct {
			Name     string            `json:"name"`
			Memories []eventMemoryItem `json:"memories"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &gotEnv); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if gotEnv.Data.Name != "回西安准备" || len(gotEnv.Data.Memories) != 2 {
		t.Fatalf("detail = %+v", gotEnv.Data)
	}
	var factsSeen bool
	for _, m := range gotEnv.Data.Memories {
		if m.ID == "m1" {
			factsSeen = len(m.Facts) == 2
			if m.Content != "买了火车票" {
				t.Errorf("content = %q", m.Content)
			}
		}
	}
	if !factsSeen {
		t.Errorf("facts not parsed: %+v", gotEnv.Data.Memories)
	}

	// 别人的事件：404，不泄露存在性
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/event-threads/e1?as=u2", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("other user status=%d, want 404", w.Code)
	}
}

func mustCreateEvent(t *testing.T, repo *service.EventRepo, id, name, summary string) {
	t.Helper()
	if err := repo.Create(t.Context(), &model.Event{ID: id, UserID: "u1", Name: name, Summary: summary}); err != nil {
		t.Fatalf("create event %s: %v", id, err)
	}
}

func ptr[T any](v T) *T { return &v }
