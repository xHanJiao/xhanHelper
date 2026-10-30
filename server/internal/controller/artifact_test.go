package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// 裸 HTML 出口的缓存契约：ETag 跟着正文变；304 不带正文；别人的页面 404。
func TestServeArtifactHTML_ETag(t *testing.T) {
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
	if err := db.Create(&model.Artifact{
		ID: "a1", UserID: "u1", ConversationID: "c1", Title: "一", Summary: "s", HTML: "<html>一</html>",
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	get := func(userID, inm string) *httptest.ResponseRecorder {
		r := gin.New()
		r.GET("/api/artifacts/:id/html", func(c *gin.Context) {
			c.Set("user_id", userID)
			h.ServeArtifactHTML(c)
		})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/artifacts/a1/html", nil)
		if inm != "" {
			req.Header.Set("If-None-Match", inm)
		}
		r.ServeHTTP(w, req)
		return w
	}

	w := get("u1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("首次应 200，实际 %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("应直接吐 HTML，实际 Content-Type=%q", ct)
	}
	if body := w.Body.String(); body != "<html>一</html>" {
		t.Fatalf("正文被包了（应裸 HTML），实际 %q", body)
	}
	if csp := w.Header().Get("Content-Security-Policy"); csp != artifactCSP {
		t.Fatalf("CSP 不对：%q", csp)
	}
	if cl := w.Header().Get("Content-Length"); cl != strconv.Itoa(len("<html>一</html>")) {
		t.Fatalf("Content-Length 不对：%q", cl)
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("缺少 ETag")
	}

	w = get("u1", etag)
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("命中 ETag 应 304 空响应，实际 %d / %d 字节", w.Code, w.Body.Len())
	}

	// 改版是原地覆盖（id 不变），ETag 必须跟着正文变，否则客户端永远拿旧缓存。
	if err := db.Model(&model.Artifact{ID: "a1"}).Update("html", "<html>二</html>").Error; err != nil {
		t.Fatalf("改版: %v", err)
	}
	if w := get("u1", ""); w.Header().Get("ETag") == etag {
		t.Fatal("正文变了 ETag 没变")
	}

	if w := get("u2", ""); w.Code != http.StatusNotFound {
		t.Fatalf("别人的页面应 404，实际 %d", w.Code)
	}
}

// If-None-Match 必须逐项比较：子串匹配会把 "xx" 误判成命中 "x"，客户端就一直停在旧页。
func TestETagMatches(t *testing.T) {
	const etag = `"x"`
	cases := []struct {
		header string
		want   bool
	}{
		{"", false},
		{`"x"`, true},
		{`"y", "x"`, true},
		{`W/"x"`, true},
		{` "x" `, true},
		{`"xx"`, false},
		{"*", true},
	}
	for _, c := range cases {
		if got := etagMatches(c.header, etag); got != c.want {
			t.Fatalf("etagMatches(%q) = %v，应为 %v", c.header, got, c.want)
		}
	}
}

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
