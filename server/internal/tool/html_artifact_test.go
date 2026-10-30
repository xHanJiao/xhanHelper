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
		json.RawMessage(`{"artifact_id":"`+id+`","view":"full"}`))
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
	res := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1"}, "get", json.RawMessage(`{"artifact_id":"legacy1","view":"full"}`))
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
	res3 := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1"}, "get", json.RawMessage(`{"artifact_id":"`+id+`","view":"full"}`))
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

// ---------- 细粒度读写（get view / patch） ----------

// 富文本页：含 span 的段落（rich），用来验证 patch 会拒绝压平格式。
const richPage = `<html><head><style>h1{color:#333} .tag{color:#a08a6c}</style></head><body>
<h1>标题</h1>
<p>含<span class="tag">标签</span>的段落，长度要够一个完整页面（内联校验要求至少 200 字节，这里纯粹是为了过闸）。</p>
</body></html>`

type outlineBlock struct {
	NID      string `json:"nid"`
	Tag      string `json:"tag"`
	Class    string `json:"class"`
	ClassCut bool   `json:"class_truncated"`
	Hash     string `json:"hash"`
	Text     string `json:"text"`
	Chars    int    `json:"chars"`
	Rich     bool   `json:"rich"`
	Image    string `json:"image"`
	Alt      string `json:"alt"`
}

type outlineResult struct {
	ArtifactID string         `json:"artifact_id"`
	Blocks     []outlineBlock `json:"blocks"`
	StyleBytes int            `json:"style_bytes"`
	PageBytes  int            `json:"page_bytes"`
	Tip        string         `json:"tip"`
}

func getOutlineResult(t *testing.T, tl *HTMLArtifactTool, id string) outlineResult {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"artifact_id": id})
	res := tl.Execute(&ExecuteContext{Ctx: context.Background(), UserID: "u1"}, "get", args)
	if !res.Success {
		t.Fatalf("get outline failed: %s", res.Error)
	}
	var out outlineResult
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("decode outline: %v", err)
	}
	return out
}

func outlineByNID(t *testing.T, out outlineResult, nid string) outlineBlock {
	t.Helper()
	for _, b := range out.Blocks {
		if b.NID == nid {
			return b
		}
	}
	t.Fatalf("outline 里没有 %s: %+v", nid, out.Blocks)
	return outlineBlock{}
}

func TestHTMLArtifactGetOutlineBlockStyle(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-outline"}

	res := tl.Execute(ec, "create", artArgs("对比", "三方案", samplePage))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)

	out := getOutlineResult(t, tl, id)
	if len(out.Blocks) != 2 {
		t.Fatalf("应有 2 块（h1 + p），实际 %+v", out.Blocks)
	}
	if b := outlineByNID(t, out, "t1"); b.Tag != "h1" || b.Hash == "" || b.Text != "三方案对比" {
		t.Fatalf("t1 不对: %+v", b)
	}
	if out.StyleBytes == 0 {
		t.Fatal("style_bytes 应大于 0")
	}
	if out.Tip == "" {
		t.Fatal("outline 应带 tip")
	}

	// block：拿到全文
	args, _ := json.Marshal(map[string]string{"artifact_id": id, "view": "block", "nid": "t2"})
	blockRes := tl.Execute(&ExecuteContext{Ctx: context.Background(), UserID: "u1"}, "get", args)
	if !blockRes.Success {
		t.Fatalf("get block failed: %s", blockRes.Error)
	}
	var block struct {
		NID  string `json:"nid"`
		Text string `json:"text"`
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(blockRes.Data, &block); err != nil {
		t.Fatalf("decode block: %v", err)
	}
	if block.NID != "t2" || !strings.Contains(block.Text, "内容内容") || block.Hash == "" {
		t.Fatalf("block 内容不对: %+v", block)
	}

	// style：拿到 CSS + hash
	args, _ = json.Marshal(map[string]string{"artifact_id": id, "view": "style"})
	styleRes := tl.Execute(&ExecuteContext{Ctx: context.Background(), UserID: "u1"}, "get", args)
	if !styleRes.Success {
		t.Fatalf("get style failed: %s", styleRes.Error)
	}
	var style struct {
		CSS  string `json:"style"`
		Hash string `json:"style_hash"`
	}
	if err := json.Unmarshal(styleRes.Data, &style); err != nil {
		t.Fatalf("decode style: %v", err)
	}
	if !strings.Contains(style.CSS, "color:#333") || style.Hash == "" {
		t.Fatalf("style 内容不对: %+v", style)
	}
}

