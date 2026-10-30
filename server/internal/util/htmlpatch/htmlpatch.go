// Package htmlpatch — HTML 原文的细粒度编辑：按块（文本 / 图片）与 <style> 精确定位替换。
//
// 页面仍然是模型自由写的 HTML（见 docs/design/html-artifact/fine-grained-edit.md），
// 这里只负责「找到那一小块、换成新的」——不引入内容模型、不拆模板。
//
// 编号（nid）每次现场生成、不落库；每个块带 hash，调用方必须回传，
// 用来发现「页面已被另一处改过」，避免把新文字写到错位的段落上。
package htmlpatch

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	// MaxTextRunes — 单块文本上限，挡住「整页塞进一个段落」这类误用。
	MaxTextRunes = 8000
	// MaxAltRunes — 图片说明上限（与 title/summary 同一档，别把整段正文塞进 alt）。
	MaxAltRunes = 200
	// MaxInsertOps / MaxRemoveOps — 单次结构修改上限，挡住「把整页搬一遍」。
	MaxInsertOps = 10
	MaxRemoveOps = 20
	// MaxFragmentBytes — 单段插入 HTML 的上限（正常一个卡片/段落几百字节）。
	MaxFragmentBytes = 64 << 10
)

// 可编辑的文本块：只收叶子块级元素（内部允许内联格式）。
var textTags = map[string]bool{
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"p": true, "li": true, "td": true, "th": true, "figcaption": true,
	"blockquote": true, "dt": true, "dd": true, "caption": true,
	// 刻意不收 `pre`：里面的缩进是内容的一部分，归一化文本会压平，patch 回去就坏了
}

// 视为「块内格式」的内联标签：出现即标 rich，文本 patch 会被拒绝（避免静默压平）。
var inlineTags = map[string]bool{
	"span": true, "a": true, "b": true, "i": true, "em": true, "strong": true,
	"code": true, "small": true, "mark": true, "sup": true, "sub": true, "u": true,
	"s": true, "del": true, "ins": true, "kbd": true, "abbr": true,
}

var (
	ErrNoSuchBlock   = errors.New("没有这个块")
	ErrDuplicateNID  = errors.New("同一次请求里重复的块")
	ErrHashMismatch  = errors.New("块 hash 不匹配")
	ErrRichBlock     = errors.New("块里含高亮/链接等内联格式")
	ErrNotTextBlock  = errors.New("这是图片块，不能写文字")
	ErrNotImageBlock = errors.New("这是文字块，不能换图")
	ErrBadImageRef   = errors.New("图片引用不合法")
	ErrTextTooLong   = errors.New("文字太长")
	ErrAltTooLong    = errors.New("图片说明太长")
	ErrMixedOp       = errors.New("同一项不能同时改文字和图片")
	ErrEmptyOp       = errors.New("这一项既没有 text，也没有 image/alt")
	ErrNoStyle       = errors.New("页面里没有 <style>")
	ErrStyleHash     = errors.New("样式 hash 不匹配")
	ErrBadFragment   = errors.New("插入内容不合法")
	ErrEditConflict  = errors.New("同一块不能同时修改和删除")
)

// Block — 一个可定位的块（给 get outline 用）。
type Block struct {
	NID       string `json:"nid"`
	Tag       string `json:"tag"`
	Class     string `json:"class,omitempty"` // 原样的 class 属性：模型插新块时照抄，间距才不会丢
	Text      string `json:"text"`            // 文本块：归一化后的全文
	Image     string `json:"image,omitempty"` // 图片块：image:<id> 或 data:（早期页面）
	Alt       string `json:"alt,omitempty"`
	Hash      string `json:"hash"`
	Rich      bool   `json:"rich,omitempty"`
	Legacy    bool   `json:"legacy,omitempty"` // 图片已内联成 data:，不能 patch
	CharCount int    `json:"chars,omitempty"`
}

