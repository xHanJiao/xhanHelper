package service

import (
	"unicode"
)

const (
	// anchorContext 锚点前后文快照窗口（rune）
	anchorContext = 60
	// anchorScoreRatio 多重命中消歧的通过阈值（前后文一致率）
	anchorScoreRatio = 0.5
	// anchorMaxHits 命中点超过这个数视为"太碎"，放弃消歧
	anchorMaxHits = 64
	// anchorFuzzyContext 夹逼用的前后文窗口（rune）
	anchorFuzzyContext = 30
	// anchorFuzzyMinContext 夹逼所需的最短前后文，太短不夹（易乱配）
	anchorFuzzyMinContext = 8
	// anchorFuzzyRatio 夹逼结果的相似度门槛：新文本与原 quote 的 LCS 占比
	anchorFuzzyRatio = 0.5
	// anchorFuzzyGapFactor 夹逼 gap 上限的倍数（相对原 span 长度）
	anchorFuzzyGapFactor = 3
	// anchorFuzzyGapSlack 夹逼 gap 上限的固定余量
	anchorFuzzyGapSlack = 200
)

// Anchor — 重定位所需的锚点信息。
// Prefix/Suffix 优先由调用方给（客户端 DOM 选区的前后文）；为空时用存储快照。
// Start/End 为上次已知偏移，仅用于消歧与夹逼的 tie-break。
type Anchor struct {
	Quote  string
	Prefix string
	Suffix string
	Start  int
	End    int
}

// Span — 定位结果。[Start, End) 为源文 rune 偏移。
// Quote 是被定位到的文本：rung 1/2 沿用原 quote；rung 3 夹逼会改写它（Fuzzy=true）。
type Span struct {
	Start, End int
	Quote      string
	Fuzzy      bool
}

// Anchorer — 单篇正文的锚定器。正文与投影各算一次，供该文档全部批注复用。
type Anchorer struct {
	src  []rune
	proj projection
}

// NewAnchorer 为 content 构建锚定器
func NewAnchorer(content string) *Anchorer {
	src := []rune(content)
	return &Anchorer{src: src, proj: project(src)}
}

// Locate 定位 quote。hintPrefix / hintSuffix 是客户端 DOM 选区的前后文（纯文本），
// 可为空 —— 非空时用于消歧（创建场景；此时还没有存储快照）。
func (x *Anchorer) Locate(quote, hintPrefix, hintSuffix string) (Span, bool) {
	return x.Relocate(Anchor{Quote: quote, Prefix: hintPrefix, Suffix: hintSuffix})
}

// Context 取 span 两侧的源文快照（各 ≤anchorContext 字），落库用。
func (x *Anchorer) Context(s Span) (string, string) {
	if s.Start < 0 || s.Start > s.End || s.End > len(x.src) {
		return "", ""
	}
	lo := s.Start - anchorContext
	if lo < 0 {
		lo = 0
	}
	hi := s.End + anchorContext
	if hi > len(x.src) {
		hi = len(x.src)
	}
	return string(x.src[lo:s.Start]), string(x.src[s.End:hi])
}

// Relocate 四级阶梯重新定位锚点。
//
//	rung 1  源文精确匹配。唯一命中直接返回（最常见、最可信的快路径）；
//	        多命中一律落到 rung 2 —— 那里客户端前后文、存储快照、正文都在投影空间，
//	        消歧只需一份实现。
//	rung 2  投影空间整段匹配（投影丢掉 Markdown 标记与空白差异）。
//	        多命中时按前后文打分；不达标且没有任何前后文（创建且客户端没给提示）
//	        则取第一处，避免刚划的线立刻显示失活。
//	rung 3  投影空间前后文夹逼，覆盖"LLM 在划线 span 内部插字/局部改写"。
//	        夹出来的新文本要过相似度门槛，否则说明整句被换掉 → 失败。
//
// 全部失败 → ok=false，调用方标 orphaned。
func (x *Anchorer) Relocate(a Anchor) (Span, bool) {
	if a.Quote == "" {
		return Span{}, false
	}
	qr := []rune(a.Quote)

	// ---- rung 1：源文精确匹配，只取唯一命中做快路径 ----
	if p, ok := uniqueHit(x.src, qr); ok {
		return Span{Start: p, End: p + len(qr), Quote: a.Quote}, true
	}

	pq := project(qr)
	if len(pq.text) == 0 {
		return Span{}, false
	}
	pre := projCtxTail(a.Prefix)
	suf := projCtxHead(a.Suffix)
	hasCtx := len(pre) > 0 || len(suf) > 0

	// ---- rung 2：投影空间整段匹配 ----
	hits, overflow := allHits(x.proj.text, pq.text)
	switch {
	case len(hits) == 1:
		return x.spanFromProj(hits[0], a.Quote, len(pq.text), false), true

	case len(hits) > 1:
		if !hasCtx {
			// 创建场景且客户端没给前后文：取第一处。
			// 此时无从判断，而"刚划的线立刻显示失活"比"锚在第一处"更让人困惑。
			return x.spanFromProj(hits[0], a.Quote, len(pq.text), false), true
		}
		if !overflow {
			if pos, ok := disambiguate(x.proj, pq.text, pre, suf, hits, a.Start); ok {
				return x.spanFromProj(pos, a.Quote, len(pq.text), false), true
			}
		}
		// 有前后文却分不出 → 交给 rung 3 夹逼，不许猜一个
	}

	// ---- rung 3：前后文夹逼 ----
	if sp, ok := x.fuzzyMatch(a, pre, suf); ok {
		return sp, true
	}
	return Span{}, false
}

