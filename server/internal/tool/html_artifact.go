package tool

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/util/htmlsanitize"
)

const (
	// maxArtifactsPerTurn — 单轮最多生成几张。防模型循环/重试刷屏，同时覆盖"给我三种风格"。
	maxArtifactsPerTurn = 3
	maxArtifactTitleLen = 80
	maxArtifactSumLen   = 200
	// turnCounterTTL — 计数表清理阈值（同一 turn 的实际生命周期是秒级）。
	turnCounterTTL = 10 * time.Minute
)

// HTMLArtifactTool — 让 LLM 生成「更好看更好读」的独立页面（手账/对比表/时间线/一页纸总结…）。
//
// 正文落 artifacts 表，工具只回 {artifact_id, title, summary}；聊天里显示一张引用卡，
// 点开在右栏渲染（见 docs/design/html-artifact/design.md）。
type HTMLArtifactTool struct {
	repo *service.ArtifactRepo

	mu        sync.Mutex
	turnCount map[string]turnCounter
}

type turnCounter struct {
	n  int
	at time.Time
}

func NewHTMLArtifactTool(repo *service.ArtifactRepo) *HTMLArtifactTool {
	return &HTMLArtifactTool{repo: repo, turnCount: make(map[string]turnCounter)}
}

func (t *HTMLArtifactTool) Name() string { return "html" }

func (t *HTMLArtifactTool) Description() string {
	return "生成一个可在右栏打开的 HTML 页面，用来做排版本身承载信息的内容（对比表、时间线、一页纸总结、图文混排、仪表盘式汇总）。"
}

func (t *HTMLArtifactTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{ShortDesc: "生成可打开的 HTML 页面（对比/时间线/总结）", Funcs: []string{"create"}}, true
}

func (t *HTMLArtifactTool) ReadOnlyFuncs() map[string]bool { return nil }

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
			"页面要求：按 760px 宽设计、不要横向滚动。body 只放底色，内容放进自己的容器（如 .page{width:100%;max-width:760px;margin:0 auto;padding:...}）；不要给 body 写 width/max-width/margin——渲染器会在外层保留桌面底色，并把容器限制在 760px 内。可以内联 <style>，但不要写 script、iframe，也不要用外链图片或字体（会被删除）。",
			"观感参考（手账只是其中一种，别套模板、别固定风格）：手账风＝米色纸底 + 胶带/时间轴 + 手写感标题；对比页＝两栏并排 + 色块区分优劣；时间线＝左侧时间列 + 竖向连线；一页纸总结＝大标题 + 三块要点。留白充足、统一配色，别做满屏边框。",
			"summary 必须一句话说清这页讲了什么（用户据此决定点不点），不要写「以下是对比」「详见下图」。",
		}, "\n"),
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"title":   map[string]interface{}{"type": "string", "description": fmt.Sprintf("页面标题，≤%d 字，会显示在卡片和右栏标签上", maxArtifactTitleLen)},
				"summary": map[string]interface{}{"type": "string", "description": fmt.Sprintf("一句话摘要，≤%d 字，说清这页讲了什么", maxArtifactSumLen)},
				"html":    map[string]interface{}{"type": "string", "description": "完整的 HTML 文档（含 <html>…</html>，样式内联在 <style> 里）"},
			},
			"required": []string{"title", "summary", "html"},
		},
	}}
}

func (t *HTMLArtifactTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	if funcName != "create" {
		return &Result{Success: false, Error: fmt.Sprintf("未知函数: %s", funcName)}
	}
	return t.create(ec, args)
}

func (t *HTMLArtifactTool) create(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
		HTML    string `json:"html"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Summary = strings.TrimSpace(req.Summary)
	if req.Title == "" || req.Summary == "" || strings.TrimSpace(req.HTML) == "" {
		return &Result{Success: false, Error: "title / summary / html 都是必填"}
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
	clean, err := htmlsanitize.Process(req.HTML)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if !t.allowOne(ec.TurnID) {
		return &Result{Success: false, Error: fmt.Sprintf("本轮已生成 %d 张页面，够了；如需更多请先问用户", maxArtifactsPerTurn)}
	}

	a := &model.Artifact{
		ID:             uuid.NewString(),
		UserID:         ec.UserID,
		ConversationID: ec.ConvID,
		Title:          req.Title,
		Summary:        req.Summary,
		HTML:           clean,
	}
	if err := t.repo.Create(ec.Ctx, a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("保存页面失败: %v", err)}
	}

	data, err := json.Marshal(map[string]any{
		"artifact_id": a.ID,
		"type":        model.ArtifactTypeHTML,
		"title":       a.Title,
		"summary":     a.Summary,
	})
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("结果序列化失败: %v", err)}
	}
	return &Result{Success: true, Data: data}
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
