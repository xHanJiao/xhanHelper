package agent

import (
	"encoding/json"
	"fmt"
	"log"

	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

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
	if err := validateMemoryFields(topic, a.Summary, a.Facts); err != nil {
		return errResult(err)
	}
	if err := validateFactCount(a.Facts); err != nil {
		return errResult(err)
	}
	mem := s.newMemory(topic, a.Summary, a.Facts)
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
	// facts 只增不减：与旧 facts 去重合并后再校验（模型可只传新增的细节）。
	// 条数上限只卡模型的入参（a.Facts），合并结果不逐出。
	if err := validateFactCount(a.Facts); err != nil {
		return errResult(err)
	}
	// topic：优先用 nameTopic 定稿的权威值；其次 LLM 传的；都没有则保留旧 topic。
	topic := keepOrReplaceTopic(keepOrReplaceTopic(s.candTopic, a.Topic), old.Topic)
	facts := mergeFacts(parseFactsPtr(old.Facts), a.Facts)
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
