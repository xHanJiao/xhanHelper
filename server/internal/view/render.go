package view

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type apiResponse struct {
	Status    string      `json:"status"`
	ErrorCode int         `json:"errorCode"`
	ErrMsg    string      `json:"errMsg"`
	Data      interface{} `json:"data"`
}

// WriteSSE 写入一条 SSE 事件。
// data 里的换行必须拆成多条 data: 行：SSE 规范里 data 只占一行，
// 裸换行之后的内容会被客户端当成未知字段丢掉（thought_delta / error 是裸文本，不是 JSON）。
func WriteSSE(w io.Writer, flusher http.Flusher, event, data string) error {
	return writeFrame(w, flusher, 0, false, event, data)
}

// WriteSSEID 写入带序号的 SSE 事件（续传锚点）。返回写错误：
// 调用方据此判定连接已死——服务端没设 WriteTimeout（SSE 长连接），
// 死连接把内核发送缓冲填满后 Write 会阻塞，只有写超时能把它叫醒。
func WriteSSEID(w io.Writer, flusher http.Flusher, id int64, event, data string) error {
	return writeFrame(w, flusher, id, true, event, data)
}

// WriteComment 写入 SSE 注释行（心跳）。客户端解析器一律忽略，用来区分
// 「静默但活着」（verify / 子任务）与「半死连接」。
func WriteComment(w io.Writer, flusher http.Flusher, text string) error {
	_, err := fmt.Fprintf(w, ": %s\n\n", text)
	flusher.Flush()
	return err
}

// SetWriteTimeout 给本次写设一个绝对截止时间（http 语义：每次写之前都要重设）。
// 返回 http.ErrNotSupported 表示底层不支持（如测试用的 ResponseRecorder），调用方记一次日志后继续即可。
func SetWriteTimeout(w http.ResponseWriter, d time.Duration) error {
	return http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d))
}

func writeFrame(w io.Writer, flusher http.Flusher, id int64, withID bool, event, data string) error {
	data = strings.ReplaceAll(data, "\r\n", "\n")
	data = strings.ReplaceAll(data, "\n", "\ndata: ")
	var err error
	if withID {
		_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, event, data)
	} else {
		_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	}
	flusher.Flush()
	return err
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
