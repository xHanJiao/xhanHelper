package tool

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/profilefield"
	"xhan-helper/internal/service"
)

// ProfileTool — 用户画像稳定层只读查询工具（user_context 子 agent 专用）。
// 读 UserProfileFact 全部 active 事实，按字段分组返回。
// 仅 subAgent 可见（appinit 注册后 HideTool）；主循环画像已由 resource zone 每轮注入，不暴露冗余查询。
type ProfileTool struct {
	factRepo *service.UserProfileFactRepo
}

// NewProfileTool 创建 profile 工具。必需依赖为 nil 时 panic（接线 bug 暴露在明处）。
func NewProfileTool(factRepo *service.UserProfileFactRepo) *ProfileTool {
	if factRepo == nil {
		panic("profile tool: factRepo nil (wiring bug)")
	}
	return &ProfileTool{factRepo: factRepo}
}

func (t *ProfileTool) Name() string { return "profile" }

func (t *ProfileTool) Description() string {
	return "查询用户画像稳定层（长期记忆的用户基本事实：称呼/出生日期/籍贯/职业/重要关系等），只读"
}

func (t *ProfileTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "get",
		Display:     "查询用户画像",
		Description: "查询用户画像稳定层的 active 事实，按字段分组返回。可指定字段过滤；不传 fields 返回全部画像",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"fields": map[string]interface{}{
					"type":        "array",
					"items":       map[string]string{"type": "string"},
					"description": "要查询的画像字段名列表，留空=全部。可选值: " + strings.Join(profilefield.ProfileFieldNames(), "/"),
				},
			},
		},
	}}
}

func (t *ProfileTool) ReadOnlyFuncs() map[string]bool { return AllReadOnly(t) }

// profileArgs — get 函数参数。
type profileArgs struct {
	Fields []string `json:"fields"`
}

func (t *ProfileTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	if funcName != "get" {
		return &Result{Success: false, Error: fmt.Sprintf("未知函数: %s", funcName)}
	}
	var a profileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}

	// 非法字段报错并列出合法字段（LLM 可自纠错），而非静默过滤
	if len(a.Fields) > 0 {
		var invalid []string
		for _, f := range a.Fields {
			if !profilefield.InProfileWhitelist(f) {
				invalid = append(invalid, f)
			}
		}
		if len(invalid) > 0 {
			return &Result{Success: false, Error: fmt.Sprintf(
				"未知画像字段: %s。可选字段: %s。请核对字段名后重试。",
				strings.Join(invalid, "/"), strings.Join(profilefield.ProfileFieldNames(), "/"))}
		}
	}

	facts, err := t.factRepo.GetActiveByUser(ec.Ctx, ec.UserID)
	if err != nil {
		log.Printf("[profile] GetActiveByUser error (user=%s): %v", ec.UserID, err)
		return &Result{Success: false, Error: "查询用户画像失败"}
	}

	want := make(map[string]bool, len(a.Fields))
	for _, f := range a.Fields {
		want[f] = true
	}

	grouped := make(map[string][]string)
	for _, f := range facts {
		if len(want) > 0 && !want[f.Field] {
			continue
		}
		grouped[f.Field] = append(grouped[f.Field], f.Value)
	}

	// 仅输出白名单字段（DB 只存白名单字段，ProfileWorker 写入时校验过）
	out := make(map[string]map[string]interface{})
	for _, def := range profilefield.ProfileFields {
		if values, ok := grouped[def.Field]; ok {
			out[def.Field] = map[string]interface{}{
				"desc":  def.InjectDesc,
				"facts": values,
			}
		}
	}

	data, err := json.Marshal(map[string]interface{}{
		"fields":   out,
		"is_empty": len(out) == 0,
	})
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("marshal error: %v", err)}
	}
	return &Result{Success: true, Data: data}
}
