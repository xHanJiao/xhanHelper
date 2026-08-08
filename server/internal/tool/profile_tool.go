package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"xhan-helper/internal/model"
	"xhan-helper/internal/profilefield"
	"xhan-helper/internal/service"
)

// ProfileTool — 用户画像工具。
// 提供 update_profile_field：在用户明确要求时更新/新增画像字段。
// 稳定层写入通道：后处理提取为主，本工具为辅（用户明确指令时即时生效）。
type ProfileTool struct {
	profileRepo *service.UserProfileRepo
}

// NewProfileTool 创建画像工具。
func NewProfileTool(profileRepo *service.UserProfileRepo) *ProfileTool {
	return &ProfileTool{profileRepo: profileRepo}
}

// Name 实现 Tool。
func (t *ProfileTool) Name() string { return "profile" }

// Description 实现 Tool。
func (t *ProfileTool) Description() string {
	return "用户画像工具。维护用户的核心事实画像（籍贯/职业/伴侣等）。"
}

// Functions 实现 Tool。仅 update_profile_field（写操作）。
func (t *ProfileTool) Functions() []FuncSchema {
	fieldEnums := make([]interface{}, 0, len(profilefield.ProfileFields))
	for _, f := range profilefield.ProfileFields {
		fieldEnums = append(fieldEnums, f.Field)
	}
	return []FuncSchema{
		{
			Name:    "update_profile_field",
			Display: "更新用户画像",
			Description: `更新或新增用户画像字段。
仅在用户明确提出修改画像的请求时调用（如"帮我改一下画像里的老家"、"我老家其实是郑州"）。
调用约束：
- 必须用户明确要求改画像；用户只是陈述事实时不要调用（陈述由后处理自动提取）
- field 必须是下列白名单字段之一
- 若新值与现有画像冲突，须向用户确认后再更新`,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"field": map[string]interface{}{
						"type":        "string",
						"enum":        fieldEnums,
						"description": "要更新的画像字段",
					},
					"value": map[string]interface{}{
						"type":        "string",
						"description": "新的字段值（用户明确陈述的内容）",
					},
				},
				"required": []interface{}{"field", "value"},
			},
		},
	}
}

// ReadOnlyFuncs 实现 Tool。空 map = 全部函数为写操作（plan 模式全部拦截）。
func (t *ProfileTool) ReadOnlyFuncs() map[string]bool { return nil }

// Execute 实现 Tool。
func (t *ProfileTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "update_profile_field":
		return t.updateProfileField(ec, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("profile: unknown function %s", funcName)}
	}
}

type updateProfileFieldArgs struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// updateProfileField 更新/新增画像字段。白名单校验 + 值校验 + 写入。
func (t *ProfileTool) updateProfileField(ec *ExecuteContext, args json.RawMessage) *Result {
	var a updateProfileFieldArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if a.Field == "" || a.Value == "" {
		return &Result{Success: false, Error: "field 和 value 都不能为空"}
	}
	if !profilefield.InProfileWhitelist(a.Field) {
		return &Result{Success: false, Error: fmt.Sprintf(
			"字段 %q 不在白名单内。可用的字段：%s。如需更新其他个性化信息，请使用 %q 字段。",
			a.Field, strings.Join(profilefield.ProfileFieldNames(), "、"), "补充说明")}
	}
	value := strings.TrimSpace(a.Value)
	if profilePlaceholders[value] {
		return &Result{Success: false, Error: "字段值不能是空或占位值"}
	}

	p := &model.UserProfile{
		UserID:     ec.UserID,
		Field:      a.Field,
		FieldValue: value,
		Source:     model.ProfileSourceExplicit, // 用户明确要求 = 最高置信度
	}
	if err := t.profileRepo.Upsert(ec.Ctx, p); err != nil {
		return &Result{Success: false, Error: "update profile field failed: " + err.Error()}
	}

	data, _ := json.Marshal(map[string]string{
		"field": a.Field,
		"value": value,
	})
	return &Result{Success: true, Data: data}
}

// profilePlaceholders — 视为无信息的占位值（与 profile worker 保持一致）。
var profilePlaceholders = map[string]bool{
	"": true, "未知": true, "无": true, "暂无": true, "不详": true,
}
