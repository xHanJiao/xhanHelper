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
		SearchMaxChars:  16000,
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
	if item["content"] != "正文" || item["site_name"] != "Example" {
		t.Errorf("item = %v", item)
	}
	// need_content=true 且拿到正文时，summary 与正文重复，不应再给
	if _, ok := item["summary"]; ok {
		t.Error("正文存在时不应同时返回 summary（同一篇正文占两遍预算）")
	}
}

func TestWebSearch_DefaultsAndClamp(t *testing.T) {
	var got map[string]interface{}
	tool, hits := newWebTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}, nil, nil)

	execWeb(t, tool, "search", `{"query":"x"}`)
	if got["count"] != float64(8) {
		t.Errorf("不传 count 应使用配置的 8，got %v", got["count"])
	}

	// 显式 count=0 必须报错（不能静默按默认），且不发请求
	before := atomic.LoadInt32(hits)
	if res := execWeb(t, tool, "search", `{"query":"x","count":0}`); res.Success {
		t.Error("count=0 应报错")
	}
	if atomic.LoadInt32(hits) != before {
		t.Error("count 非法不应发请求")
	}

	// 超上限夹取，但要回显 requested_count
	res := execWeb(t, tool, "search", `{"query":"x","count":99}`)
	if got["count"] != float64(webMaxCount) {
		t.Errorf("count 应夹到 %d，got %v", webMaxCount, got["count"])
	}
	var out struct {
		RequestedCount int `json:"requested_count"`
	}
	decodeWebData(t, res, &out)
	if out.RequestedCount != 99 {
		t.Errorf("应回显 requested_count=99，got %d", out.RequestedCount)
	}

	// 超长 query 必须报错（静默截断会把整段长句当关键词，结果完全不相关）
	long := strings.Repeat("长", webMaxQueryRunes+50)
	before = atomic.LoadInt32(hits)
	res = execWeb(t, tool, "search", `{"query":"`+long+`"}`)
	if res.Success || !strings.Contains(res.Error, "过长") {
		t.Errorf("超长 query 应报错，got %q", res.Error)
	}
	if atomic.LoadInt32(hits) != before {
		t.Error("超长 query 不应发请求")
	}
}

