package middleware

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type loggingResponseWriter struct {
	gin.ResponseWriter
	body      bytes.Buffer
	isSSE     bool
	sseBuf    bytes.Buffer
	sseCount  map[string]int
	sseTotal  int
	lastDone  string
	lastError string
}

func (w *loggingResponseWriter) WriteHeader(code int) {
	if strings.Contains(w.ResponseWriter.Header().Get("Content-Type"), "text/event-stream") {
		w.isSSE = true
		w.sseCount = make(map[string]int)
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *loggingResponseWriter) Write(data []byte) (int, error) {
	if w.isSSE {
		return w.writeSSE(data)
	}
	w.body.Write(data)
	return w.ResponseWriter.Write(data)
}

func (w *loggingResponseWriter) writeSSE(data []byte) (int, error) {
	w.sseBuf.Write(data)
	for {
		raw := w.sseBuf.Bytes()
		idx := bytes.Index(raw, []byte("\n\n"))
		if idx < 0 {
			break
		}
		frame := string(raw[:idx])
		w.sseBuf.Next(idx + 2)
		w.sseTotal++

		var evtType, evtData string
		for _, line := range strings.Split(frame, "\n") {
			if strings.HasPrefix(line, "event: ") {
				evtType = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") {
				evtData = strings.TrimPrefix(line, "data: ")
			}
		}
		w.sseCount[evtType]++
		if evtType == "done" {
			w.lastDone = evtData
		}
		if evtType == "error" {
			w.lastError = evtData
		}
	}
	return w.ResponseWriter.Write(data)
}

// RequestLogger — HTTP 请求/响应日志中间件
func RequestLogger(toolLogger *log.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		bodyBytes, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		reqBody := string(bodyBytes)
		if len(reqBody) > 2000 {
			reqBody = reqBody[:2000]
		}

		wrapper := &loggingResponseWriter{ResponseWriter: c.Writer}
		c.Writer = wrapper

		c.Next()

		duration := time.Since(start).Milliseconds()
		status := c.Writer.Status()

		if wrapper.isSSE {
			var types []string
			for evt, n := range wrapper.sseCount {
				types = append(types, fmt.Sprintf("%s=%d", evt, n))
			}
			extra := ""
			if wrapper.lastError != "" {
				extra += fmt.Sprintf(" last_error=%s", wrapper.lastError)
			}
			if wrapper.lastDone != "" {
				extra += fmt.Sprintf(" last_done=%s", wrapper.lastDone)
			}
			toolLogger.Printf("[sse] %s %s status=%d duration=%dms events=%d {%s}%s body=%s",
				c.Request.Method, c.Request.URL.Path, status, duration,
				wrapper.sseTotal, strings.Join(types, ", "), extra, reqBody)
		} else {
			respBody := wrapper.body.String()
			if len(respBody) > 2000 {
				respBody = respBody[:2000]
			}
			toolLogger.Printf("[http] %s %s status=%d duration=%dms query=%q body=%q resp=%s",
				c.Request.Method, c.Request.URL.Path, status, duration,
				c.Request.URL.RawQuery, reqBody, respBody)
		}
	}
}
