package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"xhan-helper/internal/app"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// FactWorker — 事实系统后处理子任务（PostChatWorker）。
// 编排：抽取(LLM) → 逐候选串行 { 归一化判重 → 检索 → 相似度门槛 → 决策(LLM) → 写入 }。
// 逐条处理完立即写库并同步向量：候选 N 的写入必须对候选 N+1 的检索可见，
// 否则批内互不可见会让两条相似候选都判 ADD，库里就真重复了。
type FactWorker struct {
	extractor     *FactExtractor
	decider       *FactDecider
	enabled       bool
	factRepo      *service.FactRepo
	changelogRepo *service.FactChangelogRepo
	embedder      app.Embedder
	embeddingRepo *service.EmbeddingRepo
	vectorStore   app.VectorStore
	topK          int
	dupeThreshold float64
}

// FactWorkerConfig — FactWorker 依赖配置。
// 必需依赖不做 nil 检查：为 nil 是接线 bug，应炸在明处。
type FactWorkerConfig struct {
	Extractor     *FactExtractor
	Decider       *FactDecider
	Enabled       bool
	FactRepo      *service.FactRepo
	ChangelogRepo *service.FactChangelogRepo
	Embedder      app.Embedder
	EmbeddingRepo *service.EmbeddingRepo
	VectorStore   app.VectorStore
	TopK          int     // 0 → 默认 5
	DupeThreshold float64 // <= 0 → 默认 0.95
}

// NewFactWorker 创建事实 worker。
func NewFactWorker(cfg *FactWorkerConfig) *FactWorker {
	if cfg.TopK <= 0 {
		cfg.TopK = 5
	}
	if cfg.DupeThreshold <= 0 {
		cfg.DupeThreshold = 0.95
	}
	return &FactWorker{
		extractor:     cfg.Extractor,
		decider:       cfg.Decider,
		enabled:       cfg.Enabled,
		factRepo:      cfg.FactRepo,
		changelogRepo: cfg.ChangelogRepo,
		embedder:      cfg.Embedder,
		embeddingRepo: cfg.EmbeddingRepo,
		vectorStore:   cfg.VectorStore,
		topK:          cfg.TopK,
		dupeThreshold: cfg.DupeThreshold,
	}
}

// Name 实现 PostChatWorker。
func (w *FactWorker) Name() string { return "fact" }

// factStats — 一次 Process 的计数，末尾汇总成一行日志。
type factStats struct {
	candidates     int
	skippedDupe    int
	skippedSimilar int
	failed         int
	added          int
	updated        int
	deleted        int
	none           int
}

// Process 抽取候选事实并逐条决策写入。关闭时直接跳过（不消费 LLM）。
func (w *FactWorker) Process(ctx context.Context, sess *SessionSnapshot) error {
	if !w.enabled {
		return nil
	}
	ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
		Category:    llmcost.CategoryPostChat,
		SubCategory: llmcost.SubFactExtract,
		UserID:      sess.UserID,
		ConvID:      sess.ConvID,
	})
	cands, err := w.extractor.Extract(ctx, sess.GetAllMessages())
	if err != nil {
		return err
	}

	st := factStats{candidates: len(cands)}
	if len(cands) > 0 {
		srcMsgID := lastUserMsgID(sess)
		for i, cand := range cands {
			if err := w.applyCandidate(ctx, sess.UserID, sess.ConvID, srcMsgID, i, cand, &st); err != nil {
				return err
			}
		}
	}
	log.Printf("[fact] conv=%.8s candidates=%d skipped_dupe=%d skipped_similar=%d failed=%d added=%d updated=%d deleted=%d none=%d",
		sess.ConvID, st.candidates, st.skippedDupe, st.skippedSimilar, st.failed, st.added, st.updated, st.deleted, st.none)
	return nil
}