func TestWebSearch_RecentWindow(t *testing.T) {
	var got map[string]interface{}
	tool, _ := newWebTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}, nil, nil)

	res := execWeb(t, tool, "search", `{"query":"重庆 二手房","recent":"month"}`)
	q, _ := got["query"].(string)
	want := "after:" + time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	if !strings.Contains(q, want) {
		t.Errorf("recent=month 应追加 %q，got query=%q", want, q)
	}
	if strings.Count(q, "after:") != 1 {
		t.Errorf("after: 只应出现一次，got %q", q)
	}

	var out struct {
		Today string `json:"today"`
	}
	decodeWebData(t, res, &out)
	if out.Today != time.Now().Format("2006-01-02") {
		t.Errorf("结果应回显 today，got %q", out.Today)
	}

	// query 里已有限定就不重复加
	execWeb(t, tool, "search", `{"query":"x after:2025-01-01","recent":"week"}`)
	q, _ = got["query"].(string)
	if strings.Count(q, "after:") != 1 {
		t.Errorf("已有限定不应重复追加，got %q", q)
	}

	if res := execWeb(t, tool, "search", `{"query":"x","recent":"hour"}`); res.Success {
		t.Error("非法 recent 应报错")
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
	}, func(c *config.WebToolsConfig) { c.SearchMaxChars = 20 }, nil)

	res := execWeb(t, tool, "search", `{"query":"x"}`)
	var out struct {
		Results       []map[string]interface{} `json:"results"`
		Truncated     bool                     `json:"truncated"`
		Note          string                   `json:"note"`
		ReturnedCount int                      `json:"returned_count"`
	}
	decodeWebData(t, res, &out)
	if len(out.Results) != 2 || !out.Truncated || out.ReturnedCount != 2 {
		t.Errorf("预算 20 应装下 2 条并标记截断，got len=%d truncated=%v returned=%d", len(out.Results), out.Truncated, out.ReturnedCount)
	}
	if !strings.Contains(out.Note, "2/3") || !strings.Contains(out.Note, "减少 count") {
		t.Errorf("截断说明要给出生效条数与可执行的建议，got note=%q", out.Note)
	}
	if strings.Contains(out.Note, "search_max_chars") {
		t.Errorf("模型调不到 search_max_chars，不该出现在对模型的文案里: %q", out.Note)
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
			Stage     string `json:"stage"`
			APIStatus int    `json:"api_status"`
			Reason    string `json:"reason"`
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(res.Data, &detail); err != nil {
			t.Errorf("status %d: 失败结果应带结构化 Data: %v", c.status, err)
			continue
		}
		if detail.Stage != "api" || detail.APIStatus != c.status || detail.Reason == "" {
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
	tool.probe = func(context.Context, string) (string, string, int) {
		return "unsupported_content", "Content-Type: application/pdf", http.StatusPartialContent // 206 是 Range 的产物，不该外泄
	}

	res := execWeb(t, tool, "fetch", `{"url":"https://arxiv.org/pdf/1706.03762"}`)
	if res.Success {
		t.Fatal("上游 404 必须返回失败")
	}
	if !strings.Contains(res.Error, "不是可抓取") || !strings.Contains(res.Error, "application/pdf") {
		t.Errorf("错误文案应透出分类与探测细节: %q", res.Error)
	}
	var detail struct {
		Stage        string `json:"stage"`
		APIStatus    int    `json:"api_status"`
		TargetStatus int    `json:"target_status"`
		Reason       string `json:"reason"`
		RID          string `json:"request_id"`
	}
	if err := json.Unmarshal(res.Data, &detail); err != nil {
		t.Fatalf("失败结果应带结构化 Data: %v", err)
	}
	if detail.Stage != "target" || detail.Reason != "unsupported_content" || detail.RID == "" {
		t.Errorf("detail = %+v", detail)
	}
	if detail.APIStatus != 0 {
		t.Errorf("结论来自目标站时 api_status 不该出现，got %d", detail.APIStatus)
	}
	if detail.TargetStatus != 0 {
		t.Errorf("2xx（206）不是失败原因，target_status 不该出现，got %d", detail.TargetStatus)
	}

	// 反例：目标站真返回 403（反爬）时，target_status 必须出现
	tool.probe = func(context.Context, string) (string, string, int) {
		return "blocked", "本地访问返回 403", http.StatusForbidden
	}
	res = execWeb(t, tool, "fetch", `{"url":"https://www.zhihu.com/question/1"}`)
	if err := json.Unmarshal(res.Data, &detail); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if detail.Stage != "target" || detail.TargetStatus != http.StatusForbidden || detail.APIStatus != 0 {
		t.Errorf("target 层失败应只带 target_status: %+v", detail)
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
		Stage     string `json:"stage"`
		APIStatus int    `json:"api_status"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(res.Data, &detail); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if detail.Stage != "page" || detail.Reason != "error_page" {
		t.Errorf("detail = %+v", detail)
	}
	if detail.APIStatus != 0 {
		t.Errorf("error_page 是内容质量问题，不该带 api_status，got %d", detail.APIStatus)
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
		reason, detail, status := probeFetchTarget(context.Background(), u)
		if reason != "private_address" {
			t.Errorf("%s 应判为 private_address，got %q (%s)", u, reason, detail)
		}
		if status != 0 {
			t.Errorf("%s 未发出请求，target_status 应为 0，got %d", u, status)
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

// TestFailResult_StatusOmission — stage 与状态码的可见性规则。
// 回归：api_status 曾在 DNS/超时/PDF 这些「上游没有对应 HTTP 状态」的场景里占位 404。
func TestFailResult_StatusOmission(t *testing.T) {
	tool, _ := newWebTestTool(t, func(http.ResponseWriter, *http.Request) {}, nil, nil)

	cases := []struct {
		name       string
		stage      string
		api        int
		target     int
		reason     string
		wantAPI    int
		wantTarget int
	}{
		{"DeepSeek 402", "api", 402, 0, "insufficient_balance", 402, 0},
		{"DNS 失败（DeepSeek 回了 404，但结论来自目标站）", "target", 404, 0, "dns_error", 0, 0},
		{"目标站超时", "target", 404, 0, "timeout", 0, 0},
		{"目标站 403（反爬）", "target", 404, 403, "blocked", 0, 403},
		{"PDF（探测拿到的 206 是 Range 产物，不外泄）", "target", 404, 206, "unsupported_content", 0, 0},
		{"软 404 错误页", "page", 200, 0, "error_page", 0, 0},
		{"私网地址拦截", "local", 0, 0, "private_address", 0, 0},
	}
	for _, c := range cases {
		res := tool.failResult(c.stage, c.api, c.target, c.reason, "detail", "rid")
		if res.Success {
			t.Fatalf("%s: 应判失败", c.name)
		}
		var d struct {
			Stage        string `json:"stage"`
			APIStatus    int    `json:"api_status"`
			TargetStatus int    `json:"target_status"`
		}
		if err := json.Unmarshal(res.Data, &d); err != nil {
			t.Fatalf("%s: decode: %v", c.name, err)
		}
		if d.Stage != c.stage || d.APIStatus != c.wantAPI || d.TargetStatus != c.wantTarget {
			t.Errorf("%s: got stage=%q api=%d target=%d, want stage=%q api=%d target=%d",
				c.name, d.Stage, d.APIStatus, d.TargetStatus, c.stage, c.wantAPI, c.wantTarget)
		}
	}
}

// TestClassifyTargetResponse — 目标站响应→reason 的纯函数映射（429 必须与 403 分开）。
func TestClassifyTargetResponse(t *testing.T) {
	cases := []struct {
		status int
		ct     string
		want   string
	}{
		{http.StatusTooManyRequests, "text/html", "rate_limited"},
		{http.StatusForbidden, "text/html", "blocked"},
		{http.StatusUnauthorized, "text/html", "blocked"},
		{http.StatusNotFound, "text/html", "not_found"},
		{http.StatusGone, "text/html", "not_found"},
		{http.StatusInternalServerError, "text/html", "target_error"},
		{http.StatusOK, "application/pdf", "unsupported_content"},
		{http.StatusPartialContent, "application/pdf", "unsupported_content"},
		{http.StatusOK, "text/html", "upstream_only"},
	}
	for _, c := range cases {
		if got, detail := classifyTargetResponse(c.status, c.ct); got != c.want {
			t.Errorf("status=%d ct=%q: got %q (%s), want %q", c.status, c.ct, got, detail, c.want)
		}
	}
}

// TestCleanArticleContent — 去掉页面外壳，但不动正文与 JSON/代码结构。
func TestCleanArticleContent(t *testing.T) {
	in := strings.Join([]string{
		"央行发布最新房贷利率政策",
		"",
		"",
		"",
		"记者今日从央行获悉，首套房贷利率下调 0.2 个百分点。",
		"打开APP",
		"评论 0",
		"点赞 1.2万",
		"即将离开，打开今日头条",
		"😀😀😀😀😀😀😀😀😀",
		"相关推荐",
		"这是一条不该被保留的推荐内容",
	}, "\n")
	got := cleanArticleContent(in)
	for _, banned := range []string{"打开APP", "评论 0", "点赞 1.2万", "即将离开", "😀", "相关推荐", "不该被保留"} {
		if strings.Contains(got, banned) {
			t.Errorf("应去掉 %q，got:\n%s", banned, got)
		}
	}
	if !strings.Contains(got, "首套房贷利率下调 0.2 个百分点") {
		t.Errorf("正文被误删:\n%s", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("连续空行应压缩:\n%s", got)
	}

	// JSON 不能被破坏（缩进结构 / 内容中的引号）
	jsonIn := "{\n  \"id\": 1,\n  \"empty\": \"\",\n  \"body\": \"正文内容\"\n}"
	if got := cleanArticleContent(jsonIn); got != jsonIn {
		t.Errorf("JSON 不应被改写:\nwant %q\ngot  %q", jsonIn, got)
	}

	// 长行里的「打开APP」只是正文提及，不该误删
	longLine := strings.Repeat("背景说明", 20) + "打开APP 的转化率是本次讨论重点"
	if got := cleanArticleContent(longLine); got != longLine {
		t.Errorf("长行不应被当作外壳删掉, got %q", got)
	}
}

// TestWebSearch_NeedContentFallsBackToSummary — 个别结果抽不出正文时，仍要能读到摘要。
func TestWebSearch_NeedContentFallsBackToSummary(t *testing.T) {
	tool, _ := newWebTestTool(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[
			{"title":"有正文","url":"https://a.com/1","summary":"摘要1","content":"正文1","rerank_score":1.8},
			{"title":"没正文","url":"https://a.com/2","summary":"摘要2","content":"","rerank_score":1.7}
		]}`))
	}, nil, nil)

	res := execWeb(t, tool, "search", `{"query":"x","need_content":true}`)
	var out struct {
		Results []struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
			Content string `json:"content"`
		} `json:"results"`
	}
	decodeWebData(t, res, &out)
	if len(out.Results) != 2 {
		t.Fatalf("results = %d", len(out.Results))
	}
	for _, r := range out.Results {
		switch r.Title {
		case "有正文":
			if r.Content == "" || r.Summary != "" {
				t.Errorf("有正文时只给 content: %+v", r)
			}
		case "没正文":
			if r.Summary == "" || r.Content != "" {
				t.Errorf("没正文时应回退到 summary: %+v", r)
			}
		}
	}
}
