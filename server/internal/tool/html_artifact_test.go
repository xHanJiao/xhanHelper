package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
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

// tinyPNG — 真能解码的最小 PNG（内联前会压缩，只对魔数做手脚的假图会被拒）。
// 每次调用改一个像素，让 sha256 不同 —— 否则 Upload 会按内容去重成同一张，张数就测不出来了。
var tinypngSeq int

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	tinypngSeq++
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(tinypngSeq%8, tinypngSeq%8, color.RGBA{R: uint8(tinypngSeq), G: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

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

// reviseArgs — 「改这一版」：在 artArgs 基础上带 artifact_id。
func reviseArgs(title, summary, html, id string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"title": title, "summary": summary, "html": html, "artifact_id": id})
	return b
}

// reviseHTMLArgs — 改版只给 html（标题/摘要沿用旧页）。
func reviseHTMLArgs(html, id string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"html": html, "artifact_id": id})
	return b
}

func artifactIDFrom(t *testing.T, res *Result) string {
	t.Helper()
	var out struct {
		ArtifactID string `json:"artifact_id"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return out.ArtifactID
}

// list：跨会话也能看到（换窗口说「改刚才那页」时靠它找 id），倒序、只给自己的、不碰正文。
func TestHTMLArtifactListAcrossConversations(t *testing.T) {
	tl, db, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ctx := context.Background()

	base := time.Now().Add(-time.Hour)
	seed := []model.Artifact{
		{ID: "p1", UserID: "u1", ConversationID: "c1", Title: "10.18 手账", Summary: "s1", HTML: samplePage, CreatedAt: base},
		{ID: "p2", UserID: "u1", ConversationID: "c2", Title: "10.19 手账", Summary: "s2", HTML: samplePage, CreatedAt: base.Add(time.Minute)},
		{ID: "p3", UserID: "u2", ConversationID: "c3", Title: "别人的", Summary: "s3", HTML: samplePage, CreatedAt: base.Add(2 * time.Minute)},
	}
	for i := range seed {
		if err := db.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", seed[i].ID, err)
		}
	}

	// 故意用一个全新的会话 id：证明 list 不受当前会话限制
	ec := &ExecuteContext{Ctx: ctx, UserID: "u1", ConvID: "c9", TurnID: "turn-list"}
	res := tl.Execute(ec, "list", json.RawMessage(`{}`))
	if !res.Success {
		t.Fatalf("list failed: %s", res.Error)
	}

	var out struct {
		Items []struct {
			ArtifactID string `json:"artifact_id"`
			Title      string `json:"title"`
			Summary    string `json:"summary"`
			CreatedAt  string `json:"created_at"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if out.Total != 2 || len(out.Items) != 2 {
		t.Fatalf("应只列出 u1 的 2 页，实际 total=%d len=%d", out.Total, len(out.Items))
	}
	if out.Items[0].ArtifactID != "p2" || out.Items[1].ArtifactID != "p1" {
		t.Fatalf("应按时间倒序，实际 %+v", out.Items)
	}
	if out.Items[0].Title != "10.19 手账" || out.Items[0].CreatedAt == "" {
		t.Fatalf("标题/时间应返回，实际 %+v", out.Items[0])
	}

	// 拿 list 给的 id 能直接改
	res2 := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1", ConvID: "c9", TurnID: "turn-list-2"},
		"create", reviseArgs("改过", "s", samplePage, out.Items[0].ArtifactID))
	if !res2.Success {
		t.Fatalf("用 list 的 id 改版失败: %s", res2.Error)
	}
}

// get：读回模型给的原文（图片仍是 `image:<id>`，不是几 MB 的 data:），越权/不存在报错。
func TestHTMLArtifactGetReturnsSource(t *testing.T) {
	tl, _, images, cleanup := setupArtifactTool(t)
	defer cleanup()
	ctx := context.Background()

	asset, err := images.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, tinyPNG(t))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	page := strings.Replace(samplePage, "</body>", `<img src="image:`+asset.ID+`" alt="图"></body>`, 1)

	res := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1", ConvID: "c1", TurnID: "turn-get"},
		"create", artArgs("带图", "一页带图", page))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)

	res2 := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1"}, "get",
		json.RawMessage(`{"artifact_id":"`+id+`"}`))
	if !res2.Success {
		t.Fatalf("get failed: %s", res2.Error)
	}
	var out struct {
		ArtifactID string `json:"artifact_id"`
		Title      string `json:"title"`
		Summary    string `json:"summary"`
		HTML       string `json:"html"`
	}
	if err := json.Unmarshal(res2.Data, &out); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if out.ArtifactID != id || out.Title != "带图" || out.Summary != "一页带图" {
		t.Fatalf("get 元数据不对: %+v", out)
	}
	if out.HTML != page {
		t.Fatal("应原样返回模型给的原文（改版要在这份上改）")
	}
	if !strings.Contains(out.HTML, "image:"+asset.ID) || strings.Contains(out.HTML, "data:image") {
		t.Fatal("读回的图片必须是 image: 引用，不能是内联 base64")
	}

	// 越权：别人的 id 按不存在处理
	if res := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u2"}, "get",
		json.RawMessage(`{"artifact_id":"`+id+`"}`)); res.Success {
		t.Fatal("越权读取应失败")
	}
	if res := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1"}, "get",
		json.RawMessage(`{"artifact_id":"nope"}`)); res.Success {
		t.Fatal("不存在的 id 应失败")
	}
	if res := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1"}, "get",
		json.RawMessage(`{}`)); res.Success {
		t.Fatal("缺 artifact_id 应失败")
	}
}