// Op — 一次替换。Text / Image 二选一；Alt 可单独改。
type Op struct {
	NID   string
	Hash  string
	Text  *string
	Image *string
	Alt   *string
}

type scanned struct {
	Block
	node *html.Node
}

// Scan 按文档顺序返回所有可编辑块（文本块 + 图片）。
func Scan(source string) ([]Block, error) {
	_, blocks, err := parse(source)
	if err != nil {
		return nil, err
	}
	out := make([]Block, len(blocks))
	for i := range blocks {
		out[i] = blocks[i].Block
	}
	return out, nil
}

// Op、InsertOp、RemoveOp 组成一次编辑。三者都基于**同一份原始文档**的 nid：
// 同一块不能既改又删，插入锚点也不能是被删的块。
type InsertOp struct {
	After string // 插到哪个块之后
	Hash  string // 锚点的版本凭证
	HTML  string // 块级 HTML 片段（可含 image:<id>）
}

type RemoveOp struct {
	NID  string
	Hash string
}

type Edit struct {
	Ops     []Op
	Inserts []InsertOp
	Removes []RemoveOp
}

// ApplyEdit 应用一批编辑：替换内容 + 插入片段 + 删除块。
//
// 全部校验通过才动节点：任一 nid/hash 不符、片段不合法、同一块既改又删，
// 都直接返回错误，不做部分写入。
func ApplyEdit(source string, edit Edit) (string, []string, error) {
	if len(edit.Ops) == 0 && len(edit.Inserts) == 0 && len(edit.Removes) == 0 {
		return "", nil, errors.New("没有任何修改")
	}
	doc, blocks, err := parse(source)
	if err != nil {
		return "", nil, err
	}
	byNID := make(map[string]*scanned, len(blocks))
	for i := range blocks {
		byNID[blocks[i].NID] = &blocks[i]
	}

	// ---- 校验（全部通过前不动节点） ----
	removed := make(map[string]bool, len(edit.Removes))
	for _, r := range edit.Removes {
		b, ok := byNID[r.NID]
		if !ok {
			return "", nil, fmt.Errorf("%w: %s", ErrNoSuchBlock, r.NID)
		}
		if removed[r.NID] {
			return "", nil, fmt.Errorf("%w: %s", ErrDuplicateNID, r.NID)
		}
		if r.Hash != b.Hash {
			return "", nil, fmt.Errorf("%w: %s", ErrHashMismatch, r.NID)
		}
		removed[r.NID] = true
	}

	seen := make(map[string]bool, len(edit.Ops))
	for _, op := range edit.Ops {
		b, ok := byNID[op.NID]
		if !ok {
			return "", nil, fmt.Errorf("%w: %s", ErrNoSuchBlock, op.NID)
		}
		if seen[op.NID] {
			return "", nil, fmt.Errorf("%w: %s", ErrDuplicateNID, op.NID)
		}
		seen[op.NID] = true
		if removed[op.NID] {
			return "", nil, fmt.Errorf("%w: %s", ErrEditConflict, op.NID)
		}
		if err := validateOp(b, op); err != nil {
			return "", nil, err
		}
	}

	type insertPlan struct {
		after string
		nodes []*html.Node
	}
	plans := make([]insertPlan, 0, len(edit.Inserts))
	for _, ins := range edit.Inserts {
		a, ok := byNID[ins.After]
		if !ok {
			return "", nil, fmt.Errorf("%w: %s（插入锚点）", ErrNoSuchBlock, ins.After)
		}
		if removed[ins.After] {
			return "", nil, fmt.Errorf("%w: %s（插入锚点被删了）", ErrEditConflict, ins.After)
		}
		if ins.Hash != a.Hash {
			return "", nil, fmt.Errorf("%w: %s（插入锚点）", ErrHashMismatch, ins.After)
		}
		nodes, err := parseFragment(ins.HTML)
		if err != nil {
			return "", nil, fmt.Errorf("%s: %w", ins.After, err)
		}
		plans = append(plans, insertPlan{after: ins.After, nodes: nodes})
	}

	// ---- 应用 ----
	changed := make([]string, 0, len(edit.Ops)+len(edit.Removes))
	for _, op := range edit.Ops {
		applyOp(byNID[op.NID], op)
		changed = append(changed, op.NID)
	}
	// 同锚点多次插入：按数组顺序接在上一条后面（不能都插在锚点后，否则后写在前）
	tails := make(map[string]*html.Node, len(plans))
	for _, plan := range plans {
		insertAfter := byNID[plan.after].node
		if t, ok := tails[plan.after]; ok {
			insertAfter = t
		}
		parent := insertAfter.Parent
		next := insertAfter.NextSibling
		for _, n := range plan.nodes {
			parent.InsertBefore(n, next)
		}
		tails[plan.after] = plan.nodes[len(plan.nodes)-1]
	}
	for _, r := range edit.Removes {
		removeBlock(byNID[r.NID])
		changed = append(changed, r.NID)
	}

	out, err := render(doc)
	if err != nil {
		return "", nil, err
	}
	return out, changed, nil
}

