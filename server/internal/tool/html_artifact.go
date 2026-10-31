package tool

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/util/htmlpatch"
	"xhan-helper/internal/util/htmlsanitize"
)

const (
	// maxArtifactsPerTurn — 单轮最多生成几张。防模型循环/重试刷屏，同时覆盖"给我三种风格"。
	maxArtifactsPerTurn = 3
	maxArtifactTitleLen = 80
	maxArtifactSumLen   = 200
	// maxArtifactTags / maxArtifactTagsLen — 标签必填且最多 5 个（防标签泛滥把词云冲垮）。
	maxArtifactTags    = 5
	maxArtifactTagsLen = 100
	// maxInlineImages — 单页最多内联几张图。真正兜体积的是 MaxInlineBytes，
	// 这个只管"别为一张页读几十个文件"（一页手账放 12 张已经是极限）。
	maxInlineImages = 12
	// turnCounterTTL — 计数表清理阈值（同一 turn 的实际生命周期是秒级）。
	turnCounterTTL = 10 * time.Minute
	// maxReadBytes — `get` 一次能返回的 HTML 上限。再大模型也读不动（还挤掉对话窗口），
	// 让整页读回变成"读不回来"，比塞进去一坨用不上的上下文好。
	// 注意它比历史回放的截断阈值（llm.max_tool_result_chars = 20000 字符）大：同一轮内不受影响，
	// 跨轮必须重新 get（见 get 的描述）。
	maxReadBytes = 128 << 10
	// outlineTextChars — get(outline) 每块给多少字（够认出是哪一段；全文走 view=block）。
	outlineTextChars = 60
	// maxOutlineClassRunes — outline 里 class 的截断长度（工具类框架可能很长，但插入时照抄开头通常够）。
	maxOutlineClassRunes = 80
	// maxPatchOps / maxStyleBytes — patch 单次上限，挡住「把整页拆成几百块」这类误用。
	maxPatchOps   = 50
	maxStyleBytes = 64 << 10
)

// HTMLArtifactTool — 让 LLM 生成「更好看更好读」的独立页面（手账/对比表/时间线/一页纸总结…）。
//
// 正文落 artifacts 表，工具只回 {artifact_id, title, summary}；聊天里显示一张引用卡，
// 点开在右栏渲染（见 docs/design/html-artifact/design.md）。
type HTMLArtifactTool struct {
	repo   *service.ArtifactRepo
	images *service.ImageStore

	mu        sync.Mutex
	turnCount map[string]turnCounter
}

type turnCounter struct {
	n  int
	at time.Time
}

func NewHTMLArtifactTool(repo *service.ArtifactRepo, images *service.ImageStore) *HTMLArtifactTool {
	if repo == nil {
		panic("html_artifact: nil repo (wiring bug)")
	}
	if images == nil {
		panic("html_artifact: nil image store (wiring bug)")
	}
	return &HTMLArtifactTool{repo: repo, images: images, turnCount: make(map[string]turnCounter)}
}

func (t *HTMLArtifactTool) Name() string { return "html" }

func (t *HTMLArtifactTool) Description() string {
	return "生成、修改、读取、删除可在右栏（灵阅）打开的 HTML 页面，用来做排版本身承载信息的内容（对比表、时间线、一页纸总结、图文混排、仪表盘式汇总）。每页必须带 1–5 个标签，用于灵阅的词云与筛选。"
}

func (t *HTMLArtifactTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{ShortDesc: "生成/改/读/删可打开的 HTML 页面（对比/时间线/总结），每页带 1–5 个标签", Funcs: []string{"create", "list", "get", "patch"}, Others: "删除页面、标签（list_tags）"}, true
}

// ReadOnlyFuncs — `list` / `get` / `list_tags` 只读，plan 模式也能用；`create` / `patch` / `delete` 写库。
func (t *HTMLArtifactTool) ReadOnlyFuncs() map[string]bool {
	return map[string]bool{"list": true, "get": true, "list_tags": true}
}