func TestHTMLArtifactPatchTextAndStyle(t *testing.T) {
	tl, db, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-patch"}

	res := tl.Execute(ec, "create", artArgs("对比", "三方案", samplePage))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)

	out := getOutlineResult(t, tl, id)
	t1 := outlineByNID(t, out, "t1")

	patchArgs, _ := json.Marshal(map[string]any{
		"artifact_id": id,
		"ops": []map[string]string{
			{"nid": "t1", "hash": t1.Hash, "text": "改过的标题"},
		},
	})
	pres := tl.Execute(ec, "patch", patchArgs)
	if !pres.Success {
		t.Fatalf("patch failed: %s", pres.Error)
	}

	// 库里原文换了、成品也换了
	var got model.Artifact
	if err := db.First(&got, "id = ?", id).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if !strings.Contains(got.SourceHTML, "改过的标题") {
		t.Fatal("source_html 没更新")
	}
	if !strings.Contains(got.HTML, "改过的标题") {
		t.Fatal("html 成品没更新")
	}
	// 再读 outline：hash 变了、文字变了
	after := getOutlineResult(t, tl, id)
	if b := outlineByNID(t, after, "t1"); b.Text != "改过的标题" || b.Hash == t1.Hash {
		t.Fatalf("outline 没反映修改: %+v", b)
	}

	// 改样式（只给 style）
	patchArgs, _ = json.Marshal(map[string]any{
		"artifact_id": id,
		"style":       "h1{color:#900}",
		"style_hash":  styleHashOf(t, tl, id),
	})
	pres = tl.Execute(ec, "patch", patchArgs)
	if !pres.Success {
		t.Fatalf("patch style failed: %s", pres.Error)
	}
	if err := db.First(&got, "id = ?", id).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if !strings.Contains(got.SourceHTML, "color:#900") {
		t.Fatal("样式没写进 source_html")
	}
	// 正文没被样式改动影响
	if !strings.Contains(got.SourceHTML, "改过的标题") {
		t.Fatal("改样式不该动正文")
	}
}

func styleHashOf(t *testing.T, tl *HTMLArtifactTool, id string) string {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"artifact_id": id, "view": "style"})
	res := tl.Execute(&ExecuteContext{Ctx: context.Background(), UserID: "u1"}, "get", args)
	if !res.Success {
		t.Fatalf("get style failed: %s", res.Error)
	}
	var style struct {
		Hash string `json:"style_hash"`
	}
	if err := json.Unmarshal(res.Data, &style); err != nil {
		t.Fatalf("decode style: %v", err)
	}
	return style.Hash
}

func TestHTMLArtifactPatchRejectsStaleHash(t *testing.T) {
	tl, db, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-stale"}

	res := tl.Execute(ec, "create", artArgs("对比", "三方案", samplePage))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)

	// 模拟另一处先改了一版
	simulated := strings.Replace(samplePage, "三方案对比", "别人改过", 1)
	if err := db.Model(&model.Artifact{}).Where("id = ?", id).
		Update("source_html", simulated).Error; err != nil {
		t.Fatalf("simulate concurrent edit: %v", err)
	}

	// 拿旧页面的 nid/hash 去 patch：必须被拒
	patchArgs, _ := json.Marshal(map[string]any{
		"artifact_id": id,
		"ops":         []map[string]string{{"nid": "t1", "hash": "deadbeef", "text": "不该写入"}},
	})
	pres := tl.Execute(ec, "patch", patchArgs)
	if pres.Success {
		t.Fatal("旧 hash 应拒绝")
	}
	if !strings.Contains(pres.Error, "页面已变") {
		t.Fatalf("错误应说明页面已变，实际 %q", pres.Error)
	}
	if got := mustLoad(t, db, id); got.SourceHTML != simulated {
		t.Fatal("被拒的 patch 不该动库")
	}
}

