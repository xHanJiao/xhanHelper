package service

import (
	"strings"
	"testing"
)

const baseDoc = "第一章 引言\n\n" +
	"我们的结论是：N.E.K.O 的记忆是渲染期副产物。\n\n" +
	"第二章 对照\n\n" +
	"xhanHelper 走的是异步提取，N.E.K.O 走的是渲染期拼接。\n\n" +
	"第三章 结论\n\n" +
	"照搬抽象层会落空。\n"

const baseQuote = "N.E.K.O 的记忆是渲染期副产物"

// 定位 + 幂等：每条划线落库后，用落库的锚点再跑一次必须回到同一位置
func TestLocateAndRelocate(t *testing.T) {
	cases := []struct {
		name  string
		quote string
		want  string // 期望在源文中命中的原文片段
	}{
		{"普通中文", "并不存在一个独立的记忆模块", ""},
		{"跨粗体标记", "而是散落在 persona 渲染", ""},
		{"表格单元格", "请求期渲染拼接", "请求期渲染拼接"},
		{"行内代码", "user_memories", "user_memories"},
		{"有序列表项", "第二点", ""},
		{"引用块", "引用块里的一句话", "引用块里的一句话"},
		{"换行折叠", "渲染、 pending/confirmed", ""},
	}
	// 前几个 case 的正文在上面拼的 doc 里不存在，单独用一份带标记的
	markerDoc := "# 记忆系统\n\n" +
		"N.E.K.O 的记忆系统并不存在一个独立的记忆模块，而是**散落在 persona 渲染**、\n" +
		"pending/confirmed 反思与向量召回三处的隐式拼接。\n\n" +
		"| 维度 | N.E.K.O | xhanHelper |\n|---|---|---|\n" +
		"| 写入时机 | 请求期渲染拼接 | 对话结束后异步提取 |\n\n" +
		"1. 第一点\n2. 第二点\n\n" +
		"- 列表项里的 `user_memories` 表\n\n" +
		"> 引用块里的一句话\n"
	cases[0].want = "并不存在一个独立的记忆模块"
	cases[1].want = "而是**散落在 persona 渲染**"
	cases[4].want = "第二点"
	cases[6].want = "渲染**、\npending/confirmed"

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ax := NewAnchorer(markerDoc)
			sp, ok := ax.Locate(tc.quote, "", "")
			if !ok {
				t.Fatalf("Locate(%q) 未命中", tc.quote)
			}
			src := []rune(markerDoc)
			got := string(src[sp.Start : sp.Start+len([]rune(tc.want))])
			if got != tc.want {
				t.Fatalf("定位到 %q，期望 %q（offset %d）", got, tc.want, sp.Start)
			}
			// 幂等
			pfx, sfx := ax.Context(sp)
			again, ok2 := ax.Relocate(Anchor{Quote: sp.Quote, Prefix: pfx, Suffix: sfx, Start: sp.Start, End: sp.End})
			if !ok2 || again.Start != sp.Start {
				t.Fatalf("重定位不幂等：Locate=%d Relocate=%d ok=%v", sp.Start, again.Start, ok2)
			}
		})
	}
}

// 抗平移：在锚点前面插一段，重定位必须回到同一句话（不是同一个偏移）
func TestRelocateSurvivesShift(t *testing.T) {
	original := "第一段。\n\n要划的重点句在这里。\n\n第三段。"
	ax := NewAnchorer(original)
	sp, ok := ax.Locate("要划的重点句在这里", "", "")
	if !ok {
		t.Fatal("初始定位失败")
	}
	pfx, sfx := ax.Context(sp)

	shifted := "新增了一段完全无关的内容，还把原文拆成了两行。\n\n第一段。\n\n要划的重点句在这里。\n\n第三段。"
	got, ok := NewAnchorer(shifted).Relocate(Anchor{Quote: sp.Quote, Prefix: pfx, Suffix: sfx, Start: sp.Start, End: sp.End})
	if !ok {
		t.Fatal("平移后应仍能命中")
	}
	if txt := string([]rune(shifted)[got.Start:got.End]); txt != "要划的重点句在这里" {
		t.Fatalf("命中了 %q", txt)
	}
	if got.Start == sp.Start {
		t.Fatalf("偏移没变，说明没有真的重定位（%d）", got.Start)
	}
}

