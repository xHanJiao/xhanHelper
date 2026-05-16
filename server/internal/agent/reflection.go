package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

const launchAgentPrompt = `你是 xhanHelper 的「反思协调员」。你的职责是：定期审视用户各关注主题的最新动态，发现值得提醒用户的事，整理成简报投递到信箱。

## 核心原则

1. 你不需要和用户对话——你的输出会被投递到信箱，用户稍后查看。不要在回复中写问候语。
2. 简报不是日报——只有真的有事时才推送。没事就安静跳过。
3. 每次最多推送 3 条发现。宁可少，不要水。

## 你能使用的工具

1. theme_analyst(theme_name, theme_description, theme_id, mode) — 分析指定主题。mode: cold_start / hot_run。返回该主题的发现列表。
2. scorer(findings) — 对所有发现打分排序，返回 TopN。
3. secretary(findings) — 将 TopN 发现转化为友好的人性化提醒文字，然后投递到信箱。**必须在 scorer 之后、mailbox 之前调用**。
4. mailbox.deliver(items) — 投递消息。items 每项 {title, content, type, source, entity_type, entity_id}。theme_finding 必须带 entity_type:"theme" + entity_id:主题ID。
（候选主题由系统自动发现，你不需要调用 theme_discoverer。）

## 你的工作流程

### 第一步：选择主题
查看活跃主题列表。全选所有活跃主题（除非某主题自上次分析后完全无新数据）。

### 第二步：调用 theme_analyst
对每个选中的主题，调用 theme_analyst。根据状态页决定 mode：
- 状态页为空（首次分析）→ mode="cold_start"
- 状态页有内容 → mode="hot_run"
如果返回了 skip_reason（无发现），记录但不影响后续。

### 第三步：收集发现并打分
如果所有主题均无发现 → 不做任何事，结束本轮。
如果至少有一条发现 → 为每条补全 finding_id 和 theme_name → 调用 scorer → scorer 返回 TopN（结构化 JSON）。

### 第四步：转化风格并投递
调用 secretary 将 scorer 返回的 TopN 转化成 messages，然后把每条 message 映射为 mailbox.deliver 的 item：{title: message.title, content: message.content, type: "theme_finding", source: 主题名}。
（候选主题由系统自动发现，你不需要调用 theme_discoverer。）

完成。`

// ReflectionOrchestrator — 反思循环编排器（独立于 HTTP server）
type ReflectionOrchestrator struct {
	flashClient   llm.LLMClient
	reflectionReg *tool.Registry
	traceRepo     *service.ReflectionTraceRepo
	themeStore    *ThemeStore
	mailboxRepo   *service.MailboxRepo
	msgRepo       *service.MessageRepo
	noteRepo      *service.NoteRepo
	memRepo       *service.UserMemoryRepo
	maxIterations int // 启动 Agent 最大轮次
}

func NewReflectionOrchestrator(
	flashClient llm.LLMClient,
	reflectionReg *tool.Registry,
	traceRepo *service.ReflectionTraceRepo,
	themeStore *ThemeStore,
	mailboxRepo *service.MailboxRepo,
	msgRepo *service.MessageRepo,
	noteRepo *service.NoteRepo,
	memRepo *service.UserMemoryRepo,
	maxIterations int,
) *ReflectionOrchestrator {
	return &ReflectionOrchestrator{
		flashClient:   flashClient,
		reflectionReg: reflectionReg,
		traceRepo:     traceRepo,
		themeStore:    themeStore,
		mailboxRepo:   mailboxRepo,
		msgRepo:       msgRepo,
		noteRepo:      noteRepo,
		memRepo:       memRepo,
		maxIterations: maxIterations,
	}
}