// mustLoad 读整行。
func mustLoad(t *testing.T, db *gorm.DB, id string) model.Artifact {
	t.Helper()
	var a model.Artifact
	if err := db.First(&a, "id = ?", id).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	return a
}

func TestHTMLArtifactPatchRejectsRichBlock(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-rich"}

	res := tl.Execute(ec, "create", artArgs("标题", "含内联格式", richPage))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)
	out := getOutlineResult(t, tl, id)
	var rich outlineBlock
	for _, b := range out.Blocks {
		if b.Rich {
			rich = b
		}
	}
	if rich.NID == "" {
		t.Fatalf("应有一块 rich: %+v", out.Blocks)
	}
	patchArgs, _ := json.Marshal(map[string]any{
		"artifact_id": id,
		"ops":         []map[string]string{{"nid": rich.NID, "hash": rich.Hash, "text": "新的"}},
	})
	pres := tl.Execute(ec, "patch", patchArgs)
	if pres.Success {
		t.Fatal("rich 块写文字应拒绝")
	}
	if !strings.Contains(pres.Error, "内联格式") {
		t.Fatalf("错误应说明内联格式，实际 %q", pres.Error)
	}
}

func TestHTMLArtifactPatchRejectsLegacyPage(t *testing.T) {
	tl, db, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ctx := context.Background()

	// 早期页面：只有 html，没有 source_html
	legacy := &model.Artifact{
		ID: "legacy-1", UserID: "u1", ConversationID: "c1",
		Title: "旧页", Summary: "旧摘要", HTML: samplePage,
	}
	if err := db.Create(legacy).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	patchArgs, _ := json.Marshal(map[string]any{
		"artifact_id": "legacy-1",
		"ops":         []map[string]string{{"nid": "t1", "hash": "x", "text": "新的"}},
	})
	pres := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1"}, "patch", patchArgs)
	if pres.Success {
		t.Fatal("老页面 patch 应拒绝")
	}
	if !strings.Contains(pres.Error, "早期格式") {
		t.Fatalf("错误应说明早期格式，实际 %q", pres.Error)
	}
	// outline 也拒绝；full 仍可读回（这里 html 列小，不超限）
	args, _ := json.Marshal(map[string]string{"artifact_id": "legacy-1"})
	if res := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1"}, "get", args); res.Success {
		t.Fatal("老页面 outline 应拒绝")
	}
	args, _ = json.Marshal(map[string]string{"artifact_id": "legacy-1", "view": "full"})
	if res := tl.Execute(&ExecuteContext{Ctx: ctx, UserID: "u1"}, "get", args); !res.Success {
		t.Fatalf("老页面 full 应可读回: %s", res.Error)
	}
}

func TestHTMLArtifactPatchRequiresOpsOrStyle(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-empty"}
	res := tl.Execute(ec, "create", artArgs("对比", "三方案", samplePage))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)

	args, _ := json.Marshal(map[string]any{"artifact_id": id})
	pres := tl.Execute(ec, "patch", args)
	if pres.Success {
		t.Fatal("什么都不传应拒绝")
	}
}