func (t *HTMLArtifactTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:    "create",
		Display: "生成页面",
		Description: strings.Join([]string{
			"生成一页 HTML 并存入会话，聊天里会显示一张可点开的卡片。",
			"只在「排版本身承载信息」时用：多列对比、时间线、一页纸总结、图文混排、仪表盘式汇总。",
			"不要用：纯文字、代码、短列表、单张表、需要用户复制走的内容——这些直接用 markdown。内容少于 200 字不要用。",
			"拿不准就不要用。一次只生成一张，需要多张先问用户。",
			"调用之后，正文只写结论/推荐/下一步，不要再用 markdown 复述页面里的内容。",
			"用户说「再改改 / 换版式 / 调一下样式」时，**不要新建**。改版按改动大小选动作：① 改文字/数字/换图 → html.get(view=\"outline\") 找 nid → html.patch；② 加/删段落与图片 → html.patch(insert / remove)；③ 改颜色/字体/间距 → html.get(view=\"style\") → html.patch(style=…)；④ 只有重排版式、整页换结构才 html.get(view=\"full\") 后带 artifact_id 调 create 整页覆盖。**不要为改一句话重传整页。**不知道是哪一页就先 html.list 找 id（换了窗口、上下文里没有都算）。只有在没有旧页、确实要做新的一页时才不传 artifact_id。",
			"get 读回的 HTML 里图片是 `image:<id>` 引用，改完原样留着即可，服务端会重新内联。",
			"**改版时不要传 title / summary**：它们描述的是「这一页是什么」，不是「这次改了什么」。不传会自动沿用旧页的标题和摘要。只有整页主题真的变了（比如三页变四页）才传，传了也必须是一句话的整页摘要，别写「本次修改/其余沿用上一版」这类修改记录。",
			"页面要求：按 760px 宽设计、不要横向滚动。body 只放底色，内容放进自己的容器（如 .page{width:100%;max-width:760px;margin:0 auto;padding:...}）；不要给 body 写 width/max-width/margin——渲染器会在外层保留桌面底色，并把容器限制在 760px 内。可以内联 <style>，但不要写 script、iframe，也不要用外链图片或字体（会被删除）。",
			fmt.Sprintf("放用户图片：先用 image.list_images 拿 id（scope=turn 看用户本轮发的图），再在 HTML 里写 <img src=\"image:<id>\" alt=\"...\">。服务端会把图片压到适合屏幕的尺寸（最长边 %d）再内联进页面，别自己写 base64、也不用担心原图多大。图片只能放 <img> 里，不能做 CSS 背景图（url(image:...) 会被拒绝）。单页最多 %d 张不同图片（同一张重复引用只算一张）且内联后的图片总量 ≤%dMB。", service.InlineMaxEdge, maxInlineImages, htmlsanitize.MaxInlineBytes>>20),
			"观感参考（手账只是其中一种，别套模板、别固定风格）：手账风＝米色纸底 + 胶带/时间轴 + 手写感标题；对比页＝两栏并排 + 色块区分优劣；时间线＝左侧时间列 + 竖向连线；一页纸总结＝大标题 + 三块要点。留白充足、统一配色，别做满屏边框。",
			"summary 必须一句话说清这页讲了什么（用户据此决定点不点），不要写「以下是对比」「详见下图」，也不要在里面记修改历史。",
			"每页必须带 1–5 个标签（tags，半角逗号分隔）。打之前先 html.list_tags 看已有哪些标签能复用，别造同义词（手账/手帐）；灵阅标签独立于文档标签，别拿 document.list_tags 的结果套用。改版不传 tags＝沿用旧页；旧页还没有标签时，这次必须补上。",
		}, "\n"),
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"title":   map[string]interface{}{"type": "string", "description": fmt.Sprintf("页面标题，≤%d 字，会显示在卡片和右栏标签上。新建必填；改版不传＝沿用旧页", maxArtifactTitleLen)},
				"summary": map[string]interface{}{"type": "string", "description": fmt.Sprintf("一句话摘要，≤%d 字，说清这页讲了什么。新建必填；改版不传＝沿用旧页（别在这里写本次改了什么）", maxArtifactSumLen)},
				"tags":    map[string]interface{}{"type": "string", "description": fmt.Sprintf("标签，半角逗号分隔，必填 1–%d 个（如 手账,旅行）。新建必传；改版不传＝沿用旧页，旧页没有标签时必须补上", maxArtifactTags)},
				"html":    map[string]interface{}{"type": "string", "description": "完整的 HTML 文档（含 <html>…</html>，样式内联在 <style> 里）"},
				"artifact_id": map[string]interface{}{
					"type":        "string",
					"description": "要改已有页面时传它的 id（上一次结果的 artifact_id，或 html.list 里的 artifact_id），会原地覆盖那一页；做新的一页就不要传。",
				},
			},
			// 改版只需要 html：title/summary 不传就沿用旧页（见下方 create 的校验）
			"required": []string{"html"},
		},
	}, {
		Name:    "list",
		Display: "查看页面",
		Description: strings.Join([]string{
			"列出最近生成的页面（就是用户说的「灵阅」/手账/那张页），按时间倒序，带 artifact_id 和标签。",
			"用途：用户要改/删某一页但没给 id、或换了窗口不记得 id 时，先调它认出是哪一页，再用 html.get + html.patch 改（只有重排版式才 create 整页覆盖）。",
			"用户按关键词找页时传 keyword（搜标题和摘要）；按标签找页时传 tag；要往后翻用 offset。别靠一页页读标题猜。",
			"结果里的 untagged 是还没有标签的老页数量（必填规则上线前生成的）：下次改到它们时记得补 tags。",
			"只读：不会新建、不会修改。",
		}, "\n"),
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "返回几条，默认 10，最多 30",
				},
				"offset": map[string]interface{}{
					"type":        "integer",
					"description": "跳过前几条，默认 0（翻页用）",
				},
				"keyword": map[string]interface{}{
					"type":        "string",
					"description": "按标题/摘要搜关键词（可选）",
				},
				"tag": map[string]interface{}{
					"type":        "string",
					"description": "只看带这个标签的页（可选，完整标签名）",
				},
			},
		},
	}, {
		Name:    "get",
		Display: "读取页面",
		Description: strings.Join([]string{
			"读取一页，按 view 返回不同粒度。改版先读最小的那层，别默认读整页。",
			"view=\"outline\"（默认）：块清单（nid / 标签 / 首 60 字 / hash / 是否含内联格式）。改文字、换图先看它。",
			"view=\"block\" + nid：该块的全文。",
			"view=\"style\"：<style> 里的 CSS 与 hash，改颜色/字体/间距用。",
			"view=\"full\"：整页原文（含 <style>）。只有重排版式、整页换结构才用（增删块用 patch 的 insert/remove）；超过 128KB 读不回来。",
			"图片是 `image:<id>` 引用（不是几 MB 的 base64），原样保留即可。",
			"hash 是页面的版本凭证：传给 html.patch，页面被别处改过时会拒绝，防止改错段。",
			"读回的内容只在当前这一轮算数（历史回放会截断超长工具结果）：下一轮要再改，重新 get。",
		}, "\n"),
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"artifact_id": map[string]interface{}{
					"type":        "string",
					"description": "要读的页面 id（html.list 里的 artifact_id）",
				},
				"view": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"outline", "block", "style", "full"},
					"description": "默认 outline；改文字/换图/加删段落用 outline（配 patch），改样式用 style，只有重排版式才 full",
				},
				"nid": map[string]interface{}{
					"type":        "string",
					"description": "view=block 时必填：outline 里给的块编号（如 t3 / img1）",
				},
			},
			"required": []string{"artifact_id"},
		},
	}, {
		Name:    "patch",
		Display: "局部修改页面",
		Description: strings.Join([]string{
			"按块或样式做局部修改，不重传整页。改文字、数字、换图、改 CSS 必须用它。",
			"流程：html.get(view=\"outline\") 拿 nid + hash → patch。改样式：get(view=\"style\") 拿 css + hash → patch(style=…)；这页还没有 <style> 时 hash 为空，style_hash 留空即可新建。",
			"ops 每项：{nid, hash, text} 改文字；{nid, hash, image, alt} 换图。hash 必须原样带上（页面版本凭证）。",
			"加一段 / 加一张图：insert [{after, hash, html}]，after 是锚点块 nid，html 给块级片段（图片写 <img src=\"image:<id>\">，并复用页面已有的 class），服务端会清洗并内联。",
			"删一段 / 删一张图：remove [{nid, hash}]。删完会顺手收掉因此变空的容器（figure / div 相框、空表格骨架），不用自己再删壳；反过来，删掉某格/某卡里唯一的内容，那一格/那张卡也会一起消失。",
			"ops / insert / remove / style 至少给一个；单次最多 50 op / 10 段插入 / 20 块删除。",
			"报「页面已变」说明另一处改过（或你拿的是旧 hash）：重新 get 拿新 nid/hash，别拿旧值硬试。",
			"rich=true 的块含高亮/链接等内联格式，patch 文字会压平格式，服务端会拒绝；这种块只能 get(view=\"full\") 后整页重写。",
			"只有语义块（标题/段落/列表项/表格单元格/图注/引用）有 nid；div 里的文字不在清单里，改它请 get(view=\"full\") 整页重写，别拿不存在的 nid 反复试。",
			"锚点位置：插块落在锚点之后——锚在图注/列表项上会落到整个 figure/列表之后（锚在照片上仍留在 figure 里）；要加在页面末尾就锚在页脚之前那块（锚在页脚内部的块上会插进页脚里）。别锚在表格单元格（td/th）上，那会插出非法结构。",
			"整张 <table> 可以插；单独插 tr/td 不行，要增删行列请整页重写。表格若是页面最后一个块（后面没有块可当锚点），往后追加只能整页重写。",
			"换图必须是 image:<id> 引用（先用 image.list_images 拿 id）。",
			"改标签：tags=\"新,标签\"（整套替换，1–5 个）。传空串会被拒绝——每页至少一个标签；不传 tags 且这页还没有标签时，结果会提示你补上。",
		}, "\n"),
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"artifact_id": map[string]interface{}{
					"type":        "string",
					"description": "要改的页面 id（html.list 里的 artifact_id）",
				},
				"ops": map[string]interface{}{
					"type":     "array",
					"maxItems": 50,
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"nid":   map[string]interface{}{"type": "string", "description": "块编号，如 t3 / img1"},
							"hash":  map[string]interface{}{"type": "string", "description": "get 结果里该块的 hash，原样带上"},
							"text":  map[string]interface{}{"type": "string", "description": "新的块文字（可含换行；空字符串＝清空）"},
							"image": map[string]interface{}{"type": "string", "description": "新的图片引用，如 image:<id>"},
							"alt":   map[string]interface{}{"type": "string", "description": "图片说明"},
						},
						"required": []string{"nid", "hash"},
					},
					"description": "要替换的块；改文字给 text，换图给 image（/alt）",
				},
				"insert": map[string]interface{}{
					"type":     "array",
					"maxItems": 10,
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"after": map[string]interface{}{"type": "string", "description": "插到哪个块之后（outline 里的 nid）"},
							"hash":  map[string]interface{}{"type": "string", "description": "锚点块的 hash，原样带上"},
							"html":  map[string]interface{}{"type": "string", "description": "块级 HTML 片段（figure/div/p/img…）；图片写 <img src=\"image:<id>\">，并复用页面已有 class"},
						},
						"required": []string{"after", "hash", "html"},
					},
					"description": "要插入的片段（加图、加段落）",
				},
				"remove": map[string]interface{}{
					"type":     "array",
					"maxItems": 20,
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"nid":  map[string]interface{}{"type": "string", "description": "要删的块编号"},
							"hash": map[string]interface{}{"type": "string", "description": "该块的 hash，原样带上"},
						},
						"required": []string{"nid", "hash"},
					},
					"description": "要删除的块（变空的容器会被一并收掉）",
				},
				"style": map[string]interface{}{
					"type":        "string",
					"description": "新的整段 CSS（替换 <style> 内容）。改颜色/字体/间距用",
				},
				"style_hash": map[string]interface{}{
					"type":        "string",
					"description": "get(view=\"style\") 返回的 hash，原样带上；页面还没有 <style> 时留空（会新建一段）",
				},
				"tags": map[string]interface{}{
					"type":        "string",
					"description": fmt.Sprintf("整套替换标签，半角逗号分隔，1–%d 个。传空串会被拒绝（每页至少一个标签）；不传＝不动标签", maxArtifactTags),
				},
			},
			"required": []string{"artifact_id"},
		},
	}, {
		Name:    "list_tags",
		Display: "查看标签",
		Description: strings.Join([]string{
			"列出灵阅里用过的所有标签（按使用次数降序）。",
			"新建页或换标签前先看它：能复用已有标签就别造同义词（手账/手帐）。",
			"注意：灵阅标签独立于文档标签，不要拿 document 的 list_tags 结果套用。",
			"只读：不会新建、不会修改。",
		}, "\n"),
	}, {
		Name:    "delete",
		Display: "删除页面",
		Description: strings.Join([]string{
			"删除灵阅里的页面（可一次删多条）。",
			"只在用户明确说「删掉/去掉/不要这张」时调用；不要因为改版就删旧页——改版是 create 带 artifact_id 原地覆盖。",
			"删除是软删、灵阅没有回收站，删了不能恢复（与文档的 7 天回收站不同）。",
			"删完后聊天里那张旧卡片点开会提示「该页面已被删除」，这是预期，不要为此重建页面。",
			"不知道 id 先 html.list（可按 keyword / tag 找）；越权或已删的 id 会被静默跳过并在 missing 里返回。",
		}, "\n"),
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"artifact_ids": map[string]interface{}{
					"type":        "array",
					"maxItems":    100,
					"items":       map[string]interface{}{"type": "string"},
					"description": "要删的页面 id 列表（html.list 或上次 create 返回的 artifact_id），1–100 个",
				},
			},
			"required": []string{"artifact_ids"},
		},
	}}
}

