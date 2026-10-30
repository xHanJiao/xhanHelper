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
	"xhan-helper/internal/util/htmlsanitize"
)

const (
	// maxArtifactsPerTurn — 单轮最多生成几张。防模型循环/重试刷屏，同时覆盖"给我三种风格"。
	maxArtifactsPerTurn = 3
	maxArtifactTitleLen = 80
	maxArtifactSumLen   = 200
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
	return "生成、修改或读取可在右栏打开的 HTML 页面，用来做排版本身承载信息的内容（对比表、时间线、一页纸总结、图文混排、仪表盘式汇总）。"
}

func (t *HTMLArtifactTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{ShortDesc: "生成/修改/读取可打开的 HTML 页面（对比/时间线/总结）", Funcs: []string{"create", "list", "get"}}, true
}

// ReadOnlyFuncs — `list` / `get` 只读，plan 模式也能用；`create` 写库。
func (t *HTMLArtifactTool) ReadOnlyFuncs() map[string]bool {
	return map[string]bool{"list": true, "get": true}
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
			"用户说「再改改 / 换版式 / 调一下样式」时，**不要新建**。改版三步：① 不知道是哪一页就先 html.list 找 id（换了窗口、上下文里没有都算）；② html.get 读回那一页的 HTML；③ 在它的基础上改，带 artifact_id 调 create 原地覆盖。只有在没有旧页、确实要做新的一页时才不传 artifact_id。",
			"get 读回的 HTML 里图片是 `image:<id>` 引用，改完原样留着即可，服务端会重新内联。",
			"**改版时不要传 title / summary**：它们描述的是「这一页是什么」，不是「这次改了什么」。不传会自动沿用旧页的标题和摘要。只有整页主题真的变了（比如三页变四页）才传，传了也必须是一句话的整页摘要，别写「本次修改/其余沿用上一版」这类修改记录。",
			"页面要求：按 760px 宽设计、不要横向滚动。body 只放底色，内容放进自己的容器（如 .page{width:100%;max-width:760px;margin:0 auto;padding:...}）；不要给 body 写 width/max-width/margin——渲染器会在外层保留桌面底色，并把容器限制在 760px 内。可以内联 <style>，但不要写 script、iframe，也不要用外链图片或字体（会被删除）。",
			fmt.Sprintf("放用户图片：先用 image.list_images 拿 id（scope=turn 看用户本轮发的图），再在 HTML 里写 <img src=\"image:<id>\" alt=\"...\">。服务端会把图片压到适合屏幕的尺寸（最长边 %d）再内联进页面，别自己写 base64、也不用担心原图多大。图片只能放 <img> 里，不能做 CSS 背景图（url(image:...) 会被拒绝）。单页最多 %d 张且内联后的图片总量 ≤%dMB。", service.InlineMaxEdge, maxInlineImages, htmlsanitize.MaxInlineBytes>>20),
			"观感参考（手账只是其中一种，别套模板、别固定风格）：手账风＝米色纸底 + 胶带/时间轴 + 手写感标题；对比页＝两栏并排 + 色块区分优劣；时间线＝左侧时间列 + 竖向连线；一页纸总结＝大标题 + 三块要点。留白充足、统一配色，别做满屏边框。",
			"summary 必须一句话说清这页讲了什么（用户据此决定点不点），不要写「以下是对比」「详见下图」，也不要在里面记修改历史。",
		}, "\n"),
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"title":   map[string]interface{}{"type": "string", "description": fmt.Sprintf("页面标题，≤%d 字，会显示在卡片和右栏标签上。新建必填；改版不传＝沿用旧页", maxArtifactTitleLen)},
				"summary": map[string]interface{}{"type": "string", "description": fmt.Sprintf("一句话摘要，≤%d 字，说清这页讲了什么。新建必填；改版不传＝沿用旧页（别在这里写本次改了什么）", maxArtifactSumLen)},
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
			"列出最近生成的页面（就是用户说的「灵阅」/手账/那张页），按时间倒序，带 artifact_id。",
			"用途：用户要改某一页但没给 id、或换了窗口不记得 id 时，先调它认出是哪一页，再带 artifact_id 调 create 原地改。",
			"只读：不会新建、不会修改。",
		}, "\n"),
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "返回几条，默认 10，最多 30",
				},
			},
		},
	}, {
		Name:    "get",
		Display: "读取页面",
		Description: strings.Join([]string{
			"读回一整页的 HTML（含 <style>）和标题摘要，用来在它基础上改版。",
			"改版流程：html.list 找 id → html.get 拿原文 → 改 → 带 artifact_id 调 html.create。",
			"返回的 HTML 里图片是 `image:<id>` 引用（不是几 MB 的 base64），原样保留即可。",
			"页面超过 128KB 读不回来（会报错）；这种情况直接问用户要改哪里，别硬猜。",
			"读回的内容只在当前这一轮算数（历史回放会把超长工具结果截断）：下一轮要再改，重新 get，别凭记忆或历史里的残片改。",
		}, "\n"),
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"artifact_id": map[string]interface{}{
					"type":        "string",
					"description": "要读的页面 id（html.list 里的 artifact_id）",
				},
			},
			"required": []string{"artifact_id"},
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
	default:
		return &Result{Success: false, Error: fmt.Sprintf("未知函数: %s", funcName)}
	}
}

