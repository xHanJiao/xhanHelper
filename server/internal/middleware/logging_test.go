package middleware

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// runLogger 跑一次中间件，返回日志内容 + handler 实际读到的请求体
func runLogger(t *testing.T, contentType string, body string) (string, string) {
	t.Helper()
	return runLoggerAt(t, "/x", contentType, body)
}

func runLoggerAt(t *testing.T, path, contentType string, body string) (string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	var buf bytes.Buffer
	var handlerBody string

	r := gin.New()
	r.Use(RequestLogger(log.New(&buf, "", 0)))
	r.POST(path, func(c *gin.Context) {
		b, _ := io.ReadAll(c.Request.Body)
		handlerBody = string(b)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	r.ServeHTTP(httptest.NewRecorder(), req)

	return buf.String(), handlerBody
}

// 凭据类请求体（登录/注册/改密）不能进日志；同时不能吞掉 body（handler 仍需完整内容）
func TestRequestLoggerRedactsAuthBodies(t *testing.T) {
	const payload = `{"username":"u","password":"s3cret-value"}`
	for _, path := range []string{"/api/auth/login", "/api/auth/register", "/api/auth/change-password"} {
		got, handlerBody := runLoggerAt(t, path, "application/json", payload)
		if strings.Contains(got, "s3cret-value") {
			t.Fatalf("%s 密码泄漏进日志: %q", path, got)
		}
		if !strings.Contains(got, "<redacted:auth>") {
			t.Fatalf("%s 日志未标记打码: %q", path, got)
		}
		if handlerBody != payload {
			t.Fatalf("%s handler 读到的请求体被破坏: %q", path, handlerBody)
		}
	}
}

// 文本请求体照常记入日志，且 handler 仍能读到完整内容
func TestRequestLoggerKeepsTextBody(t *testing.T) {
	const payload = `{"message":"你好"}`
	got, handlerBody := runLogger(t, "application/json; charset=utf-8", payload)

	if !strings.Contains(got, payload) {
		t.Fatalf("日志未包含请求体: %q", got)
	}
	if handlerBody != payload {
		t.Fatalf("handler 读到的请求体被破坏: %q", handlerBody)
	}
}

// 二进制请求体只记 Content-Type —— 记内容会把日志刷成乱码，把真实错误顶出视野
func TestRequestLoggerSkipsBinaryBody(t *testing.T) {
	// PNG 魔数（真图会被 http.DetectContentType 认成 image/png）
	body := "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR binary garbage"
	got, handlerBody := runLogger(t, "multipart/form-data; boundary=xhanBoundary", body)

	if !strings.Contains(got, "body=<multipart/form-data; boundary=xhanBoundary>") {
		t.Fatalf("二进制请求体未被跳过: %q", got)
	}
	if strings.Contains(got, "IHDR") {
		t.Fatalf("二进制内容泄漏进日志: %q", got)
	}
	if handlerBody != body {
		t.Fatalf("handler 读到的请求体被破坏: %q", handlerBody)
	}
}