// RunOnce 执行一次完整的反思循环
func (o *ReflectionOrchestrator) RunOnce(ctx context.Context, userID, triggerType string) error {
	traceID := uuid.New().String()
	log.Printf("[reflection] trace=%s trigger=%s user=%s START", traceID[:8], triggerType, userID)

	// 创建 trace
	tr := &model.ReflectionTrace{
		ID:          traceID,
		UserID:      userID,
		TriggerType: triggerType,
		StartedAt:   time.Now(),
		Status:      "running",
	}
	if err := o.traceRepo.CreateTrace(ctx, tr); err != nil {
		log.Printf("[reflection] create trace error: %v", err)
	}

	// 查询活跃主题
	activeThemes, err := o.themeStore.ListActive(ctx, userID)
	if err != nil {
		log.Printf("[reflection] ListActive error: %v", err)
		endTraceSafe(o.traceRepo, ctx, traceID, "error", 0, 0, strPtr(fmt.Sprintf("ListActive: %v", err)))
		return fmt.Errorf("list themes: %w", err)
	}

	// 统计最近活动
	recentMsgCount, recentNoteCount, recentMemCount := o.countRecentActivity(ctx, userID)

	// 构建 tools map + trace ctx（主题分析和发现共用）
	activeTools := make(map[string]tool.Tool)
	for name, t := range o.reflectionReg.GetToolMap() {
		activeTools[name] = t
	}
	tracedCtx := WithTraceInfo(ctx, o.traceRepo, traceID)

	// 没有活跃主题时，仍然尝试发现候选主题
	if len(activeThemes) == 0 {
		log.Printf("[reflection] trace=%s no active themes, running discovery only", traceID[:8])
		delivered := o.runDiscoveryOnly(tracedCtx, traceID, userID, recentMsgCount, recentNoteCount, recentMemCount)
		status := "empty"
		if delivered > 0 {
			status = "completed"
		}
		endTraceSafe(o.traceRepo, ctx, traceID, status, 0, delivered, nil)
		return nil
	}

	// 构建输入消息
	var sb strings.Builder
	sb.WriteString("## 活跃主题\n")
	for _, t := range activeThemes {
		statePreview := ""
		if t.StateText != "" {
			statePreview = fmt.Sprintf("\n  状态页: %s", t.StateText)
		}
		sb.WriteString(fmt.Sprintf("- **%s** (id=%s): %s%s\n", t.Name, t.ID, t.Description, statePreview))
	}
	sb.WriteString(fmt.Sprintf("\n## 最近 7 天活动\n"))
	sb.WriteString(fmt.Sprintf("- 新增消息：%d 条\n", recentMsgCount))
	sb.WriteString(fmt.Sprintf("- 新增笔记：%d 条\n", recentNoteCount))
	sb.WriteString(fmt.Sprintf("- 新增记忆：%d 条\n", recentMemCount))
	sb.WriteString("\n请开始分析。先对每个主题调用 theme_analyst。\n")

	// 运行 ReAct 循环。ReactLoop 内部自动写入 LLM 调用和工具错误/skip 决策到 traceRepo
	loop := NewReactLoop(o.flashClient, activeTools, launchAgentPrompt, o.maxIterations, userID, "LaunchAgent", o.traceRepo, traceID)
	result, _ := loop.Run(tracedCtx, sb.String())

	totalFindings, deliveredCount := countFindings(result)

	// 汇总错误信息
	var traceErrMsg *string
	if result.Error != nil {
		s := result.Error.Error()
		traceErrMsg = &s
	}
	if result.TerminatedEarly {
		msg := "提前终止：达到最大迭代轮数，未完成完整分析"
		if traceErrMsg != nil {
			combined := *traceErrMsg + "; " + msg
			traceErrMsg = &combined
		} else {
			traceErrMsg = &msg
		}
	}

	traceStatus := "completed"
	if traceErrMsg != nil {
		traceStatus = "error"
	} else if totalFindings == 0 {
		traceStatus = "empty"
	}

	// 每次运行都跑主题发现
	discoveryDelivered := o.runDiscoveryOnly(tracedCtx, traceID, userID, recentMsgCount, recentNoteCount, recentMemCount)
	deliveredCount += discoveryDelivered

	endTraceSafe(o.traceRepo, ctx, traceID, traceStatus, totalFindings, deliveredCount, traceErrMsg)

	pt, ct := o.traceRepo.SumTokensByTrace(ctx, traceID)
	log.Printf("[reflection] trace=%s DONE status=%s findings=%d delivered=%d iterations=%d term_early=%v tokens=%.1fk(p)+%.1fk(c)",
		traceID[:8], traceStatus, totalFindings, deliveredCount, result.Iterations, result.TerminatedEarly,
		float64(pt)/1000, float64(ct)/1000)

	return nil
}

// countFindings 统计发现数和投递数
func countFindings(result *ReactLoopResult) (totalFindings, deliveredCount int) {
	for _, tcLog := range result.ToolCallsLog {
		if tcLog.Tool == "scorer" && tcLog.Result != nil && tcLog.Result.Success && tcLog.Result.Data != nil {
			var scorerOutput struct {
				Ranked []interface{} `json:"ranked"`
			}
			if json.Unmarshal(tcLog.Result.Data, &scorerOutput) == nil {
				totalFindings += len(scorerOutput.Ranked)
			}
		}
		if tcLog.Tool == "mailbox" && tcLog.Result != nil && tcLog.Result.Success {
			deliveredCount++
		}
	}
	return
}

// runDiscoveryOnly 仅运行主题发现，投递候选主题到信箱，返回投递数
func (o *ReflectionOrchestrator) runDiscoveryOnly(ctx context.Context, traceID, userID string, msgCount, noteCount, memCount int) int {
	ec := &tool.ExecuteContext{Ctx: ctx, UserID: userID}
	t, ok := o.reflectionReg.GetToolMap()["theme_discoverer"]
	if !ok {
		return 0
	}

	// 1. ThemeDiscoverer 发现候选
	args, _ := json.Marshal(map[string]interface{}{
		"recent_msg_count":  msgCount,
		"recent_note_count": noteCount,
		"recent_mem_count":  memCount,
	})
	res := t.Execute(ec, "discover", args)
	if !res.Success || res.Data == nil {
		log.Printf("[reflection] discovery failed: %v", res.Error)
		return 0
	}

	var output struct {
		Candidates []map[string]interface{} `json:"candidates"`
	}
	if json.Unmarshal(res.Data, &output) != nil || len(output.Candidates) == 0 {
		return 0
	}

	// 2. 逐个写入 theme_states + Secretary 改写 + mailbox 投递
	delivered := 0

	for _, c := range output.Candidates {
		if o.deliverCandidate(ctx, userID, c) {
			delivered++
		}
	}

	log.Printf("[reflection] discovery delivered %d/%d candidates", delivered, len(output.Candidates))
	return delivered
}

