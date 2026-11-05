package controller

import (
	"context"
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

// /api/chat 必须校验 conversation_id 的归属——否则会按他人 conv.ID 读历史、
// 往他人会话写消息。拒绝路径在 orchestrator 之前返回，所以依赖可以传 nil。
func TestChatRejectsForeignConversation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Conversation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	convRepo := service.NewConversationRepo(db)
	if err := convRepo.Create(context.Background(), &model.Conversation{ID: "conv-victim", UserID: "user-victim"}); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}

	h := chat(nil, nil, convRepo, nil, nil, nil)
	newCtx := func(convID, userID string) (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/chat",
			strings.NewReader(`{"conversation_id":"`+convID+`","message":"hi"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("user_id", userID)
		return c, w
	}

	c, w := newCtx("conv-victim", "user-attacker")
	h(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("他人会话应被拒绝，got status=%d body=%s", w.Code, w.Body.String())
	}

	conv, err := convRepo.FindByID(context.Background(), "conv-victim")
	if err != nil || conv == nil || conv.UserID != "user-victim" {
		t.Fatalf("victim conversation mutated: %+v err=%v", conv, err)
	}

	// 不存在的会话仍按 404 处理（不静默新建）
	c, w = newCtx("conv-missing", "user-victim")
	h(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("不存在的会话应 404，got status=%d body=%s", w.Code, w.Body.String())
	}
}

// DB 故障不能折叠成 404：否则用户看到"会话不存在"（前端可能清掉当前会话视图），
// 日志里也查不到原因。FindByID 查不到返回 (nil, nil)，err != nil 必是真故障。
func TestChatConversationLookupDBErrorIsNot404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Conversation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	h := chat(nil, nil, service.NewConversationRepo(db), nil, nil, nil)
	newCtx := func(body string) (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("user_id", "user-1")
		return c, w
	}

	// 显式会话：FindByID 故障 → 500
	c, w := newCtx(`{"conversation_id":"conv-1","message":"hi"}`)
	h(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("显式会话 DB 故障应 500，got status=%d body=%s", w.Code, w.Body.String())
	}

	// 新入口：FindDefaultByUser 故障也不能当成"没有默认会话"去新建
	c, w = newCtx(`{"message":"hi"}`)
	h(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("默认会话查询 DB 故障应 500，got status=%d body=%s", w.Code, w.Body.String())
	}
}
