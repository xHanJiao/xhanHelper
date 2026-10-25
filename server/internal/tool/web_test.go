package tool

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"xhan-helper/internal/config"
)

// fakeWebQuota 模拟 SearchUsageRepo：limit 次以内成功，超出返回「已用完」。
type fakeWebQuota struct {
	limit int
	used  int32
	err   error
}

func (f *fakeWebQuota) TryUse(_ context.Context, _ string, totalQuota int, _ time.Time, _ int) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	n := atomic.AddInt32(&f.used, 1)
	// 与 SearchUsageRepo 一致：超限时返回当前总配额（used == totalQuota），供调用方区分「超限」和「故障」
	if int(n) > f.limit {
		return totalQuota, errors.New("搜索次数已用完")
	}
	return int(n), nil
}

// newWebTestTool 建一个指向 httptest 的 WebTool，返回工具与命中计数。
func newWebTestTool(t *testing.T, handler http.HandlerFunc, mutate func(*config.WebToolsConfig), quota WebQuota) (*WebTool, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	cfg := config.WebToolsConfig{
		Enabled:         true,
		APIKey:          "test-key",
		BaseURL:         srv.URL,
		TimeoutSeconds:  5,
		SearchCount:     8,
		MaxContentChars: 8000,
		DailyLimit:      100,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	tool := NewWebTool(cfg, quota)
	tool.retryDelay = time.Millisecond
	return tool, &hits
}

func execWeb(t *testing.T, tool *WebTool, fn string, args string) *Result {
	t.Helper()
	return tool.Execute(&ExecuteContext{Ctx: context.Background()}, fn, json.RawMessage(args))
}

func decodeWebData(t *testing.T, res *Result, dst interface{}) {
	t.Helper()
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}
	if err := json.Unmarshal(res.Data, dst); err != nil {
		t.Fatalf("decode data: %v (%s)", err, string(res.Data))
	}
}

func TestWebTool_CatalogAndReadOnly(t *testing.T) {
	tool, _ := newWebTestTool(t, func(http.ResponseWriter, *http.Request) {}, nil, nil)

	entry, ok := tool.Catalog()
	if !ok {
		t.Fatal("web 应实现 CatalogProvider")
	}
	if len(entry.Funcs) != 2 || entry.Funcs[0] != "search" || entry.Funcs[1] != "fetch" {
		t.Errorf("catalog funcs = %v", entry.Funcs)
	}
	if entry.ShortDesc == "" {
		t.Error("catalog 缺 ShortDesc")
	}
	ro := tool.ReadOnlyFuncs()
	if !ro["search"] || !ro["fetch"] {
		t.Errorf("search/fetch 必须只读（plan 模式依赖）: %v", ro)
	}
}

func TestWebSearch_RequestAndShaping(t *testing.T) {
	var got map[string]interface{}
	tool, _ := newWebTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/tools/search" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		// 响应里塞上需要被瘦身掉的字段
		_, _ = w.Write([]byte(`{"results":[{
			"rerank_score":2.4,"title":"T","url":"https://e.com/a","snippet":"S",
			"summary":"长篇摘要","content":"正文","site_name":"Example","site_icon":"https://e.com/f.ico",
			"date":"2026-06-01T00:00:00+00:00","authority":3}]}`))
	}, nil, nil)

	res := execWeb(t, tool, "search", `{"query":"deepseek 发布","count":5,"need_content":true}`)
	var out struct {
		Results []map[string]interface{} `json:"results"`
	}
	decodeWebData(t, res, &out)

	if got["query"] != "deepseek 发布" || got["count"] != float64(5) || got["need_content"] != true {
		t.Errorf("request body = %v", got)
	}
	if len(out.Results) != 1 {
		t.Fatalf("results len = %d", len(out.Results))
	}
	item := out.Results[0]
	for _, banned := range []string{"snippet", "authority", "rerank_score", "site_icon"} {
		if _, ok := item[banned]; ok {
			t.Errorf("返回应瘦身，不应包含 %s", banned)
		}
	}
	if item["summary"] != "长篇摘要" || item["content"] != "正文" || item["site_name"] != "Example" {
		t.Errorf("item = %v", item)
	}
}

