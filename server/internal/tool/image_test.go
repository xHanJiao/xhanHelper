package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
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

	tl := NewImageTool(service.NewImageAssetRepo(db))
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
