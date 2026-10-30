package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
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

// source=doc 的文档配图即使过期也不能被 TTL 清理（文档是"作品"，配图不能被删）。
func TestImageStoreCleanupExemptsDocImages(t *testing.T) {
	store, db, cleanup := setupImageStore(t, 1<<20)
	defer cleanup()
	ctx := context.Background()

	docImg, err := store.Upload(ctx, "u1", model.ImageSourceDoc, model.ImageVisibilityPublic, fakePNG)
	if err != nil {
		t.Fatalf("upload doc image: %v", err)
	}
	// 同时制造一个同样过期的聊天图作对照，确认清理确实在跑
	chatImg, err := store.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, append(fakePNG, "chat"...))
	if err != nil {
		t.Fatalf("upload chat image: %v", err)
	}
	old := time.Now().AddDate(0, 0, -30)
	db.Model(&model.ImageAsset{}).Where("id = ?", docImg.ID).
		Updates(map[string]interface{}{"referenced": true, "last_used_at": old})
	db.Model(&model.ImageAsset{}).Where("id = ?", chatImg.ID).
		Updates(map[string]interface{}{"referenced": true, "last_used_at": old})

	stats, err := store.Cleanup(ctx, 15, 3)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if stats.Referenced != 1 {
		t.Errorf("expected only the chat image cleaned, stats=%+v", stats)
	}
	if _, err := os.Stat(store.FilePath(docImg)); err != nil {
		t.Errorf("doc image should survive cleanup: %v", err)
	}
	if _, err := os.Stat(store.FilePath(chatImg)); !os.IsNotExist(err) {
		t.Error("stale chat image should be deleted")
	}
}

// 记录在、磁盘文件没了（迁移/外部删除遗留）→ Cleanup 应删掉记录，且不误伤文件还在的。
func TestImageStoreCleanupPurgesMissingFile(t *testing.T) {
	store, _, cleanup := setupImageStore(t, 1<<20)
	defer cleanup()
	ctx := context.Background()

	broken, err := store.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, fakePNG)
	if err != nil {
		t.Fatalf("upload broken: %v", err)
	}
	healthy, err := store.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, append(fakePNG, "ok"...))
	if err != nil {
		t.Fatalf("upload healthy: %v", err)
	}

	// 模拟文件被外部清掉，只留记录
	if err := os.Remove(store.FilePath(broken)); err != nil {
		t.Fatalf("remove file: %v", err)
	}

	stats, err := store.Cleanup(ctx, 15, 3)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if stats.Missing != 1 {
		t.Errorf("expected 1 missing purged, got %+v", stats)
	}
	if _, err := store.Repo().GetByID(ctx, broken.ID); err == nil {
		t.Error("broken record should be deleted")
	}
	if _, err := store.Repo().GetByID(ctx, healthy.ID); err != nil {
		t.Errorf("healthy record should survive: %v", err)
	}
	if _, err := os.Stat(store.FilePath(healthy)); err != nil {
		t.Errorf("healthy file should survive: %v", err)
	}
}

// 图片目录整体不可用（未挂载/被改名）→ Cleanup 应放弃本次清理，绝不能把全部记录判为 missing 删光。
func TestImageStoreCleanupSkipsWhenDirMissing(t *testing.T) {
	store, _, cleanup := setupImageStore(t, 1<<20)
	defer cleanup()
	ctx := context.Background()

	asset, err := store.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, fakePNG)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if err := os.RemoveAll(store.Dir()); err != nil {
		t.Fatalf("remove dir: %v", err)
	}

	stats, err := store.Cleanup(ctx, 15, 3)
	if err == nil {
		t.Error("expected error when image dir unavailable")
	}
	if stats.Missing != 0 || stats.Referenced != 0 || stats.Orphans != 0 {
		t.Errorf("nothing should be purged, got %+v", stats)
	}
	if _, err := store.Repo().GetByID(ctx, asset.ID); err != nil {
		t.Errorf("record should survive missing dir: %v", err)
	}
}

// 描述生成的假实现：file_id 随便给一个，描述由测试指定。
type fakeUploader struct {
	fileID string
	err    error
}

func (f fakeUploader) Upload(ctx context.Context, filename string, data []byte) (string, error) {
	return f.fileID, f.err
}

type fakeDescriber struct {
	desc string
	err  error
}

func (f *fakeDescriber) Describe(ctx context.Context, fileID string) (string, error) {
	return f.desc, f.err
}

// 描述链路（EnsureFileID → 视觉模型 → 回写）成功：描述与 description_at 都落库。
func TestImageStoreDescribeSuccess(t *testing.T) {
	store, _, cleanup := setupImageStore(t, 1<<20)
	defer cleanup()
	ctx := context.Background()

	store.uploader = fakeUploader{fileID: "fid-1"}
	asset, err := store.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, fakePNG)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	// 上传后再接 describer：避免 Upload 起的异步 goroutine 与本次同步调用竞争
	store.SetDescriber(&fakeDescriber{desc: "一只坐着的橘猫"})

	if err := store.describe(ctx, asset); err != nil {
		t.Fatalf("describe: %v", err)
	}
	got, err := store.Repo().GetByID(ctx, asset.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Description != "一只坐着的橘猫" {
		t.Errorf("description = %q, want 一只坐着的橘猫", got.Description)
	}
	if got.DescriptionAt == nil {
		t.Error("description_at should be set on success")
	}
	if got.DeepSeekFileID != "fid-1" {
		t.Errorf("file_id should be backfilled, got %q", got.DeepSeekFileID)
	}
}

