package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

// WebFetchTool — 抓取 URL 网页正文，剥离 HTML 标签返回纯文本。
// 仅 subAgent（explore）可见：注册时 HideTool（见 appinit/tools.go）。
type WebFetchTool struct {
	client   *http.Client
	maxBytes int
}

// NewWebFetchTool 创建 webfetch 工具。timeoutSecs/maxBytes 为 0 时用默认值（15s / 51200）。
func NewWebFetchTool(timeoutSecs int, maxBytes int) *WebFetchTool {
	if timeoutSecs <= 0 {
		timeoutSecs = 15
	}
	if maxBytes <= 0 {
		maxBytes = 51200
	}
	return &WebFetchTool{
		client:   &http.Client{Timeout: time.Duration(timeoutSecs) * time.Second},
		maxBytes: maxBytes,
	}
}

func (t *WebFetchTool) Name() string { return "webfetch" }

func (t *WebFetchTool) Description() string {
	return "抓取指定 URL 的网页正文，返回纯文本（HTML 已剥离标签）。【成本警告】抓取结果会全部注入 LLM 上下文，非常消耗 token：一次抓取可达 5 万字符。仅在必须读取具体页面原文时使用，优先选择汇总页/官方文档/百科等高效来源，避免逐页深挖多个 URL；需要 JS 渲染的动态页面请用 browser 工具。"
}

func (t *WebFetchTool) ReadOnlyFuncs() map[string]bool { return AllReadOnly(t) }

func (t *WebFetchTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "fetch_url",
		Display:     "抓取网页",
		Description: "抓取指定 URL 的网页内容，返回剥离 HTML 标签后的纯文本。注意：结果会全部进入上下文，token 消耗大（单页可达 5 万字符），只读最有价值的一两个页面，不要连续抓取多个 URL。",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "要抓取的网页 URL（完整的 https?:// 地址）",
				},
			},
			"required": []interface{}{"url"},
		},
	}}
}

func (t *WebFetchTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "fetch_url":
		return t.fetchURL(ec.Ctx, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("未知函数: %s", funcName)}
	}
}

// fetchURL 抓取 URL 并返回 JSON：{"url","title","text"}。
func (t *WebFetchTool) fetchURL(ctx context.Context, args json.RawMessage) *Result {
	var params struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &params); err != nil || params.URL == "" {
		return &Result{Success: false, Error: "参数解析失败: url 必填"}
	}
	u, err := url.Parse(params.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return &Result{Success: false, Error: fmt.Sprintf("无效的 URL（仅支持 http/https）: %s", params.URL)}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", params.URL, nil)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("创建请求失败: %v", err)}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; xhanHelper/1.0)")

	resp, err := t.client.Do(req)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("抓取失败: %v", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return &Result{Success: false, Error: fmt.Sprintf("HTTP %d: 页面返回错误状态码", resp.StatusCode)}
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.HasPrefix(ct, "text/html") && !strings.HasPrefix(ct, "text/plain") {
		return &Result{Success: false, Error: fmt.Sprintf("内容类型为 %s，请改用 browser 工具访问该页面", ct)}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(t.maxBytes)+1))
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("读取响应失败: %v", err)}
	}
	truncated := len(body) > t.maxBytes

	title, text := "", strings.TrimSpace(string(body))
	if strings.HasPrefix(ct, "text/html") {
		title, text = html2text(body)
	}

	if truncated || len(text) > t.maxBytes {
		text = truncateUTF8(text, t.maxBytes) + "[内容已截断]"
	}

	data, _ := json.Marshal(map[string]string{
		"url":   params.URL,
		"title": title,
		"text":  text,
	})
	return &Result{Success: true, Data: data}
}

// truncateUTF8 按字节截断并清理边界上的非法 UTF-8，避免切在多字节字符中间。
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return strings.ToValidUTF8(s[:maxBytes], "")
}

// html2text 用 Tokenizer 抽取正文文本，同时捕获 <title> 内容。
// 跳过 script/style/noscript；块级标签处换行；文本间用空格分隔。
func html2text(body []byte) (title, text string) {
	z := html.NewTokenizer(bytes.NewReader(body))
	var buf, titleBuf strings.Builder
	skipDepth := 0
	inTitle := false
	var last rune

	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return strings.TrimSpace(titleBuf.String()), strings.TrimSpace(buf.String())
		case html.StartTagToken, html.SelfClosingTagToken:
			raw, _ := z.TagName()
			tag := string(bytes.ToLower(raw))
			if tag == "title" {
				inTitle = true
			}
			if tag == "script" || tag == "style" || tag == "noscript" {
				skipDepth++
			}
			if tag == "br" || tag == "p" || tag == "div" || tag == "li" ||
				tag == "h1" || tag == "h2" || tag == "h3" || tag == "h4" {
				buf.WriteRune('\n')
			}
		case html.EndTagToken:
			raw, _ := z.TagName()
			tag := string(bytes.ToLower(raw))
			if tag == "title" {
				inTitle = false
			}
			if tag == "script" || tag == "style" || tag == "noscript" {
				if skipDepth > 0 {
					skipDepth--
				}
			}
		case html.TextToken:
			if inTitle {
				titleBuf.WriteString(strings.TrimSpace(string(z.Text())))
				continue
			}
			if skipDepth == 0 {
				trimmed := strings.TrimSpace(string(z.Text()))
				if trimmed != "" {
					runes := []rune(trimmed)
					if last != 0 && !unicode.IsSpace(last) {
						buf.WriteRune(' ')
					}
					buf.WriteString(trimmed)
					last = runes[len(runes)-1]
				}
			}
		}
	}
}