func (t *HTMLArtifactTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "create":
		return t.create(ec, args)
	case "list":
		return t.list(ec, args)
	case "get":
		return t.get(ec, args)
	case "patch":
		return t.patch(ec, args)
	case "list_tags":
		return t.listTags(ec, args)
	case "delete":
		return t.delete(ec, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("未知函数: %s", funcName)}
	}
}

// get — 按 view 返回页面内容：outline（默认）/ block / style / full。
//
// 读的是 `source_html`（图片仍是 `image:<id>`）：`HTML` 列里图片已内联成几 MB 的 data: URI，
// 整页塞回模型上下文不现实。定位规则见 docs/design/html-artifact/fine-grained-edit.md。
func (t *HTMLArtifactTool) get(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ArtifactID string `json:"artifact_id"`
		View       string `json:"view"`
		NID        string `json:"nid"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	id := strings.TrimSpace(req.ArtifactID)
	if id == "" {
		return &Result{Success: false, Error: "artifact_id 必填（先用 html.list 查）"}
	}
	view := strings.TrimSpace(req.View)
	if view == "" {
		view = "outline"
	}

	// 先取窄行（不带最大 8MB 的 html 列）；只有老页面整页读回时才去读正文
	a, err := t.repo.GetForRead(ec.Ctx, ec.UserID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &Result{Success: false, Error: fmt.Sprintf("读不到页面 %s（可能已被删除）；先用 html.list 看看还有哪些", id)}
		}
		log.Printf("[html] get id=%s user=%s: %v", id, ec.UserID, err)
		return &Result{Success: false, Error: fmt.Sprintf("读页面失败: %v", err)}
	}
	src := a.SourceHTML
	if src == "" {
		// 这次改动之前生成的页面没存原文：按块读写不可用，只有整页读回这一条路（带图基本都会超限）
		if view != "full" {
			return &Result{Success: false, Error: "这页是早期格式（没有原文），没法按块读写；只能 view=\"full\" 整页读回或重新生成"}
		}
		full, err := t.repo.GetByID(ec.Ctx, ec.UserID, id)
		if err != nil {
			log.Printf("[html] get html fallback id=%s user=%s: %v", id, ec.UserID, err)
			return &Result{Success: false, Error: fmt.Sprintf("读页面失败: %v", err)}
		}
		src = full.HTML
	}

	switch view {
	case "outline":
		return t.getOutline(src, a)
	case "block":
		return t.getBlock(src, strings.TrimSpace(req.NID))
	case "style":
		return t.getStyle(src)
	case "full":
		if len(src) > maxReadBytes {
			return &Result{Success: false, Error: fmt.Sprintf("这页太大（%d 字节，读回上限 %d），没法整页读回来；改用 view=\"outline\" 按块改，或直接问用户要改哪一块", len(src), maxReadBytes)}
		}
		return resultJSON(map[string]any{
			"artifact_id": a.ID,
			"title":       a.Title,
			"summary":     a.Summary,
			"tags":        a.Tags,
			"html":        src,
		})
	default:
		return &Result{Success: false, Error: fmt.Sprintf("未知 view: %s（可选 outline / block / style / full）", view)}
	}
}

// getOutline — 块清单。每块给 nid/tag/hash + 首 60 字，改文字/换图先看它（1–3KB）。
func (t *HTMLArtifactTool) getOutline(src string, a *model.Artifact) *Result {
	blocks, err := htmlpatch.Scan(src)
	if err != nil {
		log.Printf("[html] get outline id=%s: %v", a.ID, err)
		return &Result{Success: false, Error: fmt.Sprintf("解析页面失败: %v", err)}
	}
	items := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		items = append(items, blockJSON(b, outlineTextChars))
	}
	styleBytes := 0
	if css, _, err := htmlpatch.Style(src); err == nil {
		styleBytes = len(css)
	} else {
		log.Printf("[html] get outline style id=%s: %v", a.ID, err)
	}
	return resultJSON(map[string]any{
		"artifact_id": a.ID,
		"title":       a.Title,
		"summary":     a.Summary,
		"tags":        a.Tags,
		"blocks":      items,
		"style_bytes": styleBytes,
		"page_bytes":  len(src),
		"tip":         "改文字/换图/加图/删块都用 html.patch（ops / insert / remove），不要重传整页；改样式先 get(view=\"style\") 再 patch(style=…)。插入新块时把相邻块的 class 照抄到片段上（每块都带 class），否则没有间距/配色。div 里的文字没有 nid（只认标题/段落/列表/表格单元格等语义块），改它请 view=\"full\" 整页重写。加到末尾锚在页脚之前那块",
	})
}

// getBlock — 某一块的全文（outline 里文本被截断时用）。
func (t *HTMLArtifactTool) getBlock(src, nid string) *Result {
	if nid == "" {
		return &Result{Success: false, Error: "view=\"block\" 需要 nid（先用 view=\"outline\" 看块清单）"}
	}
	blocks, err := htmlpatch.Scan(src)
	if err != nil {
		log.Printf("[html] get block parse: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("解析页面失败: %v", err)}
	}
	for _, b := range blocks {
		if b.NID == nid {
			return resultJSON(blockJSON(b, 0))
		}
	}
	return &Result{Success: false, Error: fmt.Sprintf("没有这个块 %s；先用 html.get(view=\"outline\") 看现有块", nid)}
}

// blockJSON — 块 → 上下文里的形态。两类硬约束都在这儿：
//   - data: 图片只给字节数，绝不回传 base64（一张 3–5MB 的照片会把 outline 变成几 MB）；
//   - textLimit > 0 时截断文字（outline 用），0 表示给全文（view=block 用）。
func blockJSON(b htmlpatch.Block, textLimit int) map[string]any {
	out := map[string]any{"nid": b.NID, "tag": b.Tag, "hash": b.Hash}
	if b.Class != "" {
		// 插入新块时要照抄相邻块的 class（间距/配色都写在 class 上），给模型现成的
		out["class"] = truncateRunes(b.Class, maxOutlineClassRunes)
		if utf8.RuneCountInString(b.Class) > maxOutlineClassRunes {
			out["class_truncated"] = true // 截断了就别整个照抄，抄到半截类名对不上
		}
	}
	if b.Tag == "img" {
		if b.Legacy {
			out["legacy"] = true
			out["image_bytes"] = len(b.Image)
		} else {
			out["image"] = b.Image
			if b.Alt != "" {
				out["alt"] = b.Alt
			}
		}
		return out
	}
	out["chars"] = b.CharCount
	if b.Rich {
		out["rich"] = true
	}
	if textLimit > 0 && b.CharCount > textLimit {
		out["text"] = truncateRunes(b.Text, textLimit)
		out["truncated"] = true
	} else {
		out["text"] = b.Text
	}
	return out
}

// getStyle — <style> 里的 CSS 与 hash，改颜色/字体/间距用。
func (t *HTMLArtifactTool) getStyle(src string) *Result {
	css, hash, err := htmlpatch.Style(src)
	if err != nil {
		log.Printf("[html] get style: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("读取样式失败: %v", err)}
	}
	out := map[string]any{"style": css, "style_hash": hash, "bytes": len(css)}
	if hash == "" {
		out["tip"] = "这页没有 <style>：直接 patch(style=…, style_hash=\"\") 会新建一段"
	}
	return resultJSON(out)
}

// patch — 按块或样式做局部修改，不重传整页（docs/design/html-artifact/fine-grained-edit.md）。
func (t *HTMLArtifactTool) patch(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ArtifactID string `json:"artifact_id"`
		Ops        []struct {
			NID   string  `json:"nid"`
			Hash  string  `json:"hash"`
			Text  *string `json:"text"`
			Image *string `json:"image"`
			Alt   *string `json:"alt"`
		} `json:"ops"`
		Insert []struct {
			After string `json:"after"`
			Hash  string `json:"hash"`
			HTML  string `json:"html"`
		} `json:"insert"`
		Remove []struct {
			NID  string `json:"nid"`
			Hash string `json:"hash"`
		} `json:"remove"`
		Style     *string `json:"style"`
		StyleHash string  `json:"style_hash"`
		Tags      *string `json:"tags"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	id := strings.TrimSpace(req.ArtifactID)
	if id == "" {
		return &Result{Success: false, Error: "artifact_id 必填（先用 html.list 查）"}
	}
	if len(req.Ops) == 0 && len(req.Insert) == 0 && len(req.Remove) == 0 && req.Style == nil && req.Tags == nil {
		return &Result{Success: false, Error: "ops / insert / remove / style / tags 至少给一个"}
	}
	if len(req.Ops) > maxPatchOps {
		return &Result{Success: false, Error: fmt.Sprintf("一次最多改 %d 个块（收到 %d 个）", maxPatchOps, len(req.Ops))}
	}
	if len(req.Insert) > htmlpatch.MaxInsertOps {
		return &Result{Success: false, Error: fmt.Sprintf("一次最多插入 %d 段（收到 %d 段）", htmlpatch.MaxInsertOps, len(req.Insert))}
	}
	if len(req.Remove) > htmlpatch.MaxRemoveOps {
		return &Result{Success: false, Error: fmt.Sprintf("一次最多删 %d 块（收到 %d 块）", htmlpatch.MaxRemoveOps, len(req.Remove))}
	}
	if req.Style != nil && len(*req.Style) > maxStyleBytes {
		return &Result{Success: false, Error: fmt.Sprintf("样式太长（%d 字节，上限 %d）", len(*req.Style), maxStyleBytes)}
	}

	a, err := t.repo.GetForRead(ec.Ctx, ec.UserID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &Result{Success: false, Error: fmt.Sprintf("读不到页面 %s（可能已被删除）；先用 html.list 看看还有哪些", id)}
		}
		log.Printf("[html] patch id=%s user=%s: %v", id, ec.UserID, err)
		return &Result{Success: false, Error: fmt.Sprintf("读页面失败: %v", err)}
	}
	src := a.SourceHTML
	hasStructure := len(req.Ops) > 0 || len(req.Insert) > 0 || len(req.Remove) > 0
	if src == "" && (hasStructure || req.Style != nil) {
		// 只有改标签不需要原文；改内容只能整页重建（create 带 artifact_id）
		return &Result{Success: false, Error: "这页是早期格式（没有原文），没法按块改；改标签可以直接传 tags，改内容请整页重建"}
	}
	if req.Style != nil && strings.TrimSpace(req.StyleHash) == "" {
		// 页面本来没有 <style> 时允许新建；有旧样式就必须带 hash，否则并发改会互相覆盖
		_, hash, err := htmlpatch.Style(src)
		if err != nil {
			return &Result{Success: false, Error: fmt.Sprintf("读取样式失败: %v", err)}
		}
		if hash != "" {
			return &Result{Success: false, Error: "改样式必须带 style_hash；先 html.get(view=\"style\") 拿 CSS 和 hash"}
		}
	}

	newSrc := src
	var changed []string
	if hasStructure {
		edit := htmlpatch.Edit{
			Ops:     make([]htmlpatch.Op, 0, len(req.Ops)),
			Inserts: make([]htmlpatch.InsertOp, 0, len(req.Insert)),
			Removes: make([]htmlpatch.RemoveOp, 0, len(req.Remove)),
		}
		for _, o := range req.Ops {
			edit.Ops = append(edit.Ops, htmlpatch.Op{
				NID:   strings.TrimSpace(o.NID),
				Hash:  strings.TrimSpace(o.Hash),
				Text:  o.Text,
				Image: o.Image,
				Alt:   o.Alt,
			})
		}
		for _, ins := range req.Insert {
			edit.Inserts = append(edit.Inserts, htmlpatch.InsertOp{
				After: strings.TrimSpace(ins.After),
				Hash:  strings.TrimSpace(ins.Hash),
				HTML:  ins.HTML,
			})
		}
		for _, rm := range req.Remove {
			edit.Removes = append(edit.Removes, htmlpatch.RemoveOp{
				NID:  strings.TrimSpace(rm.NID),
				Hash: strings.TrimSpace(rm.Hash),
			})
		}
		newSrc, changed, err = htmlpatch.ApplyEdit(newSrc, edit)
		if err != nil {
			return &Result{Success: false, Error: patchErrorMessage(err)}
		}
	}
	if req.Style != nil {
		newSrc, err = htmlpatch.ReplaceStyle(newSrc, *req.Style, strings.TrimSpace(req.StyleHash))
		if err != nil {
			return &Result{Success: false, Error: patchErrorMessage(err)}
		}
	}
	// 两处返回同一组字段（没有 tip 的幂等分支也不例外）
	patchResult := func(tagsOut string, pageBytes int, changedNow bool, tip string) *Result {
		inserted, removed := len(req.Insert), len(req.Remove)
		updated := changed
		if !changedNow {
			inserted, removed, updated = 0, 0, nil // 幂等重放：这一轮什么都没做
		}
		out := map[string]any{
			"artifact_id":   id,
			"title":         a.Title,
			"summary":       a.Summary,
			"tags":          tagsOut,
			"updated":       updated,
			"inserted":      inserted,
			"removed":       removed,
			"style_updated": req.Style != nil,
			"page_bytes":    pageBytes,
		}
		if tip != "" {
			out["tip"] = tip
		}
		return resultJSON(out)
	}

	// 标签先校验：必填 1–5 个，传空串＝想清空，直接拒绝（每页至少一个标签）。
	tagsOut := a.Tags
	tagsChanged := false
	if req.Tags != nil {
		v, msg := validateArtifactTags(*req.Tags)
		if msg != "" {
			return &Result{Success: false, Error: msg}
		}
		tagsOut = v
		tagsChanged = v != a.Tags
	}

	if newSrc == src {
		if !tagsChanged {
			// 幂等重放（同一个 patch 又来一次）：内容没变，直接当成功
			return patchResult(a.Tags, len(src), false, "")
		}
		ok, err := t.repo.SetTags(ec.Ctx, ec.UserID, id, tagsOut)
		if err != nil {
			log.Printf("[html] patch tags id=%s user=%s: %v", id, ec.UserID, err)
			return &Result{Success: false, Error: fmt.Sprintf("保存标签失败: %v", err)}
		}
		if !ok {
			return &Result{Success: false, Error: "页面已变（另一处删除了它），请重新 html.list 确认"}
		}
		return patchResult(tagsOut, len(src), false, "标签已更新")
	}

	// 重新清洗 + 内联：patch 改的是原文，成品也要跟着重算
	clean, inlined, err := t.inlineImages(ec, newSrc)
	if err != nil {
		if errors.Is(err, htmlsanitize.ErrTooShort) {
			return &Result{Success: false, Error: "删完这页就空了（不足 200 字节）：至少留一块，或用 create 重建整页"}
		}
		return &Result{Success: false, Error: err.Error()}
	}

	// 只有显式传了 tags 才把标签并进这次 UPDATE；不传就完全不碰 tags 列，
	// 避免把并发「只改标签」的结果静默写回旧值。
	var tagsParam *string
	if req.Tags != nil {
		tagsParam = &tagsOut
	}
	ok, err := t.repo.UpdateIfSourceUnchanged(ec.Ctx, ec.UserID, id, src, clean, newSrc, tagsParam)
	if err != nil {
		log.Printf("[html] patch save id=%s user=%s: %v", id, ec.UserID, err)
		return &Result{Success: false, Error: fmt.Sprintf("保存页面失败: %v", err)}
	}
	if !ok {
		return &Result{Success: false, Error: "页面已变（另一处改过或已删除），请重新 html.get 拿新 nid/hash"}
	}
	if len(inlined) > 0 {
		if err := t.images.Repo().MarkReferenced(ec.Ctx, inlined, ec.UserID); err != nil {
			log.Printf("[html] patch mark referenced failed: %v", err)
		}
	}
	tip := "已只改这一块；下次改文字继续用 patch，不要重传整页"
	if len(req.Insert) > 0 || len(req.Remove) > 0 {
		// 增删块之后 nid 会顺移（content hash 没变，变的是「第几块」），旧 nid/hash 配对失效
		tip = "已改结构；块编号已重排，继续改请重新 get(view=\"outline\") 拿新 nid/hash，不要重传整页"
	}
	if tagsOut == "" {
		// 存量老页：内容照改，但提醒这一次或下一次把必填标签补上
		tip += "；这页还没有标签，下次 patch 时带上 tags（1–5 个）"
	}
	return patchResult(tagsOut, len(newSrc), true, tip)
}

