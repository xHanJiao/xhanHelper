package agent

import (
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"sort"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/util"
)

// topic_namer — topic 单独一次 LLM 调用（区别于决策循环）。
//
// 为什么单独做：topic 是记忆卡片的标题/分类依据，要求非空且 ≤5 字。塞进抽取段
// 让 LLM「顺手输出」时，20 字上限都被频繁违反（改版后产生一批 7~20 字 topic）。
// 单独一次调用 + 明确的近邻列表，执行更稳（这也是本仓库改版前的做法）。
//
// 三步：① 用 summary 向量召回已有 topic（近邻，带引用数+相似度）
//
//	② 调 LLM 定 topic（优先复用已有）
//	③ 落库时 newMemory → reuseTopic 做 0.8 强制归并（既有线上逻辑）
//
// 重试上限：首次 + 重试 2 次；三次都不合规（空或超 5 字）才强制截断到 5 字
// （topic 必填，不能因不合规丢弃记忆）。
const (
	topicNameMaxAttempts = 3    // 首次 + 重试 2 次
	topicNeighborTopK    = 10   // 喂给 LLM 的已有 topic 条数
	topicRecallPreCap    = 40   // 算相似度前按引用次数粗截断的上限（省 GetOrEmbed 调用）
	topicFallbackName    = "其它" // 三次都不合规 + summary 全非中文时的保底 topic（topic 必填）
)

const topicNamerSystem = `你是记忆的主题归类助手。用户给你一条记忆和该用户已有的主题列表，你选一个最合适的主题词。
优先复用已有主题；已有列表里确实没有合适的，才新建一个。
新建的主题词必须：非空、5 个汉字以内、一个词，且**只能由汉字组成**——不许出现英文、数字、标点、空格。
只输出主题词本身，不要任何解释或 JSON。`

// topicNeighbor — 喂给 LLM 的一条已有 topic 参考。
type topicNeighbor struct {
	Topic      string
	RefCount   int64
	Similarity float64
}

// nameTopic 为一条候选记忆定 topic。summaryVec 是候选 summary 的向量（决策循环已算过，复用）。
// 失败一律降级并记日志，绝不返回空 topic。
func (d *RecordDecisionLoop) nameTopic(ctx context.Context, userID, topic, summary string, summaryVec []float64) string {
	// 默认值：LLM 全挂时用抽取段/主 LLM 给的原始 topic；仍为空则用 summary 兜底。
	fallback := clipTopic(topic)
	if fallback == "" {
		fallback = clipTopic(summary)
	}

	neighbors := d.recallTopics(ctx, userID, summaryVec)
	userMsg := buildTopicNamerInput(summary, neighbors)

	llmClient := d.deps.FlashLLM
	if d.deps.LLMBG != nil {
		llmClient = d.deps.LLMBG
	}

	var named string
	for attempt := 0; attempt < topicNameMaxAttempts; attempt++ {
		msg, err := llmClient.ChatSync(ctx, &llm.ChatRequest{
			SystemPrompt: topicNamerSystem,
			Messages:     []llm.Message{{Role: "user", Content: userMsg}},
			UserID:       userID,
		})
		if err != nil {
			log.Printf("[topic_namer] chat error (attempt %d/%d): %v", attempt+1, topicNameMaxAttempts, err)
			continue
		}
		// 合规 = trim 后非空且 ≤5 字；不合规重试。
		if t, ok := validTopic(msg.Content); ok {
			named = t
			break
		}
		log.Printf("[topic_namer] attempt %d/%d invalid topic %q", attempt+1, topicNameMaxAttempts, clipTopic(msg.Content))
	}
	if named == "" {
		// 三次都不合规：截断兜底。clipTopic 后仍可能为空（summary 全英文/数字），
		// 此时用固定保底值（topic 必填，不能因不合规丢弃记忆）。
		named = fallback
		if named == "" {
			named = topicFallbackName
		}
	}
	// 0.8 强制归并不在这里做：newMemory → reuseTopic 已在落库前用 topic 词向量做同一判定
	// （既有线上逻辑，保持不动）。这里只保证返回一个非空、≤5 字的 topic。
	return named
}

// validTopic 判定 LLM 输出的 topic 是否合规：trim 后非空、≤5 字、纯中文。合规返回 trim 值。
func validTopic(raw string) (string, bool) {
	t := strings.TrimSpace(raw)
	if t == "" || len([]rune(t)) > memoryTopicMaxLen || !isAllHan(t) {
		return "", false
	}
	return t, true
}