func TestWebSearch_DefaultsAndClamp(t *testing.T) {
	var got map[string]interface{}
	tool, _ := newWebTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}, nil, nil)

	execWeb(t, tool, "search", `{"query":"x"}`)
	if got["count"] != float64(8) {
		t.Errorf("不传 count 应使用配置的 8，got %v", got["count"])
	}

	execWeb(t, tool, "search", `{"query":"x","count":99}`)
	if got["count"] != float64(webMaxCount) {
		t.Errorf("count 应夹到 %d，got %v", webMaxCount, got["count"])
	}

	long := strings.Repeat("长", webMaxQueryRunes+50)
	execWeb(t, tool, "search", `{"query":"`+long+`"}`)
	q, _ := got["query"].(string)
	if n := utf8.RuneCountInString(q); n != webMaxQueryRunes {
		t.Errorf("query 应截到 %d 字符，got %d", webMaxQueryRunes, n)
	}
}

func TestWebSearch_EmptyResults(t *testing.T) {
	tool, _ := newWebTestTool(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	}, nil, nil)

	res := execWeb(t, tool, "search", `{"query":"没有这种东西"}`)
	var out struct {
		Results []interface{} `json:"results"`
		Note    string        `json:"note"`
	}
	decodeWebData(t, res, &out)
	if out.Results == nil || len(out.Results) != 0 {
		t.Errorf("空结果应为空数组，got %v", out.Results)
	}
	if out.Note == "" {
		t.Error("空结果应给出换词提示")
	}
}

func TestWebSearch_BudgetTruncation(t *testing.T) {
	tool, _ := newWebTestTool(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[
			{"title":"1","url":"u1","summary":"一二三四五六七八九十"},
			{"title":"2","url":"u2","summary":"一二三四五六七八九十"},
			{"title":"3","url":"u3","summary":"一二三四五六七八九十"}]}`))
	}, func(c *config.WebToolsConfig) { c.MaxContentChars = 20 }, nil)

	res := execWeb(t, tool, "search", `{"query":"x"}`)
	var out struct {
		Results   []map[string]interface{} `json:"results"`
		Truncated bool                     `json:"truncated"`
	}
	decodeWebData(t, res, &out)
	if len(out.Results) != 2 || !out.Truncated {
		t.Errorf("预算 20 应装下 2 条并标记截断，got len=%d truncated=%v", len(out.Results), out.Truncated)
	}
}

func TestWebSearch_BadArgsAndEmptyQuery(t *testing.T) {
	tool, hits := newWebTestTool(t, func(http.ResponseWriter, *http.Request) {}, nil, nil)

	if res := execWeb(t, tool, "search", `{"query":"   "}`); res.Success {
		t.Error("空白 query 应失败")
	}
	if res := execWeb(t, tool, "search", `{"query":"x","unknown":1}`); res.Success {
		t.Error("未知参数应失败（decodeArgs 严格模式）")
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Error("参数校验失败不应发请求")
	}
}

func TestWebSearch_RetryOn429(t *testing.T) {
	var calls int32
	tool, _ := newWebTestTool(t, func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"title":"T","url":"u","summary":"s"}]}`))
	}, nil, nil)

	res := execWeb(t, tool, "search", `{"query":"x"}`)
	if !res.Success {
		t.Fatalf("429 后重试应成功: %s", res.Error)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("应一共请求 2 次，got %d", calls)
	}
}

func TestWebSearch_ErrorMessages(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusPaymentRequired, "余额不足"},
		{http.StatusForbidden, "拒绝访问"},
		{http.StatusNotFound, "页面不存在"},
		{http.StatusBadRequest, "请求不合法"},
	}
	for _, c := range cases {
		tool, _ := newWebTestTool(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(`{"error":{"message":"'count' must be between 1 and 50 (request_id: abc12345-6789)"}}`))
		}, nil, nil)
		res := execWeb(t, tool, "search", `{"query":"x"}`)
		if res.Success || !strings.Contains(res.Error, c.want) {
			t.Errorf("status %d: want error containing %q, got %q", c.status, c.want, res.Error)
		}
		// 失败结果要带可机器判断的字段，且 request_id 单独透出
		var detail struct {
			UpstreamStatus int    `json:"upstream_status"`
			Reason         string `json:"reason"`
			RequestID      string `json:"request_id"`
		}
		if err := json.Unmarshal(res.Data, &detail); err != nil {
			t.Errorf("status %d: 失败结果应带结构化 Data: %v", c.status, err)
			continue
		}
		if detail.UpstreamStatus != c.status || detail.Reason == "" {
			t.Errorf("status %d: detail = %+v", c.status, detail)
		}
		if detail.RequestID != "abc12345-6789" {
			t.Errorf("status %d: request_id = %q", c.status, detail.RequestID)
		}
	}
}