// 多重命中：靠前后文消歧到指定的那一处
func TestRelocateDisambiguate(t *testing.T) {
	doc := "结论是明确的。中间垫很多字把两处候选推开。另一个结论是明确的，但语境不同。"
	ax := NewAnchorer(doc)
	src := []rune(doc)
	q := []rune("结论是明确的")
	second := indexRunes(src, q, indexRunes(src, q, 0)+1)
	if second < 0 {
		t.Fatal("测试数据有误：应有第二处命中")
	}

	sp, ok := ax.Relocate(Anchor{
		Quote:  "结论是明确的",
		Prefix: string(src[second-8 : second]),
		Suffix: string(src[second+len(q):]),
	})
	if !ok {
		t.Fatal("消歧失败")
	}
	if sp.Start != second {
		t.Fatalf("消歧选错位置：got %d want %d", sp.Start, second)
	}
}

// 【A 的回归钉子】多重命中且前后文与任何一处都不符 → 必须判失活，不许猜第一处。
// 这条断言在旧的 `disambiguate` 实现（不达标时 return hits[0]）下会失败。
func TestDisambiguateRefusesWhenNoContextMatches(t *testing.T) {
	doc := "甲结论是明确的。中间垫话。乙结论是明确的。收尾。"
	ax := NewAnchorer(doc)

	sp, ok := ax.Relocate(Anchor{
		Quote:  "结论是明确的",
		Prefix: "完全不相干的前文内容",
		Suffix: "完全不相干的后文内容",
	})
	if ok {
		t.Fatalf("前后文与两处都不符时必须判失活，却命中 offset=%d quote=%q", sp.Start, sp.Quote)
	}
}

// 完全没有任何前后文（创建且客户端没给提示）时取第一处，避免刚划线就显示失活
func TestRelocateWithoutContextTakesFirstHit(t *testing.T) {
	doc := "甲结论是明确的。中间垫话。乙结论是明确的。"
	sp, ok := NewAnchorer(doc).Relocate(Anchor{Quote: "结论是明确的"})
	if !ok {
		t.Fatal("无前后文时应取第一处而不是失活")
	}
	if sp.Start != 1 {
		t.Fatalf("应取第一处（offset 1），got %d", sp.Start)
	}
}

// 【B 的回归钉子】短句重复时，客户端给的 DOM 前后文必须能把划线钉在用户选的那一处。
// 这里前后文故意带 Markdown 标记差异（源文有 **加粗**，DOM 侧没有），验证投影对齐。
func TestLocateUsesDOMHintsForRepeatedQuote(t *testing.T) {
	doc := "甲结论是明确的。中间垫话。这是**加粗**的话。结论是明确的，语境不同。"
	ax := NewAnchorer(doc)

	noHint, _ := ax.Locate("结论是明确的", "", "")
	if noHint.Start != 1 {
		t.Fatalf("无提示时应取第一处（offset 1），got %d", noHint.Start)
	}

	// DOM 侧的前后文：不含 Markdown 标记
	sp, ok := ax.Locate("结论是明确的", "中间垫话。这是加粗的话。", "，语境不同。")
	if !ok {
		t.Fatal("带 DOM 前后文时应命中")
	}
	want := strings.Index(doc, "结论是明确的，语境不同")
	if sp.Start != len([]rune(doc[:want])) {
		t.Fatalf("应命中第二处（offset %d），got %d", len([]rune(doc[:want])), sp.Start)
	}
}