// recallTopics 用 summary 向量召回已有 topic：先按相似度取最相近的 top-K，再按引用次数降序展示。
// 向量走 GetOrEmbed（缓存优先），已有向量不重复调 API。
//
// 先按引用次数粗截断（topicRecallPreCap）再算相似度，避免对全量 topic 逐个 GetOrEmbed
// （对齐 AGENTS.md「禁止循环内逐个访问数据库」的精神）。
func (d *RecordDecisionLoop) recallTopics(ctx context.Context, userID string, summaryVec []float64) []topicNeighbor {
	if len(summaryVec) == 0 {
		return nil
	}
	refs, err := d.deps.distinctTopicsWithCount(ctx, userID)
	if err != nil {
		log.Printf("[topic_namer] distinct topics with count error: %v (no neighbors)", err)
		return nil
	}
	// 两阶段启发式：先按引用次数取前 N（refs 已按 count 降序）再算相似度。
	// ponytail: 高频但低频兄弟 topic 会被粗截断漏掉；topic 量级到几百再考虑去掉这层。
	if len(refs) > topicRecallPreCap {
		refs = refs[:topicRecallPreCap]
	}
	out := make([]topicNeighbor, 0, len(refs))
	for _, r := range refs {
		cv, err := d.deps.ConceptEmbed.GetOrEmbed(ctx, "topic", r.Topic, userID)
		if err != nil || len(cv) == 0 {
			continue
		}
		out = append(out, topicNeighbor{
			Topic: r.Topic, RefCount: r.Count, Similarity: util.CosineSimilarity(summaryVec, cv),
		})
	}
	// 按相似度取最相近的 top-K（真正该被复用/归并的），再按引用次数降序展示。
	sort.Slice(out, func(i, j int) bool {
		if out[i].Similarity != out[j].Similarity {
			return out[i].Similarity > out[j].Similarity
		}
		return out[i].RefCount > out[j].RefCount
	})
	if len(out) > topicNeighborTopK {
		out = out[:topicNeighborTopK]
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RefCount > out[j].RefCount })
	return out
}

// buildTopicNamerInput 拼 namer 的 user 消息：记忆内容 + 已有 topic 近邻。
// 一律经 xml.Marshal 输出（summary 是用户内容，手拼会拼出非良构 XML，见 record_loop_render.go）。
func buildTopicNamerInput(summary string, neighbors []topicNeighbor) string {
	doc := topicNamerInputXML{
		Memory: strings.TrimSpace(summary),
	}
	for _, n := range neighbors {
		doc.Topics = append(doc.Topics, topicNeighborXML{
			Value:      n.Topic,
			RefCount:   n.RefCount,
			Similarity: fmt.Sprintf("%.2f", n.Similarity),
		})
	}
	b, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		// 防御性兜底：encoding/xml 对控制字节通常替换成 U+FFFD 而非报错（见 record_loop_render_test.go），
		// 所以这条分支现实中基本不可达；真报错就只给记忆内容，不让整轮失败。
		log.Printf("[topic_namer] marshal input error: %v (drop neighbors)", err)
		fallback := topicNamerInputXML{Memory: strings.TrimSpace(summary)}
		b, err = xml.MarshalIndent(fallback, "", "  ")
		if err != nil {
			log.Printf("[topic_namer] marshal fallback error: %v", err)
			return ""
		}
	}
	return string(b)
}

// topicNamerInputXML — namer 的输入块。
type topicNamerInputXML struct {
	XMLName xml.Name           `xml:"topic_naming"`
	Memory  string             `xml:"memory"`
	Topics  []topicNeighborXML `xml:"existing_topics>topic,omitempty"`
}

type topicNeighborXML struct {
	Value      string `xml:"value,attr"`
	RefCount   int64  `xml:"ref_count,attr"`
	Similarity string `xml:"similarity,attr"`
}

// clipTopic 兜底：只保留汉字、截到 ≤5 字。去掉英文/数字/标点后可能为空（调用方需处理）。
func clipTopic(topic string) string {
	r := []rune(util.HanOnly(topic))
	if len(r) > memoryTopicMaxLen {
		r = r[:memoryTopicMaxLen]
	}
	return string(r)
}
