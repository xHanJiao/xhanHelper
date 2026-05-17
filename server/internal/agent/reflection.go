package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// ReflectionOrchestrator — 反思循环编排器（独立于 HTTP server）
type ReflectionOrchestrator struct {
	flashClient        llm.LLMClient
	reflectionReg      *tool.Registry
	traceRepo          *service.ReflectionTraceRepo
	themeStore         *ThemeStore
	mailboxRepo        *service.MailboxRepo
	msgRepo            *service.MessageRepo
	noteRepo           *service.NoteRepo
	memRepo            *service.UserMemoryRepo
	pendingFindingRepo *service.PendingFindingRepo
	maxIterations      int
	maxDeliveryRetries int
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
	pendingFindingRepo *service.PendingFindingRepo,
	maxIterations int,
) *ReflectionOrchestrator {
	return &ReflectionOrchestrator{
		flashClient:        flashClient,
		reflectionReg:      reflectionReg,
		traceRepo:          traceRepo,
		themeStore:         themeStore,
		mailboxRepo:        mailboxRepo,
		msgRepo:            msgRepo,
		noteRepo:           noteRepo,
		memRepo:            memRepo,
		pendingFindingRepo: pendingFindingRepo,
		maxIterations:      maxIterations,
		maxDeliveryRetries: 3,
	}
}

// SetMaxDeliveryRetries 设置投递重试上限
func (o *ReflectionOrchestrator) SetMaxDeliveryRetries(n int) { o.maxDeliveryRetries = n }

var runOnceExpireDays = 7

// DiscoverDraftFromConversation 从单个会话的消息中发现草稿主题
func (o *ReflectionOrchestrator) DiscoverDraftFromConversation(ctx context.Context, userID, convID string) error {
	msgs, err := o.msgRepo.ListByConversation(ctx, convID, 50)
	if err != nil || len(msgs) == 0 {
		return err
	}
	var sb string
	for _, m := range msgs {
		if m.Role == "tool" {
			continue
		}
		sb += fmt.Sprintf("[%s]: %s\n", m.Role, m.Content)
	}
	if sb == "" {
		return nil
	}

	// 复用 ThemeDiscoverer 的 conversation 模式
	t, ok := o.reflectionReg.GetToolMap()["theme_discoverer"]
	if !ok {
		return nil
	}
	args, _ := json.Marshal(map[string]interface{}{
		"mode":     "conversation",
		"messages": sb,
	})
	res := t.Execute(&tool.ExecuteContext{Ctx: ctx, UserID: userID}, "discover", args)
	if !res.Success || res.Data == nil {
		return nil
	}

	var output struct {
		Candidates []map[string]interface{} `json:"candidates"`
	}
	if json.Unmarshal(res.Data, &output) != nil || len(output.Candidates) == 0 {
		return nil
	}

	// 创建草稿（不投递信箱）
	for _, c := range output.Candidates {
		name, _ := c["name"].(string)
		desc, _ := c["description"].(string)
		if name == "" {
			continue
		}
		existing, _ := o.themeStore.GetByName(ctx, userID, name)
		if existing != nil && (existing.Status == "candidate" || existing.Status == "active" || existing.Status == "draft") {
			continue
		}
		metaJSON, _ := json.Marshal(map[string]interface{}{"candidate_info": c})
		metaStr := string(metaJSON)
		theme := &model.ThemeState{
			ID:          uuid.New().String(),
			UserID:      userID,
			Name:        name,
			Description: desc,
			Status:      "draft",
			Metadata:    &metaStr,
		}
		if err := o.themeStore.Create(ctx, theme); err != nil {
			log.Printf("[reflection] create draft error: %v", err)
		} else {
			log.Printf("[reflection] created draft: %s", name)
		}
	}
	return nil
}

// RunSilent 静默模式：只发现草稿主题，不投递信箱
func (o *ReflectionOrchestrator) RunSilent(ctx context.Context, userID, triggerType string) error {
	return o.runOnce(ctx, userID, triggerType, true)
}

// RunOnce 执行一次完整的反思循环
func (o *ReflectionOrchestrator) RunOnce(ctx context.Context, userID, triggerType string) error {
	return o.runOnce(ctx, userID, triggerType, false)
}