// 换图：patch 图片块后，原文保留 image: 引用、成品重新内联成 data:。
func TestHTMLArtifactPatchImage(t *testing.T) {
	tl, db, images, cleanup := setupArtifactTool(t)
	defer cleanup()
	ctx := context.Background()
	ec := &ExecuteContext{Ctx: ctx, UserID: "u1", ConvID: "c1", TurnID: "turn-img"}

	first, err := images.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, tinyPNG(t))
	if err != nil {
		t.Fatalf("upload first: %v", err)
	}
	second, err := images.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, tinyPNG(t))
	if err != nil {
		t.Fatalf("upload second: %v", err)
	}

	page := strings.Replace(samplePage, "</body>", `<img src="image:`+first.ID+`" alt="旧图"></body>`, 1)
	page = strings.Replace(page, "<h1>三方案对比</h1>", `<h1>三方案对比</h1><p>换图测试的正文，长度够过闸。</p>`, 1)
	res := tl.Execute(ec, "create", artArgs("带图", "一页带图", page))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)

	img := outlineByNID(t, getOutlineResult(t, tl, id), "img1")
	patchArgs, _ := json.Marshal(map[string]any{
		"artifact_id": id,
		"ops": []map[string]string{
			{"nid": "img1", "hash": img.Hash, "image": "image:" + second.ID, "alt": "新图"},
		},
	})
	pres := tl.Execute(ec, "patch", patchArgs)
	if !pres.Success {
		t.Fatalf("patch image failed: %s", pres.Error)
	}
	got := mustLoad(t, db, id)
	if !strings.Contains(got.SourceHTML, "image:"+second.ID) {
		t.Fatal("原文里应换成新的 image: 引用")
	}
	if strings.Contains(got.SourceHTML, "data:image") {
		t.Fatal("原文不该出现 base64")
	}
	if !strings.Contains(got.HTML, "data:image") {
		t.Fatal("成品应重新内联成 data:")
	}
}

func TestHTMLArtifactPatchLimits(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-limits"}

	res := tl.Execute(ec, "create", artArgs("对比", "三方案", samplePage))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)
	t1 := outlineByNID(t, getOutlineResult(t, tl, id), "t1")

	// ops 超过 50
	ops := make([]map[string]string, 0, maxPatchOps+1)
	for i := 0; i < maxPatchOps+1; i++ {
		ops = append(ops, map[string]string{"nid": "t1", "hash": t1.Hash, "text": "x"})
	}
	args, _ := json.Marshal(map[string]any{"artifact_id": id, "ops": ops})
	if pres := tl.Execute(ec, "patch", args); pres.Success || !strings.Contains(pres.Error, "最多改") {
		t.Fatalf("ops 超限应拒绝，实际 success=%v err=%q", pres.Success, pres.Error)
	}

	// style 超过 64KB
	args, _ = json.Marshal(map[string]any{
		"artifact_id": id,
		"style":       strings.Repeat("a", maxStyleBytes+1),
		"style_hash":  styleHashOf(t, tl, id),
	})
	if pres := tl.Execute(ec, "patch", args); pres.Success || !strings.Contains(pres.Error, "样式太长") {
		t.Fatalf("style 超限应拒绝，实际 success=%v err=%q", pres.Success, pres.Error)
	}

	// 改样式不带 style_hash
	args, _ = json.Marshal(map[string]any{"artifact_id": id, "style": "h1{color:#000}"})
	if pres := tl.Execute(ec, "patch", args); pres.Success || !strings.Contains(pres.Error, "style_hash") {
		t.Fatalf("缺 style_hash 应拒绝，实际 success=%v err=%q", pres.Success, pres.Error)
	}
}

func TestHTMLArtifactPatchOpsAndStyleTogether(t *testing.T) {
	tl, db, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-both"}

	res := tl.Execute(ec, "create", artArgs("对比", "三方案", samplePage))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)
	t1 := outlineByNID(t, getOutlineResult(t, tl, id), "t1")

	args, _ := json.Marshal(map[string]any{
		"artifact_id": id,
		"ops":         []map[string]string{{"nid": "t1", "hash": t1.Hash, "text": "一起改的标题"}},
		"style":       "h1{color:#123}",
		"style_hash":  styleHashOf(t, tl, id),
	})
	if pres := tl.Execute(ec, "patch", args); !pres.Success {
		t.Fatalf("同时改正文和样式应成功: %s", pres.Error)
	}
	got := mustLoad(t, db, id)
	if !strings.Contains(got.SourceHTML, "一起改的标题") || !strings.Contains(got.SourceHTML, "color:#123") {
		t.Fatal("正文和样式都应写入")
	}
}

