package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// scope=turn 只返回本轮附件，顺序跟消息里的 id 一致，并给出可直接用的 <img> 片段。
func TestImageToolScopeTurn(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "i.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.ImageAsset{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, id := range []string{"a1", "a2"} {
		if err := db.Create(&model.ImageAsset{
			ID: id, UserID: "u1", Ext: "png", Mime: "image/png",
			Visibility: model.ImageVisibilityPrivate, Source: model.ImageSourceChat,
			CreatedAt: time.Now(),
		}).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	tl := NewImageTool(service.NewImageAssetRepo(db),
		service.NewImageStore(filepath.Join(t.TempDir(), "images"), 32<<20, service.NewImageAssetRepo(db), nil),
		&fixedEmbedder{vec: []float64{1, 0}}, newFakeVectorStore())
	// 本轮只发了 a2（顺序由消息字段决定，不按时间）
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", UserImageIDs: []string{"a2"}}
	args, _ := json.Marshal(map[string]any{"scope": "turn"})
	res := tl.Execute(ec, "list_images", args)
	if !res.Success {
		t.Fatalf("list_images: %s", res.Error)
	}

	var d struct {
		Images []struct {
			ID   string `json:"id"`
			HTML string `json:"html"`
		} `json:"images"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d.Total != 1 || len(d.Images) != 1 || d.Images[0].ID != "a2" {
		t.Fatalf("应只返回本轮附件 a2，实际 %+v", d)
	}
	if d.Images[0].HTML != `<img src="image:a2" alt="chat">` {
		t.Fatalf("html 片段不对: %s", d.Images[0].HTML)
	}
}

// 记录在、文件没了（导库/外部删除）：list_images 必须标 available:false，
// 否则模型挑了它才在 create 里整笔回滚。
func TestImageToolMarksMissingFiles(t *testing.T) {
	tmp := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(tmp, "i.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.ImageAsset{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewImageAssetRepo(db)
	for _, id := range []string{"ok1", "gone1"} {
		if err := db.Create(&model.ImageAsset{
			ID: id, UserID: "u1", Ext: "png", Mime: "image/png",
			Visibility: model.ImageVisibilityPrivate, Source: model.ImageSourceChat,
			CreatedAt: time.Now(),
		}).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	store := service.NewImageStore(filepath.Join(tmp, "images"), 32<<20, repo, nil)
	if err := os.MkdirAll(store.Dir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ok := &model.ImageAsset{ID: "ok1", Ext: "png"}
	if err := os.WriteFile(store.FilePath(ok), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tl := NewImageTool(repo, store, &fixedEmbedder{vec: []float64{1, 0}}, newFakeVectorStore())
	res := tl.Execute(&ExecuteContext{Ctx: context.Background(), UserID: "u1"}, "list_images", nil)
	if !res.Success {
		t.Fatalf("list_images: %s", res.Error)
	}
	var d struct {
		Images []struct {
			ID        string `json:"id"`
			Available bool   `json:"available"`
		} `json:"images"`
		Hint string `json:"hint"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[string]bool{}
	for _, it := range d.Images {
		got[it.ID] = it.Available
	}
	if !got["ok1"] || got["gone1"] {
		t.Fatalf("available 标记不对: %+v", got)
	}
	if !strings.Contains(d.Hint, "文件已丢失") {
		t.Fatalf("hint 应提醒别用死图: %s", d.Hint)
	}
}
