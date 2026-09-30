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

func setupEphemeralTest(t *testing.T) (*service.ConversationRepo, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Conversation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cleanup := func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			sqlDB.Close()
		}
	}
	return service.NewConversationRepo(db), cleanup
}

func patchEphemeral(t *testing.T, h gin.HandlerFunc, convID, userID, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPatch, "/api/conversations/"+convID+"/ephemeral", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: convID}}
	c.Set("user_id", userID)
	h(c)
	return w
}

func TestUpdateConversationEphemeral_NonOwnerForbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, cleanup := setupEphemeralTest(t)
	defer cleanup()
	if err := repo.Create(t.Context(), &model.Conversation{ID: "c1", UserID: "u1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	h := updateConversationEphemeral(repo, func(_, _, _ string) {})
	w := patchEphemeral(t, h, "c1", "u2", `{"ephemeral":true}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateConversationEphemeral_DefaultConversationRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, cleanup := setupEphemeralTest(t)
	defer cleanup()
	if err := repo.Create(t.Context(), &model.Conversation{ID: "c-def", UserID: "u1", IsDefault: true}); err != nil {
		t.Fatalf("create: %v", err)
	}
	called := false
	h := updateConversationEphemeral(repo, func(_, _, _ string) { called = true })
	w := patchEphemeral(t, h, "c-def", "u1", `{"ephemeral":true}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for default conversation, got %d: %s", w.Code, w.Body.String())
	}
	if called {
		t.Errorf("默认会话不应补挂 session_end 任务")
	}
}

func TestUpdateConversationEphemeral_EnableSchedulesDestroy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, cleanup := setupEphemeralTest(t)
	defer cleanup()
	ctx := t.Context()
	if err := repo.Create(ctx, &model.Conversation{ID: "c1", UserID: "u1", Title: "t"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	var gotUser, gotConv, gotMsg string
	calls := 0
	h := updateConversationEphemeral(repo, func(u, c, m string) {
		calls++
		gotUser, gotConv, gotMsg = u, c, m
	})

	w := patchEphemeral(t, h, "c1", "u1", `{"ephemeral":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if calls != 1 || gotUser != "u1" || gotConv != "c1" || gotMsg != "" {
		t.Errorf("补挂任务参数错误: calls=%d user=%q conv=%q msg=%q", calls, gotUser, gotConv, gotMsg)
	}
	extra, err := repo.GetExtra(ctx, "c1")
	if err != nil {
		t.Fatalf("GetExtra: %v", err)
	}
	if !extra.Ephemeral {
		t.Errorf("开启后 Extra.Ephemeral 应为 true")
	}

	// 关闭不应再补挂
	calls = 0
	w2 := patchEphemeral(t, h, "c1", "u1", `{"ephemeral":false}`)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	if calls != 0 {
		t.Errorf("关闭无痕不应补挂任务")
	}
	if extra2, _ := repo.GetExtra(ctx, "c1"); extra2.Ephemeral {
		t.Errorf("关闭后 Extra.Ephemeral 应为 false")
	}
}

func TestConvExtraEphemeral(t *testing.T) {
	cases := []struct {
		name string
		conv *model.Conversation
		want bool
	}{
		{"nil", nil, false},
		{"empty", &model.Conversation{Extra: ""}, false},
		{"empty object", &model.Conversation{Extra: "{}"}, false},
		{"true", &model.Conversation{ID: "c", Extra: `{"ephemeral":true}`}, true},
		{"false", &model.Conversation{ID: "c", Extra: `{"ephemeral":false}`}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := convExtraEphemeral(tc.conv); got != tc.want {
				t.Errorf("convExtraEphemeral = %v, want %v", got, tc.want)
			}
		})
	}
}
