package middleware

import (
	"io"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// RequestLogger — HTTP/SSE 请求日志中间件
func RequestLogger(toolLogger *log.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		bodyBytes, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))

		reqBody := string(bodyBytes)
		if len(reqBody) > 2000 {
			reqBody = reqBody[:2000]
		}

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