// validateOp — 单条替换的全部前置校验（不碰节点）。
func validateOp(b *scanned, op Op) error {
	if op.Text == nil && op.Image == nil && op.Alt == nil {
		return fmt.Errorf("%w: %s", ErrEmptyOp, op.NID)
	}
	if op.Text != nil && op.Image != nil {
		return fmt.Errorf("%w: %s", ErrMixedOp, op.NID)
	}
	if op.Hash != b.Hash {
		return fmt.Errorf("%w: %s", ErrHashMismatch, op.NID)
	}
	if op.Text != nil {
		if b.Tag == "img" {
			return fmt.Errorf("%w: %s", ErrNotTextBlock, op.NID)
		}
		if b.Rich {
			return fmt.Errorf("%w: %s", ErrRichBlock, op.NID)
		}
		if utf8.RuneCountInString(*op.Text) > MaxTextRunes {
			return fmt.Errorf("%w: %s 超过 %d 字", ErrTextTooLong, op.NID, MaxTextRunes)
		}
	}
	if op.Image != nil {
		if b.Tag != "img" {
			return fmt.Errorf("%w: %s", ErrNotImageBlock, op.NID)
		}
		if !strings.HasPrefix(*op.Image, "image:") || len(*op.Image) <= len("image:") {
			return fmt.Errorf("%w: %s 必须是 image:<id> 引用", ErrBadImageRef, op.NID)
		}
		if b.Legacy {
			return fmt.Errorf("%w: %s 这张图是 base64 直写，不支持 patch；请改成 image:<id> 引用或整页重写", ErrBadImageRef, op.NID)
		}
	}
	if op.Alt != nil {
		if b.Tag != "img" {
			return fmt.Errorf("%w: %s", ErrNotImageBlock, op.NID)
		}
		if utf8.RuneCountInString(*op.Alt) > MaxAltRunes {
			return fmt.Errorf("%w: %s 超过 %d 字", ErrAltTooLong, op.NID, MaxAltRunes)
		}
	}
	return nil
}

func applyOp(b *scanned, op Op) {
	switch {
	case op.Text != nil:
		setText(b.node, *op.Text)
	case op.Image != nil:
		setAttr(b.node, "src", *op.Image)
	}
	if op.Alt != nil {
		setAttr(b.node, "alt", *op.Alt)
	}
}

