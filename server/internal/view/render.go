package view

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

type apiResponse struct {
	Status    string      `json:"status"`
	ErrorCode int         `json:"errorCode"`
	ErrMsg    string      `json:"errMsg"`
	Data      interface{} `json:"data"`
}

// WriteSSE 写入一条 SSE 事件
func WriteSSE(w io.Writer, flusher http.Flusher, event, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	flusher.Flush()
}

// JSONOK 返回统一格式 200 JSON
func JSONOK(c *gin.Context, data interface{}) {
	c.JSON(200, apiResponse{
		Status: "ok",
		Data:   data,
	})
}

// JSONError 返回统一格式错误 JSON 并中断后续 handler
func JSONError(c *gin.Context, code int, msg string) {
	c.JSON(code, apiResponse{
		Status:    "error",
		ErrorCode: code,
		ErrMsg:    msg,
	})
	c.Abort()
}

// EscapeJSON 转义字符串用于嵌入 JSON
func EscapeJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
