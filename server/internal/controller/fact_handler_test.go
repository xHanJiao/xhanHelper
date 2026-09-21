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

// 事实面板靠这个接口读列表：只回 active、分页正确、空结果必须是 [] 而非 null
// （Go 的 nil 切片 marshal 成 null，客户端按 [AppFact] 解码会抛错、界面变成报错态）。
func TestListFacts_ActiveOnlyAndEmptyArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Fact{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewFactRepo(db)
	h := NewFactHandler(repo)
	ctx := t.Context()

	seed := []model.Fact{
		{ID: "f1", UserID: "u1", Subject: "我", Kind: model.FactKindState, Content: "在东京", ContentHash: "h1", Status: model.FactStatusActive},
		{ID: "f2", UserID: "u1", Subject: "老K", Kind: model.FactKindEvent, Content: "吵过架", ContentHash: "h2", Status: model.FactStatusActive},
		{ID: "f3", UserID: "u1", Subject: "我", Kind: model.FactKindState, Content: "已被删", ContentHash: "h3", Status: model.FactStatusDeleted},
		{ID: "f4", UserID: "u2", Subject: "别人", Kind: model.FactKindState, Content: "不是我的", ContentHash: "h4", Status: model.FactStatusActive},
	}
	for i := range seed {
		if err := repo.Create(ctx, &seed[i]); err != nil {
			t.Fatalf("create fact %s: %v", seed[i].ID, err)
		}
	}

	type listResp struct {
		Items []model.Fact `json:"items"`
		Total int64        `json:"total"`
	}

	do := func(t *testing.T, userID, query string) (listResp, string) {
		t.Helper()
		r := gin.New()
		r.GET("/api/facts", func(c *gin.Context) {
			c.Set("user_id", userID)
			h.ListFacts(c)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/facts"+query, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
		}
		var wrapper struct {
			Status string          `json:"status"`
			Data   json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &wrapper); err != nil {
			t.Fatalf("unmarshal wrapper: %v (body %s)", err, w.Body.String())
		}
		if wrapper.Status != "ok" {
			t.Fatalf("status field = %q, body = %s", wrapper.Status, w.Body.String())
		}
		var raw struct {
			Items json.RawMessage `json:"items"`
			Total int64           `json:"total"`
		}
		if err := json.Unmarshal(wrapper.Data, &raw); err != nil {
			t.Fatalf("unmarshal data: %v (data %s)", err, wrapper.Data)
		}
		var resp listResp
		resp.Total = raw.Total
		if err := json.Unmarshal(raw.Items, &resp.Items); err != nil {
			t.Fatalf("unmarshal items: %v (items %s)", err, raw.Items)
		}
		return resp, string(raw.Items)
	}

	t.Run("只回 active 且只回本人", func(t *testing.T) {
		resp, _ := do(t, "u1", "")
		if resp.Total != 2 || len(resp.Items) != 2 {
			t.Fatalf("total=%d len=%d, want 2/2", resp.Total, len(resp.Items))
		}
		for _, f := range resp.Items {
			if f.Status != model.FactStatusActive {
				t.Errorf("fact %s status = %q, want active", f.ID, f.Status)
			}
			if f.UserID != "u1" {
				t.Errorf("fact %s user = %q, want u1", f.ID, f.UserID)
			}
		}
	})

	t.Run("分页", func(t *testing.T) {
		resp, _ := do(t, "u1", "?limit=1&offset=1")
		if resp.Total != 2 {
			t.Errorf("total = %d, want 2（total 是总数，不受 limit 影响）", resp.Total)
		}
		if len(resp.Items) != 1 {
			t.Fatalf("len = %d, want 1", len(resp.Items))
		}
	})

	t.Run("无数据回空数组不是 null", func(t *testing.T) {
		resp, rawItems := do(t, "u-nobody", "")
		if len(resp.Items) != 0 || resp.Total != 0 {
			t.Fatalf("total=%d len=%d, want 0/0", resp.Total, len(resp.Items))
		}
		if rawItems != "[]" {
			t.Errorf("items raw = %s, want []", rawItems)
		}
	})
}