// deliverCandidate 处理单个候选主题：写 theme_states → Secretary → mailbox
func (o *ReflectionOrchestrator) deliverCandidate(ctx context.Context, userID string, c map[string]interface{}) bool {
	name, _ := c["name"].(string)
	desc, _ := c["description"].(string)
	if name == "" {
		return false
	}

	existing, _ := o.themeStore.GetByName(ctx, userID, name)
	if existing != nil && (existing.Status == "candidate" || existing.Status == "active") {
		log.Printf("[reflection] discovery skip duplicate: %s", name)
		return false
	}

	metaJSON, _ := json.Marshal(map[string]interface{}{"candidate_info": c})
	metaStr := string(metaJSON)
	themeID := uuid.New().String()
	theme := &model.ThemeState{
		ID:          themeID,
		UserID:      userID,
		Name:        name,
		Description: desc,
		Status:      "candidate",
		Metadata:    &metaStr,
	}
	if err := o.themeStore.Create(ctx, theme); err != nil {
		log.Printf("[reflection] discovery create candidate error: %v", err)
		return false
	}

	secT, _ := o.reflectionReg.GetToolMap()["secretary"]
	secTitle, secContent := o.rewriteCandidate(ctx, userID, c, secT)
	if secTitle == "" {
		secTitle = name
		secContent = desc
	}

	mailboxT, _ := o.reflectionReg.GetToolMap()["mailbox"]
	if mailboxT == nil {
		return false
	}
	itemArgs, _ := json.Marshal(map[string]interface{}{"items": []map[string]interface{}{{
		"title":       secTitle,
		"content":     secContent,
		"type":        "candidate_theme",
		"source":      "主题发现",
		"entity_type": "theme",
		"entity_id":   themeID,
	}}})
	res := mailboxT.Execute(&tool.ExecuteContext{Ctx: ctx, UserID: userID}, "deliver", itemArgs)
	return res.Success
}

// rewriteCandidate 调 Secretary 将候选主题人话化
func (o *ReflectionOrchestrator) rewriteCandidate(ctx context.Context, userID string, c map[string]interface{}, secT tool.Tool) (title, content string) {
	if secT == nil {
		return
	}
	// 映射为 Secretary 认识的 finding 格式
	finding := map[string]interface{}{
		"title":            c["name"],
		"content":          c["rationale"],
		"suggested_action": "",
		"theme_name":       c["name"],
	}
	cJSON, _ := json.Marshal(map[string]interface{}{"findings": []interface{}{finding}})
	secRes := secT.Execute(&tool.ExecuteContext{Ctx: ctx, UserID: userID}, "rewrite", cJSON)
	if !secRes.Success || secRes.Data == nil {
		return
	}
	var secOutput struct {
		Messages []struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(secRes.Data, &secOutput) != nil || len(secOutput.Messages) == 0 {
		return
	}
	return secOutput.Messages[0].Title, secOutput.Messages[0].Content
}

// countRecentActivity 统计最近 7 天用户活动数量
func (o *ReflectionOrchestrator) countRecentActivity(ctx context.Context, userID string) (msgCount, noteCount, memCount int) {
	since := time.Now().AddDate(0, 0, -7)
	if n, err := o.msgRepo.CountRecent(ctx, userID, since); err != nil {
		log.Printf("[reflection] CountRecent(msg) error: %v", err)
	} else {
		msgCount = int(n)
	}
	if n, err := o.noteRepo.CountRecent(ctx, userID, since); err != nil {
		log.Printf("[reflection] CountRecent(note) error: %v", err)
	} else {
		noteCount = int(n)
	}
	if n, err := o.memRepo.CountRecent(ctx, userID, since); err != nil {
		log.Printf("[reflection] CountRecent(mem) error: %v", err)
	} else {
		memCount = int(n)
	}
	return
}

func strPtr(s string) *string { return &s }

func logErr(format string, args ...interface{}) {
	log.Printf(format, args...)
}

func endTraceSafe(repo *service.ReflectionTraceRepo, ctx context.Context, traceID, status string, totalFindings, deliveredCount int, errMsg *string) {
	if err := repo.EndTrace(ctx, traceID, status, totalFindings, deliveredCount, errMsg); err != nil {
		log.Printf("[reflection] EndTrace error: %v", err)
	}
}

func createDecisionSafe(repo *service.ReflectionTraceRepo, ctx context.Context, decision *model.ReflectionDecision) {
	if err := repo.CreateDecision(ctx, decision); err != nil {
		log.Printf("[reflection] CreateDecision error: %v", err)
	}
}