// spanFromProj 把投影空间的 [p, p+n) 映射回源文
func (x *Anchorer) spanFromProj(p int, quote string, n int, fuzzy bool) Span {
	return Span{Start: x.proj.toOrig[p], End: x.proj.toOrig[p+n-1] + 1, Quote: quote, Fuzzy: fuzzy}
}

// fuzzyMatch rung 3：用前后文夹出区间。要求前后文都够长、成对、gap 合理，
// 且夹出来的文本与原 quote 足够相似（否则是"整句被换掉"，不是"被改写"）。
func (x *Anchorer) fuzzyMatch(a Anchor, pre, suf []rune) (Span, bool) {
	P := capTail(pre, anchorFuzzyContext)
	S := capHead(suf, anchorFuzzyContext)
	if len(P) < anchorFuzzyMinContext || len(S) < anchorFuzzyMinContext {
		return Span{}, false
	}

	ph, _ := allHits(x.proj.text, P)
	sh, _ := allHits(x.proj.text, S)
	if len(ph) == 0 || len(sh) == 0 {
		return Span{}, false
	}

	origLen := a.End - a.Start
	if origLen <= 0 {
		origLen = len([]rune(a.Quote))
	}
	gapMax := origLen * anchorFuzzyGapFactor
	if gapMax < origLen+anchorFuzzyGapSlack {
		gapMax = origLen + anchorFuzzyGapSlack
	}

	// 挑 gap 合理、且离旧偏移最近的一对
	bestP, bestS, bestCost := -1, -1, 0
	for _, p := range ph {
		afterP := p + len(P)
		for _, s := range sh {
			if s <= afterP {
				continue
			}
			if s-afterP > gapMax {
				continue
			}
			cost := abs(x.proj.toOrig[p]-a.Start) + abs(x.proj.toOrig[s]-a.End)
			if bestP < 0 || cost < bestCost {
				bestP, bestS, bestCost = p, s, cost
			}
		}
	}
	if bestP < 0 {
		return Span{}, false
	}

	// 夹出来的区间是「前文末尾 → 后文开头」，两侧会带上段落间被折叠的空白，
	// 去掉它才是用户眼里的那段文字（否则划线会拖到上一段/下一段）。
	lo, hi := bestP+len(P), bestS
	for lo < hi && x.proj.text[lo] == ' ' {
		lo++
	}
	for hi > lo && x.proj.text[hi-1] == ' ' {
		hi--
	}
	if lo >= hi {
		return Span{}, false
	}

	newQuote := string(x.proj.text[lo:hi])
	orig := []rune(a.Quote)
	// 相似度 < anchorFuzzyRatio → 内容被换掉，交回上层判 orphan
	if float64(lcsLen(orig, []rune(newQuote))) < float64(len(orig))*anchorFuzzyRatio {
		return Span{}, false
	}

	return Span{
		Start: x.proj.toOrig[lo],
		End:   x.proj.toOrig[hi-1] + 1,
		Quote: newQuote,
		Fuzzy: true,
	}, true
}

// ============ 匹配原语 ============

// uniqueHit 判断 needle 在 hay 中是否恰好出现一次，是则返回位置。
func uniqueHit(hay, needle []rune) (int, bool) {
	if len(needle) == 0 || len(needle) > len(hay) {
		return 0, false
	}
	first := indexRunes(hay, needle, 0)
	if first < 0 {
		return 0, false
	}
	if indexRunes(hay, needle, first+1) >= 0 {
		return 0, false
	}
	return first, true
}

// allHits 收集 needle 在 hay 中的全部命中点。
// overflow 表示命中点超过 anchorMaxHits（复读机文档），调用方应放弃消歧。
func allHits(hay, needle []rune) ([]int, bool) {
	if len(needle) == 0 || len(needle) > len(hay) {
		return nil, false
	}
	hits := make([]int, 0, 4)
	from := 0
	for {
		i := indexRunes(hay, needle, from)
		if i < 0 {
			return hits, false
		}
		if len(hits) == anchorMaxHits {
			return hits, true
		}
		hits = append(hits, i)
		from = i + 1
	}
}