func TestHTMLArtifactGetBlockUnknownNID(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-unknown"}

	res := tl.Execute(ec, "create", artArgs("对比", "三方案", samplePage))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)

	args, _ := json.Marshal(map[string]string{"artifact_id": id, "view": "block", "nid": "t99"})
	pres := tl.Execute(&ExecuteContext{Ctx: context.Background(), UserID: "u1"}, "get", args)
	if pres.Success {
		t.Fatal("不存在的 nid 应失败")
	}
	if !strings.Contains(pres.Error, "没有这个块") {
		t.Fatalf("错误应说明没有这个块，实际 %q", pres.Error)
	}
}

// base64 直写的图片不能把整段塞进 outline / block（一张 3–5MB 的图会把 1KB 变成几 MB）。
func TestHTMLArtifactOutlineNeverLeaksBase64(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-b64"}

	page := strings.Replace(samplePage, "</body>",
		`<img src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==" alt="直写"></body>`, 1)
	res := tl.Execute(ec, "create", artArgs("直写图", "base64 图", page))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)

	args, _ := json.Marshal(map[string]string{"artifact_id": id})
	got := tl.Execute(&ExecuteContext{Ctx: context.Background(), UserID: "u1"}, "get", args)
	if !got.Success {
		t.Fatalf("outline failed: %s", got.Error)
	}
	raw := string(got.Data)
	if strings.Contains(raw, "iVBORw0KGgo") {
		t.Fatal("outline 不该回传 base64")
	}
	if !strings.Contains(raw, "image_bytes") || !strings.Contains(raw, "legacy") {
		t.Fatalf("应给 legacy + image_bytes: %s", raw)
	}

	// block 视图同样不能漏
	args, _ = json.Marshal(map[string]string{"artifact_id": id, "view": "block", "nid": "img1"})
	got = tl.Execute(&ExecuteContext{Ctx: context.Background(), UserID: "u1"}, "get", args)
	if !got.Success {
		t.Fatalf("block failed: %s", got.Error)
	}
	if strings.Contains(string(got.Data), "iVBORw0KGgo") {
		t.Fatal("block 不该回传 base64")
	}
}

func TestHTMLArtifactPatchInsertImage(t *testing.T) {
	tl, db, images, cleanup := setupArtifactTool(t)
	defer cleanup()
	ctx := context.Background()
	ec := &ExecuteContext{Ctx: ctx, UserID: "u1", ConvID: "c1", TurnID: "turn-ins"}

	asset, err := images.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, tinyPNG(t))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	res := tl.Execute(ec, "create", artArgs("对比", "三方案", samplePage))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)
	t1 := outlineByNID(t, getOutlineResult(t, tl, id), "t1")

	frag := `<figure><img src="image:` + asset.ID + `" alt="新图"><figcaption>图注</figcaption></figure>`
	args, _ := json.Marshal(map[string]any{
		"artifact_id": id,
		"insert": []map[string]string{
			{"after": "t1", "hash": t1.Hash, "html": frag},
		},
	})
	if pres := tl.Execute(ec, "patch", args); !pres.Success {
		t.Fatalf("insert failed: %s", pres.Error)
	} else if !strings.Contains(string(pres.Data), "块编号已重排") {
		t.Fatalf("结构改动后应提示重新 get(outline): %s", pres.Data)
	}
	got := mustLoad(t, db, id)
	if !strings.Contains(got.SourceHTML, "image:"+asset.ID) {
		t.Fatal("原文里应插入 image: 引用")
	}
	if !strings.Contains(got.HTML, "data:image") {
		t.Fatal("成品应已内联")
	}
	// h1 之后应该多出图片块
	after := getOutlineResult(t, tl, id)
	if len(after.Blocks) < 2 || after.Blocks[1].Tag != "img" {
		t.Fatalf("插入位置不对: %+v", after.Blocks)
	}
}