// patchErrorMessage — 把 htmlpatch 的错误翻成模型能照着自愈的话。
func patchErrorMessage(err error) string {
	switch {
	case errors.Is(err, htmlpatch.ErrHashMismatch):
		return "页面已变（另一处改过），请重新 html.get 拿新 nid/hash"
	case errors.Is(err, htmlpatch.ErrStyleHash):
		return "样式已变（另一处改过），请重新 html.get(view=\"style\")"
	case errors.Is(err, htmlpatch.ErrRichBlock):
		return fmt.Sprintf("%v；要改它请 view=\"full\" 后整页重写", err)
	case errors.Is(err, htmlpatch.ErrNoSuchBlock):
		return fmt.Sprintf("%v；先用 html.get(view=\"outline\") 看现有块", err)
	case errors.Is(err, htmlpatch.ErrNoHead):
		return err.Error() + "；只能 view=\"full\" 后整页改"
	case errors.Is(err, htmlpatch.ErrEditConflict):
		return fmt.Sprintf("%v；改和删分成两次 patch 做", err)
	case errors.Is(err, htmlpatch.ErrBadFragment):
		return err.Error()
	case errors.Is(err, htmlpatch.ErrBadAnchor):
		return fmt.Sprintf("%v；加段落请锚在整张 <table> 之后的块上，加行/加格只能 view=\"full\" 后整页重写", err)
	case errors.Is(err, htmlpatch.ErrTextTooLong),
		errors.Is(err, htmlpatch.ErrDuplicateNID),
		errors.Is(err, htmlpatch.ErrMixedOp),
		errors.Is(err, htmlpatch.ErrNotTextBlock),
		errors.Is(err, htmlpatch.ErrNotImageBlock),
		errors.Is(err, htmlpatch.ErrBadImageRef):
		return err.Error()
	default:
		return fmt.Sprintf("修改页面失败: %v", err)
	}
}