func TestWeb_QuotaExhausted(t *testing.T) {
	tool, hits := newWebTestTool(t, func(http.ResponseWriter, *http.Request) {}, nil, &fakeWebQuota{limit: 0})

	for _, fn := range []string{"search", "fetch"} {
		args := `{"query":"x"}`
		if fn == "fetch" {
			args = `{"url":"https://e.com/a"}`
		}
		res := execWeb(t, tool, fn, args)
		if res.Success {
			t.Fatalf("%s 额度耗尽应失败", fn)
		}
		if !strings.Contains(res.Error, "额度已用完") || !strings.Contains(res.Error, "web_explore") {
			t.Errorf("%s 错误文案应给出额度与降级路径，got %q", fn, res.Error)
		}
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Error("额度耗尽不应发请求")
	}
}

func TestWeb_QuotaUnlimited(t *testing.T) {
	tool, _ := newWebTestTool(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"title":"","content":""}`))
	}, func(c *config.WebToolsConfig) { c.DailyLimit = -1 }, nil)

	if res := execWeb(t, tool, "fetch", `{"url":"https://e.com/a"}`); !res.Success {
		t.Errorf("daily_limit<0 应不限额: %s", res.Error)
	}
}

func TestWebFetch_SuccessAndTruncate(t *testing.T) {
	tool, _ := newWebTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tools/fetch" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"title":"文章","content":"一二三四五六七八九十一二三四五六七八九十"}`))
	}, func(c *config.WebToolsConfig) { c.MaxContentChars = 10 }, nil)

	res := execWeb(t, tool, "fetch", `{"url":"https://e.com/a"}`)
	var out struct {
		URL       string `json:"url"`
		Title     string `json:"title"`
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
	}
	decodeWebData(t, res, &out)
	if out.Title != "文章" || out.URL != "https://e.com/a" {
		t.Errorf("out = %+v", out)
	}
	if !out.Truncated || utf8.RuneCountInString(out.Content) != 10 {
		t.Errorf("应截到 10 字符并标记，got len=%d truncated=%v", utf8.RuneCountInString(out.Content), out.Truncated)
	}
}

func TestWebFetch_InvalidURL(t *testing.T) {
	tool, hits := newWebTestTool(t, func(http.ResponseWriter, *http.Request) {}, nil, nil)

	for _, u := range []string{"", "ftp://e.com/a", "e.com/a", "https://user:pass@e.com/a"} {
		res := execWeb(t, tool, "fetch", `{"url":"`+u+`"}`)
		if res.Success {
			t.Errorf("url %q 应被拒", u)
		}
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Error("非法 URL 不应发请求")
	}
}

func TestWeb_UnknownFunction(t *testing.T) {
	tool, _ := newWebTestTool(t, func(http.ResponseWriter, *http.Request) {}, nil, nil)
	if res := execWeb(t, tool, "crawl", `{}`); res.Success {
		t.Error("未知函数应失败")
	}
}

// ---- 回归修复：错误分类 / 转义还原 / 结果清洗 ----

