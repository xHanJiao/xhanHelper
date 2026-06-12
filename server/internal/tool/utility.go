package tool

import (
	"encoding/json"
	"time"

	"xhan-helper/internal/config"
)

// DatetimeTool — 获取当前日期时间
type DatetimeTool struct{}

func NewDatetimeTool() *DatetimeTool        { return &DatetimeTool{} }
func (t *DatetimeTool) Name() string        { return "datetime" }
func (t *DatetimeTool) Description() string { return "获取当前日期时间信息" }

func (t *DatetimeTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "get_current_time",
			Description: "获取当前日期、时间、星期、时区",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
	}
}

func (t *DatetimeTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	now := time.Now().In(config.BJ)
	data, _ := json.Marshal(map[string]string{
		"datetime": now.Format("2006-01-02 15:04:05"),
		"date":     now.Format("2006-01-02"),
		"time":     now.Format("15:04:05"),
		"weekday":  now.Weekday().String(),
		"timezone": "UTC+8",
	})
	return &Result{Success: true, Data: data}
}