// 【C 的回归钉子】LLM 在划线 span 内部插字 → 夹逼认下来（Fuzzy），不是失活
func TestFuzzyMatchGrowsSpanOnInnerInsert(t *testing.T) {
	ax := NewAnchorer(baseDoc)
	orig, ok := ax.Locate(baseQuote, "", "")
	if !ok {
		t.Fatal("初始定位失败")
	}
	pfx, sfx := ax.Context(orig)

	edited := strings.Replace(baseDoc, "渲染期副产物", "渲染期（重要）副产物", 1)
	got, ok := NewAnchorer(edited).Relocate(Anchor{
		Quote: orig.Quote, Prefix: pfx, Suffix: sfx, Start: orig.Start, End: orig.End,
	})
	if !ok {
		t.Fatal("内部插字后应夹逼命中，而不是失活")
	}
	if !got.Fuzzy {
		t.Fatal("应由夹逼得出（Fuzzy=true）")
	}
	want := "N.E.K.O 的记忆是渲染期（重要）副产物"
	if txt := string([]rune(edited)[got.Start:got.End]); txt != want {
		t.Fatalf("夹出的区间是 %q，期望 %q", txt, want)
	}
}

// 整句被换掉（前后文还在）→ 相似度不达标 → 必须失活，不许把划线挪到新句子上
func TestFuzzyMatchRejectsFullRewrite(t *testing.T) {
	ax := NewAnchorer(baseDoc)
	orig, ok := ax.Locate(baseQuote, "", "")
	if !ok {
		t.Fatal("初始定位失败")
	}
	pfx, sfx := ax.Context(orig)

	edited := strings.Replace(baseDoc, baseQuote, "这段话已经被完全替换成别的内容了", 1)
	if _, ok := NewAnchorer(edited).Relocate(Anchor{
		Quote: orig.Quote, Prefix: pfx, Suffix: sfx, Start: orig.Start, End: orig.End,
	}); ok {
		t.Fatal("整句被换掉后必须判失活")
	}
}

// 前后文在文中也重复时，夹逼必须配对离旧偏移最近的那一对，不许乱配
func TestFuzzyMatchPicksNearestPair(t *testing.T) {
	doc := "共同前文提示内容。\n\n改过的目标句子内容。\n\n共同后文提示内容。\n\n中间内容。\n\n" +
		"共同前文提示内容。\n\n又改过的目标句子内容。\n\n共同后文提示内容。\n"
	ax := NewAnchorer(doc)
	src := []rune(doc)
	first := indexRunes(src, []rune("改过的目标句子内容"), 0)

	a := Anchor{
		Quote:  "原本的目标句子内容",
		Prefix: "共同前文提示内容。",
		Suffix: "共同后文提示内容。",
		Start:  first,
		End:    first + len([]rune("改过的目标句子内容")),
	}
	sp, ok := ax.fuzzyMatch(a, []rune(a.Prefix), []rune(a.Suffix))
	if !ok {
		t.Fatal("夹逼应命中")
	}
	// 区间两侧是「前文末尾 → 后文开头」，只裁空白，句末标点会留在里面
	txt := string(src[sp.Start:sp.End])
	if !strings.HasPrefix(txt, "改过的目标句子内容") {
		t.Fatalf("夹出 %q，期望以第一处「改过的目标句子内容」开头（说明配错了对）", txt)
	}
	p0 := indexRunes(src, []rune("共同前文提示内容。"), 0)
	second := indexRunes(src, []rune("共同前文提示内容。"), p0+1)
	if sp.Start > second {
		t.Fatalf("配到了第二对（offset %d > %d）", sp.Start, second)
	}
}

// 原文被改写 → orphan
func TestRelocateOrphaned(t *testing.T) {
	ax := NewAnchorer("话题已经完全换掉了，一个字都不一样。")
	if _, ok := ax.Relocate(Anchor{Quote: "要划的重点句在这里", Prefix: "第一段。", Suffix: "第三段。"}); ok {
		t.Fatal("改写后不应命中")
	}
}