// parseFragment — 解析插入片段，只收块级元素；script/style/iframe 之类直接拒绝。
func parseFragment(frag string) ([]*html.Node, error) {
	if strings.TrimSpace(frag) == "" {
		return nil, fmt.Errorf("%w: 片段为空", ErrBadFragment)
	}
	if len(frag) > MaxFragmentBytes {
		return nil, fmt.Errorf("%w: 超过 %d 字节", ErrBadFragment, MaxFragmentBytes)
	}
	if err := rejectTableParts(frag); err != nil {
		return nil, err
	}
	nodes, err := html.ParseFragment(strings.NewReader(frag), &html.Node{
		Type: html.ElementNode, Data: "div", DataAtom: atom.Div,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadFragment, err)
	}
	out := make([]*html.Node, 0, len(nodes))
	for _, n := range nodes {
		switch n.Type {
		case html.TextNode:
			if strings.TrimSpace(n.Data) == "" {
				continue
			}
			return nil, fmt.Errorf("%w: 片段顶层只能是块级元素，不能是散文本", ErrBadFragment)
		case html.ElementNode:
			if !insertTags[n.Data] {
				return nil, fmt.Errorf("%w: 不允许插入 <%s>", ErrBadFragment, n.Data)
			}
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: 片段为空", ErrBadFragment)
	}
	return out, nil
}

// insertTags — 片段顶层允许的标签（sanitize 还会再洗一遍）。
// 表格族只收整张 `table`：`tr/td` 之类在 <div> 上下文里会被 HTML5 直接丢掉。
var insertTags = map[string]bool{
	"div": true, "section": true, "article": true, "figure": true, "img": true,
	"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"ul": true, "ol": true, "li": true, "table": true,
	"blockquote": true, "hr": true, "pre": true,
	"figcaption": true, "span": true, "a": true,
}

// tablePartTags — 只能活在 <table> 里的标签。以 <div> 为上下文解析时它们会被静默丢弃，
// 报出来变成「片段顶层不能是散文本」，指错方向；所以按首标签提前单独拒绝。
var tablePartTags = map[string]bool{
	"tr": true, "td": true, "th": true, "thead": true, "tbody": true,
	"tfoot": true, "col": true, "colgroup": true, "caption": true,
}

func rejectTableParts(frag string) error {
	z := html.NewTokenizer(strings.NewReader(frag))
	inTable := 0
	for {
		switch tt := z.Next(); tt {
		case html.ErrorToken:
			return nil
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := string(name)
			switch {
			case tag == "table":
				if tt == html.StartTagToken {
					inTable++
				}
			case tablePartTags[tag] && inTable == 0:
				// 不在 <table> 里：解析器会把这几个标签静默丢掉，提前报清楚
				return fmt.Errorf("%w: 表格行列不能单独插入（<%s>），请整张 <table> 一起插，或整页重写", ErrBadFragment, tag)
			}
		case html.EndTagToken:
			if name, _ := z.TagName(); string(name) == "table" && inTable > 0 {
				inTable--
			}
		}
	}
}

// removeBlock — 删掉这一块，并把因此变空的祖先容器一并收掉：
// 删照片不该留空相框 + 孤立图注，删光单元格也不该留 <table> 骨架。
func removeBlock(b *scanned) {
	parent := b.node.Parent
	dropAdjacentSpace(b.node)
	removeNode(b.node)
	if parent != nil {
		pruneEmptyAncestors(parent, b.Tag == "img")
	}
}

// pruneEmptyAncestors — 从 n 起向上删掉「已经没内容」的容器（含 n 本身），停在 <body>。
func pruneEmptyAncestors(n *html.Node, mediaRemoved bool) {
	if n == nil || n.Type != html.ElementNode || n.Data == "body" || n.Data == "html" {
		return
	}
	if !residue(n, mediaRemoved) {
		return
	}
	top := n
	for p := n.Parent; p != nil && p.Type == html.ElementNode && p.Data != "body" && p.Data != "html" && residue(p, mediaRemoved); p = p.Parent {
		top = p
	}
	if top.Parent != nil {
		dropAdjacentSpace(top)
		top.Parent.RemoveChild(top)
	}
}

// dropAdjacentSpace — 删节点前清掉多余的空行：前后都是纯空白文本节点（或者后面没有兄弟）时，
// 丢掉前一个。源码里不留 `\n\n`，`get(full)` 读回来还是原来的缩进。
func dropAdjacentSpace(n *html.Node) {
	prev, next := n.PrevSibling, n.NextSibling
	if prev == nil || prev.Type != html.TextNode || strings.TrimSpace(prev.Data) != "" {
		return
	}
	if next == nil || (next.Type == html.TextNode && strings.TrimSpace(next.Data) == "") {
		n.Parent.RemoveChild(prev)
	}
}

// mediaTags — 有它就算「还有内容」，空容器不能删（删了画面会少东西）。
var mediaTags = map[string]bool{
	"img": true, "picture": true, "svg": true, "video": true, "audio": true,
	"canvas": true, "iframe": true, "embed": true, "object": true, "hr": true,
}

// content 是子树里「算不算内容」的三个信号。
type content struct {
	media       bool // 有画面（图片/视频/装饰线）
	text        bool // 有文字（图注/说明除外）
	addressable bool // 有 nid 的文本块（模型能自己 remove 的那种）
}

func contentOf(n *html.Node) content {
	var c content
	// 容器本身就是可寻址的文本块（`<p>文字 <img></p>`）：里面的文字模型自己能改能删，
	// 删图不该顺手把这一整段带走。
	if textTags[n.Data] && n.Data != "figcaption" && n.Data != "caption" {
		c.addressable = true
	}
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			switch ch.Type {
			case html.TextNode:
				if strings.TrimSpace(ch.Data) != "" {
					c.text = true
				}
			case html.ElementNode:
				switch {
				case mediaTags[ch.Data]:
					c.media = true
				case ch.Data == "figcaption" || ch.Data == "caption":
					// 图注/表说明跟着图走，本身不算内容
				default:
					if textTags[ch.Data] {
						c.addressable = true
					}
					walk(ch)
				}
			}
		}
	}
	walk(n)
	return c
}