func resultJSON(v map[string]any) *Result {
	data, err := json.Marshal(v)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("结果序列化失败: %v", err)}
	}
	return &Result{Success: true, Data: data}
}

// validateArtifactTags — 灵阅标签必填且限 1–5 个（归一化后计数）。
// 返回归一化后的标签串与给模型看的错误文案（空串＝通过）。
func validateArtifactTags(raw string) (string, string) {
	tags := service.NormalizeTags(raw)
	if tags == "" {
		return "", fmt.Sprintf("tags 必填：至少给这页打 1 个标签（1–%d 个，半角逗号分隔，如 手账,旅行）", maxArtifactTags)
	}
	if n := len(strings.Split(tags, ",")); n > maxArtifactTags {
		return "", fmt.Sprintf("标签最多 %d 个（收到 %d 个），合并成更少的维度", maxArtifactTags, n)
	}
	if utf8.RuneCountInString(tags) > maxArtifactTagsLen {
		return "", fmt.Sprintf("标签总长超过 %d 字，精简一下", maxArtifactTagsLen)
	}
	return tags, ""
}

// list — 最近生成的页面（标题 + id + 时间），让模型在「改刚才那页」时能认出目标。
//
// 用户级而不是会话级：换个窗口说「改一下刚才的灵阅」时，那页是在别的会话里生成的，
// 按 conversation_id 过滤会直接查不到（这正是「工具只能写、不知道改啥」的根因）。
func (t *HTMLArtifactTool) list(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Limit   int    `json:"limit"`
		Offset  int    `json:"offset"`
		Keyword string `json:"keyword"`
		Tag     string `json:"tag"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if req.Limit <= 0 {
		req.Limit = 10
	} else if req.Limit > 30 {
		req.Limit = 30 // 要 30 就给 30，别静默降到默认值
	}
	if req.Offset < 0 {
		req.Offset = 0
	}
	var tags []string
	if tag := strings.TrimSpace(req.Tag); tag != "" {
		tags = []string{tag}
	}
	rows, total, err := t.repo.ListByTags(ec.Ctx, ec.UserID, tags, strings.TrimSpace(req.Keyword), req.Limit, req.Offset)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("查页面列表失败: %v", err)}
	}
	items := make([]map[string]any, 0, len(rows))
	for _, a := range rows {
		items = append(items, map[string]any{
			"artifact_id": a.ID,
			"title":       a.Title,
			"summary":     a.Summary,
			"tags":        a.Tags,
			"created_at":  a.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	out := map[string]any{"items": items, "total": total}
	// 必填规则上线前生成的老页：提醒模型改到它们时补标签（查不到不影响列表）
	if n, err := t.repo.CountUntagged(ec.Ctx, ec.UserID); err != nil {
		log.Printf("[html] list count untagged user=%s: %v", ec.UserID, err)
	} else if n > 0 {
		out["untagged"] = n
	}
	return resultJSON(out)
}

// listTags — 灵阅用过的标签（按次数降序），避免模型造同义词。
func (t *HTMLArtifactTool) listTags(ec *ExecuteContext, args json.RawMessage) *Result {
	if err := decodeArgs(args, &struct{}{}); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	tags, err := t.repo.ListTags(ec.Ctx, ec.UserID)
	if err != nil {
		log.Printf("[html] list_tags user=%s: %v", ec.UserID, err)
		return &Result{Success: false, Error: fmt.Sprintf("查标签失败: %v", err)}
	}
	return resultJSON(map[string]any{"tags": tags})
}

// delete — 软删（灵阅无回收站，见 design §9.7）。越权/已删 id 静默跳过，返回 missing 说清楚。
func (t *HTMLArtifactTool) delete(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ArtifactIDs []string `json:"artifact_ids"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	seen := make(map[string]bool, len(req.ArtifactIDs))
	ids := make([]string, 0, len(req.ArtifactIDs))
	for _, id := range req.ArtifactIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return &Result{Success: false, Error: "artifact_ids 必填，至少给一个页面 id（html.list 可查）"}
	}
	if len(ids) > 100 {
		return &Result{Success: false, Error: fmt.Sprintf("一次最多删 100 页（收到 %d 个）", len(ids))}
	}
	// 先查哪些 id 真的属于该用户且还在，再把它们删掉：
	// 越权/已删的 id 只进 missing，不会被算成「删成功」。
	existing, err := t.repo.ExistingIDs(ec.Ctx, ec.UserID, ids)
	if err != nil {
		log.Printf("[html] delete existing ids user=%s: %v", ec.UserID, err)
		return &Result{Success: false, Error: fmt.Sprintf("查页面失败: %v", err)}
	}
	alive := make(map[string]bool, len(existing))
	for _, id := range existing {
		alive[id] = true
	}
	missing := make([]string, 0)
	for _, id := range ids {
		if !alive[id] {
			missing = append(missing, id)
		}
	}

	n, err := t.repo.DeleteMany(ec.Ctx, ec.UserID, existing)
	if err != nil {
		log.Printf("[html] delete user=%s: %v", ec.UserID, err)
		return &Result{Success: false, Error: fmt.Sprintf("删除页面失败: %v", err)}
	}
	out := map[string]any{"deleted": n}
	if len(missing) > 0 {
		out["missing"] = missing
	}
	return resultJSON(out)
}