// applyCandidate 处理一条候选：判重 → 检索 → 相似度门槛 → 决策 → 写入。
// 只有 DB 写失败才向上返回 error；单条候选的 LLM 失败降级为记日志并继续下一条。
func (w *FactWorker) applyCandidate(ctx context.Context, userID, convID, srcMsgID string, idx int, cand factCandidate, st *factStats) error {
	contentHash := hashPrompt(normalizeFactContent(cand.Content))

	dup, err := w.factRepo.FindByContentHash(ctx, userID, contentHash)
	if err != nil {
		return fmt.Errorf("fact dedupe: %w", err)
	}
	if dup != nil {
		st.skippedDupe++
		return nil
	}

	neighbors := w.retrieveNeighbors(ctx, userID, cand.Content)
	if len(neighbors) > 0 && neighbors[0].Score >= w.dupeThreshold {
		log.Printf("[fact] skip candidate (top1=%.4f >= %.2f): %.60s", neighbors[0].Score, w.dupeThreshold, cand.Content)
		st.skippedSimilar++
		return nil
	}

	dctx := llmcost.WithScenario(ctx, llmcost.Scenario{
		Category:    llmcost.CategoryPostChat,
		SubCategory: llmcost.SubFactDecide,
		UserID:      userID,
		ConvID:      convID,
		RefType:     "fact",
		RefID:       strconv.Itoa(idx),
	})
	actions, err := w.decider.Decide(dctx, cand, neighbors)
	if err != nil {
		log.Printf("[fact] decide candidate %d failed: %v", idx, err)
		st.failed++
		return nil
	}
	if len(actions) == 0 {
		st.none++
		return nil
	}
	for _, act := range actions {
		if err := w.applyAction(ctx, userID, srcMsgID, act, st); err != nil {
			return err
		}
	}
	return nil
}

// retrieveNeighbors 取候选的向量近邻（同用户的事实，top_k，无阈值）。
// embed 失败降级为「无近邻」并记日志：冒一条重复的风险小于丢一条事实。
func (w *FactWorker) retrieveNeighbors(ctx context.Context, userID, content string) []app.VectorMatch {
	vec, err := w.embedder.Embed(ctx, content)
	if err != nil {
		log.Printf("[fact] embed candidate error: %v (proceeding without neighbors)", err)
		return nil
	}
	return w.vectorStore.Search(vec, w.topK, func(_ string, _ []float64, meta map[string]string) bool {
		return meta["type"] == model.VectorTypeFact && meta["user_id"] == userID
	})
}

// applyAction 执行一条决策动作。
func (w *FactWorker) applyAction(ctx context.Context, userID, srcMsgID string, act factAction, st *factStats) error {
	switch act.Op {
	case factOpAdd:
		return w.addFact(ctx, userID, act, st)
	case factOpUpdate:
		return w.updateFact(ctx, userID, srcMsgID, act, st)
	case factOpDelete:
		return w.deleteFact(ctx, userID, srcMsgID, act, st)
	}
	return nil
}

// addFact 新开一条事实。
func (w *FactWorker) addFact(ctx context.Context, userID string, act factAction, st *factStats) error {
	f := &model.Fact{
		ID:          uuid.New().String(),
		UserID:      userID,
		Subject:     act.Subject,
		Kind:        act.Kind,
		Content:     act.Content,
		ContentHash: hashPrompt(normalizeFactContent(act.Content)),
		Fields:      act.Fields,
		Status:      model.FactStatusActive,
	}
	if err := w.factRepo.Create(ctx, f); err != nil {
		return err
	}
	w.syncVector(ctx, f)
	st.added++
	return nil
}

