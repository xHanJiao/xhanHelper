package profilefield

import (
	"sort"
	"strconv"
	"strings"
)

// FieldKind — 字段存储形态。
const (
	KindSingle = "single" // 单值字段：1 条 active fact（覆盖语义）
	KindList   = "list"   // 列表字段：多条 active fact（聚合注入）
)

// Layer — 字段分层，按「变化速度」从快到慢收敛：A 身份永不改、D/E/F 会随时间漂移。
// 层决定注入顺序与超预算时的丢弃优先级：层号小的先注入、最后被丢。
const (
	LayerIdentity   = 0 // A 身份：永不丢
	LayerCollab     = 1 // B 协作偏好：永不丢
	LayerConstraint = 2 // C 硬约束：永不丢
	LayerBasics     = 3 // D 近况基本盘：有预算才进
	LayerPreference = 4 // E 偏好：有预算才进
	LayerSkeleton   = 5 // F 骨架（关系 / 目标）：有预算才进

	layerUnknown = 99 // 非白名单字段（迁移残留），排最后
)

// layerNames — 层的中文名（API / 设置页分组用）。
var layerNames = map[int]string{
	LayerIdentity:   "身份",
	LayerCollab:     "协作偏好",
	LayerConstraint: "硬约束",
	LayerBasics:     "近况基本盘",
	LayerPreference: "偏好",
	LayerSkeleton:   "骨架",
}

// LayerName 返回层的中文名；未知层返回空串。
func LayerName(layer int) string { return layerNames[layer] }

// ProfileFieldDef — 用户画像稳定层字段定义。
// 白名单放代码静态配置：人工改代码 = 人工放行新增字段。
// ExtractDesc 指导"提取 LLM"如何操作（冷启动 system prompt / 正常增量 user message XML 的 <fields>）；
// InjectDesc 告诉"对话主 LLM"这是什么（XMLContext <profile> 块注入时使用）；
// BriefDesc 是增量提取用的一句话极简描述（token 优先，砍掉完整 ExtractDesc）。
// 配额限制在单条 fact 级别：MaxLen 单条长度上限、MaxFacts 条数上限，
// 字段总长 = MaxLen × MaxFacts 自然推导，不设字段级总长限制。
type ProfileFieldDef struct {
	Field       string // 白名单字段名
	Layer       int    // 分层（决定注入顺序与丢弃优先级）
	ExtractDesc string // 提取指引：如何找、怎么写、何时省略（冷启动用）
	InjectDesc  string // 注入说明：告诉主 LLM 这个字段是什么
	BriefDesc   string // 增量提取极简描述（一句话，带配额）
	Kind        string // single / list
	MaxLen      int    // 单条 fact 长度上限（字，rune）
	MaxFacts    int    // fact 条数上限
}

