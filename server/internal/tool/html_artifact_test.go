package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupArtifactTool(t *testing.T) (*HTMLArtifactTool, *gorm.DB, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(tmpDir, "a.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Artifact{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cleanup := func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			sqlDB.Close()
		}
		os.RemoveAll(tmpDir)
	}
	return NewHTMLArtifactTool(service.NewArtifactRepo(db)), db, cleanup
}

const samplePage = `<html><head><style>h1{color:#333}</style></head><body>
<h1>三方案对比</h1>
<p>` + `内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容` + `</p>
</body></html>`

func artArgs(title, summary, html string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"title": title, "summary": summary, "html": html})
	return b
}

func TestHTMLArtifactCreate(t *testing.T) {
	tl, db, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-1"}

	res := tl.Execute(ec, "create", artArgs("对比", "三种方案的成本与维护性对比", samplePage))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	var d struct {
		ArtifactID string `json:"artifact_id"`
		Title      string `json:"title"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil || d.ArtifactID == "" {
		t.Fatalf("bad result payload: %s", string(res.Data))
	}

	var got model.Artifact
	if err := db.First(&got, "id = ?", d.ArtifactID).Error; err != nil {
		t.Fatalf("artifact not saved: %v", err)
	}
	if !strings.Contains(got.HTML, "Content-Security-Policy") {
		t.Error("入库前应已注入 CSP")
	}
	if strings.Contains(got.HTML, "<script") {
		t.Error("script 应被清掉")
	}
}

func TestHTMLArtifactRejectsTruncated(t *testing.T) {
	tl, db, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-2"}

	res := tl.Execute(ec, "create", artArgs("t", "s", strings.Replace(samplePage, "</html>", "", 1)))
	if res.Success {
		t.Fatal("缺少 </html> 必须失败")
	}
	var n int64
	db.Model(&model.Artifact{}).Count(&n)
	if n != 0 {
		t.Fatalf("失败时不应落库，实际 %d 条", n)
	}
}

func TestHTMLArtifactPerTurnCap(t *testing.T) {
	tl, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-3"}

	for i := 0; i < maxArtifactsPerTurn; i++ {
		if res := tl.Execute(ec, "create", artArgs("t", "s", samplePage)); !res.Success {
			t.Fatalf("第 %d 张应成功: %s", i+1, res.Error)
		}
	}
	if res := tl.Execute(ec, "create", artArgs("t", "s", samplePage)); res.Success {
		t.Fatal("超过单轮上限必须拒绝")
	}
	// 新的一轮重新计数
	ec.TurnID = "turn-4"
	if res := tl.Execute(ec, "create", artArgs("t", "s", samplePage)); !res.Success {
		t.Fatalf("新一轮应重新计数: %s", res.Error)
	}
}
