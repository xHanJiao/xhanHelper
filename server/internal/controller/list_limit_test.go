package controller

import (
	"encoding/json"
	"fmt"
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

// ?limit= 必须夹到 listLimitMax：用 agents 列表做代表，锁住「接口层套了 parseListLimit」。
func TestListAgentsClampsLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "agents.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Agent{}, &model.Conversation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	rows := make([]model.Agent, 0, listLimitMax+5)
	for i := 0; i < listLimitMax+5; i++ {
		rows = append(rows, model.Agent{
			ID: fmt.Sprintf("a%03d", i), UserID: "u1",
			Name: fmt.Sprintf("n%d", i), SystemPrompt: "p",
		})
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	h := NewAgentHandler(service.NewAgentRepo(db), nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/agents?limit=100000", nil)
	c.Set("user_id", "u1")
	h.ListAgents(c)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Agents []model.Agent `json:"agents"`
			Total  int64         `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data.Agents) != listLimitMax {
		t.Fatalf("单页应夹到 %d, got %d", listLimitMax, len(resp.Data.Agents))
	}
	if resp.Data.Total != int64(listLimitMax+5) {
		t.Fatalf("total 应反映全量 %d, got %d", listLimitMax+5, resp.Data.Total)
	}
}