// ProfileFields — 用户画像稳定层白名单（按 Layer 升序，同层内按字段名，保证注入顺序确定）。
var ProfileFields = []ProfileFieldDef{
	// ==================== A 身份（永不丢） ====================
	{
		Field: "称呼",
		Layer: LayerIdentity,
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
		Layer: LayerIdentity,
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
		Field: "常驻地",
		Layer: LayerIdentity,
		ExtractDesc: `用户当前长期居住的城市（不是籍贯，也不是出差/旅游地）。
如"北京"、"西安（计划回迁）"。
只写当前常驻地；出生地写进「籍贯」。如未找到，省略此字段`,
		InjectDesc: "用户当前常驻地（长期居住的城市）",
		BriefDesc:  "用户当前常驻地（城市）",
		Kind:       KindSingle,
		MaxLen:     30,
		MaxFacts:   1,
	},
	{
		Field: "性别",
		Layer: LayerIdentity,
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
		Field: "籍贯",
		Layer: LayerIdentity,
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

	// ==================== B 协作偏好（永不丢） ====================
	{
		Field: "协作偏好",
		Layer: LayerCollab,
		ExtractDesc: `用户希望助手如何与用户协作的稳定要求。
包括：沟通风格（如"结论先行"、"别反问，直接给方案"、"别写客套话"、"别用 emoji"）、
格式偏好（如"代码优先，不要长文"、"表格优先"）、称呼与语气禁忌。
判定标准：每条都必须能直接读成"助手应该……"。
用户自己为人处事的原则（如何对待他人/服务业、要不要当面拒绝、个人价值观）**不是**协作偏好，
除非它明确要求助手照做（如"我情绪化时请直接纠正我"）。这类内容属于记忆/事件，不写这里。
只提取用户明确表达过的长期要求；一次性的临时交代不算。如未找到，省略此字段`,
		InjectDesc: "用户对助手协作方式的稳定要求（沟通风格 / 格式 / 禁忌）",
		BriefDesc:  "对助手的要求条目（必须能读成「助手应该…」；沟通风格/格式/禁忌，不含用户自己为人处世的原则）",
		Kind:       KindList,
		MaxLen:     40,
		MaxFacts:   5,
	},

	// ==================== C 硬约束（永不丢） ====================
	{
		Field: "硬约束",
		Layer: LayerConstraint,
		ExtractDesc: `用户身上稳定成立的限制或"不能做"的事。
包括：食物过敏与忌口、健康或用药限制、宗教/伦理禁忌、法律身份限制（户籍/签证等）。
只收录长期成立的限制，不收录一次性的"这次不吃"。如未找到，省略此字段`,
		InjectDesc: "用户的硬约束（过敏 / 忌口 / 健康 / 宗教 / 法律限制）",
		BriefDesc:  "硬约束条目（过敏/忌口/健康/宗教/法律限制，一条一个）",
		Kind:       KindList,
		MaxLen:     50,
		MaxFacts:   5,
	},

	// ==================== D 近况基本盘（有预算才进） ====================
	{
		Field: "职业阶段",
		Layer: LayerBasics,
		ExtractDesc: `用户当前的职业状态，不写公司名和技术栈细节。
如"互联网后端，5 年经验"、"gap 期求职中"、"自由职业（设计）"。
只写当前状态，不写履历时间线（换过几家公司属于事件，不进这里）。
如未找到，省略此字段`,
		InjectDesc: "用户当前的职业状态",
		BriefDesc:  "职业状态条目（当前行业/角色/求职状态，一条一个）",
		Kind:       KindList,
		MaxLen:     50,
		MaxFacts:   4,
	},
	{
		Field: "家庭结构",
		Layer: LayerBasics,
		ExtractDesc: `用户家庭的基本骨架：婚姻状况、子女、父母大致所在、兄弟姐妹。
一句话一条，如"CJ: 伴侣"、"父母在焦作"、"弟常勇，有一子"。
只写稳定的结构性关系，不写具体事件（谁生病、谁吵架属于记忆和事件）。
如未找到，省略此字段`,
		InjectDesc: "用户家庭结构的骨架（亲密关系 / 子女 / 父母 / 兄弟姐妹）",
		BriefDesc:  "家庭结构条目（伴侣/子女/父母/兄弟姐妹，骨架一句一条）",
		Kind:       KindList,
		MaxLen:     40,
		MaxFacts:   3,
	},
	{
		Field: "财务轮廓",
		Layer: LayerBasics,
		ExtractDesc: `用户财务的稳定轮廓，不写具体数字。
如"有房贷"、"收入稳定"、"刚工作，积蓄少"。
只提取用户明确陈述的模糊轮廓，不要从消费记录或账单推断；具体金额属于事件。
如未找到，省略此字段`,
		InjectDesc: "用户财务的整体轮廓（不写具体数字）",
		BriefDesc:  "财务轮廓条目（资产/收入/负债的模糊轮廓）",
		Kind:       KindList,
		MaxLen:     40,
		MaxFacts:   3,
	},

	// ==================== E 偏好（有预算才进） ====================
	{
		Field: "饮食偏好",
		Layer: LayerPreference,
		ExtractDesc: `用户在饮食上的稳定偏好：喜欢什么、不喜欢什么、长期习惯。
只写偏好本身（如"喜欢气泡水"、"不喝咖啡因"、"爱吃面食"），
不写某次吃了什么、喝了多少、花了多少钱（那些属于记忆和事件）。
忌口与过敏写进「硬约束」，不写这里。如未找到，省略此字段`,
		InjectDesc: "用户的饮食偏好（只写长期偏好，不含忌口与具体事件）",
		BriefDesc:  "饮食偏好条目（喜欢/不喜欢，一条一个）",
		Kind:       KindList,
		MaxLen:     40,
		MaxFacts:   4,
	},

	// ==================== F 骨架（有预算才进） ====================
	{
		Field: "重要关系",
		Layer: LayerSkeleton,
		ExtractDesc: `用户生活中重要的人物及其关系。
格式: "人名: 关系, 简要介绍; 人名: 关系, 简要介绍"。
如"CJ: 伴侣, 自由职业; 张伟: 同事, 同组后端开发"。
只收录对话中反复出现的关键人物，偶然提及一次的不算。
不写带日期/地点/金额的具体事件。如未找到，省略此字段`,
		InjectDesc: "用户生活中重要的人物及其关系",
		BriefDesc:  "重要人物条目（人名: 关系, 简介；一人一条）",
		Kind:       KindList,
		MaxLen:     80,
		MaxFacts:   10,
	},
	{
		Field: "长期目标",
		Layer: LayerSkeleton,
		ExtractDesc: `用户当前明确的长期目标（半年以上的方向性目标）。
如"计划回西安定居"、"计划在西安买房"、"转做 AI 应用方向"。
只写目标本身：不写已完成或已放弃的历史、不写立场观点。
旅行、出差、看房、考试报名这类有具体起止时间的安排属于事件，不进这里。
如未找到，省略此字段`,
		InjectDesc: "用户当前的核心长期目标（方向性，不含具体行程安排）",
		BriefDesc:  "长期目标条目（半年以上的方向性目标；旅行/出差/看房等行程不算）",
		Kind:       KindList,
		MaxLen:     50,
		MaxFacts:   3,
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

func init() {
	// 白名单顺序即注入顺序：先按层、再按字段名，保证字节稳定（prompt cache 友好）。
	sort.SliceStable(ProfileFields, func(i, j int) bool {
		if ProfileFields[i].Layer != ProfileFields[j].Layer {
			return ProfileFields[i].Layer < ProfileFields[j].Layer
		}
		return ProfileFields[i].Field < ProfileFields[j].Field
	})
}

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

// SortKey 返回字段的排序键（层, 字段名）。非白名单字段层为 99，排最后。
// 注入侧与解析侧共用同一把尺子，避免两处顺序漂移。
func SortKey(field string) (int, string) {
	if def, ok := profileFieldIndex[field]; ok {
		return def.Layer, def.Field
	}
	return layerUnknown, field
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
// 格式："字段名(≤N字,上限M条): 一句话说明"，如 "硬约束(≤50字,上限5条): 过敏/忌口条目"。
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
