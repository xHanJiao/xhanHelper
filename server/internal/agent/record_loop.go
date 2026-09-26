package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

// RecordInline — inline 入口：主循环 record_memory 工具调用此处。
// 对一条候选跑决策循环，返回 `{action,id,summary}` JSON 供主 LLM 知晓发生了什么。
func (d *RecordDecisionLoop) RecordInline(ctx context.Context, userID, topic, summary string, facts []string) ([]byte, error) {
	// 主聊天 inline 调用：补审计场景（决策循环内含 LLM 调用），保留上游 UserID/ConvID。
	// 不这样做就会丢场景 → 审计记成空 category（前端「未标注」）。
	sc, _ := llmcost.ScenarioFromContext(ctx)
	sc.Category = llmcost.CategoryTool
	sc.SubCategory = llmcost.SubRecordMemory
	if sc.UserID == "" {
		sc.UserID = userID
	}
	ctx = llmcost.WithScenario(ctx, sc)

	res, err := d.Decide(ctx, userID, recordCandidate{Summary: summary, Facts: facts, Topic: topic})
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"action": res.Action, "id": res.ID, "summary": summary})
}

// Decide 对一条候选跑一遍决策循环。返回本条的实际动作。LLM 出错时上抛。
func (d *RecordDecisionLoop) Decide(ctx context.Context, userID string, cand recordCandidate) (*recordLoopResult, error) {
	neighbors, summaryVec, err := d.preRetrieve(ctx, userID, cand.Summary)
	if err != nil {
		return nil, fmt.Errorf("record loop pre-retrieve: %w", err)
	}
	// topic 单独一次 LLM 调用定稿（在决策循环之前）。summaryVec 复用上面那次 embed。
	cand.Topic = d.nameTopic(ctx, userID, cand.Topic, cand.Summary, summaryVec)

	state := &recordLoopState{deps: d.deps, cfg: d.cfg, userID: userID, source: d.source,
		candTopic: cand.Topic, neighbors: neighbors, idSet: map[string]bool{}, idType: map[string]string{}}
	for _, n := range neighbors {
		state.idSet[n.ID] = true
		state.idType[n.ID] = n.Type
	}

	registry := tool.NewRegistry()
	registerRecordLoopTools(registry, state)
	llmClient := d.deps.FlashLLM
	if d.deps.LLMBG != nil {
		llmClient = d.deps.LLMBG
	}
	loop := NewMiniReAct(&MiniReActConfig{
		Client:           llmClient,
		Tools:            recordToolDefinitions(),
		ToolRegistry:     registry,
		SystemPrompt:     recordLoopSystemPrompt,
		FinalRoundPrompt: recordLoopFinalRoundPrompt,
		UserID:           userID,
		MaxIterations:    d.cfg.MaxIterations,
		Caller:           recordLoopCaller,
		OnEvent:          nil, // 后台/工具内静默，不推 SSE
		// MsgSink 零值 nil：循环消息纯内存，不落库。
	})

	res, err := func() (*MiniReActResult, error) {
		input, err := renderCandidateInput(cand, neighbors)
		if err != nil {
			return nil, err
		}
		return loop.Run(ctx, input)
	}()
	if err != nil {
		return nil, fmt.Errorf("record loop run: %w", err)
	}

	out := &recordLoopResult{Action: "none"}
	for _, tc := range res.ToolCallLog {
		_, fn := splitToolFuncName(tc.Function)
		if isWriteFunc(fn) && tc.Result != nil && tc.Result.Success {
			out.Action = writeFuncAction(fn)
			out.ID = extractResultID(tc.Result)
			break
		}
	}
	// 一个写入工具都没调 → NONE：对最高分记忆近邻做「再次确认」（须过阈值）。
	if out.Action == "none" {
		if id := d.confirmTopNeighbor(ctx, userID, neighbors); id != "" {
			out.Action = "confirm"
			out.ID = id
		}
	}
	log.Printf("[record_loop] user=%.8s source=%s action=%s id=%.8s cand=%.40s",
		userID, d.source, out.Action, out.ID, cand.Summary)
	return out, nil
}

// preRetrieve 预检索：embed 候选 summary → memory + note 两域取近邻。
// 返回的近邻用于决策；embedding 向量一并返回供 topic 命名复用（不重复 embed）。
// 这是「ReAct 保留下来」的前提：没有它，模型得先花一轮自己搜。
func (d *RecordDecisionLoop) preRetrieve(ctx context.Context, userID, summary string) ([]recordNeighbor, []float64, error) {
	// TODO: 这里的嵌入结果可以做一些复用。
	vec, err := d.deps.Embedder.Embed(ctx, summary)
	if err != nil {
		return nil, nil, fmt.Errorf("embed candidate: %w", err)
	}
	match := func(t string) func(retrieval.Entry) bool {
		return func(e retrieval.Entry) bool {
			return e.Metadata["type"] == t && e.Metadata["user_id"] == userID
		}
	}
	var out []recordNeighbor
	for _, spec := range []struct {
		vtype  string
		prefix string
		typ    string
	}{
		{model.VectorTypeMemory, model.VectorPrefixMemory, model.EntityTypeMemory},
		{model.VectorTypeNote, model.VectorPrefixNote, model.EntityTypeNote},
	} {
		for _, m := range d.deps.VectorStore.Search(vec, d.cfg.NeighborTopK, match(spec.vtype)) {
			if m.Score < d.cfg.NeighborThresh {
				continue
			}
			out = append(out, recordNeighbor{
				ID:      strings.TrimPrefix(m.ID, spec.prefix),
				Type:    spec.typ,
				Topic:   m.Metadata["topic"],
				Content: m.Metadata["content"],
				Score:   m.Score,
			})
		}
	}
	return out, vec, nil
}

// confirmTopNeighbor NONE 时取最高分记忆近邻，相似度须 ≥ ConfirmThresh。
// 卡阈值的原因：NONE 只说「没有新东西要写」，不代表最高分近邻就是被重申的那条。
func (d *RecordDecisionLoop) confirmTopNeighbor(ctx context.Context, userID string, neighbors []recordNeighbor) string {
	var best *recordNeighbor
	for i := range neighbors {
		n := &neighbors[i]
		if n.Type != model.EntityTypeMemory || n.Score < d.cfg.ConfirmThresh {
			continue
		}
		if best == nil || n.Score > best.Score {
			best = n
		}
	}
	if best == nil {
		return ""
	}
	if err := d.deps.MemRepo.UpdateConfirm(ctx, best.ID); err != nil {
		log.Printf("[record_loop] confirm %.8s error: %v", best.ID, err)
		return ""
	}
	return best.ID
}
