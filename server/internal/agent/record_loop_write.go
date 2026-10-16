package agent

import (
	"encoding/json"
	"fmt"
	"log"

	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

// filterWrittenFacts 写入侧的最后一道原话校验：模型写进来的每条 fact 必须是**候选原话**的一段
// （可截断不可改写）。丢片段不丢写入——facts 允许为空。
//
// 为什么需要它：入口的 verifyVerbatimFacts 校验的是「候选」，而真正落库的是决策循环那次采样的
// tool call 参数——第二次独立采样。没有这道收口，「facts 一定是用户原话」这个契约在最末端就是空的
// （见 docs/design/memory-facts-verbatim §3.6）。
func (s *recordLoopState) filterWrittenFacts(facts []string) []string {
	if len(facts) == 0 {
		return nil
	}
	kept, dropped := verifyVerbatimFacts(facts, s.candFacts)
	if dropped > 0 {
		log.Printf("[record_loop] %s facts 写入校验：丢弃 %d 条非候选原话片段", s.source, dropped)
	}
	return kept
}

func (s *recordLoopState) saveMemory(ec *tool.ExecuteContext, args json.RawMessage) *tool.Result {
	var a struct {
		Topic   string   `json:"topic"`
		Summary string   `json:"summary"`
		Facts   []string `json:"facts"`
	}
	if err := decodeRecordArgs(args, &a); err != nil {
		return errResult(err)
	}
	if s.saved {
		return &tool.Result{Success: false, Error: "一条候选只能写一条记忆"}
	}
	// topic 以 nameTopic 定稿的 s.candTopic 为准（LLM 的 topic 参数只是兜底）：
	// 定稿发生在决策循环之前，权威值不该被循环里 LLM 随手填的值覆盖或抹空。
	topic := keepOrReplaceTopic(s.candTopic, a.Topic)
	facts := s.filterWrittenFacts(a.Facts)
	if err := validateMemoryFields(topic, a.Summary, facts); err != nil {
		return errResult(err)
	}
	if err := validateFactCount(facts); err != nil {
		return errResult(err)
	}
	mem := s.newMemory(topic, a.Summary, facts)
	if err := s.persist(ec.Ctx, mem, true); err != nil {
		return errResult(err)
	}
	s.saved = true
	return &tool.Result{Success: true, Data: actionJSON("add", mem.ID)}
}

func (s *recordLoopState) updateMemory(ec *tool.ExecuteContext, args json.RawMessage) *tool.Result {
	var a struct {
		TargetID string   `json:"target_id"`
		Topic    string   `json:"topic"`
		Summary  string   `json:"summary"`
		Facts    []string `json:"facts"`
	}
	if err := decodeRecordArgs(args, &a); err != nil {
		return errResult(err)
	}
	if err := s.checkTarget(a.TargetID, model.EntityTypeMemory); err != nil {
		return errResult(err)
	}
	old, err := s.deps.MemRepo.FindByID(ec.Ctx, s.userID, a.TargetID)
	if err != nil {
		return errResult(fmt.Errorf("find memory: %w", err))
	}
	if old == nil {
		// 被并发删除：不算失败，记日志即可。
		log.Printf("[record_loop] update target %.8s not found, skip", a.TargetID)
		return &tool.Result{Success: false, Error: "目标记忆不存在"}
	}
	// facts 整体覆盖：不再是「与旧值去重合并」——旧做法只按精确字符串去重，
	// 换句话说的同一件事会一直累积，最后一条记忆挂几十条自相矛盾的细节（见
	// docs/design/memory-facts-verbatim/design.md §1）。facts 表示「这条记忆当前的出处」。
	facts := s.filterWrittenFacts(a.Facts)
	if err := validateFactCount(facts); err != nil {
		return errResult(err)
	}
	// topic：优先用 nameTopic 定稿的权威值；其次 LLM 传的；都没有则保留旧 topic。
	topic := keepOrReplaceTopic(keepOrReplaceTopic(s.candTopic, a.Topic), old.Topic)
	if err := validateMemoryFields(topic, a.Summary, facts); err != nil {
		return errResult(err)
	}
	mem := s.newMemory(topic, a.Summary, facts)
	mem.ID = old.ID
	if err := s.persist(ec.Ctx, mem, false); err != nil {
		return errResult(err)
	}
	return &tool.Result{Success: true, Data: actionJSON("update", mem.ID)}
}

func (s *recordLoopState) deleteMemory(ec *tool.ExecuteContext, args json.RawMessage) *tool.Result {
	id, err := s.targetID(args, model.EntityTypeMemory)
	if err != nil {
		return errResult(err)
	}
	if err := s.deps.MemRepo.Delete(ec.Ctx, id); err != nil {
		return errResult(fmt.Errorf("delete memory: %w", err))
	}
	s.purge(ec.Ctx, model.EntityTypeMemory, id)
	return &tool.Result{Success: true, Data: actionJSON("delete", id)}
}

func (s *recordLoopState) deleteNote(ec *tool.ExecuteContext, args json.RawMessage) *tool.Result {
	id, err := s.targetID(args, model.EntityTypeNote)
	if err != nil {
		return errResult(err)
	}
	if err := s.deps.NoteRepo.SoftDelete(ec.Ctx, id, s.userID); err != nil {
		return errResult(fmt.Errorf("soft delete note: %w", err))
	}
	s.purge(ec.Ctx, model.EntityTypeNote, id)
	return &tool.Result{Success: true, Data: actionJSON("delete", id)}
}