// disambiguate 多重命中时按前后文一致率挑最优。
// 有前后文却达不到阈值时返回 ok=false —— 绝不"猜一个"（静默挪位比失活更糟）。
func disambiguate(p projection, needle, pre, suf []rune, hits []int, oldStart int) (int, bool) {
	need := int(float64(len(pre)+len(suf)) * anchorScoreRatio)

	best, bestScore, bestDist := hits[0], -1, 0
	for _, h := range hits {
		score := 2*commonTail(pre, p.text[:h]) + 2*commonHead(suf, p.text[h+len(needle):])
		dist := abs(p.toOrig[h] - oldStart)
		if score > bestScore || (score == bestScore && dist < bestDist) {
			best, bestScore, bestDist = h, score, dist
		}
	}
	if need > 0 && bestScore < need {
		return 0, false
	}
	return best, true
}

// ============ 纯文本投影 ============

// projection — 源文的纯文本投影 + 「投影下标 → 源文下标」映射。
// 目的是让 DOM 里选中的文本（不含 Markdown 标记）能在源文里定位：
// 例：源文 `这是**重点**内容`，DOM 文本是「这是重点内容」。
type projection struct {
	text   []rune
	toOrig []int
}

// mdInlineMarkers 行内标记字符，投影时直接丢弃。
// 只做字符级剔除，不解析链接目标 `(url)`：多留几个字符只会让前后文评分多一点噪声，
// 不会让定位错位（每个投影字符都映射回真实源文下标）。
var mdInlineMarkers = map[rune]bool{
	'*': true, '_': true, '`': true, '~': true, '|': true, '[': true, ']': true,
}

// project 把 Markdown 源文投影为纯文本：丢弃行内标记与行首块标记，连续空白折叠为单个半角空格。
func project(src []rune) projection {
	out := make([]rune, 0, len(src))
	idx := make([]int, 0, len(src))
	lineStart := true
	prevSpace := true // 起手当作已有空格，避免首字符就是折叠空格

	push := func(r rune, i int) {
		out = append(out, r)
		idx = append(idx, i)
	}

	for i := 0; i < len(src); i++ {
		r := src[i]
		switch {
		case unicode.IsSpace(r):
			if !prevSpace {
				push(' ', i)
				prevSpace = true
			}
			lineStart = r == '\n'
			continue

		case mdInlineMarkers[r]:
			continue

		case lineStart && isBlockMarker(r):
			// 行首块标记（# > - + *），连同紧随的空格一起丢
			for i+1 < len(src) && isBlockMarker(src[i+1]) {
				i++
			}
			if i+1 < len(src) && src[i+1] == ' ' {
				i++
			}
			prevSpace = true
			continue

		case lineStart && r >= '0' && r <= '9':
			// 有序列表标记 "12. "
			j := i
			for j < len(src) && src[j] >= '0' && src[j] <= '9' {
				j++
			}
			if j < len(src) && (src[j] == '.' || src[j] == ')') && j+1 < len(src) && src[j+1] == ' ' {
				i = j + 1
				prevSpace = true
				continue
			}
		}

		push(r, i)
		prevSpace = false
		lineStart = false
	}

	// 尾部折叠空格裁掉（与首部对称），避免"正文以空白结尾"时投影比 quote 多一个空格
	if n := len(out); n > 0 && out[n-1] == ' ' {
		out = out[:n-1]
		idx = idx[:n-1]
	}

	return projection{text: out, toOrig: idx}
}

// isBlockMarker 行首块标记字符
func isBlockMarker(r rune) bool {
	return r == '#' || r == '>' || r == '-' || r == '+' || r == '*'
}

// ============ 小工具 ============

// projCtxTail 把前后文提示投影后取尾部（前缀方向）
func projCtxTail(s string) []rune {
	return capTail(project([]rune(s)).text, anchorContext)
}

// projCtxHead 把前后文提示投影后取头部（后缀方向）
func projCtxHead(s string) []rune {
	return capHead(project([]rune(s)).text, anchorContext)
}

// indexRunes 在 hay[from:] 中查找 needle，返回下标；未找到返回 -1。
func indexRunes(hay, needle []rune, from int) int {
	n := len(needle)
	if n == 0 || from < 0 || from+n > len(hay) {
		return -1
	}
	for i := from; i+n <= len(hay); i++ {
		if hay[i] != needle[0] {
			continue
		}
		match := true
		for j := 1; j < n; j++ {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// capTail 取尾部最多 n 个 rune
func capTail(s []rune, n int) []rune {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// capHead 取头部最多 n 个 rune
func capHead(s []rune, n int) []rune {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// commonTail 尾部逐字符相同的最大长度
func commonTail(a, b []rune) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

// commonHead 头部逐字符相同的最大长度
func commonHead(a, b []rune) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// lcsLen 最长公共子序列长度（滚动数组，O(len(a)×len(b))）。
// 只在 rung 3 夹逼时调用，span 规模 ≤ 几百 rune。
func lcsLen(a, b []rune) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			switch {
			case a[i-1] == b[j-1]:
				cur[j] = prev[j-1] + 1
			case prev[j] >= cur[j-1]:
				cur[j] = prev[j]
			default:
				cur[j] = cur[j-1]
			}
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