// residue — 这个容器已经只剩「删完的残渣」。
//
// 完全空（没画面、没文字）当然是残渣。删的是图片时再放宽一档：没有画面、也没有
// 模型能寻址的文本块，也算残渣——`.cap` 这种 div 写的图注没有 nid，模型删不掉，
// 留着就是一条悬空图注 + 一段空白。反过来，卡片里还有 h3/p（有 nid）就不算残渣，
// 模型想删自己会删（判据自洽：收缩只清理「模型没法寻址」的东西）。
func residue(n *html.Node, mediaRemoved bool) bool {
	c := contentOf(n)
	if !c.media && !c.text {
		return true
	}
	return mediaRemoved && !c.media && !c.addressable
}

func removeNode(n *html.Node) {
	if n.Parent != nil {
		n.Parent.RemoveChild(n)
	}
}

// Style 返回页面第一段 <style> 的 CSS 与它的 hash。
// 多段 <style> 只认文档序第一段（模型正常只写一段；见设计文档 §五）。
func Style(source string) (css string, hash string, err error) {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", "", fmt.Errorf("parse html: %w", err)
	}
	n := findStyleNode(doc)
	if n == nil {
		return "", "", ErrNoStyle
	}
	css = rawText(n)
	return css, hashOf(css), nil
}

// ReplaceStyle 用新 CSS 替换页面第一段 <style>（必须已存在）。
// expectHash 非空时校验，防止「拿到的是旧样式」。
func ReplaceStyle(source, css, expectHash string) (string, error) {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}
	n := findStyleNode(doc)
	if n == nil {
		return "", ErrNoStyle
	}
	if expectHash != "" && expectHash != hashOf(rawText(n)) {
		return "", ErrStyleHash
	}
	setRawText(n, css)
	return render(doc)
}

// ---------- 解析与遍历 ----------