func (t *HTMLArtifactTool) create(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ArtifactID string `json:"artifact_id"`
		Title      string `json:"title"`
		Summary    string `json:"summary"`
		Tags       string `json:"tags"`
		HTML       string `json:"html"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	req.ArtifactID = strings.TrimSpace(req.ArtifactID)
	req.Title = strings.TrimSpace(req.Title)
	req.Summary = strings.TrimSpace(req.Summary)
	if strings.TrimSpace(req.HTML) == "" {
		return &Result{Success: false, Error: "html 必填"}
	}
	revising := req.ArtifactID != ""
	if !revising && (req.Title == "" || req.Summary == "") {
		return &Result{Success: false, Error: "新建页面时 title / summary 必填"}
	}
	// 改版：标题/摘要不传就沿用旧页——它们描述「这一页是什么」，不是「这次改了什么」。
	// 先取旧值再校验长度，模型不用为沿用的旧值负责。
	var oldTags string
	if revising {
		old, err := t.repo.GetMeta(ec.Ctx, ec.UserID, req.ArtifactID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return &Result{Success: false, Error: fmt.Sprintf("要改的页面 %s 不存在（可能已被删除）；如果要做新的一页，就别传 artifact_id", req.ArtifactID)}
			}
			log.Printf("[html] get meta id=%s user=%s: %v", req.ArtifactID, ec.UserID, err)
			return &Result{Success: false, Error: fmt.Sprintf("查页面失败: %v", err)}
		}
		if req.Title == "" {
			req.Title = old.Title
		}
		if req.Summary == "" {
			req.Summary = old.Summary
		}
		oldTags = old.Tags
	}
	// 标签：新建必填；改版不传＝沿用旧页，旧页本来没有标签时这次必须补（存量老页的补标签口）。
	var tags string
	if revising {
		if strings.TrimSpace(req.Tags) == "" {
			if oldTags == "" {
				return &Result{Success: false, Error: "这页还没有标签：改版时请一起传 tags（1–5 个，半角逗号分隔），正好把老页补上"}
			}
			tags = oldTags
		} else {
			v, msg := validateArtifactTags(req.Tags)
			if msg != "" {
				return &Result{Success: false, Error: msg}
			}
			tags = v
		}
	} else {
		v, msg := validateArtifactTags(req.Tags)
		if msg != "" {
			return &Result{Success: false, Error: msg}
		}
		tags = v
	}
	if utf8.RuneCountInString(req.Title) > maxArtifactTitleLen {
		return &Result{Success: false, Error: fmt.Sprintf("title 超过 %d 字", maxArtifactTitleLen)}
	}
	if utf8.RuneCountInString(req.Summary) > maxArtifactSumLen {
		return &Result{Success: false, Error: fmt.Sprintf("summary 超过 %d 字", maxArtifactSumLen)}
	}
	if ec.ConvID == "" {
		return &Result{Success: false, Error: "当前没有会话上下文，无法生成页面"}
	}

	// 先校验、清洗，再扣配额：截断是最常见的失败（全链路没有 finish_reason 处理），
	// 失败也扣次数会让模型连撞三次后整轮报废。
	clean, inlined, err := t.inlineImages(ec, req.HTML)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	// 配额在这里判（内联之后）：最坏情况是本轮第 4 次 create 先读了几张图才被拒，
	// 这是拿到"失败不扣配额"的代价，换的是截断重试不会把一轮额度耗光。
	if !t.allowOne(ec.TurnID) {
		return &Result{Success: false, Error: fmt.Sprintf("本轮已生成 %d 张页面，够了；如需更多请先问用户", maxArtifactsPerTurn)}
	}

	id := req.ArtifactID
	if id != "" {
		// 「改这一版」：原地覆盖，不新建行（见 design §4.2）。
		// 找不到就报错而不是偷偷新建：静默新建正是「改一次多一张」的来源。
		ok, err := t.repo.Update(ec.Ctx, ec.UserID, id, req.Title, req.Summary, tags, clean, req.HTML)
		if err != nil {
			return &Result{Success: false, Error: fmt.Sprintf("保存页面失败: %v", err)}
		}
		if !ok {
			return &Result{Success: false, Error: fmt.Sprintf("要改的页面 %s 不存在（可能已被删除）；如果要做新的一页，就别传 artifact_id", id)}
		}
	} else {
		id = uuid.NewString()
		a := &model.Artifact{
			ID:             id,
			UserID:         ec.UserID,
			ConversationID: ec.ConvID,
			Title:          req.Title,
			Summary:        req.Summary,
			Tags:           tags,
			HTML:           clean,
			SourceHTML:     req.HTML,
		}
		if err := t.repo.Create(ec.Ctx, a); err != nil {
			return &Result{Success: false, Error: fmt.Sprintf("保存页面失败: %v", err)}
		}
	}

	// 被页面用到的图续一次保留期（15 天），方便同一会话隔几天再修订；不改全局 TTL。
	// 本轮附件已由 orchestrator 在开头标过，这里真正起作用的是 scope=all 取来的老图。
	// 失败只影响"以后还能不能用同一张图"，页面本身已内联，不影响本次结果。
	if len(inlined) > 0 {
		if err := t.images.Repo().MarkReferenced(ec.Ctx, inlined, ec.UserID); err != nil {
			log.Printf("[html] mark inlined images referenced failed: %v", err)
		}
	}

	out := map[string]any{
		"artifact_id": id,
		"type":        model.ArtifactTypeHTML,
		"title":       req.Title,
		"summary":     req.Summary,
		"tags":        tags,
	}
	// 大页整页覆盖时顺手推一把：下次改文字走 patch，别重传整页（docs/design/html-artifact/fine-grained-edit.md §4.7）
	if revising && len(req.HTML) > 10<<10 {
		out["tip"] = "下次改文字用 html.patch（先 get view=\"outline\" 拿 nid），不要重传整页"
	}
	return resultJSON(out)
}

// inlineImages — 把 <img src="image:<id>"> 换成 data: URI，返回清洗后的 HTML 与被内联的图片 id。
//
// 图片在这里内联：模型只写引用，服务端换成 data:，生成后页面与源文件解耦，
// 源图之后被 TTL 清掉也不影响（设计文档 §4.3）。任何一张图失败 → 整页失败，不静默少图。
//
// 张数/字节在闭包里就地挡：整页 8MB 的闸在 Render 之后才判，
// 只靠它会让几十张大图先被读完、base64 一遍再报错（内存和 IO 都白花）。
func (t *HTMLArtifactTool) inlineImages(ec *ExecuteContext, raw string) (string, []string, error) {
	resolved := make(map[string]string)
	var inlined []string
	var inlinedBytes int64
	clean, err := htmlsanitize.ProcessWithImages(raw, func(id string) (string, error) {
		if uri, ok := resolved[id]; ok {
			return uri, nil
		}
		if len(resolved) >= maxInlineImages {
			return "", fmt.Errorf("页面引用的图片超过 %d 张，少放几张", maxInlineImages)
		}
		uri, err := t.images.DataURI(ec.Ctx, ec.UserID, id)
		if err != nil {
			// 点名是哪张 + 给下一步：改旧页时源图可能已过 15 天保留期，这是唯一能自愈的路径
			return "", fmt.Errorf("图片 %s 内联失败（可能已过期或已删）：从页面里去掉这张 <img> 或换一张再交：%w", id, err)
		}
		inlinedBytes += int64(len(uri))
		if inlinedBytes > htmlsanitize.MaxInlineBytes {
			return "", fmt.Errorf("页面内联图片总量超过 %d 字节，少放几张或换小图", htmlsanitize.MaxInlineBytes)
		}
		resolved[id] = uri
		inlined = append(inlined, id)
		return uri, nil
	})
	if err != nil {
		return "", nil, err
	}
	return clean, inlined, nil
}

// allowOne — 单轮配额。turnCount 只增不减会脏，按 TTL 顺手清一遍。
func (t *HTMLArtifactTool) allowOne(turnID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for k, v := range t.turnCount {
		if now.Sub(v.at) > turnCounterTTL {
			delete(t.turnCount, k)
		}
	}
	c := t.turnCount[turnID]
	if c.n >= maxArtifactsPerTurn {
		return false
	}
	c.n++
	c.at = now
	t.turnCount[turnID] = c
	return true
}

// 保证实现了 Tool 接口（编译期）。
var _ Tool = (*HTMLArtifactTool)(nil)
