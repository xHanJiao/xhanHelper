package tool

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
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
	now := time.Now()
	data, _ := json.Marshal(map[string]string{
		"datetime": now.Format("2006-01-02 15:04:05"),
		"date":     now.Format("2006-01-02"),
		"time":     now.Format("15:04:05"),
		"weekday":  now.Weekday().String(),
		"timezone": now.Format("MST"),
	})
	return &Result{Success: true, Data: data}
}

// UUIDTool — 生成 UUID
type UUIDTool struct{}

func NewUUIDTool() *UUIDTool            { return &UUIDTool{} }
func (t *UUIDTool) Name() string        { return "uuid" }
func (t *UUIDTool) Description() string { return "生成唯一标识符" }

func (t *UUIDTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "generate",
			Description: "生成一个 UUID",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
	}
}

func (t *UUIDTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	data, _ := json.Marshal(map[string]string{"uuid": uuid.New().String()})
	return &Result{Success: true, Data: data}
}