// DeliverPendingFindings 扫暂存发现，经 Scorer → Secretary → 投递信箱
func (o *ReflectionOrchestrator) DeliverPendingFindings(ctx context.Context, userID string) (int, error) {
	traceID := uuid.New().String()
	log.Printf("[reflection] trace=%s trigger=deliver_window user=%s START", traceID[:8], userID)

	tr := &model.ReflectionTrace{
		ID: traceID, UserID: userID, TriggerType: "deliver_window",
		StartedAt: time.Now(), Status: "running",
	}
	o.traceRepo.CreateTrace(ctx, tr)

	// 从表拉 pending findings
	rawFindings, err := o.pendingFindingRepo.ListPending(ctx, userID)
	if err != nil {
		endTraceSafe(o.traceRepo, ctx, traceID, "error", 0, 0, strPtr(err.Error()))
		return 0, err
	}

	if len(rawFindings) == 0 {
		endTraceSafe(o.traceRepo, ctx, traceID, "empty", 0, 0, nil)
		log.Printf("[reflection] trace=%s DONE: no pending findings", traceID[:8])
		return 0, nil
	}

	// 转换格式 + 收集 IDs
	ids := make([]string, 0, len(rawFindings))
	allFindings := make([]map[string]interface{}, 0, len(rawFindings))
	themeSet := make(map[string]string)
	for _, f := range rawFindings {
		ids = append(ids, f.ID)
		m := map[string]interface{}{
			"finding_id": f.ID,
			"theme_id":   f.ThemeID,
			"theme_name": f.ThemeName,
			"title":      f.Title,
			"content":    f.Content,
			"urgency":    f.Urgency,
		}
		if f.SourceRefs != "" {
			var refs []string
			json.Unmarshal([]byte(f.SourceRefs), &refs)
			m["source_refs"] = refs
		}
		allFindings = append(allFindings, m)
		themeSet[f.ThemeID] = f.ThemeName
	}

	// Scorer → Secretary → Mailbox 管道
	scorerT, _ := o.reflectionReg.GetToolMap()["scorer"]
	secT, _ := o.reflectionReg.GetToolMap()["secretary"]
	mailT, _ := o.reflectionReg.GetToolMap()["mailbox"]
	ec := &tool.ExecuteContext{Ctx: ctx, UserID: userID}

	scorerPayload, _ := json.Marshal(map[string]interface{}{"findings": allFindings})
	scorerRes := scorerT.Execute(ec, "score", scorerPayload)
	if !scorerRes.Success || scorerRes.Data == nil {
		o.incrementRetries(ctx, ids)
		endTraceSafe(o.traceRepo, ctx, traceID, "error", len(allFindings), 0, strPtr("scorer failed"))
		return 0, fmt.Errorf("scorer failed")
	}

	var scorerOutput struct {
		Ranked []json.RawMessage `json:"ranked"`
	}
	json.Unmarshal(scorerRes.Data, &scorerOutput)
	secPayload, _ := json.Marshal(map[string]interface{}{"findings": scorerOutput.Ranked})
	secRes := secT.Execute(ec, "humanize", secPayload)
	if !secRes.Success || secRes.Data == nil {
		o.incrementRetries(ctx, ids)
		endTraceSafe(o.traceRepo, ctx, traceID, "error", len(allFindings), 0, strPtr("secretary failed"))
		return 0, fmt.Errorf("secretary failed")
	}

	var secOutput struct {
		Messages []struct {
			Title        string `json:"title"`
			Notification string `json:"notification"`
		} `json:"messages"`
	}
	json.Unmarshal(secRes.Data, &secOutput)
	mailItems := make([]map[string]interface{}, 0, len(secOutput.Messages))
	for _, msg := range secOutput.Messages {
		item := map[string]interface{}{
			"title":   msg.Title,
			"content": msg.Notification,
			"type":    "theme_finding",
			"source":  "主题简报",
		}
		if len(themeSet) == 1 {
			for tid, tname := range themeSet {
				item["entity_type"] = "theme"
				item["entity_id"] = tid
				item["source"] = tname
			}
		}
		mailItems = append(mailItems, item)
	}
	mailPayload, _ := json.Marshal(map[string]interface{}{"items": mailItems})
	mailRes := mailT.Execute(ec, "deliver", mailPayload)

	delivered := 0
	if mailRes.Success {
		delivered = len(mailItems)
		o.pendingFindingRepo.MarkDelivered(ctx, ids)
	} else {
		o.incrementRetries(ctx, ids)
	}

	endTraceSafe(o.traceRepo, ctx, traceID, "completed", len(allFindings), delivered, nil)
	log.Printf("[reflection] trace=%s DONE findings=%d delivered=%d", traceID[:8], len(allFindings), delivered)
	return delivered, nil
}

func (o *ReflectionOrchestrator) incrementRetries(ctx context.Context, ids []string) {
	log.Printf("[reflection] delivery failed, incrementing retries for %d findings", len(ids))
	// Increment retry count for all and mark those exceeding limit as failed
	for _, id := range ids {
		o.pendingFindingRepo.IncrementRetry(ctx, id)
	}
	if o.maxDeliveryRetries > 0 {
		// TODO: query retry_count > max and mark failed — skip for now, simple is ok
	}
}

// DiscoverAndDeliver 发现候选主题并投递（cron 低频调用）
func (o *ReflectionOrchestrator) DiscoverAndDeliver(ctx context.Context, userID string) (int, error) {
	traceID := uuid.New().String()
	log.Printf("[reflection] trace=%s trigger=discover user=%s START", traceID[:8], userID)

	tr := &model.ReflectionTrace{
		ID: traceID, UserID: userID, TriggerType: "discover",
		StartedAt: time.Now(), Status: "running",
	}
	o.traceRepo.CreateTrace(ctx, tr)

	o.themeStore.Expire(ctx, userID, runOnceExpireDays)

	msgCount, noteCount, memCount := o.countRecentActivity(ctx, userID)
	tracedCtx := WithTraceInfo(ctx, o.traceRepo, traceID)
	delivered := o.runDiscoveryAndDrafts(tracedCtx, traceID, userID, msgCount, noteCount, memCount, true)

	status := "completed"
	if delivered == 0 {
		status = "empty"
	}
	endTraceSafe(o.traceRepo, ctx, traceID, status, 0, delivered, nil)
	log.Printf("[reflection] trace=%s DONE delivered=%d", traceID[:8], delivered)
	return delivered, nil
}

