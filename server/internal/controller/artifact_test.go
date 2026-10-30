package controller

import (
	"fmt"
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

// 批量删除的接口契约：空/超限 400，去重，只删自己的（越权 id 静默跳过）。
func TestBatchDeleteArtifacts_Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Artifact{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewArtifactRepo(db)
	h := NewArtifactHandler(repo)
	ctx := t.Context()

	seed := []model.Artifact{
		{ID: "a1", UserID: "u1", ConversationID: "c1", Title: "一", Summary: "s", HTML: "<html>1</html>"},
		{ID: "a2", UserID: "u1", ConversationID: "c1", Title: "二", Summary: "s", HTML: "<html>2</html>"},
		{ID: "a3", UserID: "u2", ConversationID: "c2", Title: "别人的", Summary: "s", HTML: "<html>3</html>"},
	}
	for i := range seed {
		if err := db.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", seed[i].ID, err)
		}
	}

	do := func(userID, body string) *httptest.ResponseRecorder {
		r := gin.New()
		r.POST("/api/artifacts/batch-delete", func(c *gin.Context) {
			c.Set("user_id", userID)
			h.BatchDeleteArtifacts(c)
		})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/artifacts/batch-delete", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}

	for _, body := range []string{`{}`, `{"ids":[]}`, `{"ids":["",""]}`} {
		if w := do("u1", body); w.Code != http.StatusBadRequest {
			t.Fatalf("body=%s 应 400，实际 %d（%s）", body, w.Code, w.Body.String())
		}
	}
	tooMany := make([]string, 0, maxBatchDelete+1)
	for i := 0; i <= maxBatchDelete; i++ {
		tooMany = append(tooMany, fmt.Sprintf(`"x%d"`, i))
	}
	if w := do("u1", `{"ids":[`+strings.Join(tooMany, ",")+`]}`); w.Code != http.StatusBadRequest {
		t.Fatalf("超过 %d 条应 400，实际 %d", maxBatchDelete, w.Code)
	}

	// 混入别人的 + 重复：只删自己的 2 条
	w := do("u1", `{"ids":["a1","a2","a3","a1"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("normal: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"deleted":2`) {
		t.Fatalf("应删 2 条，响应 %s", w.Body.String())
	}
	if _, err := repo.GetByID(ctx, "u1", "a1"); err == nil {
		t.Fatal("a1 应已软删")
	}
	if _, err := repo.GetByID(ctx, "u2", "a3"); err != nil {
		t.Fatalf("别人的页面不该被删: %v", err)
	}
}