// list 是只读函数：plan 模式也要能查（ReadOnlyFuncs 声明必须与 Functions 一致）。
func TestHTMLArtifactListIsReadOnly(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()

	ro := tl.ReadOnlyFuncs()
	if !ro["list"] || !ro["get"] || ro["create"] {
		t.Fatalf("只应声明 list/get 为只读，实际 %+v", ro)
	}
	// 声明与 Functions() 不一致会 panic（ValidateReadOnlyFuncs 的契约）
	reg := NewRegistry()
	reg.Register(tl)
	reg.ValidateReadOnlyFuncs()
}

// 改版 = 原地覆盖：同一个 id、行数不变、正文/标题换掉（灵阅里不会多一条）。
func TestHTMLArtifactReviseOverwritesInPlace(t *testing.T) {
	tl, db, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ctx := context.Background()

	res := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1", ConvID: "c1", TurnID: "turn-r1"},
		"create", artArgs("初版", "第一版", samplePage))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)

	revised := strings.Replace(samplePage, "三方案对比", "改过的标题", 1)
	res2 := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1", ConvID: "c1", TurnID: "turn-r2"},
		"create", reviseArgs("改版", "第二版", revised, id))
	if !res2.Success {
		t.Fatalf("revise failed: %s", res2.Error)
	}
	if got := artifactIDFrom(t, res2); got != id {
		t.Fatalf("改版应返回同一个 id，实际 %s != %s", got, id)
	}

	var count int64
	if err := db.Model(&model.Artifact{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("改版不应新增行，实际 %d 行", count)
	}

	var got model.Artifact
	if err := db.First(&got, "id = ?", id).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Title != "改版" || got.Summary != "第二版" || !strings.Contains(got.HTML, "改过的标题") {
		t.Fatalf("原地覆盖没生效：title=%q summary=%q", got.Title, got.Summary)
	}
	if strings.Contains(got.HTML, "三方案对比") {
		t.Fatal("旧正文应被替换")
	}
}

