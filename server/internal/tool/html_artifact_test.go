package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupArtifactTool(t *testing.T) (*HTMLArtifactTool, *gorm.DB, *service.ImageStore, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(tmpDir, "a.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Artifact{}, &model.ImageAsset{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	images := service.NewImageStore(filepath.Join(tmpDir, "images"), 32<<20, service.NewImageAssetRepo(db), nil)
	cleanup := func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			sqlDB.Close()
		}
		os.RemoveAll(tmpDir)
	}
	return NewHTMLArtifactTool(service.NewArtifactRepo(db), images), db, images, cleanup
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
	tl, db, _, cleanup := setupArtifactTool(t)
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
	tl, db, _, cleanup := setupArtifactTool(t)
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
	tl, _, _, cleanup := setupArtifactTool(t)
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

// 校验/清洗失败不能扣配额：截断是最常见的失败，连撞三次不该让整轮报废。
func TestHTMLArtifactFailedDoesNotConsumeQuota(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-5"}

	broken := artArgs("t", "s", strings.Replace(samplePage, "</html>", "", 1))
	for i := 0; i < maxArtifactsPerTurn+2; i++ {
		if res := tl.Execute(ec, "create", broken); res.Success {
			t.Fatalf("第 %d 次截断页面应失败", i+1)
		}
	}
	if res := tl.Execute(ec, "create", artArgs("t", "s", samplePage)); !res.Success {
		t.Fatalf("失败不应扣配额，重写后应成功: %s", res.Error)
	}
}

// 图片在生成时内联：入库的是 data: URI，源图之后被删也不影响页面。
func TestHTMLArtifactInlinesImage(t *testing.T) {
	tl, db, images, cleanup := setupArtifactTool(t)
	defer cleanup()
	ctx := context.Background()
	// 只需魔数正确：Upload 按 DetectContentType 判类型，DataURI 不解析像素
	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte(strings.Repeat("0", 64))...)
	asset, err := images.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, png)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	page := strings.Replace(samplePage, "</body>", `<img src="image:`+asset.ID+`" alt="测试图"></body>`, 1)
	ec := &ExecuteContext{Ctx: ctx, UserID: "u1", ConvID: "c1", TurnID: "turn-img"}
	res := tl.Execute(ec, "create", artArgs("带图", "一页带图的总结", page))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}

	var got model.Artifact
	if err := db.Order("created_at DESC").First(&got).Error; err != nil {
		t.Fatalf("load artifact: %v", err)
	}
	if !strings.Contains(got.HTML, "data:image/png;base64,") {
		t.Fatal("图片应内联成 data: URI")
	}
	if strings.Contains(got.HTML, "image:"+asset.ID) {
		t.Fatal("入库后不应残留 image: 引用")
	}

	// 源图被清理也不影响已生成的页面
	var a model.ImageAsset
	if err := db.First(&a, "id = ?", asset.ID).Error; err != nil {
		t.Fatalf("load asset: %v", err)
	}
	if !a.Referenced {
		t.Error("内联后应标记 referenced（续期）")
	}
	if err := images.Delete(ctx, &a); err != nil {
		t.Fatalf("delete asset: %v", err)
	}
	// 从库里重新取（不是复用删除前的快照），才真的证明页面不依赖源文件
	var after model.Artifact
	if err := db.First(&after, "id = ?", got.ID).Error; err != nil {
		t.Fatalf("reload artifact: %v", err)
	}
	if !strings.Contains(after.HTML, "data:image/png;base64,") {
		t.Fatal("源图删除后页面里的图不应受影响")
	}
}

// 引用不存在的图 → 整页失败并点名 id；没入库、不扣配额。
func TestHTMLArtifactMissingImageFails(t *testing.T) {
	tl, db, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-miss"}

	page := strings.Replace(samplePage, "</body>", `<img src="image:not-exist" alt="x"></body>`, 1)
	res := tl.Execute(ec, "create", artArgs("t", "s", page))
	if res.Success {
		t.Fatal("引用不存在的图应失败")
	}
	if !strings.Contains(res.Error, "not-exist") {
		t.Fatalf("错误应点名 id: %s", res.Error)
	}
	var n int64
	if err := db.Model(&model.Artifact{}).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("失败不应入库，实际 %d 条", n)
	}
	if res := tl.Execute(ec, "create", artArgs("t", "s", samplePage)); !res.Success {
		t.Fatalf("图片失败不应扣配额: %s", res.Error)
	}
}

// 张数闸：超过上限立刻拒，不去读第 13 个文件（这里第 13 个 id 根本不存在）。
func TestHTMLArtifactRejectsTooManyImages(t *testing.T) {
	tl, _, images, cleanup := setupArtifactTool(t)
	defer cleanup()
	ctx := context.Background()

	ids := make([]string, 0, maxInlineImages+1)
	for i := 0; i < maxInlineImages; i++ {
		// 每张内容不同：Upload 按 sha256 去重，同一份字节只会拿到一个 id
		png := append([]byte("\x89PNG\r\n\x1a\n"), []byte(fmt.Sprintf("img-%d", i))...)
		a, err := images.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, png)
		if err != nil {
			t.Fatalf("upload: %v", err)
		}
		ids = append(ids, a.ID)
	}
	ids = append(ids, "over-limit")

	var imgs strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&imgs, `<img src="image:%s">`, id)
	}
	page := strings.Replace(samplePage, "</body>", imgs.String()+"</body>", 1)

	ec := &ExecuteContext{Ctx: ctx, UserID: "u1", ConvID: "c1", TurnID: "turn-many"}
	res := tl.Execute(ec, "create", artArgs("t", "s", page))
	if res.Success {
		t.Fatal("超过图片张数上限应失败")
	}
	if !strings.Contains(res.Error, "超过") || !strings.Contains(res.Error, "张") {
		t.Fatalf("错误应说明张数上限: %s", res.Error)
	}
}