// ============ 不变量测试 ============
//
// 不预设实现，只断言"不许发生什么"：
//
//	① 定位成功 → 命中的源文区间必须与原 quote 相似（LCS 占比 ≥ 0.5），不许指向无关处
//	② 定位失败 → ok=false（由调用方判 orphan）
//	③ 可逆编辑撤销后 → 必须回到原偏移
//
// 写法刻意与实现无关：编辑算子只操作字符串，断言只看"命中的文本长什么样"。
func TestRelocateInvariants(t *testing.T) {
	ax := NewAnchorer(baseDoc)
	orig, ok := ax.Locate(baseQuote, "", "")
	if !ok {
		t.Fatal("初始定位失败")
	}
	pfx, sfx := ax.Context(orig)
	origQuote := []rune(orig.Quote)

	type editOp struct {
		name    string
		forward func(string) string
		revert  func(string) string
	}
	ops := []editOp{
		{
			name:    "在锚点前插入整段",
			forward: func(d string) string { return "新插入的前言段落。\n\n" + d },
			revert:  func(d string) string { return strings.TrimPrefix(d, "新插入的前言段落。\n\n") },
		},
		{
			name:    "在锚点后追加整段",
			forward: func(d string) string { return d + "\n\n新追加的尾注段落。\n" },
			revert:  func(d string) string { return strings.TrimSuffix(d, "\n\n新追加的尾注段落。\n") },
		},
		{
			name: "在 span 内部插字",
			forward: func(d string) string {
				return strings.Replace(d, "渲染期副产物", "渲染期（重要）副产物", 1)
			},
			revert: func(d string) string {
				return strings.Replace(d, "渲染期（重要）副产物", "渲染期副产物", 1)
			},
		},
		{
			name:    "删掉锚点所在段落之前的整段",
			forward: func(d string) string { return strings.Replace(d, "第一章 引言\n\n", "", 1) },
			revert:  func(d string) string { return "第一章 引言\n\n" + d },
		},
		{
			name:    "重排换行（插入多余空行）",
			forward: func(d string) string { return strings.Replace(d, "\n\n第二章", "\n\n\n第二章", 1) },
			revert:  func(d string) string { return strings.Replace(d, "\n\n\n第二章", "\n\n第二章", 1) },
		},
		{
			name:    "给 quote 加粗（改变 Markdown 标记）",
			forward: func(d string) string { return strings.Replace(d, baseQuote, "**"+baseQuote+"**", 1) },
			revert:  func(d string) string { return strings.Replace(d, "**"+baseQuote+"**", baseQuote, 1) },
		},
		{
			name:    "改动文档别处的行内代码（不影响锚点）",
			forward: func(d string) string { return strings.Replace(d, "异步提取", "`异步提取`", 1) },
			revert:  func(d string) string { return strings.Replace(d, "`异步提取`", "异步提取", 1) },
		},
	}

	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			edited := op.forward(baseDoc)
			sp, found := NewAnchorer(edited).Relocate(Anchor{
				Quote: orig.Quote, Prefix: pfx, Suffix: sfx, Start: orig.Start, End: orig.End,
			})

			// ① 成功 → 命中文本必须与原 quote 相似；② 失败 → 就是 orphan，不许是"别处"
			if found {
				src := []rune(edited)
				if sp.Start < 0 || sp.End > len(src) || sp.Start >= sp.End {
					t.Fatalf("返回了非法区间 [%d,%d)（len=%d）", sp.Start, sp.End, len(src))
				}
				hit := src[sp.Start:sp.End]
				ratio := float64(lcsLen(hit, origQuote)) / float64(len(origQuote))
				if ratio < anchorFuzzyRatio {
					t.Fatalf("命中 %q 与原 quote 相似度仅 %.2f，指向了无关位置", string(hit), ratio)
				}
			}

			// ③ 撤销编辑后必须回到原偏移
			back := op.revert(edited)
			sp2, found2 := NewAnchorer(back).Relocate(Anchor{
				Quote: orig.Quote, Prefix: pfx, Suffix: sfx, Start: orig.Start, End: orig.End,
			})
			if !found2 {
				t.Fatalf("撤销编辑后应能命中\n撤销后正文：%q", back)
			}
			if sp2.Start != orig.Start {
				t.Fatalf("撤销编辑后偏移应回到 %d，got %d", orig.Start, sp2.Start)
			}
		})
	}
}

