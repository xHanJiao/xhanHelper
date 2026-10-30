package htmlpatch

import (
	"errors"
	"strings"
	"testing"
)

const samplePage = `<html><head><style>
  body{background:#fff}
  .tag{color:#a08a6c}
</style></head><body>
  <h1>标题</h1>
  <p>第一段
     有缩进。</p>
  <p>含<span class="tag">标签</span>的段</p>
  <figure><img src="image:abc-1" alt="菜单"><figcaption>图注</figcaption></figure>
  <ul><li>条目一</li><li>条目二</li></ul>
  <table><tr><td>单元格</td></tr></table>
</body></html>`

func findBlock(t *testing.T, blocks []Block, nid string) Block {
	t.Helper()
	for _, b := range blocks {
		if b.NID == nid {
			return b
		}
	}
	t.Fatalf("没有 %s（现有：%+v）", nid, blocks)
	return Block{}
}

func blockText(blocks []Block, nid string) string {
	for _, b := range blocks {
		if b.NID == nid {
			return b.Text
		}
	}
	return ""
}

func mustScan(t *testing.T, source string) []Block {
	t.Helper()
	blocks, err := Scan(source)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return blocks
}

func TestScanBlocksInDocumentOrder(t *testing.T) {
	blocks := mustScan(t, samplePage)
	want := []string{"t1", "t2", "t3", "img1", "t4", "t5", "t6", "t7"}
	var got []string
	for _, b := range blocks {
		got = append(got, b.NID)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("块顺序不对\n got: %v\nwant: %v", got, want)
	}
	if b := findBlock(t, blocks, "t1"); b.Tag != "h1" || b.Text != "标题" {
		t.Fatalf("t1: %+v", b)
	}
	// 源码缩进折叠成一个空格，不变成换行
	if b := findBlock(t, blocks, "t2"); b.Text != "第一段 有缩进。" {
		t.Fatalf("t2 文本归一化不对: %q", b.Text)
	}
	// 含 span → rich
	if b := findBlock(t, blocks, "t3"); !b.Rich {
		t.Fatalf("t3 应标 rich: %+v", b)
	}
	if b := findBlock(t, blocks, "img1"); b.Tag != "img" || b.Image != "image:abc-1" || b.Alt != "菜单" {
		t.Fatalf("img1: %+v", b)
	}
	if b := findBlock(t, blocks, "t7"); b.Tag != "td" || b.Text != "单元格" {
		t.Fatalf("t7: %+v", b)
	}
	// 图注前后的结构不该把 figure 当成可编辑块
	if b := findBlock(t, blocks, "t4"); b.Tag != "figcaption" || b.Text != "图注" {
		t.Fatalf("t4: %+v", b)
	}
}