func TestHTMLArtifactPatchRemoveImage(t *testing.T) {
	tl, db, images, cleanup := setupArtifactTool(t)
	defer cleanup()
	ctx := context.Background()
	ec := &ExecuteContext{Ctx: ctx, UserID: "u1", ConvID: "c1", TurnID: "turn-rm"}

	asset, err := images.Upload(ctx, "u1", model.ImageSourceChat, model.ImageVisibilityPrivate, tinyPNG(t))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	page := strings.Replace(samplePage, "</body>",
		`<figure><img src="image:`+asset.ID+`" alt="待删"><figcaption>这张要删</figcaption></figure></body>`, 1)
	res := tl.Execute(ec, "create", artArgs("带图", "待删一张", page))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)
	img := outlineByNID(t, getOutlineResult(t, tl, id), "img1")

	args, _ := json.Marshal(map[string]any{
		"artifact_id": id,
		"remove":      []map[string]string{{"nid": "img1", "hash": img.Hash}},
	})
	if pres := tl.Execute(ec, "patch", args); !pres.Success {
		t.Fatalf("remove failed: %s", pres.Error)
	}
	got := mustLoad(t, db, id)
	if strings.Contains(got.SourceHTML, "<figure") || strings.Contains(got.SourceHTML, "<img") {
		t.Fatal("图应连 figure 一起删掉")
	}
	if !strings.Contains(got.SourceHTML, "三方案对比") {
		t.Fatal("其余内容不该动")
	}
}

func TestHTMLArtifactPatchStructureLimits(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-slimit"}

	res := tl.Execute(ec, "create", artArgs("对比", "三方案", samplePage))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)
	t1 := outlineByNID(t, getOutlineResult(t, tl, id), "t1")

	inserts := make([]map[string]string, 0, 11)
	for i := 0; i < 11; i++ {
		inserts = append(inserts, map[string]string{"after": "t1", "hash": t1.Hash, "html": "<p>x</p>"})
	}
	args, _ := json.Marshal(map[string]any{"artifact_id": id, "insert": inserts})
	if pres := tl.Execute(ec, "patch", args); pres.Success || !strings.Contains(pres.Error, "最多插入") {
		t.Fatalf("插入超限应拒绝，实际 success=%v err=%q", pres.Success, pres.Error)
	}

	removes := make([]map[string]string, 0, 21)
	for i := 0; i < 21; i++ {
		removes = append(removes, map[string]string{"nid": "t1", "hash": t1.Hash})
	}
	args, _ = json.Marshal(map[string]any{"artifact_id": id, "remove": removes})
	if pres := tl.Execute(ec, "patch", args); pres.Success || !strings.Contains(pres.Error, "最多删") {
		t.Fatalf("删除超限应拒绝，实际 success=%v err=%q", pres.Success, pres.Error)
	}
}

func TestHTMLArtifactPatchEditConflict(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-conf"}

	res := tl.Execute(ec, "create", artArgs("对比", "三方案", samplePage))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)
	t1 := outlineByNID(t, getOutlineResult(t, tl, id), "t1")

	args, _ := json.Marshal(map[string]any{
		"artifact_id": id,
		"ops":         []map[string]string{{"nid": "t1", "hash": t1.Hash, "text": "改"}},
		"remove":      []map[string]string{{"nid": "t1", "hash": t1.Hash}},
	})
	if pres := tl.Execute(ec, "patch", args); pres.Success || !strings.Contains(pres.Error, "同时修改和删除") {
		t.Fatalf("同时改删应拒绝，实际 success=%v err=%q", pres.Success, pres.Error)
	}
}

// outline 要带 class：插入新块时照抄相邻块的 class 才有间距/配色。
func TestHTMLArtifactOutlineCarriesClass(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-class"}

	page := `<html><head><style>h1{color:#333}</style></head><body>` +
		`<h1 class="title">标题</h1><p class="sec lead">正文内容</p>` +
		`<div class="fig"><img class="photo" src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==" alt="照片"></div></body></html>`
	res := tl.Execute(ec, "create", artArgs("带类", "class 透出", page))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	out := getOutlineResult(t, tl, artifactIDFrom(t, res))
	if got := outlineByNID(t, out, "t1").Class; got != "title" {
		t.Fatalf("h1 的 class 应透出，实际 %q", got)
	}
	if got := outlineByNID(t, out, "t2").Class; got != "sec lead" {
		t.Fatalf("段落的 class 应透出，实际 %q", got)
	}
	// 图片块同样要带（insert 一张新图时照抄它）
	if got := outlineByNID(t, out, "img1").Class; got != "photo" {
		t.Fatalf("图片的 class 应透出，实际 %q", got)
	}
}