// get — 读回一页的原文（图片仍是 `image:<id>` 引用）。
//
// 为什么读原文而不是 `HTML` 列：那列里图片已经内联成几 MB 的 data: URI，
// 整页塞回模型上下文不现实（也是"工具只能写、不知道改啥"的另一半）。
func (t *HTMLArtifactTool) get(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ArtifactID string `json:"artifact_id"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	id := strings.TrimSpace(req.ArtifactID)
	if id == "" {
		return &Result{Success: false, Error: "artifact_id 必填（先用 html.list 查）"}
	}
	// 先取窄行（不带最大 8MB 的 html 列）；只有老页面回退时才去读正文
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
		// 这次改动之前生成的页面没存原文：只能退回内联后的正文（带图基本都会超限）
		full, err := t.repo.GetByID(ec.Ctx, ec.UserID, id)
		if err != nil {
			log.Printf("[html] get html fallback id=%s user=%s: %v", id, ec.UserID, err)
			return &Result{Success: false, Error: fmt.Sprintf("读页面失败: %v", err)}
		}
		src = full.HTML
	}
	if len(src) > maxReadBytes {
		return &Result{Success: false, Error: fmt.Sprintf("这页太大（%d 字节，读回上限 %d），没法整页读回来；直接问用户要改哪一块", len(src), maxReadBytes)}
	}
	data, err := json.Marshal(map[string]any{
		"artifact_id": a.ID,
		"title":       a.Title,
		"summary":     a.Summary,
		"html":        src,
	})
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("结果序列化失败: %v", err)}
	}
	return &Result{Success: true, Data: data}
}

// list — 最近生成的页面（标题 + id + 时间），让模型在「改刚才那页」时能认出目标。
//
// 用户级而不是会话级：换个窗口说「改一下刚才的灵阅」时，那页是在别的会话里生成的，
// 按 conversation_id 过滤会直接查不到（这正是「工具只能写、不知道改啥」的根因）。
func (t *HTMLArtifactTool) list(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Limit int `json:"limit"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if req.Limit <= 0 {
		req.Limit = 10
	} else if req.Limit > 30 {
		req.Limit = 30 // 要 30 就给 30，别静默降到默认值
	}
	rows, total, err := t.repo.ListByUser(ec.Ctx, ec.UserID, req.Limit, 0)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("查页面列表失败: %v", err)}
	}
	items := make([]map[string]any, 0, len(rows))
	for _, a := range rows {
		items = append(items, map[string]any{
			"artifact_id": a.ID,
			"title":       a.Title,
			"summary":     a.Summary,
			"created_at":  a.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	data, err := json.Marshal(map[string]any{
		"items": items,
		"total": total,
	})
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("结果序列化失败: %v", err)}
	}
	return &Result{Success: true, Data: data}
}

func (t *HTMLArtifactTool) create(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ArtifactID string `json:"artifact_id"`
		Title      string `json:"title"`
		Summary    string `json:"summary"`
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
		ok, err := t.repo.Update(ec.Ctx, ec.UserID, id, req.Title, req.Summary, clean, req.HTML)
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

	data, err := json.Marshal(map[string]any{
		"artifact_id": id,
		"type":        model.ArtifactTypeHTML,
		"title":       req.Title,
		"summary":     req.Summary,
	})
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("结果序列化失败: %v", err)}
	}
	return &Result{Success: true, Data: data}
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