func TestWebFetch_404ClassifiedByProbe(t *testing.T) {
	tool, _ := newWebTestTool(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"failed to fetch URL (request_id: b05b7e7a-3408-4d51)"}}`))
	}, nil, nil)
	tool.probe = func(context.Context, string) (string, string) {
		return "unsupported_content", "Content-Type: application/pdf"
	}

	res := execWeb(t, tool, "fetch", `{"url":"https://arxiv.org/pdf/1706.03762"}`)
	if res.Success {
		t.Fatal("上游 404 必须返回失败")
	}
	if !strings.Contains(res.Error, "不是可抓取") || !strings.Contains(res.Error, "application/pdf") {
		t.Errorf("错误文案应透出分类与探测细节: %q", res.Error)
	}
	var detail struct {
		Status int    `json:"upstream_status"`
		Reason string `json:"reason"`
		RID    string `json:"request_id"`
	}
	if err := json.Unmarshal(res.Data, &detail); err != nil {
		t.Fatalf("失败结果应带结构化 Data: %v", err)
	}
	if detail.Status != http.StatusNotFound || detail.Reason != "unsupported_content" || detail.RID == "" {
		t.Errorf("detail = %+v", detail)
	}
	if strings.Contains(res.Error, "b05b7e7a-3408-4d51）失败") {
		t.Error("request_id 不应混进上游 message 原文")
	}
}

func TestWebFetch_ErrorPageDetected(t *testing.T) {
	body := "Search \\/ Fetch API is now available\\, powered by our in\\-house search engine\\.\n\n\\# Page Not Found\n\nWe could not find what you were looking for\\."
	tool, _ := newWebTestTool(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"title": "DeepSeek API Docs", "content": body})
	}, nil, nil)

	res := execWeb(t, tool, "fetch", `{"url":"https://api-docs.deepseek.com/this-page-does-not-exist-xyz"}`)
	if res.Success {
		t.Fatalf("200 + 错误页必须判为失败，got success data=%s", string(res.Data))
	}
	if !strings.Contains(res.Error, "错误页") {
		t.Errorf("错误文案应说明是错误页: %q", res.Error)
	}
	var detail struct {
		Status int    `json:"upstream_status"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(res.Data, &detail); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if detail.Status != http.StatusOK || detail.Reason != "error_page" {
		t.Errorf("detail = %+v", detail)
	}
	// 转义也要还原，文案里不该出现反斜杠
	if strings.Contains(res.Error, `\`) {
		t.Errorf("错误文案不应带转义反斜杠: %q", res.Error)
	}
}

func TestWebFetch_MarkdownUnescape(t *testing.T) {
	// 上游实测形态：JSON 与 Markdown 结构全被转义
	content := "\\{\\\"id\\\"\\: 1\\,\n\\# 标题\n\\| a \\| b \\|"
	tool, _ := newWebTestTool(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"title": `T\-1`, "content": content})
	}, nil, nil)

	res := execWeb(t, tool, "fetch", `{"url":"https://e.com/a"}`)
	var out struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	decodeWebData(t, res, &out)
	if out.Title != "T-1" {
		t.Errorf("title 应还原转义，got %q", out.Title)
	}
	for _, want := range []string{`{"id": 1,`, "# 标题", "| a | b |"} {
		if !strings.Contains(out.Content, want) {
			t.Errorf("正文应还原出 %q，got %q", want, out.Content)
		}
	}
}

func TestWebSearch_CleanupAndUnescape(t *testing.T) {
	tool, _ := newWebTestTool(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[
			{"title":"","url":"https://drop.me/1","summary":"空标题","rerank_score":2.0},
			{"title":"A","url":"https://a.com/x?utm_source=wx&partner=9&id=7","summary":"摘要A","rerank_score":1.8},
			{"title":"A重复","url":"https://a.com/x?id=7","summary":"同地址","rerank_score":1.7},
			{"title":"B","url":"https://b.com/y","summary":"摘要B","content":"\\# 标题","rerank_score":1.6},
			{"title":"C","url":"https://c.com/z","summary":"摘要C","rerank_score":1.5},
			{"title":"D","url":"https://d.com/w","summary":"低分","rerank_score":0.4}
		]}`))
	}, nil, nil)

	res := execWeb(t, tool, "search", `{"query":"x","need_content":true}`)
	var out struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	decodeWebData(t, res, &out)
	if len(out.Results) != 3 {
		t.Fatalf("应丢空标题/去重/滤低分后剩 3 条，got %d: %+v", len(out.Results), out.Results)
	}
	if out.Results[0].URL != "https://a.com/x?id=7" {
		t.Errorf("URL 应剥掉跟踪参数，got %q", out.Results[0].URL)
	}
	foundUnescaped := false
	for _, r := range out.Results {
		if r.Title == "B" {
			if !strings.Contains(r.Content, "# 标题") {
				t.Errorf("search 正文也应还原转义，got %q", r.Content)
			}
			foundUnescaped = true
		}
	}
	if !foundUnescaped {
		t.Error("应保留 B 条结果")
	}
}

func TestProbeFetchTarget_RejectsPrivateAddress(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:9/x", "http://10.0.0.1/x", "http://169.254.169.254/latest/meta-data"} {
		reason, detail := probeFetchTarget(context.Background(), u)
		if reason != "private_address" {
			t.Errorf("%s 应判为 private_address，got %q (%s)", u, reason, detail)
		}
	}
}

func TestCleanSearchURL(t *testing.T) {
	cases := map[string]string{
		"https://a.com/x?utm_source=wx&partner=9&id=7": "https://a.com/x?id=7",
		"https://a.com/x?id=7":                         "https://a.com/x?id=7",
		"https://a.com/x":                              "https://a.com/x",
	}
	for in, want := range cases {
		if got := cleanSearchURL(in); got != want {
			t.Errorf("cleanSearchURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLooksLikeWebErrorPage(t *testing.T) {
	if !looksLikeWebErrorPage("DeepSeek API Docs", "# Page Not Found\n\nWe could not find...") {
		t.Error("短正文 + not-found 文案应判为错误页")
	}
	if looksLikeWebErrorPage("T", strings.Repeat("正文", 500)+"page not found") {
		t.Error("长正文不应判为错误页")
	}
	if looksLikeWebErrorPage("T", "一篇正常短文，讲 404 状态码的用法和排查") {
		t.Error("正常短文（即便提到 404）在缺少 not-found 句式时不应误判")
	}
}