// 重复短句版本的不变量：用「锚点周围文本指纹」做非循环断言。
// 编辑没碰到锚点附近时，重定位结果周围的那一段原文必须逐字不变 ——
// 断言本身不依赖任何定位逻辑，所以能抓住"跳到另一处同文本位置"这类静默错位。
func TestRelocateInvariants_RepeatedQuote(t *testing.T) {
	doc := "第一章 铺垫内容。\n\n甲结论是明确的，但它讲的是别的事。\n\n第二章 对照内容。\n\n" +
		"乙结论是明确的，这里的语境完全不同。\n\n第三章 收尾内容。\n"
	const q = "结论是明确的"
	qr := []rune(q)
	ax := NewAnchorer(doc)
	src := []rune(doc)
	first := indexRunes(src, qr, 0)
	second := indexRunes(src, qr, first+1)
	if second < 0 {
		t.Fatal("测试数据有误：应有第二处命中")
	}

	// 用第二处的前后文当提示（等价于客户端给的 DOM 前后文）
	orig, ok := ax.Locate(q, string(src[second-10:second]), string(src[second+len(qr):second+len(qr)+10]))
	if !ok || orig.Start != second {
		t.Fatalf("初始定位应落在第二处 %d，got %d ok=%v", second, orig.Start, ok)
	}
	pfx, sfx := ax.Context(orig)

	const pad = 15
	fingerprint := string(src[second-pad : second+len(qr)+pad])

	ops := []struct {
		name      string
		edit      func(string) string
		wantOrpnd bool // 锚点前后文被彻底改写 → 必须失活，不许挪到第一处同文本
	}{
		{name: "前插整段", edit: func(d string) string { return "新插入的前言段落内容。\n\n" + d }},
		{name: "后追加整段", edit: func(d string) string { return d + "\n\n新追加的尾注段落内容。\n" }},
		{name: "改动第一处的上下文", edit: func(d string) string {
			return strings.Replace(d, "甲结论是明确的，但它讲的是别的事。", "第一章的内容被完全重写了，跟结论无关。", 1)
		}},
		{name: "在第一处同文本前插入整段", edit: func(d string) string {
			return strings.Replace(d, "甲结论是明确的", "新插入的一段无关内容，把第一处推远。\n\n甲结论是明确的", 1)
		}},
		{name: "锚点前后文被全部改写", wantOrpnd: true, edit: func(d string) string {
			d = strings.Replace(d, "第二章 对照内容。", "换成一个完全不同的句子。", 1)
			d = strings.Replace(d, "，这里的语境完全不同。", "然后就是另一码事了。", 1)
			return strings.Replace(d, "第三章 收尾内容。", "最后还是别的东西。", 1)
		}},
	}

	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			edited := op.edit(doc)
			sp, found := NewAnchorer(edited).Relocate(Anchor{
				Quote: orig.Quote, Prefix: pfx, Suffix: sfx, Start: orig.Start, End: orig.End,
			})
			if op.wantOrpnd {
				if found {
					t.Fatalf("前后文被彻底改写后必须失活，却返回 offset=%d（疑似挪到了第一处同文本）", sp.Start)
				}
				return
			}
			if !found {
				t.Fatal("锚点附近没被动过，不该失活")
			}
			esrc := []rune(edited)
			if sp.Start < pad || sp.End+pad > len(esrc) {
				t.Fatalf("返回区间 [%d,%d) 越界", sp.Start, sp.End)
			}
			got := string(esrc[sp.Start-pad : sp.End+pad])
			if got != fingerprint {
				t.Fatalf("锚点周围文本变了，说明跳到了别处：\ngot  %q\nwant %q", got, fingerprint)
			}
		})
	}
}

