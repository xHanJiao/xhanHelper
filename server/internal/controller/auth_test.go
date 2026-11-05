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

func newAuthTestRepo(t *testing.T) (*service.UserRepo, *gorm.DB, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.UserToken{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	return service.NewUserRepo(db, 5, 4), db, func() { sqlDB.Close() }
}

func doAuthPost(t *testing.T, h gin.HandlerFunc, path, body string, params gin.Params) int {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.RemoteAddr = "192.0.2.1:12345"
	c.Params = params
	h(c)
	return w.Code
}

func TestLoginDBErrorIs500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, _, closeDB := newAuthTestRepo(t)
	closeDB()

	code := doAuthPost(t, loginUser(repo, nil, newAuthFailureLimiter()), "/api/auth/login",
		`{"username":"alice","password":"password-123"}`, nil)
	if code != http.StatusInternalServerError {
		t.Fatalf("DB 故障应 500, got %d", code)
	}
}

func TestRegisterDBErrorIs500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, _, closeDB := newAuthTestRepo(t)
	closeDB()
	enabled := true

	code := doAuthPost(t, registerUser(repo, &enabled, nil, nil, newAuthFailureLimiter()), "/api/auth/register",
		`{"username":"alice","password":"password-123"}`, nil)
	if code != http.StatusInternalServerError {
		t.Fatalf("DB 故障应 500, got %d", code)
	}
}

func TestRegisterDuplicateIs409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, _, closeDB := newAuthTestRepo(t)
	defer closeDB()
	if _, _, err := repo.Create(t.Context(), "alice", "password-123"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	enabled := true

	code := doAuthPost(t, registerUser(repo, &enabled, nil, nil, newAuthFailureLimiter()), "/api/auth/register",
		`{"username":"alice","password":"password-456"}`, nil)
	if code != http.StatusConflict {
		t.Fatalf("重名应 409, got %d", code)
	}
}

func TestResetPasswordUnknownUserIs404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, _, closeDB := newAuthTestRepo(t)
	defer closeDB()

	code := doAuthPost(t, resetUserPassword(repo), "/api/admin/users/missing/reset-password",
		``, gin.Params{{Key: "id", Value: "missing"}})
	if code != http.StatusNotFound {
		t.Fatalf("用户不存在应 404, got %d", code)
	}
}

func TestResetPasswordDBErrorIs500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, _, closeDB := newAuthTestRepo(t)
	closeDB()

	code := doAuthPost(t, resetUserPassword(repo), "/api/admin/users/alice/reset-password",
		``, gin.Params{{Key: "id", Value: "alice"}})
	if code != http.StatusInternalServerError {
		t.Fatalf("DB 故障应 500, got %d", code)
	}
}

func doRefresh(t *testing.T, repo *service.UserRepo, token string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	c.Set("bearer_token", token)
	refreshToken(repo)(c)
	return w.Code
}

// 心跳续期：token 无效 → 401（客户端该登出）；DB 故障 → 500（不能连带踢所有人下线）。
func TestRefreshTokenErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, _, closeDB := newAuthTestRepo(t)
	if code := doRefresh(t, repo, "no-such-token"); code != http.StatusUnauthorized {
		t.Fatalf("无效 token 应 401, got %d", code)
	}
	closeDB()
	if code := doRefresh(t, repo, "any-token"); code != http.StatusInternalServerError {
		t.Fatalf("DB 故障应 500, got %d", code)
	}
}
