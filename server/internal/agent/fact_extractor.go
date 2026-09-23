package agent

import (
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
)

// factKindState / factKindEvent — kind 的两个合法取值。
const (
	factKindState = "state"
	factKindEvent = "event"
)

// defaultFactMaxCandidates — 抽取器内部兜底上限（与 config.yaml 的 facts.max_candidates 一致）。
const defaultFactMaxCandidates = 5

// factCandidate — 抽取产出的一条候选事实。不落库，交给决策段处理。
// Fields 用 map[string]any 而非 map[string]string：模型偶尔把 due_at 写成数字，
// 用 string 接会让整批解码失败；写库前再 Marshal 成 JSON 字符串。
type factCandidate struct {
	Subject string         `json:"subject"`
	Kind    string         `json:"kind"`
	Content string         `json:"content"`
	Fields  map[string]any `json:"fields,omitempty"`
}

// factExtractResult — 抽取段 LLM 的输出结构（一个键 facts）。
type factExtractResult struct {
	Facts []factCandidate `json:"facts"`
}

// factExtractSystemPrompt — 事实抽取专用 system prompt。
// 不复用 postChatSharedSystemPrompt：那条写死了「输入均为用户角色的发言」，
// 与事实抽取喂 user + assistant 的事实矛盾，且改共享常量会动其他后处理链路的缓存前缀。
const factExtractSystemPrompt = `你是一个对话后处理助手。输入是一段用户与助手的对话记录，按时间顺序排列，每个 turn 带发生时间。`

