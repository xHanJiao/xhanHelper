package profilefield

import (
	"strconv"
	"strings"
)

// FieldKind — 字段存储形态。
const (
	KindSingle = "single" // 单值字段：1 条 active fact（覆盖语义）
	KindList   = "list"   // 列表字段：多条 active fact（聚合注入）
)

// ProfileFieldDef — 用户画像稳定层字段定义。
// 白名单放代码静态配置：人工改代码 = 人工放行新增字段。
// ExtractDesc 指导"提取 LLM"如何操作（冷启动 system prompt / 正常增量 user message XML 的 <fields>）；
// InjectDesc 告诉"对话主 LLM"这是什么（XMLContext <profile> 块注入时使用）；
// BriefDesc 是增量提取用的一句话极简描述（token 优先，砍掉完整 ExtractDesc）。
// 配额限制在单条 fact 级别：MaxLen 单条长度上限、MaxFacts 条数上限，
// 字段总长 = MaxLen × MaxFacts 自然推导，不设字段级总长限制。
type ProfileFieldDef struct {
	Field       string // 白名单字段名
	ExtractDesc string // 提取指引：如何找、怎么写、何时省略（冷启动用）
	InjectDesc  string // 注入说明：告诉主 LLM 这个字段是什么
	BriefDesc   string // 增量提取极简描述（一句话，带配额）
	Kind        string // single / list
	MaxLen      int    // 单条 fact 长度上限（字，rune）
	MaxFacts    int    // fact 条数上限
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
		BriefDesc:  "用户偏好的称呼（仅用户明确指定时）",
		Kind:       KindSingle,
		MaxLen:     10,
		MaxFacts:   1,
	},
	{
		Field: "出生日期",
		ExtractDesc: `用户的出生日期，生日是重要信息，记得在生日时送上祝福。
仅接受用户明确陈述的日期（如"我是95年的"、"我3月15号生日"），不要从"我今年N岁"推算。
值格式：完整日期用 YYYY-MM-DD（如"1995-03-15"）；仅知年份时只写 4 位年份（如"1995"）
仅知年月时写年+月如"1995-02"。如未找到，省略此字段`,
		InjectDesc: "用户的出生日期（生日），用于年龄判断与生日祝福",
		BriefDesc:  "用户出生日期（YYYY-MM-DD，仅知年份可只写年份）",
		Kind:       KindSingle,
		MaxLen:     10,
		MaxFacts:   1,
	},
	{
		Field: "籍贯",
		ExtractDesc: `用户的籍贯或老家。
用户可能会说"我老家xxx的"、"我是xxx人"等。
值应为地名，如"河南焦作"。
如未找到，省略此字段`,
		InjectDesc: "用户的籍贯/老家",
		BriefDesc:  "用户籍贯/老家",
		Kind:       KindSingle,
		MaxLen:     30,
		MaxFacts:   1,
	},
	{
		Field: "性别",
		ExtractDesc: `用户的性别。值为"男"或"女"。
仅当用户明确陈述时提取。不要从称谓（如"我女朋友"）推断性别。
如未找到，省略此字段`,
		InjectDesc: "用户的性别",
		BriefDesc:  "用户性别（男/女）",
		Kind:       KindSingle,
		MaxLen:     2,
		MaxFacts:   1,
	},
	{
		Field: "职业领域",
		ExtractDesc: `用户当前从事的行业和岗位方向。
不限具体技术栈或公司名，侧重"做什么领域、什么角色"。
如"互联网产品经理"、"医学生"、"自由职业（设计）"。
简洁概括即可。如未找到，省略此字段`,
		InjectDesc: "用户当前从事的行业和岗位方向",
		BriefDesc:  "职业履历条目（当前行业/岗位/求职状态等，一条一个独立事实）",
		Kind:       KindList,
		MaxLen:     60,
		MaxFacts:   6,
	},
	{
		Field: "重要关系",
		ExtractDesc: `用户生活中重要的人物及其关系。
格式: "人名: 关系, 简要介绍; 人名: 关系, 简要介绍"。
如"CJ: 伴侣, 自由职业; 张伟: 同事, 同组后端开发"。
只收录对话中反复出现的关键人物，偶然提及一次的不算。
如未找到，省略此字段`,
		InjectDesc: "用户生活中重要的人物及其关系",
		BriefDesc:  "重要人物条目（人名: 关系, 简介；一人一条）",
		Kind:       KindList,
		MaxLen:     80,
		MaxFacts:   10,
	},
	{
		Field: "饮食习惯",
		ExtractDesc: `用户长期稳定的饮食偏好、忌口或特殊需求。
不是一时的口味变化，而是持续存在的饮食模式。
如"不吃辣, 喜欢面食"、"素食"、"对海鲜过敏"。
只提取用户明确陈述且是长期习惯的信息。如未找到，省略此字段`,
		InjectDesc: "用户长期稳定的饮食偏好与忌口",
		BriefDesc:  "饮食偏好/忌口条目（长期习惯）",
		Kind:       KindList,
		MaxLen:     60,
		MaxFacts:   5,
	},
	{
		Field: "财务概况",
		ExtractDesc: `用户资产/收入/负债的整体轮廓，不是具体数字。
变化极慢的财务基本面。
如"有房贷, 收入稳定"、"刚工作, 月入中等"。
只提取用户明确陈述的模糊轮廓，不要从消费记录或账单推断。
如未找到，省略此字段`,
		InjectDesc: "用户资产/收入/负债的整体轮廓",
		BriefDesc:  "财务事实条目（资产/收入/负债/购房计划等）",
		Kind:       KindList,
		MaxLen:     50,
		MaxFacts:   5,
	},
	{
		Field: "长期目标",
		ExtractDesc: `用户当前人生阶段的核心长期目标。
如"计划3年内在北京定居买房"、"备考研究生"。
只提取用户明确陈述的具体目标，不要从对话主题推测。
如未找到，省略此字段`,
		InjectDesc: "用户当前人生阶段的核心长期目标",
		BriefDesc:  "长期目标条目（明确陈述的具体目标）",
		Kind:       KindList,
		MaxLen:     50,
		MaxFacts:   5,
	},
	{
		Field: "补充说明",
		ExtractDesc: `用户的个性化稳定信息，不在上述9个固定字段覆盖范围内。
可以是任何值得助手长期记忆的用户特征或情况。
由LLM自行判断是否值得写入，宁缺毋滥。
如未找到，省略此字段`,
		InjectDesc: "用户的个性化补充信息（LLM自动提取，可能不够准确）",
		BriefDesc:  "个性化杂项事实（短条目，宁缺毋滥）",
		Kind:       KindList,
		MaxLen:     30,
		MaxFacts:   10,
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

// ProfileFieldNames 返回白名单字段名列表（供工具 enum、校验等使用）。
func ProfileFieldNames() []string {
	names := make([]string, 0, len(ProfileFields))
	for _, f := range ProfileFields {
		names = append(names, f.Field)
	}
	return names
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

// ProfileExtractFieldTextBrief 生成增量提取用的极简字段清单（token 优先）。
// 格式："字段名(≤N字,上限M条): 一句话说明"，如 "补充说明(≤30字,上限10条): 个性化杂项事实"。
func ProfileExtractFieldTextBrief() string {
	var sb strings.Builder
	for _, f := range ProfileFields {
		sb.WriteString("- ")
		sb.WriteString(f.Field)
		sb.WriteString("(≤")
		sb.WriteString(strconv.Itoa(f.MaxLen))
		sb.WriteString("字")
		if f.Kind == KindList {
			sb.WriteString(",上限")
			sb.WriteString(strconv.Itoa(f.MaxFacts))
			sb.WriteString("条")
		}
		sb.WriteString("): ")
		sb.WriteString(f.BriefDesc)
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}
