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

// 列表/默认会话接口必须返回会话真实 mode。
// 客户端 Conversation.mode 依赖它（ChatViewModel.loadConversation），
// 缺失会让 UI 恒显示 work，而服务端仍按库里的 plan 强制只读 —— 静默失配。
func TestListConversations_ReturnsMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Conversation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	convRepo := service.NewConversationRepo(db)
	ctx := t.Context()

	if err := convRepo.Create(ctx, &model.Conversation{ID: "c-plan", UserID: "u1", Title: "p"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := convRepo.SetMode(ctx, "c-plan", model.ConvModePlan); err != nil {
		t.Fatalf("set mode: %v", err)
	}
	if err := convRepo.Create(ctx, &model.Conversation{ID: "c-work", UserID: "u1", Title: "w"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 撤档前写入的严格档行（2026-10-14 删档）：读侧必须归一到 plan。
	// 不归一的话 ModeRestrictsTools 判成不受限，旧严格会话会被静默放开全部工具。
	if err := convRepo.Create(ctx, &model.Conversation{ID: "c-legacy", UserID: "u1", Title: "legacy"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := convRepo.SetMode(ctx, "c-legacy", "strict"); err != nil {
		t.Fatalf("set legacy mode: %v", err)
	}

	got := map[string]string{}
	for _, tc := range []struct {
		name    string
		handler gin.HandlerFunc
		idOf    func(map[string]interface{}) string
	}{
		{"list", listConversations(convRepo, nil), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
			c.Set("user_id", "u1")
			tc.handler(c)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			var resp struct {
				Data struct {
					Conversations []map[string]interface{} `json:"conversations"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v\n%s", err, w.Body.String())
			}
			if len(resp.Data.Conversations) != 3 {
				t.Fatalf("want 3 conversations, got %d: %s", len(resp.Data.Conversations), w.Body.String())
			}
			for _, m := range resp.Data.Conversations {
				id, _ := m["id"].(string)
				mode, ok := m["mode"].(string)
				if !ok || mode == "" {
					t.Fatalf("会话 %s 的响应缺 mode 字段：%v", id, m)
				}
				got[id] = mode
			}
		})
	}

	if got["c-plan"] != model.ConvModePlan {
		t.Errorf("plan 会话应返回 plan，got %q（客户端会误显示 work）", got["c-plan"])
	}
	if got["c-work"] != model.ConvModeWork {
		t.Errorf("普通会话应返回 work，got %q", got["c-work"])
	}
	// 撤档前写入的 strict 行：读侧归一为 plan（否则客户端显示关闭、服务端却按不受限放行工具）
	if got["c-legacy"] != model.ConvModePlan {
		t.Errorf("历史 strict 行应归一为 plan，got %q", got["c-legacy"])
	}
}