// updateFact 覆盖一条既有事实。目标不存在（被并发删除）时跳过，不算失败。
func (w *FactWorker) updateFact(ctx context.Context, userID, srcMsgID string, act factAction, st *factStats) error {
	old, err := w.factRepo.FindByID(ctx, userID, act.TargetID)
	if err != nil {
		return err
	}
	if old == nil {
		log.Printf("[fact] update target %.8s not found, skip", act.TargetID)
		st.failed++
		return nil
	}
	if err := w.factRepo.UpdateContent(ctx, old.ID, act.Content,
		hashPrompt(normalizeFactContent(act.Content)), act.Subject, act.Kind, act.Fields); err != nil {
		return err
	}
	w.logChange(ctx, userID, old.ID, model.FactOpUpdate, old.Content, act.Content, srcMsgID)

	// 向量 metadata 必须是更新后的值，否则下一轮检索命中却读到旧正文
	updated := *old
	updated.Content = act.Content
	if act.Subject != "" {
		updated.Subject = act.Subject
	}
	if act.Kind != "" {
		updated.Kind = act.Kind
	}
	if act.Fields != nil {
		updated.Fields = act.Fields
	}
	w.syncVector(ctx, &updated)
	st.updated++
	return nil
}

// deleteFact 软删一条事实，并清掉内存向量与 embedding 行。
func (w *FactWorker) deleteFact(ctx context.Context, userID, srcMsgID string, act factAction, st *factStats) error {
	old, err := w.factRepo.FindByID(ctx, userID, act.TargetID)
	if err != nil {
		return err
	}
	if old == nil {
		log.Printf("[fact] delete target %.8s not found, skip", act.TargetID)
		st.failed++
		return nil
	}
	if err := w.factRepo.SoftDelete(ctx, old.ID); err != nil {
		return err
	}
	w.logChange(ctx, userID, old.ID, model.FactOpDelete, old.Content, "", srcMsgID)
	w.vectorStore.Delete(model.VectorPrefixFact + old.ID)
	if err := w.embeddingRepo.DeleteByEntity(ctx, model.EntityTypeFact, old.ID); err != nil {
		log.Printf("[fact] delete embedding %.8s error: %v", old.ID, err)
	}
	st.deleted++
	return nil
}

// syncVector 同步写向量与 embedding 行（写库后立即调用，保证后续候选检索可见）。
// embed 失败只记日志：事实已在库里，这次缺的是可检索性，下次被 UPDATE 时会顺带补上。
func (w *FactWorker) syncVector(ctx context.Context, f *model.Fact) {
	vec, err := w.embedder.Embed(ctx, f.Content)
	if err != nil {
		log.Printf("[fact] embed fact %.8s error: %v", f.ID, err)
		return
	}
	w.vectorStore.Upsert(model.VectorPrefixFact+f.ID, vec, map[string]string{
		"type":    model.VectorTypeFact,
		"user_id": f.UserID,
		"subject": f.Subject,
		"kind":    f.Kind,
		"content": f.Content,
	})
	embStr := llm.EmbeddingToString(vec)
	if err := w.embeddingRepo.Upsert(ctx, &model.Embedding{
		ID:         f.ID,
		EntityType: model.EntityTypeFact,
		EntityID:   f.ID,
		UserID:     f.UserID,
		Model:      w.embedder.ModelName(),
		Embedding:  &embStr,
	}); err != nil {
		log.Printf("[fact] upsert embedding %.8s error: %v", f.ID, err)
	}
}

// logChange 写一条变更审计。审计不是主契约，失败只记日志不打断。
func (w *FactWorker) logChange(ctx context.Context, userID, factID, op, oldContent, newContent, srcMsgID string) {
	if err := w.changelogRepo.Create(ctx, &model.FactChangelog{
		ID:          idgen.Next(),
		UserID:      userID,
		FactID:      factID,
		Op:          op,
		OldContent:  oldContent,
		NewContent:  newContent,
		SourceMsgID: srcMsgID,
	}); err != nil {
		log.Printf("[fact] changelog %s %.8s error: %v", op, factID, err)
	}
}

// lastUserMsgID 取快照里最后一条 user 消息的 id（changelog 的来源标注，可能为空）。
func lastUserMsgID(sess *SessionSnapshot) string {
	msgs := sess.GetUserMessages()
	if len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1].ID
}