// 描述链路失败（上传/识图出错）：只标记 description_at（避免重试），描述留空。
func TestImageStoreDescribeFailureMarksAttempt(t *testing.T) {
	store, _, cleanup := setupImageStore(t, 1<<20)
	defer cleanup()
	ctx := context.Background()

	store.uploader = fakeUploader{err: errors.New("llm upload boom")}
	asset, err := store.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, fakePNG)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	store.SetDescriber(&fakeDescriber{desc: "不该被写进去"})

	if err := store.describe(ctx, asset); err == nil {
		t.Fatal("describe should fail when file_id upload fails")
	}
	got, err := store.Repo().GetByID(ctx, asset.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Description != "" {
		t.Errorf("description should stay empty, got %q", got.Description)
	}
	if got.DescriptionAt == nil {
		t.Error("description_at should be marked on failure to stop retries")
	}
}

// 去重命中但文件丢了（导库只搬了 DB、没搬 images）：重传同一份字节要把文件补回去，
// 否则 DataURI 永远报「图片文件已丢失」，重发多少次都一样。
func TestImageStoreUploadHealsMissingFile(t *testing.T) {
	store, _, cleanup := setupImageStore(t, 32<<20)
	defer cleanup()
	ctx := context.Background()

	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	data := buf.Bytes()

	asset, err := store.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, data)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	path := store.FilePath(asset)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove file: %v", err)
	}

	// 文件缺失时读不到（现状：内联会报「图片文件已丢失」）
	if _, err := store.DataURI(ctx, "u1", asset.ID); err == nil {
		t.Fatal("文件缺失时 DataURI 应报错")
	}

	again, err := store.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, data)
	if err != nil {
		t.Fatalf("re-upload: %v", err)
	}
	if again.ID != asset.ID {
		t.Fatalf("同内容应复用同一条记录，实际 %s != %s", again.ID, asset.ID)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("重传应把文件补回去: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("补回的文件内容应与上传字节一致")
	}
	if _, err := store.DataURI(ctx, "u1", asset.ID); err != nil {
		t.Fatalf("补回后应能内联: %v", err)
	}
}

// 内联：大图会被压到适合屏幕的尺寸并落盘缓存，第二次直接用缓存（不再压一遍）。
func TestImageStoreDataURIShrinksAndCaches(t *testing.T) {
	store, db, cleanup := setupImageStore(t, 32<<20)
	defer cleanup()
	ctx := context.Background()

	// 真图：3000×2000 的 JPEG（压缩后应显著变小）
	src := image.NewRGBA(image.Rect(0, 0, 3000, 2000))
	for y := 0; y < 2000; y += 7 {
		for x := 0; x < 3000; x += 7 {
			src.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	raw := buf.Bytes()

	a := &model.ImageAsset{
		ID: "big-img", UserID: "u1", Ext: "jpg", Mime: "image/jpeg",
		Bytes: int64(len(raw)), Visibility: model.ImageVisibilityPrivate, Source: model.ImageSourceChat,
	}
	if err := db.Create(a).Error; err != nil {
		t.Fatalf("create record: %v", err)
	}
	if err := os.MkdirAll(store.Dir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(store.FilePath(a), raw, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	uri, err := store.DataURI(ctx, "u1", a.ID)
	if err != nil {
		t.Fatalf("DataURI: %v", err)
	}
	if !strings.HasPrefix(uri, "data:image/jpeg;base64,") {
		t.Fatalf("应以 JPEG data URI 返回，实际前缀 %q", uri[:min(32, len(uri))])
	}
	payload := strings.TrimPrefix(uri, "data:image/jpeg;base64,")
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if int64(len(decoded)) >= int64(len(raw))/2 {
		t.Fatalf("应压到原图一半以下：%d -> %d", len(raw), len(decoded))
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil {
		t.Fatalf("decode shrunk: %v", err)
	}
	if cfg.Width > InlineMaxEdge && cfg.Height > InlineMaxEdge {
		t.Fatalf("最长边应 ≤%d，实际 %d×%d", InlineMaxEdge, cfg.Width, cfg.Height)
	}

	// 缓存：第二次不再重压（缓存文件 mtime 不变）
	cache, _, ok := store.inlineCache(a.ID)
	if !ok {
		t.Fatal("应落盘内联缓存")
	}
	before, err := os.Stat(cache)
	if err != nil {
		t.Fatalf("stat cache: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	again, err := store.DataURI(ctx, "u1", a.ID)
	if err != nil {
		t.Fatalf("DataURI 第二次: %v", err)
	}
	if again != uri {
		t.Fatal("缓存命中应返回同一份 URI")
	}
	after, err := os.Stat(cache)
	if err != nil {
		t.Fatalf("stat cache: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("第二次不应重新压缩写盘")
	}

	// 越权读取按「不存在」处理
	if _, err := store.DataURI(ctx, "u2", a.ID); err == nil {
		t.Fatal("越权读取应报错")
	}

	// 删除图片时缓存一并清掉
	if err := store.Delete(ctx, a); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatalf("删除后内联缓存应被清掉，err=%v", err)
	}
}