// 删完这页就空了：不能报「HTML 太短，不像一个完整页面」（模型没法自愈）。
func TestHTMLArtifactRemoveLastBlockSaysPageWouldBeEmpty(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-empty"}

	body := strings.Repeat("正文", 90) // 180 字 ≈ 540 字节，删掉后整页只剩骨架
	page := `<html><head><style>h1{color:#333}</style></head><body><p>` + body + `</p></body></html>`
	res := tl.Execute(ec, "create", artArgs("只剩一段", "删空测试", page))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)
	t1 := outlineByNID(t, getOutlineResult(t, tl, id), "t1")

	args, _ := json.Marshal(map[string]any{
		"artifact_id": id,
		"remove":      []map[string]string{{"nid": "t1", "hash": t1.Hash}},
	})
	pres := tl.Execute(ec, "patch", args)
	if pres.Success {
		t.Fatal("删空整页应被拒绝")
	}
	if !strings.Contains(pres.Error, "删完这页就空了") {
		t.Fatalf("文案要指向下一步，实际 %q", pres.Error)
	}
}

// class 截断时给标记：整段照抄会抄到半截类名。
func TestHTMLArtifactOutlineMarksTruncatedClass(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-classcut"}

	long := strings.Repeat("c", maxOutlineClassRunes+10)
	page := `<html><head><style>h1{color:#333}</style></head><body><h1 class="` + long + `">标题</h1>` +
		`<p>` + strings.Repeat("正文", 60) + `</p></body></html>`
	res := tl.Execute(ec, "create", artArgs("长类名", "截断标记", page))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	out := getOutlineResult(t, tl, artifactIDFrom(t, res))
	b := outlineByNID(t, out, "t1")
	if !b.ClassCut || len([]rune(b.Class)) != maxOutlineClassRunes {
		t.Fatalf("应标 class_truncated 并截到 %d 字，实际 %+v", maxOutlineClassRunes, b)
	}
}

// 锚点在表格单元格里：必须拒（不然源码写成 <tr><p>，浏览器把内容挤出表格），
// 且文案要指出两条出路；页面本身不能被改。
func TestHTMLArtifactPatchRejectsTableAnchor(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-td-anchor"}

	page := `<html><head><style>td{padding:4px}</style></head><body>` +
		`<p>表格前的正文，长度要够一个完整页面（内联校验要求至少 200 字节）。</p>` +
		`<table><tr><td>甲</td><td>乙</td></tr></table></body></html>`
	res := tl.Execute(ec, "create", artArgs("表格", "两列", page))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)
	td := outlineByNID(t, getOutlineResult(t, tl, id), "t2")
	if td.Tag != "td" {
		t.Fatalf("t2 应是单元格: %+v", td)
	}

	args, _ := json.Marshal(map[string]any{
		"artifact_id": id,
		"insert":      []map[string]string{{"after": "t2", "hash": td.Hash, "html": `<p class="lead">新段落</p>`}},
	})
	pres := tl.Execute(ec, "patch", args)
	if pres.Success {
		t.Fatal("锚在 td 上插段落应被拒绝")
	}
	for _, want := range []string{"<table>", `view="full"`} {
		if !strings.Contains(pres.Error, want) {
			t.Fatalf("文案应给出路（缺 %q）：%s", want, pres.Error)
		}
	}

	fullArgs, _ := json.Marshal(map[string]any{"artifact_id": id, "view": "full"})
	fres := tl.Execute(ec, "get", fullArgs)
	var full struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal(fres.Data, &full); err != nil {
		t.Fatalf("decode full: %v", err)
	}
	if strings.Contains(full.HTML, "新段落") || full.HTML != page {
		t.Fatalf("失败不能改动页面:\n%s", full.HTML)
	}
}

