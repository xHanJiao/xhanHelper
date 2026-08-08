package agent

import "strings"

// ProfileFieldDef — 用户画像稳定层字段定义。
// 白名单放代码静态配置：人工改代码 = 人工放行新增字段。
// ExtractDesc 指导"提取 LLM"如何操作（冷启动 system prompt / 正常增量 user message XML 的 <fields>）；
// InjectDesc 告诉"对话主 LLM"这是什么（XMLContext <profile> 块注入时使用）。
// 两处复用同一份常量，改描述只需改这一处。
type ProfileFieldDef struct {
	Field       string // DB 字段名（user_profile.field）
	ExtractDesc string // 提取指引：如何找、怎么写、何时省略
	InjectDesc  string // 注入说明：告诉主 LLM 这个字段是什么
	Type        string // 预留：string / list
}

// ProfileFields — 用户画像稳定层白名单。
var ProfileFields = []ProfileFieldDef{
	{
		Field: "称呼",
		ExtractDesc: `用户希望你如何称呼他/她。
可以是昵称、英文名或特定称谓。
仅当用户明确说了"你可以叫我xxx"、"叫我xxx就好"等类似表达时才提取。
不要从对话中推断称呼偏好。
如未找到，省略此字段`,
		InjectDesc: "用户偏好的称呼方式",
		Type:       "string",
	},
	{
		Field: "出生年份",
		ExtractDesc: `用户的出生年份，用于推断人生阶段。
仅接受用户明确陈述的具体年份（如"我是95年的"），不要从"我今年N岁"推算。
值应为4位年份数字字符串，如"1995"。
如未找到，省略此字段`,
		InjectDesc: "用户的出生年份，用于年龄相关判断",
		Type:       "string",
	},
	{
		Field: "籍贯",
		ExtractDesc: `用户的籍贯或老家。
用户可能会说"我老家xxx的"、"我是xxx人"等。
值应为地名，如"河南焦作"。
如未找到，省略此字段`,
		InjectDesc: "用户的籍贯/老家",
		Type:       "string",
	},
	{
		Field: "性别",
		ExtractDesc: `用户的性别。值为"男"或"女"。
仅当用户明确陈述时提取。不要从称谓（如"我女朋友"）推断性别。
如未找到，省略此字段`,
		InjectDesc: "用户的性别",
		Type:       "string",
	},
	{
		Field: "职业领域",
		ExtractDesc: `用户当前从事的行业和岗位方向。
不限具体技术栈或公司名，侧重"做什么领域、什么角色"。
如"互联网产品经理"、"医学生"、"自由职业（设计）"。
简洁概括即可。如未找到，省略此字段`,
		InjectDesc: "用户当前从事的行业和岗位方向",
		Type:       "string",
	},
	{
		Field: "重要关系",
		ExtractDesc: `用户生活中重要的人物及其关系。
格式: "人名: 关系, 简要介绍; 人名: 关系, 简要介绍"。
如"CJ: 伴侣, 自由职业; 张伟: 同事, 同组后端开发"。
只收录对话中反复出现的关键人物，偶然提及一次的不算。
如未找到，省略此字段`,
		InjectDesc: "用户生活中重要的人物及其关系",
		Type:       "string",
	},
	{
		Field: "饮食习惯",
		ExtractDesc: `用户长期稳定的饮食偏好、忌口或特殊需求。
不是一时的口味变化，而是持续存在的饮食模式。
如"不吃辣, 喜欢面食"、"素食"、"对海鲜过敏"。
只提取用户明确陈述且是长期习惯的信息。如未找到，省略此字段`,
		InjectDesc: "用户长期稳定的饮食偏好与忌口",
		Type:       "string",
	},
	{
		Field: "财务概况",
		ExtractDesc: `用户资产/收入/负债的整体轮廓，不是具体数字。
变化极慢的财务基本面。
如"有房贷, 收入稳定"、"刚工作, 月入中等"。
只提取用户明确陈述的模糊轮廓，不要从消费记录或账单推断。
如未找到，省略此字段`,
		InjectDesc: "用户资产/收入/负债的整体轮廓",
		Type:       "string",
	},
	{
		Field: "长期目标",
		ExtractDesc: `用户当前人生阶段的核心长期目标。
如"计划3年内在北京定居买房"、"备考研究生"。
只提取用户明确陈述的具体目标，不要从对话主题推测。
如未找到，省略此字段`,
		InjectDesc: "用户当前人生阶段的核心长期目标",
		Type:       "string",
	},
	{
		Field: "补充说明",
		ExtractDesc: `用户的个性化稳定信息，不在上述9个固定字段覆盖范围内。
可以是任何值得助手长期记忆的用户特征或情况。
由LLM自行判断是否值得写入，宁缺毋滥。
如未找到，省略此字段`,
		InjectDesc: "用户的个性化补充信息（LLM自动提取，可能不够准确）",
		Type:       "string",
	},
}

// profileFieldIndex — 字段名 → ProfileFieldDef 映射，供 O(1) 白名单判断。
var profileFieldIndex = func() map[string]ProfileFieldDef {
	m := make(map[string]ProfileFieldDef, len(ProfileFields))
	for _, f := range ProfileFields {
		m[f.Field] = f
	}
	return m
}()

// InProfileWhitelist 判断字段是否在白名单内。
func InProfileWhitelist(field string) bool {
	_, ok := profileFieldIndex[field]
	return ok
}

// ProfileFieldDesc 返回字段定义，不存在返回 nil。
func ProfileFieldDesc(field string) *ProfileFieldDef {
	f, ok := profileFieldIndex[field]
	if !ok {
		return nil
	}
	return &f
}

// ProfileExtractFieldText 生成冷启动/提取 prompt 中的字段定义文本（"字段名\n描述" 逐字段拼接）。
func ProfileExtractFieldText() string {
	var sb strings.Builder
	for _, f := range ProfileFields {
		sb.WriteString("### ")
		sb.WriteString(f.Field)
		sb.WriteString("\n")
		sb.WriteString(f.ExtractDesc)
		sb.WriteString("\n\n")
	}
	return strings.TrimSpace(sb.String())
}