func TestProjectCollapsesWhitespace(t *testing.T) {
	got := string(project([]rune("  a\n\n\tb   c  ")).text)
	if got != "a b c" {
		t.Fatalf("project = %q", got)
	}
}

func TestProjectMapsBackToSource(t *testing.T) {
	src := []rune("看**这里**的内容")
	p := project(src)
	i := indexRunes(p.text, []rune("这里"), 0)
	if i < 0 {
		t.Fatal("投影后未命中")
	}
	s, e := p.toOrig[i], p.toOrig[i+len([]rune("这里"))-1]+1
	if got := string(src[s:e]); got != "这里" {
		t.Fatalf("映射回源文得到 %q", got)
	}
}

// 缩进的块标记必须和列 0 的形式一样被剥掉：客户端那边 marked 早已把它渲染成
// <ul>/<ol>/<blockquote>，DOM 文本里根本没有 `-` / `1.` / `>` 这些字符。
// 投影里留一个 `-`，跨嵌套列表项的选区就永远锚不上（rung 2 找不到、掉进夹逼）。
func TestProjectStripsIndentedBlockMarkers(t *testing.T) {
	src := "- 父项：\n" +
		"  - 子项一\n" +
		"  - 子项二\n" +
		"    1. 孙项\n" +
		"  > 缩进引用\n" +
		"  正文里的 3 - 2 要留着"
	got := string(project([]rune(src)).text)
	want := "父项： 子项一 子项二 孙项 缩进引用 正文里的 3 - 2 要留着"
	if got != want {
		t.Fatalf("project = %q，期望 %q", got, want)
	}
}