// normalizeFactContent 归一化 content 用于精确判重：抹平写法差异，保留内容差异。
// 只去空白/标点/符号，全角转半角、大小写归一——数字与日期必须保留，
// 否则「9-18 和老K吵架」与「9-25 和老K吵架」会归一成同一个串，第二条被静默丢掉。
func normalizeFactContent(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\u3000' { // 全角空格
			continue
		}
		if r >= 0xFF01 && r <= 0xFF5E { // 全角 ASCII → 半角
			r -= 0xFEE0
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// —— 决策段 ——

// 动作类型（factAction.Op）。
const (
	factOpAdd    = "add"
	factOpUpdate = "update"
	factOpDelete = "delete"
)

// factDecideSystemPrompt — 决策段专用 system prompt。
// 不复用 postChatSharedSystemPrompt：那条描述的是「用户发言的时间序对话」，
// 而这里的输入是「一条候选 + 它的近邻」，两者对不上。
const factDecideSystemPrompt = `你是一个事实库的写入决策助手。输入是一条候选事实和它在事实库里检索到的近邻事实。`

// factDecideInstruction — 决策段指令消息。常量，放在 user 消息最前（候选块紧随其后），
// 以便命中 provider 的 prompt 前缀缓存。正文见 docs/design/facts-system/decision.md §5.1。
const factDecideInstruction = `你是事实库的写入决策者。上游给你一条候选事实，以及它在事实库里检索到的近邻事实。
你要为这条候选选一个动作：新开一条、改一条、删一条，或者什么都不做。

## 输入

<candidate>：待写入的候选，字段 subject / kind / fields / content。
<neighbors>：按 content 向量相似度从事实库取回的近邻（不设阈值，可能一条都不相关）。
每个近邻写成 <fact id="..." subject="..." kind="...">正文</fact>，id 是它的唯一标识。

## 动作

- add_fact(subject, kind, content, fields?)：新开一条。
- update_fact(target_id, content, subject?, kind?, fields?)：近邻里有一条在回答同一个问题，答案变了或者更全。
- delete_fact(target_id) 加 add_fact：候选被近邻里某条明确否定。
- 什么都不调：与某条近邻同义，跳过。

## 判据

同一个问题 = 同一主体 ＋ 同一断言位。
库里有「本人只做技术」，候选是「本人兼产品开发」——同主体、同一断言位（他在这个项目里担任什么），答案变了 → update_fact。

update 还是 add，看是不是同一条事实的更完整版本：多了一件无关的新事 → add_fact；同一件事变了 → update_fact。

delete 只用于明确否定：「我不想再管那笔债了」否定「计划为家里还债」→ delete_fact；只是补充细节 → update_fact。

观点不许覆盖成「最新观点」。观点是 event，说出口就固定，只增不改。

近邻可能一条都不相关，也可能内容相近但不是同一件事。以判据为准，不要因为拿到了 id 就硬找一条来改。

## 输出

这条候选只选一个动作（delete 加 add 算一个组合），不许返回整库新状态。
写成 tool call。
target_id 只能取自输入里的 id，不许自造。
update_fact 的 content 必须写改写后的完整句子——自足成句、主体名写进句子，不是只写变化的那半句。
候选的 fields（如 due_at）与近邻旧值不同时一并给出；没有就不传。`

// factDecideTool — 决策段的工具定义。三个函数只是模型的输出格式，
// 执行者是本文件，不进 tool.Registry（不走注册表的校验/白名单/只读那套）。
var factDecideTool = llm.ToolDefinition{
	Name:        "facts",
	Description: "对候选事实执行写入决策",
	Functions: []llm.FuncDef{
		{
			Name:        "add_fact",
			Description: "新开一条事实",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"subject": map[string]interface{}{"type": "string", "description": "这条事实关于谁／什么"},
					"kind":    map[string]interface{}{"type": "string", "enum": []string{factKindState, factKindEvent}},
					"content": map[string]interface{}{"type": "string", "description": "自足成句的一句话，只放一个断言"},
					"fields":  map[string]interface{}{"type": "object", "description": `可选字段，如 {"due_at":"2026-09-21"}`},
				},
				"required": []string{"subject", "kind", "content"},
			},
		},
		{
			Name:        "update_fact",
			Description: "覆盖近邻中某一条事实（同一主体、同一断言位，答案变了）",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"target_id": map[string]interface{}{"type": "string", "description": "被覆盖的近邻 id，只能取自输入的 id"},
					"content":   map[string]interface{}{"type": "string", "description": "改写后的完整句子（不是只写变化的那半句）"},
					"subject":   map[string]interface{}{"type": "string", "description": "主体变了才传"},
					"kind":      map[string]interface{}{"type": "string", "enum": []string{factKindState, factKindEvent}},
					"fields":    map[string]interface{}{"type": "object", "description": `字段与旧值不同时一并给出，如 {"due_at":"2026-09-25"}`},
				},
				"required": []string{"target_id", "content"},
			},
		},
		{
			Name:        "delete_fact",
			Description: "删除近邻中某条被候选明确否定的事实",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"target_id": map[string]interface{}{"type": "string", "description": "被删除的近邻 id，只能取自输入的 id"},
				},
				"required": []string{"target_id"},
			},
		},
	},
}