func TestScanIsStable(t *testing.T) {
	a := mustScan(t, samplePage)
	b := mustScan(t, samplePage)
	if len(a) != len(b) {
		t.Fatalf("两次扫描块数不同: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].NID != b[i].NID || a[i].Hash != b[i].Hash {
			t.Fatalf("第 %d 块不稳定: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestApplyText(t *testing.T) {
	blocks := mustScan(t, samplePage)
	target := findBlock(t, blocks, "t2")
	newText := "改过的第一段"
	out, changed, err := ApplyEdit(samplePage, Edit{Ops: []Op{{NID: "t2", Hash: target.Hash, Text: &newText}}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(changed) != 1 || changed[0] != "t2" {
		t.Fatalf("changed: %v", changed)
	}
	after := mustScan(t, out)
	if got := blockText(after, "t2"); got != newText {
		t.Fatalf("t2 没改成: %q", got)
	}
	// 其他块文字不变
	if got := blockText(after, "t1"); got != "标题" {
		t.Fatalf("t1 被误改: %q", got)
	}
}

func TestApplyTextKeepsLineBreaks(t *testing.T) {
	blocks := mustScan(t, samplePage)
	target := findBlock(t, blocks, "t1")
	newText := "第一行\n第二行"
	out, _, err := ApplyEdit(samplePage, Edit{Ops: []Op{{NID: "t1", Hash: target.Hash, Text: &newText}}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := blockText(mustScan(t, out), "t1"); got != newText {
		t.Fatalf("换行没保住: %q", got)
	}
	if !strings.Contains(out, "<br") {
		t.Fatal("换行应写成 <br>")
	}
}

func TestApplyImage(t *testing.T) {
	blocks := mustScan(t, samplePage)
	target := findBlock(t, blocks, "img1")
	img := "image:new-2"
	alt := "新菜单"
	out, _, err := ApplyEdit(samplePage, Edit{Ops: []Op{{NID: "img1", Hash: target.Hash, Image: &img, Alt: &alt}}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	b := findBlock(t, mustScan(t, out), "img1")
	if b.Image != img || b.Alt != alt {
		t.Fatalf("图片没换: %+v", b)
	}
}

func TestApplyRejectsStaleHash(t *testing.T) {
	blocks := mustScan(t, samplePage)
	target := findBlock(t, blocks, "t2")
	text := "x"
	_, _, err := ApplyEdit(samplePage, Edit{Ops: []Op{{NID: "t2", Hash: "deadbeef", Text: &text}}})
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("hash 不符应拒绝，实际 %v（目标 %+v）", err, target)
	}
}

func TestApplyRejectsRichBlock(t *testing.T) {
	blocks := mustScan(t, samplePage)
	target := findBlock(t, blocks, "t3")
	text := "x"
	if _, _, err := ApplyEdit(samplePage, Edit{Ops: []Op{{NID: "t3", Hash: target.Hash, Text: &text}}}); !errors.Is(err, ErrRichBlock) {
		t.Fatalf("rich 块应拒绝，实际 %v", err)
	}
}

func TestApplyRejectsTypeMismatch(t *testing.T) {
	blocks := mustScan(t, samplePage)
	text := "x"
	img := "image:x"
	if _, _, err := ApplyEdit(samplePage, Edit{Ops: []Op{{NID: "img1", Hash: findBlock(t, blocks, "img1").Hash, Text: &text}}}); !errors.Is(err, ErrNotTextBlock) {
		t.Fatalf("给图片写 text 应拒绝，实际 %v", err)
	}
	if _, _, err := ApplyEdit(samplePage, Edit{Ops: []Op{{NID: "t1", Hash: findBlock(t, blocks, "t1").Hash, Image: &img}}}); !errors.Is(err, ErrNotImageBlock) {
		t.Fatalf("给文本块换图应拒绝，实际 %v", err)
	}
}

func TestApplyRejectsDuplicateAndUnknownNID(t *testing.T) {
	blocks := mustScan(t, samplePage)
	h := findBlock(t, blocks, "t1").Hash
	text := "a"
	if _, _, err := ApplyEdit(samplePage, Edit{Ops: []Op{
		{NID: "t1", Hash: h, Text: &text},
		{NID: "t1", Hash: h, Text: &text},
	}}); !errors.Is(err, ErrDuplicateNID) {
		t.Fatalf("重复 nid 应拒绝，实际 %v", err)
	}
	if _, _, err := ApplyEdit(samplePage, Edit{Ops: []Op{{NID: "t99", Hash: h, Text: &text}}}); !errors.Is(err, ErrNoSuchBlock) {
		t.Fatalf("不存在的 nid 应拒绝，实际 %v", err)
	}
}

func TestApplyRejectsInlineImage(t *testing.T) {
	legacy := strings.Replace(samplePage, `src="image:abc-1"`, `src="data:image/png;base64,AAA"`, 1)
	blocks := mustScan(t, legacy)
	b := findBlock(t, blocks, "img1")
	if !b.Legacy {
		t.Fatalf("data: 图应标 legacy: %+v", b)
	}
	img := "image:new"
	if _, _, err := ApplyEdit(legacy, Edit{Ops: []Op{{NID: "img1", Hash: b.Hash, Image: &img}}}); !errors.Is(err, ErrBadImageRef) {
		t.Fatalf("早期 base64 图应拒绝，实际 %v", err)
	}
}

func TestStyleReadAndReplace(t *testing.T) {
	css, hash, err := Style(samplePage)
	if err != nil {
		t.Fatalf("style: %v", err)
	}
	if !strings.Contains(css, "background:#fff") || hash == "" {
		t.Fatalf("样式提取不对: %q / %q", css, hash)
	}

	newCSS := "body{background:#111;color:#eee}"
	out, err := ReplaceStyle(samplePage, newCSS, hash)
	if err != nil {
		t.Fatalf("replace style: %v", err)
	}
	got, _, err := Style(out)
	if err != nil {
		t.Fatalf("style after: %v", err)
	}
	if got != newCSS {
		t.Fatalf("样式没换: %q", got)
	}
	// 正文块不受影响
	if blocks := mustScan(t, out); blockText(blocks, "t1") != "标题" {
		t.Fatal("换样式不该动正文")
	}
}

func TestStyleRejectsStaleHash(t *testing.T) {
	if _, err := ReplaceStyle(samplePage, "body{}", "deadbeef"); !errors.Is(err, ErrStyleHash) {
		t.Fatalf("样式 hash 不符应拒绝，实际 %v", err)
	}
}

func TestStyleMissing(t *testing.T) {
	noStyle := `<html><body><p>x</p></body></html>`
	if _, _, err := Style(noStyle); !errors.Is(err, ErrNoStyle) {
		t.Fatalf("没有 style 应报错，实际 %v", err)
	}
}

func TestApplyTextTooLong(t *testing.T) {
	blocks := mustScan(t, samplePage)
	h := findBlock(t, blocks, "t1").Hash
	long := strings.Repeat("字", MaxTextRunes+1)
	if _, _, err := ApplyEdit(samplePage, Edit{Ops: []Op{{NID: "t1", Hash: h, Text: &long}}}); !errors.Is(err, ErrTextTooLong) {
		t.Fatalf("超长文本应拒绝，实际 %v", err)
	}
}

func TestTextOfKeepsWordSeparator(t *testing.T) {
	page := `<html><body><p>Hello <em>world</em> again</p></body></html>`
	blocks := mustScan(t, page)
	if len(blocks) != 1 {
		t.Fatalf("应只有 1 块: %+v", blocks)
	}
	if blocks[0].Text != "Hello world again" {
		t.Fatalf("内联元素之间不能粘字: %q", blocks[0].Text)
	}
}

func TestApplyRejectsLongAlt(t *testing.T) {
	blocks := mustScan(t, samplePage)
	img := findBlock(t, blocks, "img1")
	alt := strings.Repeat("字", MaxAltRunes+1)
	if _, _, err := ApplyEdit(samplePage, Edit{Ops: []Op{{NID: "img1", Hash: img.Hash, Alt: &alt}}}); !errors.Is(err, ErrAltTooLong) {
		t.Fatalf("超长 alt 应拒绝，实际 %v", err)
	}
}

func TestApplyRejectsEmptyOp(t *testing.T) {
	blocks := mustScan(t, samplePage)
	h := findBlock(t, blocks, "t1").Hash
	if _, _, err := ApplyEdit(samplePage, Edit{Ops: []Op{{NID: "t1", Hash: h}}}); !errors.Is(err, ErrEmptyOp) {
		t.Fatalf("空 op 应报 ErrEmptyOp，实际 %v", err)
	}
}

// pre 里的缩进是内容，归一化会压平，所以 pre 刻意不进可编辑块。
func TestPreIsNotEditable(t *testing.T) {
	page := `<html><body><pre>func main() {
    fmt.Println("hi")
}</pre></body></html>`
	for _, b := range mustScan(t, page) {
		if b.Tag == "pre" {
			t.Fatalf("pre 不该出现: %+v", b)
		}
	}
}

// ---------- insert / remove ----------

func TestInsertAfterAnchor(t *testing.T) {
	blocks := mustScan(t, samplePage)
	h := findBlock(t, blocks, "t1").Hash
	frag := `<figure class="photo"><img src="image:new-9" alt="新图"><figcaption>新图注</figcaption></figure>`
	out, _, err := ApplyEdit(samplePage, Edit{
		Inserts: []InsertOp{{After: "t1", Hash: h, HTML: frag}},
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	after := mustScan(t, out)
	// h1 之后应立刻是新的图片块
	if len(after) < 2 || after[0].NID != "t1" || after[1].Tag != "img" || after[1].Image != "image:new-9" {
		t.Fatalf("插入位置不对: %+v", after)
	}
}

func TestInsertSameAnchorKeepsOrder(t *testing.T) {
	blocks := mustScan(t, samplePage)
	h := findBlock(t, blocks, "t1").Hash
	out, _, err := ApplyEdit(samplePage, Edit{
		Inserts: []InsertOp{
			{After: "t1", Hash: h, HTML: `<p>第一段新内容</p>`},
			{After: "t1", Hash: h, HTML: `<p>第二段新内容</p>`},
		},
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	texts := make([]string, 0, 3)
	for _, b := range mustScan(t, out) {
		if b.Tag == "p" && strings.Contains(b.Text, "新内容") {
			texts = append(texts, b.Text)
		}
	}
	if len(texts) != 2 || texts[0] != "第一段新内容" || texts[1] != "第二段新内容" {
		t.Fatalf("同锚点顺序应与数组一致，实际 %v", texts)
	}
}

func TestInsertRejectsBadFragment(t *testing.T) {
	blocks := mustScan(t, samplePage)
	h := findBlock(t, blocks, "t1").Hash
	for _, frag := range []string{
		`<script>alert(1)</script>`,
		`<style>body{}</style>`,
		`<iframe src="x"></iframe>`,
		`散文本`,
		`   `,
	} {
		if _, _, err := ApplyEdit(samplePage, Edit{
			Inserts: []InsertOp{{After: "t1", Hash: h, HTML: frag}},
		}); !errors.Is(err, ErrBadFragment) {
			t.Fatalf("片段 %q 应拒绝，实际 %v", frag, err)
		}
	}
}

func TestInsertRejectsStaleAnchor(t *testing.T) {
	if _, _, err := ApplyEdit(samplePage, Edit{
		Inserts: []InsertOp{{After: "t1", Hash: "deadbeef", HTML: `<p>x</p>`}},
	}); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("锚点 hash 不符应拒绝，实际 %v", err)
	}
	if _, _, err := ApplyEdit(samplePage, Edit{
		Inserts: []InsertOp{{After: "t99", Hash: "x", HTML: `<p>x</p>`}},
	}); !errors.Is(err, ErrNoSuchBlock) {
		t.Fatalf("锚点不存在应拒绝，实际 %v", err)
	}
}

func TestRemoveTextBlock(t *testing.T) {
	blocks := mustScan(t, samplePage)
	out, changed, err := ApplyEdit(samplePage, Edit{
		Removes: []RemoveOp{{NID: "t2", Hash: findBlock(t, blocks, "t2").Hash}},
	})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(changed) != 1 || changed[0] != "t2" {
		t.Fatalf("changed: %v", changed)
	}
	if strings.Contains(out, "第一段") {
		t.Fatal("被删的段落还在")
	}
	if _, ok := findBlockOK(mustScan(t, out), "t2"); ok {
		// 编号会顺移，只要没有原文即可
		t.Log("块编号重排是预期的（nid 现场生成）")
	}
}

// 删 figure 里的图：连 figure 与图注一起删，不留空相框。
func TestRemoveImageRemovesFigure(t *testing.T) {
	blocks := mustScan(t, samplePage)
	out, _, err := ApplyEdit(samplePage, Edit{
		Removes: []RemoveOp{{NID: "img1", Hash: findBlock(t, blocks, "img1").Hash}},
	})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if strings.Contains(out, "<figure") || strings.Contains(out, "图注") {
		t.Fatalf("figure 与图注应一起删: %s", out)
	}
}

func TestRemoveImageWithoutFigure(t *testing.T) {
	page := `<html><body><p>正文够长够长够长</p><img src="image:a" alt="裸图"></body></html>`
	blocks := mustScan(t, page)
	out, _, err := ApplyEdit(page, Edit{
		Removes: []RemoveOp{{NID: "img1", Hash: findBlock(t, blocks, "img1").Hash}},
	})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if strings.Contains(out, "<img") {
		t.Fatal("裸图应被删")
	}
	if !strings.Contains(out, "正文够长够长够长") {
		t.Fatal("别的内容不该动")
	}
}

func TestRemoveRejectsStaleHash(t *testing.T) {
	if _, _, err := ApplyEdit(samplePage, Edit{
		Removes: []RemoveOp{{NID: "t1", Hash: "deadbeef"}},
	}); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("hash 不符应拒绝，实际 %v", err)
	}
}

func TestEditConflicts(t *testing.T) {
	blocks := mustScan(t, samplePage)
	h := findBlock(t, blocks, "t1").Hash
	text := "x"
	if _, _, err := ApplyEdit(samplePage, Edit{
		Ops:     []Op{{NID: "t1", Hash: h, Text: &text}},
		Removes: []RemoveOp{{NID: "t1", Hash: h}},
	}); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("同一块既改又删应拒绝，实际 %v", err)
	}
	if _, _, err := ApplyEdit(samplePage, Edit{
		Inserts: []InsertOp{{After: "t1", Hash: h, HTML: `<p>x</p>`}},
		Removes: []RemoveOp{{NID: "t1", Hash: h}},
	}); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("插入锚点被删应拒绝，实际 %v", err)
	}
}

func TestApplyEditEmpty(t *testing.T) {
	if _, _, err := ApplyEdit(samplePage, Edit{}); err == nil {
		t.Fatal("空编辑应拒绝")
	}
}

// findBlockOK — 找块（不 fatal），删块测试用。
func findBlockOK(blocks []Block, nid string) (Block, bool) {
	for _, b := range blocks {
		if b.NID == nid {
			return b, true
		}
	}
	return Block{}, false
}

// ---------- 删块后的空容器收缩 ----------

// 同一个 figure 里两张图：删一张不能把另一张带走（复审 P1）。
func TestRemoveImageKeepsSiblingImage(t *testing.T) {
	page := `<html><body><p>正文够长够长够长</p>` +
		`<figure><img src="image:a" alt="A"><img src="image:b" alt="B"><figcaption>两张</figcaption></figure></body></html>`
	blocks := mustScan(t, page)
	out, _, err := ApplyEdit(page, Edit{Removes: []RemoveOp{{NID: "img1", Hash: findBlock(t, blocks, "img1").Hash}}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if strings.Contains(out, "image:a") {
		t.Fatalf("被删的图还在: %s", out)
	}
	if !strings.Contains(out, "image:b") || !strings.Contains(out, "figcaption") {
		t.Fatalf("另一张图和图注都该留着: %s", out)
	}
}

// 同一张图包在 div.grid 里也一样。
func TestRemoveImageKeepsSiblingInNestedGrid(t *testing.T) {
	page := `<html><body><p>正文够长够长够长</p>` +
		`<figure><div class="grid"><img src="image:a"><img src="image:b"></div><figcaption>两张</figcaption></figure></body></html>`
	blocks := mustScan(t, page)
	out, _, err := ApplyEdit(page, Edit{Removes: []RemoveOp{{NID: "img1", Hash: findBlock(t, blocks, "img1").Hash}}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !strings.Contains(out, "image:b") {
		t.Fatalf("嵌套里的另一张图不该被删: %s", out)
	}
}

// div 相框 + 图注：删图后整块收掉，不留空相框（真机反馈）。
func TestRemoveImageDropsDivFrame(t *testing.T) {
	page := `<html><body><p>正文够长够长够长</p>` +
		`<div class="frame"><img src="image:a" alt="A"><figcaption>说明</figcaption></div></body></html>`
	blocks := mustScan(t, page)
	out, _, err := ApplyEdit(page, Edit{Removes: []RemoveOp{{NID: "img1", Hash: findBlock(t, blocks, "img1").Hash}}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if strings.Contains(out, "class=\"frame\"") || strings.Contains(out, "说明") {
		t.Fatalf("空相框和图注都该收掉: %s", out)
	}
	if !strings.Contains(out, "正文够长够长够长") {
		t.Fatal("别的内容不该动")
	}
}

// 删掉一个段落，但兄弟段落还有文字：容器不能动。
func TestRemoveBlockKeepsNonEmptyAncestor(t *testing.T) {
	page := `<html><body><section><p>第一段文字</p><p>第二段文字</p></section></body></html>`
	blocks := mustScan(t, page)
	out, _, err := ApplyEdit(page, Edit{Removes: []RemoveOp{{NID: "t1", Hash: findBlock(t, blocks, "t1").Hash}}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !strings.Contains(out, "<section>") || !strings.Contains(out, "第二段文字") {
		t.Fatalf("还有内容的容器不该删: %s", out)
	}
}

// 删掉最后一条列表项：空 <ul> 一起走。
func TestRemoveLastListItemDropsList(t *testing.T) {
	page := `<html><body><p>正文够长够长够长</p><ul><li>唯一一条</li></ul></body></html>`
	blocks := mustScan(t, page)
	out, _, err := ApplyEdit(page, Edit{Removes: []RemoveOp{{NID: "t2", Hash: findBlock(t, blocks, "t2").Hash}}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if strings.Contains(out, "<ul") {
		t.Fatalf("空列表该一起删: %s", out)
	}
}

// 单元格全删光：<table> 骨架不留（真机反馈）。
func TestRemoveAllCellsDropsTableSkeleton(t *testing.T) {
	page := `<html><body><p>正文够长够长够长</p>` +
		`<table><thead><tr><th>项目</th></tr></thead><tbody><tr><td>甲</td><td>乙</td></tr></tbody></table></body></html>`
	blocks := mustScan(t, page)
	removes := make([]RemoveOp, 0, 3)
	for _, b := range blocks {
		if b.Tag == "th" || b.Tag == "td" {
			removes = append(removes, RemoveOp{NID: b.NID, Hash: b.Hash})
		}
	}
	if len(removes) != 3 {
		t.Fatalf("应有 3 个单元格: %+v", blocks)
	}
	out, _, err := ApplyEdit(page, Edit{Removes: removes})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if strings.Contains(out, "<table") || strings.Contains(out, "<tr") {
		t.Fatalf("空表格骨架该一起删: %s", out)
	}
	if !strings.Contains(out, "正文够长够长够长") {
		t.Fatal("别的内容不该动")
	}
}

// ---------- 插入片段的表格族 ----------

func TestInsertRejectsTableParts(t *testing.T) {
	blocks := mustScan(t, samplePage)
	h := findBlock(t, blocks, "t5").Hash
	for _, frag := range []string{`<tr><td>y</td></tr>`, `<td>y</td>`, `<tbody><tr><td>y</td></tr></tbody>`} {
		_, _, err := ApplyEdit(samplePage, Edit{
			Inserts: []InsertOp{{After: "t5", Hash: h, HTML: frag}},
		})
		if !errors.Is(err, ErrBadFragment) || !strings.Contains(err.Error(), "表格行列") {
			t.Fatalf("片段 %q 应明确拒绝表格行列，实际 %v", frag, err)
		}
	}
}

func TestInsertWholeTable(t *testing.T) {
	blocks := mustScan(t, samplePage)
	h := findBlock(t, blocks, "t5").Hash
	out, _, err := ApplyEdit(samplePage, Edit{
		Inserts: []InsertOp{{After: "t5", Hash: h, HTML: `<table><tr><td>新格</td></tr></table>`}},
	})
	if err != nil {
		t.Fatalf("整张 table 应能插: %v", err)
	}
	if strings.Count(out, "<table") != 2 {
		t.Fatalf("应多出一张表: %s", out)
	}
	found := false
	for _, b := range mustScan(t, out) {
		if b.Tag == "td" && b.Text == "新格" {
			found = true
		}
	}
	if !found {
		t.Fatal("插入的单元格里应能扫出块，后续才能 patch")
	}
}

// 删掉唯一一块也不能把 <body> 摘掉（复审 8.2）。
func TestRemoveLastBlockKeepsBody(t *testing.T) {
	page := `<html><head><style>body{margin:0}</style></head><body><p>唯一正文</p></body></html>`
	blocks := mustScan(t, page)
	out, _, err := ApplyEdit(page, Edit{Removes: []RemoveOp{{NID: "t1", Hash: findBlock(t, blocks, "t1").Hash}}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !strings.Contains(out, "<body") {
		t.Fatalf("body 不能被摘掉: %s", out)
	}
}

// 单元格里唯一的段落被删 → 这一格跟着消失（已知行为，先钉住）。
func TestRemoveUniqueParagraphDropsCell(t *testing.T) {
	page := `<html><body><p>正文够长够长够长</p><table><tr><td><p>甲</p></td><td>乙</td></tr></table></body></html>`
	blocks := mustScan(t, page)
	var p Block
	for _, b := range blocks {
		if b.Tag == "p" && b.Text == "甲" {
			p = b
		}
	}
	if p.NID == "" {
		t.Fatalf("没找到「甲」: %+v", blocks)
	}
	out, _, err := ApplyEdit(page, Edit{Removes: []RemoveOp{{NID: p.NID, Hash: p.Hash}}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if strings.Contains(out, "甲") || strings.Contains(out, "<td></td>") {
		t.Fatalf("空单元格应被收掉（不是留个空壳）: %s", out)
	}
	if !strings.Contains(out, "乙") {
		t.Fatal("另一格不该受影响")
	}
}

// div 拼的卡片网格同理：删掉卡里唯一的内容，整张卡消失。
func TestRemoveUniqueParagraphDropsCard(t *testing.T) {
	page := `<html><body><p>正文够长够长够长</p><div class="row"><div class="cell"><p>甲</p></div><div class="cell">乙</div></div></body></html>`
	blocks := mustScan(t, page)
	out, _, err := ApplyEdit(page, Edit{Removes: []RemoveOp{{NID: "t2", Hash: findBlock(t, blocks, "t2").Hash}}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if strings.Contains(out, ">甲<") || strings.Contains(out, "class=\"cell\"></div><div") {
		t.Fatalf("空卡片应被收掉: %s", out)
	}
	if !strings.Contains(out, "乙") {
		t.Fatal("另一张卡不该受影响")
	}
}

// 真机结构：内层 .frame 收掉后，外层 .fig（胶带 + div 写的图注）也要一起走（复审 §九）。
func TestRemoveImageDropsFigFrameWithDivCaption(t *testing.T) {
	page := `<html><body><p>正文够长够长够长</p>` +
		`<div class="fig"><div class="tape"></div><div class="frame"><img src="image:a" alt="照片"></div>` +
		`<div class="cap">这张图的文件名是 a</div></div></body></html>`
	blocks := mustScan(t, page)
	out, _, err := ApplyEdit(page, Edit{Removes: []RemoveOp{{NID: "img1", Hash: findBlock(t, blocks, "img1").Hash}}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if strings.Contains(out, "class=\"fig\"") || strings.Contains(out, "这张图的文件名") {
		t.Fatalf("外层相框与悬空图注都该收掉: %s", out)
	}
	if !strings.Contains(out, "正文够长够长够长") {
		t.Fatal("别的内容不该动")
	}
}

// 放宽后的安全边界：卡片里还有可寻址的标题/正文 → 卡片留着。
func TestRemoveImageKeepsCardWithTextBlocks(t *testing.T) {
	page := `<html><body><div class="card"><img src="image:a"><h3>标题</h3><p>正文文字</p></div></body></html>`
	blocks := mustScan(t, page)
	out, _, err := ApplyEdit(page, Edit{Removes: []RemoveOp{{NID: "img1", Hash: findBlock(t, blocks, "img1").Hash}}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !strings.Contains(out, "class=\"card\"") || !strings.Contains(out, "标题") || !strings.Contains(out, "正文文字") {
		t.Fatalf("有可寻址文本的卡片不该被收: %s", out)
	}
}

// 图注写成 <p> 时：p 有 nid，模型能自己删，所以相框先留着。
func TestRemoveImageKeepsFrameWhenCaptionIsParagraph(t *testing.T) {
	page := `<html><body><div class="fig"><img src="image:a"><p class="cap">说明</p></div></body></html>`
	blocks := mustScan(t, page)
	out, _, err := ApplyEdit(page, Edit{Removes: []RemoveOp{{NID: "img1", Hash: findBlock(t, blocks, "img1").Hash}}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !strings.Contains(out, "class=\"fig\"") || !strings.Contains(out, "说明") {
		t.Fatalf("图注是 <p> 时相框保留、由模型自己删: %s", out)
	}
}

// 嵌套在 div 里的 tr 也必须被拦（复审 8.4.1）。
func TestInsertRejectsNestedTablePart(t *testing.T) {
	blocks := mustScan(t, samplePage)
	h := findBlock(t, blocks, "t5").Hash
	_, _, err := ApplyEdit(samplePage, Edit{
		Inserts: []InsertOp{{After: "t5", Hash: h, HTML: `<div class="row"><tr><td>格</td></tr></div>`}},
	})
	if !errors.Is(err, ErrBadFragment) || !strings.Contains(err.Error(), "表格行列") {
		t.Fatalf("div 里套 tr 也应拒绝，实际 %v", err)
	}
}

// 图片嵌在段落里：删图不能把整段文字带走（段落是有 nid 的块，模型自己能处理）。
func TestRemoveInlineImageKeepsSurroundingText(t *testing.T) {
	page := `<html><body><section><p>看看这张图 <img src="image:a" alt="图"></p></section></body></html>`
	blocks := mustScan(t, page)
	out, _, err := ApplyEdit(page, Edit{Removes: []RemoveOp{{NID: "img1", Hash: findBlock(t, blocks, "img1").Hash}}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !strings.Contains(out, "看看这张图") {
		t.Fatalf("段落文字不该被一起删: %s", out)
	}
}

// 块要带 class：模型插新块时照抄它，间距/配色才不会丢。
func TestScanCarriesClass(t *testing.T) {
	page := `<html><body><p class="sec lead">甲</p><img class="photo" src="image:a"></body></html>`
	blocks := mustScan(t, page)
	if got := findBlock(t, blocks, "t1").Class; got != "sec lead" {
		t.Fatalf("文本块 class: %q", got)
	}
	if got := findBlock(t, blocks, "img1").Class; got != "photo" {
		t.Fatalf("图片块 class: %q", got)
	}
}

// 删块不能留下空白行（源码里那行只是缩进，但会攒起来、也让 get(full) 难读）。
func TestRemoveBlockLeavesNoBlankLine(t *testing.T) {
	page := "<html><body>\n<section>\n  <p>甲</p>\n  <p>乙</p>\n</section>\n</body></html>"
	blocks := mustScan(t, page)
	out, _, err := ApplyEdit(page, Edit{Removes: []RemoveOp{{NID: "t1", Hash: findBlock(t, blocks, "t1").Hash}}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	for i, line := range strings.Split(out, "\n") {
		if line != "" && strings.TrimSpace(line) == "" {
			t.Fatalf("第 %d 行是空白行: %q\n%s", i+1, line, out)
		}
	}
	if !strings.Contains(out, "乙") || strings.Contains(out, "甲") {
		t.Fatalf("删错了: %s", out)
	}
}

// 收缩整块容器时同理（删图连相框一起走，不留空行）。
func TestPruneLeavesNoBlankLine(t *testing.T) {
	page := "<html><body>\n<section>\n  <p>正文够长够长够长</p>\n  <figure>\n    <img src=\"image:a\">\n  </figure>\n  <p>尾巴</p>\n</section>\n</body></html>"
	blocks := mustScan(t, page)
	out, _, err := ApplyEdit(page, Edit{Removes: []RemoveOp{{NID: "img1", Hash: findBlock(t, blocks, "img1").Hash}}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	for i, line := range strings.Split(out, "\n") {
		if line != "" && strings.TrimSpace(line) == "" {
			t.Fatalf("第 %d 行是空白行: %q\n%s", i+1, line, out)
		}
	}
	if strings.Contains(out, "<figure") {
		t.Fatalf("空相框该收掉: %s", out)
	}
}