// 反斜杠转义：marked 把 `\*` 渲染成字面 `*`（DOM 里没有反斜杠），客户端 fold 再把它当标记
// 丢掉。服务端投影必须先解析转义、再丢标记，否则 `\*` 的反斜杠会被当正文留下，两边对不上 ——
// 症状同样是「划线保存成功却画不出来」（真机在「特殊字符测试文档」上踩到）。
func TestProjectResolvesBackslashEscapes(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"转义星号", `转义星号：\*不该变成斜体\*`, "转义星号：不该变成斜体"},
		{"反斜杠路径原样", `C:\Users\Test\Documents`, `C:\Users\Test\Documents`},
		{"行尾反斜杠保留", "反斜杠：\\", "反斜杠：\\"},
		{"连续反斜杠折成一个", `\\`, `\`},
		{"转义有序标记", `1\. foo`, "foo"},
		{"转义无序标记", `\- item`, "item"},
	}
	for _, c := range cases {
		if got := string(project([]rune(c.in)).text); got != c.want {
			t.Errorf("%s: project(%q) = %q，期望 %q", c.name, c.in, got, c.want)
		}
	}
}

// 字面方括号不能被当 Markdown 链接标记剥掉：DOM 里 `[内容]` 是真实字符、客户端 fold 也保留，
// 剥掉会让 `[内容]` 划不满、`[　]` 这类选区 quote 变空（真机在「特殊字符测试文档」踩到）。
func TestProjectKeepsLiteralBrackets(t *testing.T) {
	got := string(project([]rune("方括号： [内容] 与 [　]")).text)
	want := "方括号： [内容] 与 [ ]"
	if got != want {
		t.Fatalf("project = %q，期望 %q", got, want)
	}
}

// 只选方括号包着的内容也能锚定（quote 非空、rung 1/2 命中，不掉夹逼）。
func TestLocateLiteralBrackets(t *testing.T) {
	doc := "- 方括号： [内容] 与 [x]\n"
	ax := NewAnchorer(doc)
	sp, ok := ax.Locate("[内容]", "", "")
	if !ok {
		t.Fatal("[内容] 未命中")
	}
	if sp.Fuzzy {
		t.Fatalf("走了夹逼：%q", sp.Quote)
	}
}

// 真机形状：源文含转义星号，客户端 fold 出来的 quote 是投影后的文本（无 `*`、无反斜杠）。
// 必须走 rung 2 命中、quote 原样保留，不能掉夹逼（夹逼会写回带 `\` 的 quote，客户端再也画不出）。
func TestLocateEscapedAsterisks(t *testing.T) {
	doc := "## 二、Markdown 语法场景\n\n" +
		"- 斜体：*斜体内容*\n" +
		"- 转义星号：\\*不该变成斜体\\*\n" +
		"- 行内代码：`a*b_c/d`\n"
	quote := "转义星号：不该变成斜体 行内代码：abc/d"

	ax := NewAnchorer(doc)
	sp, ok := ax.Locate(quote, "斜体：斜体内容", "")
	if !ok {
		t.Fatal("含转义星号的选区未命中")
	}
	if sp.Fuzzy {
		t.Fatalf("走了夹逼：quote 被改写成 %q", sp.Quote)
	}
	if sp.Quote != quote {
		t.Fatalf("quote 被改写：%q", sp.Quote)
	}
}

// 跨嵌套列表项的选区：原文是 `\n  - `，DOM 文本是单个空格。
// 必须走 rung 2（投影整段匹配、quote 原样保留），不能掉进 rung 3 夹逼 ——
// 夹逼会把 quote 改写成带 `- ` 的投影文本，客户端拿它去 DOM 里搜永远搜不到，
// 症状是「划线保存成功但正文里画不出来」。
func TestLocateAcrossNestedListItems(t *testing.T) {
	doc := "- 已产出素材共 4 条，均附逻辑点评：\n" +
		"  - 信息论角度：无意义对话信息熵更高，更难被压缩\n" +
		"  - 「所有男人必定都当过孙子」——双关硬撑：「孙子」为骂人语\n" +
		"  - 「一个人能走多远」——把「距离」偷换为「意愿」\n" +
		"- 点评格式约定：先复述素材，再指出其逻辑漏洞或荒诞点\n"
	// 客户端 fold 出来的 quote：块边界的换行折成单空格，不含任何列表标记
	quote := "硬撑：「孙子」为骂人语 「一个人能走多远」——把「距离」偷换为「意愿」"

	ax := NewAnchorer(doc)
	sp, ok := ax.Locate(quote, "「所有男人必定都当过孙子」——双关", "\n- 点评格式约定：先复述素材，再指出其逻辑漏洞或荒诞点")
	if !ok {
		t.Fatal("跨嵌套列表项的选区未命中")
	}
	if sp.Fuzzy {
		t.Fatalf("走了夹逼：quote 被改写成 %q", sp.Quote)
	}
	if sp.Quote != quote {
		t.Fatalf("quote 被改写：%q", sp.Quote)
	}
	src := string([]rune(doc)[sp.Start:sp.End])
	if !strings.Contains(src, "\n  - ") {
		t.Fatalf("落点没有跨过列表项边界：%q", src)
	}
	if strings.Contains(sp.Quote, "-") {
		t.Fatalf("返回的 quote 里带 `-`，客户端在 DOM 里定位不到：%q", sp.Quote)
	}
}

func TestLCSLen(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "abc", 0},
		{"abc", "abc", 3},
		{"原本的目标句子内容", "改过的目标句子内容", 7},
		{"渲染期副产物", "渲染期（重要）副产物", 6},
	}
	for _, tc := range cases {
		if got := lcsLen([]rune(tc.a), []rune(tc.b)); got != tc.want {
			t.Fatalf("lcsLen(%q,%q)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
