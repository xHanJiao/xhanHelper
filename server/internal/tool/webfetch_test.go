package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func webfetchTestTool(t *testing.T) *WebFetchTool {
	t.Helper()
	return NewWebFetchTool(5, 51200)
}

func execFetch(t *testing.T, tool *WebFetchTool, url string) *Result {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"url": url})
	return tool.Execute(&ExecuteContext{Ctx: context.Background()}, "fetch_url", args)
}

func parseFetchData(t *testing.T, res *Result) map[string]string {
	t.Helper()
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}
	var m map[string]string
	if err := json.Unmarshal(res.Data, &m); err != nil {
		t.Fatalf("unmarshal data error: %v, raw=%s", err, res.Data)
	}
	return m
}

func TestWebFetch_NormalHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><head><title>测试页面</title>
<script>var x = "should not appear";</script>
<style>.foo { color: red; }</style>
</head><body><h1>标题一</h1><p>第一段</p><p>第二段</p></body></html>`))
	}))
	defer srv.Close()

	res := execFetch(t, webfetchTestTool(t), srv.URL)
	m := parseFetchData(t, res)
	if m["title"] != "测试页面" {
		t.Errorf("title = %q, want 测试页面", m["title"])
	}
	if strings.Contains(m["text"], "should not appear") || strings.Contains(m["text"], "color: red") {
		t.Errorf("text 不应包含 script/style 内容: %s", m["text"])
	}
	for _, want := range []string{"标题一", "第一段", "第二段"} {
		if !strings.Contains(m["text"], want) {
			t.Errorf("text 缺少 %q: %s", want, m["text"])
		}
	}
}

func TestWebFetch_PlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("纯文本内容 direct"))
	}))
	defer srv.Close()

	m := parseFetchData(t, execFetch(t, webfetchTestTool(t), srv.URL))
	if m["text"] != "纯文本内容 direct" {
		t.Errorf("text = %q, want 原文直通", m["text"])
	}
}

func TestWebFetch_NonHTMLType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"a":1}`))
	}))
	defer srv.Close()

	res := execFetch(t, webfetchTestTool(t), srv.URL)
	if res.Success {
		t.Fatal("非 HTML 类型应失败")
	}
	if !strings.Contains(res.Error, "browser") {
		t.Errorf("error 应提示改用 browser: %s", res.Error)
	}
}

func TestWebFetch_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	res := execFetch(t, webfetchTestTool(t), srv.URL)
	if res.Success {
		t.Fatal("404 应失败")
	}
	if !strings.Contains(res.Error, "404") {
		t.Errorf("error 应含状态码 404: %s", res.Error)
	}
}

func TestWebFetch_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	tool := NewWebFetchTool(1, 51200)
	start := time.Now()
	res := execFetch(t, tool, srv.URL)
	if res.Success {
		t.Fatal("超时应失败")
	}
	if time.Since(start) > 1500*time.Millisecond {
		t.Errorf("超时兜底耗时过长: %v", time.Since(start))
	}
}

func TestWebFetch_Truncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head><title>t</title></head><body><p>" + strings.Repeat("字", 20000) + "</p></body></html>"))
	}))
	defer srv.Close()

	tool := NewWebFetchTool(5, 1000)
	res := execFetch(t, tool, srv.URL)
	if !res.Success {
		t.Fatalf("截断场景应成功: %v", res.Error)
	}
	var m map[string]string
	if err := json.Unmarshal(res.Data, &m); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(m["text"], "[内容已截断]") {
		t.Errorf("text 应以截断标记结尾: %s...", m["text"])
	}
}

func TestWebFetch_EmptyPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("   \n  "))
	}))
	defer srv.Close()

	m := parseFetchData(t, execFetch(t, webfetchTestTool(t), srv.URL))
	if m["text"] != "" {
		t.Errorf("空白页面 text 应为空, got %q", m["text"])
	}
}

func TestWebFetch_InvalidURL(t *testing.T) {
	res := execFetch(t, webfetchTestTool(t), "ftp://example.com/x")
	if res.Success {
		t.Fatal("非 http(s) URL 应失败")
	}
	res = execFetch(t, webfetchTestTool(t), "example.com/xxx")
	if res.Success {
		t.Fatal("无 scheme URL 应失败")
	}
}

func TestWebFetch_UnknownFunction(t *testing.T) {
	tool := webfetchTestTool(t)
	res := tool.Execute(&ExecuteContext{Ctx: context.Background()}, "nope", nil)
	if res.Success || !strings.Contains(res.Error, "未知函数") {
		t.Errorf("未知函数应报错: %+v", res)
	}
}