// list 的 limit：>30 应钳到 30，而不是静默降到默认 10。
func TestHTMLArtifactListLimitClamp(t *testing.T) {
	tl, db, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 15; i++ {
		if err := db.Create(&model.Artifact{
			ID: fmt.Sprintf("l%02d", i), UserID: "u1", ConversationID: "c1",
			Title: "t", Summary: "s", HTML: samplePage, CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	res := tl.Execute(&ExecuteContext{Ctx: context.Background(), UserID: "u1"}, "list", json.RawMessage(`{"limit":30}`))
	if !res.Success {
		t.Fatalf("list failed: %s", res.Error)
	}
	var out struct {
		Items []struct {
			ArtifactID string `json:"artifact_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(out.Items) != 15 {
		t.Fatalf("limit=30 应返回全部 15 条（钳到 30），实际 %d 条（被降到 10 就会是这个数）", len(out.Items))
	}
}

// get 的两个分支：超过读回上限要报错；老页面（没有 source_html）退回 html 列。
func TestHTMLArtifactGetLimitsAndLegacy(t *testing.T) {
	tl, db, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ctx := context.Background()

	// 老页面：只写 html，source_html 为空
	legacy := &model.Artifact{
		ID: "legacy1", UserID: "u1", ConversationID: "c1",
		Title: "旧页", Summary: "旧摘要", HTML: samplePage,
	}
	if err := db.Create(legacy).Error; err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	res := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1"}, "get", json.RawMessage(`{"artifact_id":"legacy1"}`))
	if !res.Success {
		t.Fatalf("get legacy failed: %s", res.Error)
	}
	var out struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.HTML != samplePage {
		t.Fatal("没有 source_html 的老页面应退回 html 列")
	}

	// 原文超过 128KB：拒绝（不截断——截断的 HTML 改完会坏）
	huge := strings.Replace(samplePage, "</body>", strings.Repeat("<p>填充内容</p>", 20000)+"</body>", 1)
	res2 := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1", ConvID: "c1", TurnID: "turn-big"},
		"create", artArgs("大页", "很大的一页", huge))
	if !res2.Success {
		t.Fatalf("create huge failed: %s", res2.Error)
	}
	id := artifactIDFrom(t, res2)
	res3 := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1"}, "get", json.RawMessage(`{"artifact_id":"`+id+`"}`))
	if res3.Success {
		t.Fatal("超过读回上限应拒绝")
	}
	if !strings.Contains(res3.Error, "太大") {
		t.Fatalf("错误应说明太大，实际 %q", res3.Error)
	}
}

// 改版不传 title/summary：沿用旧页的（它们描述「这一页是什么」，不是「这次改了什么」）。
func TestHTMLArtifactReviseKeepsTitleSummary(t *testing.T) {
	tl, db, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ctx := context.Background()

	res := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1", ConvID: "c1", TurnID: "turn-k1"},
		"create", artArgs("日本之行 · 10.19", "四页手账：原宿、代代木、银座。", samplePage))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)

	revised := strings.Replace(samplePage, "三方案对比", "改过的标题", 1)
	res2 := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1", ConvID: "c1", TurnID: "turn-k2"},
		"create", reviseHTMLArgs(revised, id))
	if !res2.Success {
		t.Fatalf("revise failed: %s", res2.Error)
	}
	if got := artifactIDFrom(t, res2); got != id {
		t.Fatalf("改版应返回同一个 id，实际 %s != %s", got, id)
	}

	var out struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(res2.Data, &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if out.Title != "日本之行 · 10.19" || out.Summary != "四页手账：原宿、代代木、银座。" {
		t.Fatalf("改版应沿用旧标题/摘要，实际 %+v", out)
	}

	var got model.Artifact
	if err := db.First(&got, "id = ?", id).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Title != "日本之行 · 10.19" || got.Summary != "四页手账：原宿、代代木、银座。" {
		t.Fatalf("库里标题/摘要被改掉了：%q / %q", got.Title, got.Summary)
	}
	if !strings.Contains(got.HTML, "改过的标题") {
		t.Fatal("正文应已更新")
	}
}

// 新建仍然必须给 title/summary（只有改版才允许省略）。
func TestHTMLArtifactCreateRequiresTitleAndSummary(t *testing.T) {
	tl, db, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ctx := context.Background()

	args, err := json.Marshal(map[string]string{"html": samplePage})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1", ConvID: "c1", TurnID: "turn-req"}, "create", args)
	if res.Success {
		t.Fatal("新建缺 title/summary 应失败")
	}
	if !strings.Contains(res.Error, "title / summary") {
		t.Fatalf("错误应点名缺字段，实际 %q", res.Error)
	}
	var count int64
	if err := db.Model(&model.Artifact{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("失败不应入库，实际 %d 行", count)
	}
}

// 改一个不存在 / 别人的 id：报错点名，且**不偷偷新建**（静默新建正是堆积的来源）。
func TestHTMLArtifactReviseUnknownIDDoesNotCreate(t *testing.T) {
	tl, db, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ctx := context.Background()

	// 别人的页面
	other := &model.Artifact{ID: "other", UserID: "u2", ConversationID: "c2", Title: "t", Summary: "s", HTML: samplePage}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, id := range []string{"no-such-id", "other"} {
		res := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1", ConvID: "c1", TurnID: "turn-x-" + id},
			"create", reviseArgs("t", "s", samplePage, id))
		if res.Success {
			t.Fatalf("id=%s 应该报错", id)
		}
		if !strings.Contains(res.Error, "不存在") {
			t.Fatalf("id=%s 的错误应点名不存在，实际 %q", id, res.Error)
		}
	}

	var count int64
	if err := db.Model(&model.Artifact{}).Where("user_id = ?", "u1").Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("失败不应新建，u1 实际有 %d 行", count)
	}
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
	// 必须是**真能解码**的图：内联前会压缩（imgshrink），只对魔数做手脚的假图会报「无法识别」
	png := tinyPNG(t)
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
	if !strings.Contains(res.Error, "去掉") {
		t.Fatalf("错误应给出下一步（去掉这张/换一张）: %s", res.Error)
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
		png := tinyPNG(t) // 每次内容不同才有独立 asset（Upload 按 sha256 去重无所谓，张数才是被测对象）
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