// decideParams — 三个决策函数的参数并集（共用一个结构解码）。
type decideParams struct {
	TargetID string         `json:"target_id"`
	Subject  string         `json:"subject"`
	Kind     string         `json:"kind"`
	Content  string         `json:"content"`
	Fields   map[string]any `json:"fields"`
}

// factAction — 决策段产出的一条动作，由 FactWorker 执行。
type factAction struct {
	Op       string // factOpAdd / factOpUpdate / factOpDelete
	TargetID string // update / delete 用，已校验在近邻集内
	Subject  string // 空 = 不改（update）/ 无主体（add 不该为空）
	Kind     string // 空 = 不改（update）
	Content  string
	Fields   *string // 已 Marshal 的 JSON；nil = 不改
}

// FactDecider — 决策段：把「候选 + 近邻」交给 LLM，产出写入动作。只决策，不落库。
type FactDecider struct {
	llmClient   llm.LLMClient
	temperature *float64
}

// FactDeciderConfig — 决策器依赖配置。
type FactDeciderConfig struct {
	LLMClient   llm.LLMClient
	Temperature *float64 // nil → 用模型默认
}

// NewFactDecider 创建决策器。
func NewFactDecider(cfg *FactDeciderConfig) *FactDecider {
	return &FactDecider{llmClient: cfg.LLMClient, temperature: cfg.Temperature}
}

