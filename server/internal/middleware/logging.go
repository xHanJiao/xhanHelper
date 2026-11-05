package middleware

import (
	"bytes"
	"io"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// logBodyLimit — 请求体记入日志的字节上限
const logBodyLimit = 2000

// RequestLogger — HTTP/SSE 请求日志中间件
func RequestLogger(toolLogger *log.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		reqBody := peekBody(c)

		c.Next()

		duration := time.Since(start).Milliseconds()
		status := c.Writer.Status()

		if strings.Contains(c.Writer.Header().Get("Content-Type"), "text/event-stream") {
			toolLogger.Printf("[sse] %s %s status=%d duration=%dms body=%s",
				c.Request.Method, c.Request.URL.Path, status, duration, reqBody)
		} else {
			toolLogger.Printf("[http] %s %s status=%d duration=%dms query=%q body=%s",
				c.Request.Method, c.Request.URL.Path, status, duration, c.Request.URL.RawQuery, reqBody)
		}
	}
}

// redactedBodyPaths — 请求体含凭据的路径，日志一律不记内容（别把明文密码写进日志文件）。
var redactedBodyPaths = map[string]bool{
	"/api/auth/login":           true,
	"/api/auth/register":        true,
	"/api/auth/change-password": true,
}

// peekBody 取请求体开头用于日志，并把已读部分放回请求体（handler 照常读到完整内容）。
// 只有文本请求才记内容：图片上传这类二进制体会把整行日志刷成几 KB 乱码，
// 把紧随其后的真实错误日志顶出视野（曾经因此误判"没有日志"）。
func peekBody(c *gin.Context) string {
	if redactedBodyPaths[c.Request.URL.Path] {
		return "<redacted:auth>" // 不读 body，handler 仍收到完整内容
	}
	ct := c.Request.Header.Get("Content-Type")
	if ct == "" {
		return ""
	}
	if !isTextualContentType(ct) {
		return "<" + ct + ">"
	}
	head, _ := io.ReadAll(io.LimitReader(c.Request.Body, logBodyLimit))
	c.Request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(head), c.Request.Body))
	return string(head)
}

// isTextualContentType 判断请求体是否值得记入日志
func isTextualContentType(ct string) bool {
	for _, prefix := range []string{
		"application/json",
		"application/xml",
		"application/x-www-form-urlencoded",
		"text/",
	} {
		if strings.HasPrefix(ct, prefix) {
			return true
		}
	}
	return false
}