// runOnce 保留兼容：deliver + discover（供 cron 模式使用）
func (o *ReflectionOrchestrator) runOnce(ctx context.Context, userID, triggerType string, silent bool) error {
	if silent {
		return nil // 静默模式已废弃
	}
	delivered, _ := o.DeliverPendingFindings(ctx, userID)
	disc, _ := o.DiscoverAndDeliver(ctx, userID)
	log.Printf("[reflection] runOnce combined: deliverables=%d discover=%d", delivered, disc)
	return nil
}

// runDiscoveryOnly 运行主题发现 + 投递草稿，返回投递数
func (o *ReflectionOrchestrator) runDiscoveryOnly(ctx context.Context, traceID, userID string, msgCount, noteCount, memCount int) int {
	return o.runDiscoveryAndDrafts(ctx, traceID, userID, msgCount, noteCount, memCount, false)
}

// runDiscoveryAndDrafts 运行发现 + 可选加载草稿一起投递
func (o *ReflectionOrchestrator) runDiscoveryAndDrafts(ctx context.Context, traceID, userID string, msgCount, noteCount, memCount int, includeDrafts bool) int {
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
	if err := json.Unmarshal(res.Data, &output); err != nil {
		log.Printf("[reflection] unmarshal discovery output error: %v", err)
	}

	// 1.5. 加载草稿主题（cron 全链路模式）
	if includeDrafts {
		drafts, err := o.themeStore.ListByStatus(ctx, userID, "draft")
		if err != nil {
			log.Printf("[reflection] list drafts error: %v", err)
		}
		for _, d := range drafts {
			if d.Metadata != nil {
				var meta map[string]interface{}
				if json.Unmarshal([]byte(*d.Metadata), &meta) == nil {
					if ci, ok := meta["candidate_info"].(map[string]interface{}); ok {
						ci["name"] = d.Name
						ci["description"] = d.Description
						output.Candidates = append(output.Candidates, ci)
						defer func(id string) {
							o.themeStore.UpdateStatus(ctx, id, userID, "candidate")
						}(d.ID)
					}
				}
			}
		}
		log.Printf("[reflection] loaded %d drafts into candidate list", len(drafts))
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
	if existing != nil && existing.Status == "active" {
		log.Printf("[reflection] discovery skip active: %s", name)
		return false
	}
	if existing != nil && existing.Status == "candidate" {
		log.Printf("[reflection] discovery skip duplicate candidate: %s", name)
		return false
	}

	var themeID string
	metaJSON, _ := json.Marshal(map[string]interface{}{"candidate_info": c})
	metaStr := string(metaJSON)

	if existing != nil && existing.Status == "draft" {
		// 升级 draft 为 candidate，复用已有 ID
		themeID = existing.ID
		if err := o.themeStore.UpdateStatus(ctx, themeID, userID, "candidate"); err != nil {
			log.Printf("[reflection] promote draft to candidate error: %v", err)
			return false
		}
		log.Printf("[reflection] promoted draft %s → candidate", name)
	} else {
		themeID = uuid.New().String()
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
	}

	secT, _ := o.reflectionReg.GetToolMap()["secretary"]
	secTitle, notification, greeting := o.rewriteCandidate(ctx, userID, c, secT)
	if secTitle == "" {
		secTitle = name
		notification = desc
	}

	mailboxT, _ := o.reflectionReg.GetToolMap()["mailbox"]
	if mailboxT == nil {
		return false
	}
	itemArgs, _ := json.Marshal(map[string]interface{}{"items": []map[string]interface{}{{
		"title":       secTitle,
		"content":     notification,
		"greeting":    greeting,
		"type":        "candidate_theme",
		"source":      "主题发现",
		"entity_type": "theme",
		"entity_id":   themeID,
	}}})
	res := mailboxT.Execute(&tool.ExecuteContext{Ctx: ctx, UserID: userID}, "deliver", itemArgs)
	return res.Success
}

// rewriteCandidate 调 Secretary 将候选主题人话化，返回 (title, notification, greeting)
func (o *ReflectionOrchestrator) rewriteCandidate(ctx context.Context, userID string, c map[string]interface{}, secT tool.Tool) (title, notification, greeting string) {
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
			Title        string `json:"title"`
			Notification string `json:"notification"`
			Greeting     string `json:"greeting"`
		} `json:"messages"`
	}
	if json.Unmarshal(secRes.Data, &secOutput) != nil || len(secOutput.Messages) == 0 {
		return
	}
	return secOutput.Messages[0].Title, secOutput.Messages[0].Notification, secOutput.Messages[0].Greeting
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