// updated 只列「内容被替换」的块：删块有自己的 removed 计数，不再混进来。
func TestHTMLArtifactPatchUpdatedOnlyCoversOps(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-updated"}

	res := tl.Execute(ec, "create", artArgs("两段", "updated 语义", `<html><head><style>h1{color:#333}</style></head><body><h1>标题</h1><p>`+
		strings.Repeat("正文", 100)+`</p><p>第二段</p></body></html>`))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)
	out := getOutlineResult(t, tl, id)

	type patchRes struct {
		Updated  []string `json:"updated"`
		Inserted int      `json:"inserted"`
		Removed  int      `json:"removed"`
	}
	run := func(args map[string]any) patchRes {
		b, _ := json.Marshal(args)
		r := tl.Execute(ec, "patch", b)
		if !r.Success {
			t.Fatalf("patch failed: %s", r.Error)
		}
		var pr patchRes
		if err := json.Unmarshal(r.Data, &pr); err != nil {
			t.Fatalf("decode patch result: %v", err)
		}
		return pr
	}

	t3 := outlineByNID(t, out, "t3")
	pr := run(map[string]any{"artifact_id": id, "remove": []map[string]string{{"nid": "t3", "hash": t3.Hash}}})
	if pr.Removed != 1 || len(pr.Updated) != 0 {
		t.Fatalf("删除不该出现在 updated: %+v", pr)
	}

	out = getOutlineResult(t, tl, id)
	t1 := outlineByNID(t, out, "t1")
	pr = run(map[string]any{
		"artifact_id": id,
		"ops":         []map[string]string{{"nid": "t1", "hash": t1.Hash, "text": "新标题"}},
	})
	if len(pr.Updated) != 1 || pr.Updated[0] != "t1" {
		t.Fatalf("改文字应把 nid 列进 updated: %+v", pr)
	}
}

// 没有 <style> 的页也能改样式：get(style) 回空 hash，patch 新建一段（不再死锁）。
func TestHTMLArtifactPatchStyleOnPageWithoutStyle(t *testing.T) {
	tl, _, _, cleanup := setupArtifactTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: "c1", TurnID: "turn-nostyle"}
	page := `<html><body><p>` + strings.Repeat("正文", 100) + `</p></body></html>`

	res := tl.Execute(ec, "create", artArgs("无样式", "改样式不该死锁", page))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	id := artifactIDFrom(t, res)

	getArgs, _ := json.Marshal(map[string]any{"artifact_id": id, "view": "style"})
	sres := tl.Execute(ec, "get", getArgs)
	if !sres.Success {
		t.Fatalf("无 style 时 get(style) 不该报错: %s", sres.Error)
	}
	var st struct {
		Style     string `json:"style"`
		StyleHash string `json:"style_hash"`
		Tip       string `json:"tip"`
	}
	if err := json.Unmarshal(sres.Data, &st); err != nil {
		t.Fatalf("decode style: %v", err)
	}
	if st.Style != "" || st.StyleHash != "" || st.Tip == "" {
		t.Fatalf("应回空样式 + 新建提示: %+v", st)
	}

	css := "body{background:#fafafa;color:#222}"
	pargs, _ := json.Marshal(map[string]any{"artifact_id": id, "style": css, "style_hash": ""})
	pres := tl.Execute(ec, "patch", pargs)
	if !pres.Success {
		t.Fatalf("首次写样式应成功: %s", pres.Error)
	}

	sres = tl.Execute(ec, "get", getArgs)
	if err := json.Unmarshal(sres.Data, &st); err != nil {
		t.Fatalf("decode style 2: %v", err)
	}
	if st.Style != css || st.StyleHash == "" {
		t.Fatalf("新建的样式应能读回: %+v", st)
	}

	// 现在有样式了，再不带 hash 就该拒绝（防并发覆盖）
	pres = tl.Execute(ec, "patch", pargs)
	if pres.Success || !strings.Contains(pres.Error, "style_hash") {
		t.Fatalf("已有样式时缺 hash 应拒绝，实际 %q", pres.Error)
	}
}
