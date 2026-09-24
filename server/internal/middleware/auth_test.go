package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func newAuthTestRepo(t *testing.T) *service.UserRepo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.UserToken{}); err != nil {
		t.Fatal(err)
	}
	return service.NewUserRepo(db, 60, 4)
}

// runAuth 跑一次 Auth 中间件，返回响应与 handler 读到的 ctx 标记
func runAuth(t *testing.T, local LocalAuth, bearer string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	seen := map[string]any{}
	r := gin.New()
	r.GET("/p", Auth(newAuthTestRepo(t), local), func(c *gin.Context) {
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

// 本地开启时，不匹配的 Bearer 必须回落到 DB 校验（这里无对应 token 行 → 401），
// 防止以后把绕过顺序改坏（比如误把任意 bearer 都当本地用户）。
func TestAuth_LocalEnabledWrongBearerFallsThrough(t *testing.T) {
	w, _ := runAuth(t, LocalAuth{Enabled: true, UserID: "u1", Token: "secret"}, "nope")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong bearer must fall through to DB auth and 401, got %d", w.Code)
	}
}

func TestAuth_LocalDisabledRejectsLocalToken(t *testing.T) {
	w, _ := runAuth(t, LocalAuth{}, "secret")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("local disabled: want 401, got %d", w.Code)
	}
}
