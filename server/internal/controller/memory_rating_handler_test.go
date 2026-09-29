package controller

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// 卡片反馈接口契约：合法值 200、越界 400、不存在的 id 404、别人的记忆 404。
func TestSetMemoryRating_Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserMemory{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewUserMemoryRepo(db)
	h := NewMemoryHandler(repo, nil)
	ctx := t.Context()

	if err := repo.Create(ctx, &model.UserMemory{
		ID: "m1", UserID: "u1", MemoryKey: "t", ContentHash: "h1", MemoryValue: "v", Topic: "t",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	do := func(userID, id, body string) *httptest.ResponseRecorder {
		r := gin.New()
		r.PATCH("/api/memories/:id/rating", func(c *gin.Context) {
			c.Set("user_id", userID)
			h.SetMemoryRating(c)
		})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "/api/memories/"+id+"/rating", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}

	if w := do("u1", "m1", `{"rating":1}`); w.Code != http.StatusOK {
		t.Fatalf("valid set: status=%d body=%s", w.Code, w.Body.String())
	}
	if got, _ := repo.FindByID(ctx, "u1", "m1"); got.Rating != 1 {
		t.Fatalf("rating not persisted: %d", got.Rating)
	}

	if w := do("u1", "m1", `{"rating":2}`); w.Code != http.StatusBadRequest {
		t.Fatalf("out-of-range: status=%d, want 400", w.Code)
	}
	if w := do("u1", "nope", `{"rating":1}`); w.Code != http.StatusNotFound {
		t.Fatalf("missing id: status=%d, want 404", w.Code)
	}
	if w := do("u2", "m1", `{"rating":-1}`); w.Code != http.StatusNotFound {
		t.Fatalf("other user's memory: status=%d, want 404", w.Code)
	}
	if w := do("u1", "m1", `{`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad body: status=%d, want 400", w.Code)
	}
}