// Decide 为一条候选产出动作。返回 nil 表示 NONE（模型什么都没调）。LLM 出错时上抛。
// 始终走原生 function calling：use_native_fc 只管 orchestrator 自己的工具列表。
func (d *FactDecider) Decide(ctx context.Context, cand factCandidate, neighbors []app.VectorMatch) ([]factAction, error) {
	idSet := make(map[string]bool, len(neighbors))
	var nb strings.Builder
	for _, m := range neighbors {
		id := strings.TrimPrefix(m.ID, model.VectorPrefixFact)
		idSet[id] = true
		fmt.Fprintf(&nb, "  <fact id=\"%s\" subject=\"%s\" kind=\"%s\">%s</fact>\n",
			xmlEscape(id), xmlEscape(m.Metadata["subject"]), xmlEscape(m.Metadata["kind"]),
			xmlEscape(m.Metadata["content"]))
	}

	var cb strings.Builder
	cb.WriteString("<candidate>\n")
	fmt.Fprintf(&cb, "  <subject>%s</subject>\n", xmlEscape(cand.Subject))
	fmt.Fprintf(&cb, "  <kind>%s</kind>\n", xmlEscape(cand.Kind))
	fmt.Fprintf(&cb, "  <content>%s</content>\n", xmlEscape(cand.Content))
	if len(cand.Fields) > 0 {
		if b, err := json.Marshal(cand.Fields); err != nil {
			log.Printf("[fact] marshal candidate fields error: %v", err)
		} else {
			fmt.Fprintf(&cb, "  <fields>%s</fields>\n", xmlEscape(string(b)))
		}
	}
	cb.WriteString("</candidate>\n")
	if nb.Len() == 0 {
		cb.WriteString("<neighbors></neighbors>")
	} else {
		cb.WriteString("<neighbors>\n")
		cb.WriteString(nb.String())
		cb.WriteString("</neighbors>")
	}

	msg, err := d.llmClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: factDecideSystemPrompt,
		Messages:     []llm.Message{{Role: "user", Content: factDecideInstruction + "\n\n" + cb.String()}},
		Tools:        []llm.ToolDefinition{factDecideTool},
		Temperature:  d.temperature,
	})
	if err != nil {
		return nil, fmt.Errorf("fact decide: %w", err)
	}

	out := make([]factAction, 0, 1)
	for _, tc := range msg.ToolCalls {
		// provider 把函数名拼成 facts__add_fact；裸名（模型不按前缀调）也能命中。
		_, fn := splitToolFuncName(tc.Function.Name)
		var p decideParams
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &p); err != nil {
			log.Printf("[fact] decide: bad args for %s: %v (raw %.120s)", fn, err, tc.Function.Arguments)
			continue
		}
		act, ok := buildFactAction(fn, p, idSet)
		if ok {
			out = append(out, act)
		}
	}
	return out, nil
}

// buildFactAction 校验并构造一条动作。不合法的调用丢弃并记日志（不拿零值继续写库）。
func buildFactAction(fn string, p decideParams, idSet map[string]bool) (factAction, bool) {
	content := strings.TrimSpace(p.Content)
	kind := strings.TrimSpace(p.Kind)
	switch fn {
	case "add_fact":
		if content == "" || (kind != factKindState && kind != factKindEvent) {
			log.Printf("[fact] decide: bad add_fact kind=%q content=%.40q", kind, content)
			return factAction{}, false
		}
		return factAction{
			Op:      factOpAdd,
			Subject: strings.TrimSpace(p.Subject),
			Kind:    kind,
			Content: content,
			Fields:  marshalFactFields(p.Fields),
		}, true
	case "update_fact":
		id := strings.TrimSpace(p.TargetID)
		if !idSet[id] {
			log.Printf("[fact] decide: update_fact target_id %q not in neighbors, dropped", p.TargetID)
			return factAction{}, false
		}
		if content == "" {
			log.Printf("[fact] decide: update_fact %.8s missing content, dropped", id)
			return factAction{}, false
		}
		if kind != "" && kind != factKindState && kind != factKindEvent {
			kind = "" // 非法 kind 视为没给（保留旧值），别把整条动作丢掉
		}
		return factAction{
			Op:       factOpUpdate,
			TargetID: id,
			Subject:  strings.TrimSpace(p.Subject),
			Kind:     kind,
			Content:  content,
			Fields:   marshalFactFields(p.Fields),
		}, true
	case "delete_fact":
		id := strings.TrimSpace(p.TargetID)
		if !idSet[id] {
			log.Printf("[fact] decide: delete_fact target_id %q not in neighbors, dropped", p.TargetID)
			return factAction{}, false
		}
		return factAction{Op: factOpDelete, TargetID: id}, true
	}
	log.Printf("[fact] decide: unknown function %q, dropped", fn)
	return factAction{}, false
}

// marshalFactFields 把工具参数里的 fields 编成 JSON 字符串。
// 空 map 与 nil 都返回 nil（= 不改）：模型给 {} 的意思是「没有额外字段」，不是「清空」。
func marshalFactFields(fields map[string]any) *string {
	if len(fields) == 0 {
		return nil
	}
	b, err := json.Marshal(fields)
	if err != nil {
		log.Printf("[fact] marshal fields error: %v", err)
		return nil
	}
	s := string(b)
	return &s
}