func parse(source string) (*html.Node, []scanned, error) {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return nil, nil, fmt.Errorf("parse html: %w", err)
	}
	var blocks []scanned
	var tCount, imgCount int
	var walk func(*html.Node)
	walk = func(parent *html.Node) {
		for c := parent.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.Data {
			case "head", "script", "style", "title":
				continue
			case "img":
				imgCount++
				src := attr(c, "src")
				alt := attr(c, "alt")
				blocks = append(blocks, scanned{
					Block: Block{
						NID:    fmt.Sprintf("img%d", imgCount),
						Tag:    "img",
						Class:  attr(c, "class"),
						Image:  src,
						Alt:    alt,
						Hash:   hashOf("img", src, alt),
						Legacy: strings.HasPrefix(src, "data:"),
					},
					node: c,
				})
				continue
			}
			if isTextBlock(c) {
				tCount++
				text := textOf(c)
				blocks = append(blocks, scanned{
					Block: Block{
						NID:       fmt.Sprintf("t%d", tCount),
						Tag:       c.Data,
						Class:     attr(c, "class"),
						Text:      text,
						Hash:      hashOf(c.Data, text),
						Rich:      richOf(c),
						CharCount: utf8.RuneCountInString(text),
					},
					node: c,
				})
				continue
			}
			walk(c)
		}
	}
	walk(doc)
	return doc, blocks, nil
}

func findStyleNode(doc *html.Node) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil && found == nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "style" {
				found = c
				return
			}
			walk(c)
		}
	}
	walk(doc)
	return found
}

// isTextBlock — 叶子块级元素：子节点只有文本 / br / 内联格式。
func isTextBlock(n *html.Node) bool {
	if !textTags[n.Data] {
		return false
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode, html.CommentNode:
			continue
		case html.ElementNode:
			if !inlineTags[c.Data] && c.Data != "br" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func richOf(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data != "br" {
			return true
		}
	}
	return false
}

// textOf — 归一化文本：源码缩进/换行折叠成一个空格，<br> 保留成换行。
func textOf(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			switch c.Type {
			case html.TextNode:
				sb.WriteString(collapseSoft(c.Data))
			case html.ElementNode:
				if c.Data == "br" {
					sb.WriteString("\n")
				} else {
					walk(c)
				}
			}
		}
	}
	walk(n)
	return normalizeLines(sb.String())
}

func rawText(n *html.Node) string {
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			sb.WriteString(c.Data)
		}
	}
	return sb.String()
}

// collapseSoft — 连续空白压成一个空格，但**保留首尾各一个**：
// 文本节点之间的空格是词分隔符（`Hello <em>world</em>` 不能粘成 `Helloworld`）。
// 不能用 strings.Fields：它会丢掉首尾空白，正好把分隔符吃掉。
func collapseSoft(s string) string {
	var sb strings.Builder
	inSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !inSpace {
				sb.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		sb.WriteRune(r)
		inSpace = false
	}
	return sb.String()
}

// collapse — collapseSoft + trim（行级归一化用）。
func collapse(s string) string {
	return strings.TrimSpace(collapseSoft(s))
}

func normalizeLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, strings.TrimSpace(collapse(line)))
	}
	// 行间保留一个换行；连续空行压成一个（与视觉一致）
	joined := strings.Join(out, "\n")
	for strings.Contains(joined, "\n\n") {
		joined = strings.ReplaceAll(joined, "\n\n", "\n")
	}
	return strings.TrimSpace(joined)
}

// ---------- 写回 ----------

func setText(n *html.Node, text string) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		c = next
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i > 0 {
			n.AppendChild(&html.Node{Type: html.ElementNode, Data: "br"})
		}
		if line != "" {
			n.AppendChild(&html.Node{Type: html.TextNode, Data: line})
		}
	}
}

func setRawText(n *html.Node, text string) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		c = next
	}
	if text != "" {
		n.AppendChild(&html.Node{Type: html.TextNode, Data: text})
	}
}

func setAttr(n *html.Node, key, value string) {
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = value
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: value})
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func render(doc *html.Node) (string, error) {
	var sb strings.Builder
	if err := html.Render(&sb, doc); err != nil {
		return "", fmt.Errorf("render html: %w", err)
	}
	return sb.String(), nil
}

func hashOf(parts ...string) string {
	sum := sha1.Sum([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:8]
}