// factExtractInstruction — 抽取段指令消息。常量，放在 user 消息最前（对话紧随其后），
// 以便命中 provider 的 prompt 前缀缓存。正文见 docs/design/facts-system/design.md §5.1。
const factExtractInstruction = `你是事实记录员。你的工作是从对话中抽取值得长期保留的事实。

你不做摘要，不写心得，不给建议。

## 输入

<对话>：用户与助手的一段对话，按时间顺序排列，每个 turn 带发生时间。
用户消息里的「今天（2026-09-21）」是系统补的日期标注：原词是相对说法，括号里是它的绝对日期。

## 铁律

1. 只依据用户的消息。助手说的、系统说的，一律不记为事实。用户没确认的内容，不记。
2. 抽的是事实，不是对话过程。谁问过、你答了什么、绕了几步，都不记。
3. 一次输出多条。一段对话里可能有若干条事实，逐条输出，不许合并成一条综述。
4. content 必须自足成句：脱离这段对话也能读懂。主体名（说的是谁）要写进句子；「她」「那个项目」「上次说的」一律换成确定的名词。
5. 时间只写绝对日期，格式 YYYY-MM-DD。不许出现「今天」「昨天」「下周」这类相对说法，也不许把系统标注的括号抄进 content。
6. 不确定就不记。记错的事实比漏掉更难清理。只写对话里出现过的内容，不许补齐、不许推断。
7. 助手的话不构成事实来源，除非用户确认。助手复述用户已知内容的，更不记。
8. 以最终陈述为准：用户改口、助手误判被纠正，只留最终版，中间态不产出多条。
9. 段内去重：同一事实在一段对话里被提两次，只产出一条。

## 每条事实的四个字段

- subject：这条事实关于谁／什么。取对话里真实出现的指称（人名、项目名、机构名），不要自造代号。
- kind：只有两个值，state 或 event。
- fields：可空。due_at（到期时间，YYYY-MM-DD）、has_doc_ref（文档 ID）。
- content：一句话，只放一个断言。

subject 取这句话真正在说的对象。通篇是「我」在讲，主体也可能是别人——这是最常见的错。

kind 只看一件事：会不会被后来的事实取代。
- state：当前状态、身份、关系、计划、对某事的当前判断。会被取代。
- event：一次发生的事、观点。发生即固定，只增不改。观点不许覆盖成「最新观点」。

fields：日期、金额、期限是字段，不是类别。计划 = state + due_at。
「到期／期限」写进 fields.due_at，content 里不再重复它；其余时间（某件事发生在哪天）用 YYYY-MM-DD 写在 content 的句子里。

## 什么该记，什么不该记

该记：结论、状态、身份与关系、计划、事件、观点、对外部事物的判断。
不该记：逐日读数（睡眠时长、步数、体重这类时序数据，走台账）、助手的话、对话过程、寒暄、零信息量的问答。
一段对话可能一条都不该记——那就输出空列表。

## content 是什么

content 是句子，不是要点列表；一条只放一个断言。

❌ {"subject":"老K小程序","kind":"state","content":"本人兼产品与开发、只做前端、9/21 演示"}
✅ {"subject":"老K小程序","kind":"state","content":"本人这次在老K的旅游案例展示小程序上兼产品与开发，原定位只做技术"}

## 样例

输入统一为「一个或多个 turn 的对话」，单条样例也写成只有一个 turn 的对话，不写成「一句话」。

例 1 · 多轮：跨轮拼装、以最终陈述为准
<对话>
  <turn role="user" at="2026-09-18 21:30">老K那个小程序你还记得吧，旅游案例展示加签证代办。</turn>
  <turn role="assistant" at="2026-09-18 21:30">记得，你提过初稿。现在到哪步了？</turn>
  <turn role="user" at="2026-09-18 21:31">还没动。我本来只做技术，这次可能产品和开发都我来。</turn>
  <turn role="assistant" at="2026-09-18 21:31">那得先定原型？</turn>
  <turn role="user" at="2026-09-18 21:33">嗯，想用工具生成 UI 提示词。9 月 21 号跟他对需求。</turn>
  <turn role="assistant" at="2026-09-18 21:33">21 号是交付日吗？</turn>
  <turn role="user" at="2026-09-18 21:34">不是，那天他回程，就当面演示一下。后台来不及就只做前端。</turn>
</对话>
输出：
{"facts":[
  {"subject":"老K小程序","kind":"state","fields":{"due_at":"2026-09-21"},"content":"本人这次在老K的旅游案例展示小程序上兼产品与开发，原定位只做技术"},
  {"subject":"老K小程序","kind":"event","content":"2026-09-21 不是交付日，本人与老K当面演示该小程序；后台来不及就只做前端"}
]}
不记：开场寒暄、助手那句误判与澄清提问。第 2 条是跨三轮拼出来的——日期、性质、退路缺一轮都不完整。

例 2 · 主体不是本人（通篇是「我」在讲，主体也可能是别人）
<对话>
  <turn role="user" at="2026-09-20 22:10">今天和配偶吵架了。我妈在群里说买了新电动车，配偶看见就问为啥要买，我说你要是不愿意我可以说你不让，然后就吵起来了，她把家群也退了。我对她们的沟通很紧张，有点 PTSD。买房、生孩子我都不想做了，只想躺平。</turn>
</对话>
输出：
{"facts":[
  {"subject":"配偶","kind":"event","content":"配偶因本人母亲在家族群聊里买新电动车一事与本人争吵，并退了家族群"},
  {"subject":"本人","kind":"state","content":"本人对母亲与配偶之间的沟通有创伤式反应"},
  {"subject":"本人","kind":"state","content":"本人不想买房生育了，只想躺平"}
]}

例 3 · 不产出事实
<对话>
  <turn role="user" at="2026-09-21 08:52">昨天十一点半多睡的，入睡不算顺利，今天睡到八点五十，4.5 分，头还微微疼。</turn>
</对话>
输出：{"facts":[]}
逐日读数，单条脱离序列无意义，走台账，不进事实库。

## 输出

JSON，一个键 facts：
{"facts":[{"subject":"老K小程序","kind":"state","fields":{"due_at":"2026-09-21"},"content":"本人这次在老K的旅游案例展示小程序上兼产品与开发，原定位只做技术"}]}`

// FactExtractor — 从 session 消息中抽取候选事实。由 FactWorker 调用。
// 本阶段不判重、不检索、不落库，只产出候选。
type FactExtractor struct {
	llmClient     llm.LLMClient
	maxCandidates int
	temperature   *float64
}

// FactExtractorConfig — 抽取器依赖配置。
type FactExtractorConfig struct {
	LLMClient     llm.LLMClient
	MaxCandidates int      // 0 → 默认 5
	Temperature   *float64 // 采样温度；nil → 用模型默认
}

