package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func newAuthTestRepo(t *testing.T) (*service.UserRepo, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.UserToken{}); err != nil {
		t.Fatal(err)
	}
	return service.NewUserRepo(db, 60, 4), db
}

// runAuth 跑一次 Auth 中间件，返回响应与 handler 读到的 ctx 标记
func runAuth(t *testing.T, local LocalAuth, bearer string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	repo, _ := newAuthTestRepo(t)
	return runAuthOn(t, repo, local, bearer)
}

func runAuthOn(t *testing.T, repo *service.UserRepo, local LocalAuth, bearer string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	seen := map[string]any{}
	r := gin.New()
	r.GET("/p", Auth(repo, local), func(c *gin.Context) {
		seen["user_id"] = c.GetString("user_id")
		seen["local_auth"] = c.GetBool("local_auth")
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w, seen
}

func TestAuth_LocalTokenPassthrough(t *testing.T) {
	w, seen := runAuth(t, LocalAuth{Enabled: true, UserID: "u1", Token: "secret"}, "secret")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if seen["user_id"] != "u1" || seen["local_auth"] != true {
		t.Fatalf("want local user + local_auth flag, got %+v", seen)
	}
}

// 本地模式不再回落 user_tokens 表：库里存在有效 token 也不认（只会给越界留口子）。
func TestAuth_LocalEnabledDoesNotFallBackToDB(t *testing.T) {
	repo, _ := newAuthTestRepo(t)
	_, dbToken, err := repo.Create(context.Background(), "xhan", "pw")
	if err != nil {
		t.Fatal(err)
	}
	w, _ := runAuthOn(t, repo, LocalAuth{Enabled: true, UserID: "u1", Token: "secret"}, dbToken)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("本地模式下库 token 也应被拒（401），got %d", w.Code)
	}
}

// 查不到 token 是「凭证无效」→ 401，客户端据此登出。
func TestAuth_UnknownToken401(t *testing.T) {
	w, _ := runAuth(t, LocalAuth{}, "no-such-token")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAuth_ExpiredToken401(t *testing.T) {
	repo, db := newAuthTestRepo(t)
	_, token, err := repo.Create(context.Background(), "xhan", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE user_tokens SET expires_at = ?", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}

	w, _ := runAuthOn(t, repo, LocalAuth{}, token)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("过期 token want 401, got %d", w.Code)
	}
}

// DB 故障不能折叠成 401：前端/Swift 收到 401 会清 token 登出，一次抖动就全踢。
func TestAuth_DBErrorIs500(t *testing.T) {
	repo, db := newAuthTestRepo(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.Close()

	w, _ := runAuthOn(t, repo, LocalAuth{}, "any-token")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("DB 故障 want 500, got %d", w.Code)
	}
}

// env 前缀不匹配 → 403（dev 的 token 打 prod 后端）
func TestAuth_LocalEnvMismatchRejected(t *testing.T) {
	w, _ := runAuth(t, LocalAuth{Enabled: true, UserID: "u1", Token: "prod:secret", Env: "prod"}, "dev:secret")
	if w.Code != http.StatusForbidden {
		t.Fatalf("env mismatch: want 403, got %d", w.Code)
	}
}

// env 配套 + token 一致 → 放行
func TestAuth_LocalEnvMatchOK(t *testing.T) {
	w, seen := runAuth(t, LocalAuth{Enabled: true, UserID: "u1", Token: "prod:secret", Env: "prod"}, "prod:secret")
	if w.Code != http.StatusOK || seen["local_auth"] != true {
		t.Fatalf("env match: want 200 + local_auth, got %d %+v", w.Code, seen)
	}
}

func TestAuth_LocalDisabledRejectsLocalToken(t *testing.T) {
	w, _ := runAuth(t, LocalAuth{}, "secret")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("local disabled: want 401, got %d", w.Code)
	}
}
