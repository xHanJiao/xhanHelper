package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// 只需魔数能被 http.DetectContentType 识别，文件内容不必是真图片。
var (
	fakePNG  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)
	fakeJPEG = append([]byte("\xff\xd8\xff\xe0"), bytes.Repeat([]byte{0}, 64)...)
	fakeSVG  = []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`)
)

func setupImageStore(t *testing.T, maxBytes int64) (*ImageStore, *gorm.DB, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.ImageAsset{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	dir := t.TempDir()
	store := NewImageStore(dir, maxBytes, NewImageAssetRepo(db), nil)
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
	return store, db, cleanup
}

func TestImageStoreUpload(t *testing.T) {
	store, _, cleanup := setupImageStore(t, 1<<20)
	defer cleanup()
	ctx := context.Background()

	png, err := store.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, fakePNG)
	if err != nil {
		t.Fatalf("upload png: %v", err)
	}
	if png.Ext != "png" || png.Mime != "image/png" {
		t.Errorf("unexpected png detection: ext=%s mime=%s", png.Ext, png.Mime)
	}
	if _, err := os.Stat(store.FilePath(png)); err != nil {
		t.Errorf("file not on disk: %v", err)
	}

	// 同用户同内容去重：返回同一条记录，不重复落盘
	dup, err := store.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, fakePNG)
	if err != nil {
		t.Fatalf("upload dup: %v", err)
	}
	if dup.ID != png.ID {
		t.Errorf("expected dedupe to reuse %s, got %s", png.ID, dup.ID)
	}

	// 不同用户同内容不去重（各自持有）
	other, err := store.Upload(ctx, "u2", model.ImageSourceChat, model.ImageVisibilityPrivate, fakePNG)
	if err != nil {
		t.Fatalf("upload other user: %v", err)
	}
	if other.ID == png.ID {
		t.Error("different users should not share image records")
	}

	if jpg, err := store.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, fakeJPEG); err != nil || jpg.Ext != "jpg" {
		t.Errorf("jpeg upload: asset=%v err=%v", jpg, err)
	}
}

func TestImageStoreRejectsSVGAndOversize(t *testing.T) {
	ctx := context.Background()

	// svg 是脚本注入风险，魔数不在白名单 → 拒绝
	svgStore, _, svgCleanup := setupImageStore(t, 1<<20)
	defer svgCleanup()
	if _, err := svgStore.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, fakeSVG); !errors.Is(err, ErrImageUnsupported) {
		t.Errorf("svg should be rejected, got %v", err)
	}

	// 超限图直接拒绝
	smallStore, _, smallCleanup := setupImageStore(t, 32)
	defer smallCleanup()
	if _, err := smallStore.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, fakePNG); !errors.Is(err, ErrImageTooLarge) {
		t.Errorf("oversize should be rejected, got %v", err)
	}

	// 被拒绝的图不应留下任何记录或文件
	var count int64
	smallStore.Repo().db.Model(&model.ImageAsset{}).Count(&count)
	if count != 0 {
		t.Errorf("expected 0 records, got %d", count)
	}
	entries, _ := os.ReadDir(smallStore.Dir())
	if len(entries) != 0 {
		t.Errorf("expected no files on disk, got %d", len(entries))
	}
}

func TestImageStoreEnsureFileID(t *testing.T) {
	store, _, cleanup := setupImageStore(t, 1<<20)
	defer cleanup()
	ctx := context.Background()

	asset, err := store.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, fakePNG)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	// 未注入上传器 → 明确报错，而不是静默返回空 id
	if _, err := store.EnsureFileID(ctx, asset); err == nil {
		t.Error("expected error when no uploader configured")
	}

	// 已经有 file_id 时直接返回，不再上传
	asset.DeepSeekFileID = "file-api-abc"
	got, err := store.EnsureFileID(ctx, asset)
	if err != nil || got != "file-api-abc" {
		t.Errorf("expected cached file id, got %q err=%v", got, err)
	}

	// 回填 file_id 必须真正落到列上（列名由 GORM 命名策略决定，
	// 手写列名对不上会每次都静默失败 → 图片反复重复上传）
	if err := store.Repo().SetDeepSeekFileID(ctx, asset.ID, "file-api-xyz"); err != nil {
		t.Fatalf("set file id: %v", err)
	}
	reloaded, err := store.Repo().GetByID(ctx, asset.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.DeepSeekFileID != "file-api-xyz" {
		t.Errorf("file id not persisted: %q", reloaded.DeepSeekFileID)
	}
}

func TestImageStoreCleanupTwoTier(t *testing.T) {
	store, db, cleanup := setupImageStore(t, 1<<20)
	defer cleanup()
	ctx := context.Background()

	mk := func(name string) *model.ImageAsset {
		a, err := store.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, append(fakePNG, []byte(name)...))
		if err != nil {
			t.Fatalf("upload %s: %v", name, err)
		}
		return a
	}
	oldReferenced := mk("a")
	oldOrphan := mk("b")
	freshReferenced := mk("c")

	// 新建记录必须是 NULL 而非零值时间：MySQL 严格模式（NO_ZERO_DATE）会拒绝
	// '0000-00-00' 并让整个 INSERT 失败（Error 1292）
	for _, a := range []*model.ImageAsset{oldReferenced, oldOrphan, freshReferenced} {
		if a.LastUsedAt != nil {
			t.Fatalf("fresh image should have NULL last_used_at, got %v", a.LastUsedAt)
		}
	}

	// 被引用后才写入 last_used_at
	if err := store.Repo().MarkReferenced(ctx, []string{freshReferenced.ID}, "u1"); err != nil {
		t.Fatalf("mark referenced: %v", err)
	}
	if got, err := store.Repo().GetByID(ctx, freshReferenced.ID); err != nil || got.LastUsedAt == nil {
		t.Fatalf("last_used_at should be set after MarkReferenced: err=%v asset=%+v", err, got)
	}

	// last_used_at / created_at 由 DB 决定，测试里直接改造成"过期"
	old := time.Now().AddDate(0, 0, -30)
	db.Model(&model.ImageAsset{}).Where("id = ?", oldReferenced.ID).
		Updates(map[string]interface{}{"referenced": true, "last_used_at": old})
	db.Model(&model.ImageAsset{}).Where("id = ?", oldOrphan.ID).
		Update("created_at", old)
	db.Model(&model.ImageAsset{}).Where("id = ?", freshReferenced.ID).
		Updates(map[string]interface{}{"referenced": true, "last_used_at": time.Now()})

	stats, err := store.Cleanup(ctx, 15, 3)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if stats.Referenced != 1 || stats.Orphans != 1 {
		t.Errorf("unexpected stats: %+v", stats)
	}
	if _, err := os.Stat(store.FilePath(oldReferenced)); !os.IsNotExist(err) {
		t.Error("stale referenced file should be deleted from disk")
	}
	if _, err := os.Stat(store.FilePath(oldOrphan)); !os.IsNotExist(err) {
		t.Error("stale orphan file should be deleted from disk")
	}
	if _, err := os.Stat(store.FilePath(freshReferenced)); err != nil {
		t.Errorf("fresh image should survive: %v", err)
	}
}
