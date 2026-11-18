package agent

import (
	"encoding/json"
	"fmt"

	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

// ==================== recordLoopState：闭包工具 ====================
//
// 决策循环的 8 个函数是闭包（持有 userID / repo / 近邻集合），不是注册表里的全局工具。
// MiniReAct 要求 ToolRegistry 非 nil 且统一走 ExecuteByName，故每次循环临时建一个
// registry 并用本适配器注册进去。Register 只是 map 插入，Validate* 是启动时单独调的，
// 临时注册表不会触发校验 panic。

type recordLoopState struct {
	deps      *RecordDeps
	cfg       RecordLoopConfig
	userID    string
	source    string
	candTopic string // nameTopic 定稿的 topic（权威值，写入时优先于 LLM 的 topic 参数）
	// candFacts 入口已校验过的候选原话。它是**写入侧过滤的唯一基准**：决策循环是第二次
	// 独立采样的 LLM，没有这道它在 tool call 里写什么就落什么（见 filterWrittenFacts）。
	candFacts []string
	// sourceConvID 写下这条记忆的会话（无痕销毁时按它回溯清理，见 docs/design/ephemeral-conversation.md）。
	// 只用于新建行；update 改的是别人的行，不覆盖出处。
	sourceConvID string
	idSet        map[string]bool
	idType       map[string]string
	saved        bool // 一条候选最多新增 1 条记忆
}

func (s *recordLoopState) Name() string { return recordToolMemory }
func (s *recordLoopState) Description() string {
	return "候选记忆的写入决策工具"
}

// Functions 只为一个目的存在：让临时 registry 能满足 Tool 接口。
// 真正的函数定义走 LLM 请求体的 recordToolDefinitions()（三组、函数名不带前缀）。
func (s *recordLoopState) Functions() []tool.FuncSchema {
	return []tool.FuncSchema{{
		Name: "save_memory", Description: "新开一条记忆",
		Parameters: map[string]interface{}{"type": "object"},
	}}
}

// ReadOnlyFuncs 返回 nil = 全可写。MiniReAct 里 ec.Mode 被硬编码 ConvModeWork，plan 拦截不会误伤。
func (s *recordLoopState) ReadOnlyFuncs() map[string]bool { return nil }

// Execute 分发 8 个函数。funcName 已是裸名——MiniReAct 的 splitToolFuncName 已剥掉 `memory__` 前缀。
func (s *recordLoopState) Execute(ec *tool.ExecuteContext, funcName string, args json.RawMessage) *tool.Result {
	switch funcName {
	case "search_memory_semantic":
		return s.searchSemantic(ec, model.EntityTypeMemory, args)
	case "search_note_semantic":
		return s.searchSemantic(ec, model.EntityTypeNote, args)
	case "search_memory_keyword":
		return s.searchKeyword(ec, model.EntityTypeMemory, args)
	case "search_note_keyword":
		return s.searchKeyword(ec, model.EntityTypeNote, args)
	case "save_memory":
		return s.saveMemory(ec, args)
	case "update_memory":
		return s.updateMemory(ec, args)
	case "delete_memory":
		return s.deleteMemory(ec, args)
	case "delete_note":
		return s.deleteNote(ec, args)
	}
	return &tool.Result{Success: false, Error: "unknown function: " + funcName}
}

// targetID 解析并校验 target_id：必须落在近邻集合内，且类型匹配。
func (s *recordLoopState) targetID(args json.RawMessage, wantType string) (string, error) {
	var a struct {
		TargetID string `json:"target_id"`
	}
	if err := decodeRecordArgs(args, &a); err != nil {
		return "", err
	}
	if err := s.checkTarget(a.TargetID, wantType); err != nil {
		return "", err
	}
	return a.TargetID, nil
}

// checkTarget 两个坑一起盯：① id 必须来自近邻（剥前缀后进 idSet，防幻觉 id）；
// ② 类型必须匹配，否则 delete_note 可能被喂一个 memory 的 id —— 那就删错表了。
func (s *recordLoopState) checkTarget(id, wantType string) error {
	if !s.idSet[id] {
		return fmt.Errorf("target_id 不存在（只能取自输入的近邻 id）: %s", id)
	}
	if got := s.idType[id]; got != wantType {
		return fmt.Errorf("target_id 类型不符：期望 %s，实际 %s", wantType, got)
	}
	return nil
}

// recordLoopStateAlias 让同一个 state 以不同组名注册进临时 registry。
// recordToolDefinitions() 暴露三组（record_search / memory / legacy_note），
// provider 把函数名拼成「组名__函数名」，MiniReAct 按组名查 registry。
// 三组共用同一份 Execute（按裸函数名分发），故别名只覆写 Name()，
// 其余方法全部委派给内嵌的 *recordLoopState。
type recordLoopStateAlias struct {
	*recordLoopState
	name string
}

func (a recordLoopStateAlias) Name() string { return a.name }

// registerRecordLoopTools 把 state 按三个组名各注册一次。
// 只注册 Name()="memory" 会导致 record_search / legacy_note 两组查不到（「未找到工具」）。
func registerRecordLoopTools(reg *tool.Registry, state *recordLoopState) {
	reg.Register(state)
	for _, name := range []string{recordToolSearch, recordToolLegacy} {
		reg.Register(recordLoopStateAlias{recordLoopState: state, name: name})
	}
}