// NewFactExtractor 创建抽取器。
func NewFactExtractor(cfg *FactExtractorConfig) *FactExtractor {
	if cfg.MaxCandidates <= 0 {
		cfg.MaxCandidates = defaultFactMaxCandidates
	}
	return &FactExtractor{
		llmClient:     cfg.LLMClient,
		maxCandidates: cfg.MaxCandidates,
		temperature:   cfg.Temperature,
	}
}

// Extract 从 session 消息（user + assistant）中抽取候选事实。
// 返回 nil 表示无候选。解析失败按"本次无候选"降级并记日志，不打断后续 worker。
// 缓存友好：system prompt 用固定串，指令 + 对话全部编码进单条 user 消息，常量前缀在前。
func (e *FactExtractor) Extract(ctx context.Context, msgs []model.Message) ([]factCandidate, error) {
	prompt := factExtractInstruction
	dialog, err := renderFactDialog(msgs)
	if err != nil {
		return nil, fmt.Errorf("render fact dialog: %w", err)
	}
	if dialog != "" {
		prompt = factExtractInstruction + "\n\n" + dialog
	}

	msg, err := e.llmClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: factExtractSystemPrompt,
		Messages:     []llm.Message{{Role: "user", Content: prompt}},
		Temperature:  e.temperature,
	})
	if err != nil {
		return nil, fmt.Errorf("fact extract: %w", err)
	}

	var res factExtractResult
	if _, err := ParseFlashJSON(msg.Content, &res); err != nil {
		log.Printf("[fact] extract parse failed: %v (raw %.120s)", err, msg.Content)
		return nil, nil
	}
	return sanitizeFactCandidates(res.Facts, e.maxCandidates), nil
}

// turnXML / dialogXML — fact 抽取对话块的 XML 结构（经 xml.Marshal 输出，避免手拼漏转义）。
type turnXML struct {
	XMLName xml.Name `xml:"turn"`
	Role    string   `xml:"role,attr"`
	At      string   `xml:"at,attr"`
	Content string   `xml:",chardata"`
}

type dialogXML struct {
	XMLName xml.Name  `xml:"对话"`
	Turns   []turnXML `xml:"turn"`
}

// renderFactDialog 把消息渲染成 <对话> XML 块。role 取实际值（user / assistant）。
// 每个 turn 带发生时间（北京时间）——用户消息里的相对日期由管线补成「原词（日期）」，
// assistant 消息不经管线，靠这个属性提供时间锚点。
// Content 为空的 turn 跳过（只发 tool call 的 assistant 消息 content 为空）。
// 无内容返回 ""（调用方据此不追加对话块）。渲染失败向上抛。
func renderFactDialog(msgs []model.Message) (string, error) {
	doc := dialogXML{Turns: make([]turnXML, 0, len(msgs))}
	for _, m := range msgs {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		doc.Turns = append(doc.Turns, turnXML{
			Role:    m.Role,
			At:      m.CreatedAt.In(config.BJ).Format("2006-01-02 15:04"),
			Content: content,
		})
	}
	if len(doc.Turns) == 0 {
		return "", nil
	}
	b, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal fact dialog xml: %w", err)
	}
	return string(b), nil
}

// sanitizeFactCandidates 校验候选：丢弃非法 kind 与空 content，按 max 截断。
// subject 为空不丢——content 已自足成句，subject 只做统计。
func sanitizeFactCandidates(in []factCandidate, max int) []factCandidate {
	out := make([]factCandidate, 0, len(in))
	dropped := 0
	for _, c := range in {
		c.Subject = strings.TrimSpace(c.Subject)
		c.Kind = strings.TrimSpace(c.Kind)
		c.Content = strings.TrimSpace(c.Content)
		if c.Kind != factKindState && c.Kind != factKindEvent {
			dropped++
			continue
		}
		if c.Content == "" {
			dropped++
			continue
		}
		out = append(out, c)
	}

	truncated := false
	if max > 0 && len(out) > max {
		out = out[:max]
		truncated = true
	}
	if dropped > 0 || truncated {
		log.Printf("[fact] candidates sanitized: kept=%d dropped=%d truncated=%v", len(out), dropped, truncated)
	}
	return out
}
