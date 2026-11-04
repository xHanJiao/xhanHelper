package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// 本文件实现「文档批注 AI 回复」的按钮/追问路径（AR-4）：
// document-scoped MiniReAct 跑一次 run，产出并落库一条 assistant 回复。
// 指令（批量）路径不走这里，而是由主聊天里 LLM 显式调用 document.reply_annotation 工具。
//
// 设计见 docs/design/document-annotation-reply/design.md §4。

const (
	// annotationReplyMaxContentLen 单条回复 / 追问上限（字）
	annotationReplyMaxContentLen = 4000
	// annotationReplyMaxReplies 单条批注回复条数上限
	annotationReplyMaxReplies = 100
	// annotationReplyTimeout 单次生成超时
	annotationReplyTimeout = 120 * time.Second
	// annotationReplyMaxIterations 生成 run 的最大轮数（含强制收敛轮）
	annotationReplyMaxIterations = 10
	// annotationReplyOriginRounds 原点会话近期轮数
	annotationReplyOriginRounds = 10
	// annotationReplyThreadInPrompt 注入 prompt 的线程最近条数
	annotationReplyThreadInPrompt = 20
)

// 生成失败/冲突类错误（handler 据此映射 HTTP 状态码）。
var (
	ErrAnnotationReplyBusy     = errors.New("annotation reply: annotation busy")
	ErrAnnotationReplyLimit    = errors.New("annotation reply: reply limit reached")
	ErrAnnotationReplyNotFound = errors.New("annotation reply: annotation not found")
	ErrAnnotationReplySave     = errors.New("annotation reply: save failed")
)

// AnnotationReplierConfig — AnnotationReplier 依赖。
type AnnotationReplierConfig struct {
	AnnoRepo     *service.DocumentAnnotationRepo
	DocRepo      *service.DocumentRepo
	ReplyRepo    *service.DocumentAnnotationReplyRepo
	MsgRepo      *service.MessageRepo
	SummaryRepo  *service.ConversationSummaryRepo
	Client       llm.LLMClient
	Registry     *tool.Registry
	MaxReplies   int      // 默认 annotationReplyMaxReplies
	ToolExcludes []string // 隐藏工具名，与主循环/plan 模式同源（chat.tool_excludes）
}

// AnnotationReplier — 同步生成一条批注回复（button / followup 路径）。
type AnnotationReplier struct {
	annoRepo     *service.DocumentAnnotationRepo
	docRepo      *service.DocumentRepo
	replyRepo    *service.DocumentAnnotationReplyRepo
	msgRepo      *service.MessageRepo
	summaryRepo  *service.ConversationSummaryRepo
	client       llm.LLMClient
	registry     *tool.Registry
	maxReplies   int
	toolExcludes []string
	mu           tryKeyedMutex // 按 annotation_id 串行化，防双击 / 双端同时点
}

// NewAnnotationReplier 创建生成器。必需依赖为 nil 时 panic（接线 bug 暴露在明处）。
func NewAnnotationReplier(cfg *AnnotationReplierConfig) *AnnotationReplier {
	if cfg.AnnoRepo == nil || cfg.DocRepo == nil || cfg.ReplyRepo == nil || cfg.Client == nil || cfg.Registry == nil {
		panic("annotation_reply: required dependency nil (wiring bug)")
	}
	maxReplies := cfg.MaxReplies
	if maxReplies <= 0 {
		maxReplies = annotationReplyMaxReplies
	}
	return &AnnotationReplier{
		annoRepo:     cfg.AnnoRepo,
		docRepo:      cfg.DocRepo,
		replyRepo:    cfg.ReplyRepo,
		msgRepo:      cfg.MsgRepo,
		summaryRepo:  cfg.SummaryRepo,
		client:       cfg.Client,
		registry:     cfg.Registry,
		maxReplies:   maxReplies,
		toolExcludes: cfg.ToolExcludes,
	}
}

// AnnotationReplyOutcome — 本次生成新增的回复行 + 该批注回复总数。
type AnnotationReplyOutcome struct {
	Added      []model.DocumentAnnotationReply
	ReplyCount int64
}

// Generate 跑一次 run 并落库回复。
// userContent 非空 = 先作为一条 role=user 追问（Source=followup）；空 = 纯按钮生成（Source=button）。
// 原子性：只有生成成功才把「追问 + 回复」一起落库；生成失败什么都不落（可安全重试）。
func (r *AnnotationReplier) Generate(ctx context.Context, userID, annotationID, userContent string) (*AnnotationReplyOutcome, error) {
	unlock, ok := r.mu.tryLock(annotationID)
	if !ok {
		return nil, ErrAnnotationReplyBusy
	}
	defer unlock()

	anno, err := r.annoRepo.GetByID(ctx, annotationID, userID)
	if err != nil {
		return nil, fmt.Errorf("%w: annotation=%s: %v", ErrAnnotationReplyNotFound, annotationID, err)
	}
	doc, err := r.docRepo.GetByID(ctx, anno.DocumentID, userID)
	if err != nil {
		return nil, fmt.Errorf("%w: document=%s: %v", ErrAnnotationReplyNotFound, anno.DocumentID, err)
	}

	count, err := r.replyRepo.CountByAnnotation(ctx, annotationID, userID)
	if err != nil {
		return nil, fmt.Errorf("annotation reply: count: %w", err)
	}
	if count >= int64(r.maxReplies) {
		return nil, ErrAnnotationReplyLimit
	}

	thread, _, err := r.replyRepo.ListByAnnotationID(ctx, annotationID, userID, 200, 0)
	if err != nil {
		return nil, fmt.Errorf("annotation reply: load thread: %w", err)
	}

	prompt := r.buildUserMessage(ctx, userID, doc, anno, thread, userContent)

	// 只读工具（AR-10）：取「全部可见工具的只读函数」，与 plan 模式同一套（同套 tool_excludes），写函数不下发。
	// ponytail: 只读全量会让每轮请求体的 tools 变大（工具越多越大）；实测伤性能再给 MiniReAct 加渐进暴露。
	tools := filterToolDefs(r.registry.ReadOnlyToolDefinitions(), r.toolExcludes)
	// 按过滤结果派生 AllowedTools 跑期 guard：即便模型幻觉出写函数也被拦（防御性，AR-10）。
	allowed := make(map[string][]string, len(tools))
	for _, d := range tools {
		names := make([]string, 0, len(d.Functions))
		for _, f := range d.Functions {
			names = append(names, f.Name)
		}
		allowed[d.Name] = names
	}
	runCtx, cancel := context.WithTimeout(ctx, annotationReplyTimeout)
	defer cancel()
	react := NewMiniReAct(&MiniReActConfig{
		Client:        r.client,
		Tools:         tools,
		ToolRegistry:  r.registry,
		SystemPrompt:  annotationReplySystemPrompt,
		UserID:        userID,
		MaxIterations: annotationReplyMaxIterations,
		MaxToolResult: 3000,
		Caller:        "annotation_reply",
		SubAgent:      SubAgentExt{AllowedTools: allowed},
	})
	result, err := react.Run(runCtx, prompt)
	if err != nil {
		return nil, fmt.Errorf("annotation reply: generate: %w", err)
	}
	content := strings.TrimSpace(result.FinalContent)
	if content == "" {
		return nil, fmt.Errorf("annotation reply: generate empty content (iterations=%d)", result.Iterations)
	}
	// 成本/轮数观测：只读工具全量 + 轮数上限下，iterations 触顶（=用掉强制收敛轮）即「工具太多/轮数不够」的信号。
	log.Printf("annotation_reply: run done annotation=%s iterations=%d/%d tools=%d", annotationID, result.Iterations, annotationReplyMaxIterations, len(tools))
	content = truncateRunes(content, annotationReplyMaxContentLen)

	// ID 用雪花（时间有序）：追问是 [user, assistant] 同批落库，created_at 被 GORM 取同一个 now
	// 而相同，排序只能靠 id 破平；uuid 随机会让 user/assistant 顺序随机反转。雪花按调用序递增，
	// 保证 id ASC = 插入序（repo 的 ListByAnnotationID / LatestByAnnotationIDs / 窗口函数都依赖它）。
	source := "button"
	added := make([]model.DocumentAnnotationReply, 0, 2)
	if userContent != "" {
		source = "followup"
		added = append(added, model.DocumentAnnotationReply{
			ID:           idgen.Next(),
			AnnotationID: annotationID,
			DocumentID:   anno.DocumentID,
			UserID:       userID,
			Role:         "user",
			Content:      userContent,
			Source:       source,
		})
	}
	added = append(added, model.DocumentAnnotationReply{
		ID:           idgen.Next(),
		AnnotationID: annotationID,
		DocumentID:   anno.DocumentID,
		UserID:       userID,
		Role:         "assistant",
		Content:      content,
		Source:       source,
	})

	if err := r.replyRepo.CreateBatch(ctx, added); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAnnotationReplySave, err)
	}
	return &AnnotationReplyOutcome{Added: added, ReplyCount: count + int64(len(added))}, nil
}

// buildUserMessage 组装本次 run 的 user 消息（动态上下文一律进 user 消息，不进 system prompt）。
func (r *AnnotationReplier) buildUserMessage(ctx context.Context, userID string, doc *model.Document, anno *model.DocumentAnnotation, thread []model.DocumentAnnotationReply, userContent string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "【文档】《%s》\n", doc.Title)
	fmt.Fprintf(&b, "【划线原文】%s\n", anno.Quote)
	if strings.TrimSpace(anno.Note) != "" {
		fmt.Fprintf(&b, "【用户批注】%s\n", anno.Note)
	} else {
		b.WriteString("【用户批注】（无，用户只划了线）\n")
	}
	if anno.Orphaned {
		b.WriteString("【注意】这条划线已失活（原文被改写），可能对不上当前正文。\n")
	}

	if len(thread) > 0 {
		b.WriteString("\n【已有讨论】\n")
		// 只带最近若干条、每条截断，避免历史线程撑爆上下文/成本（thread 按时间正序）
		if start := len(thread) - annotationReplyThreadInPrompt; start > 0 {
			thread = thread[start:]
		}
		for _, rep := range thread {
			who := "用户"
			if rep.Role == "assistant" {
				who = "你"
			}
			fmt.Fprintf(&b, "%s：%s\n", who, truncateRunes(strings.TrimSpace(rep.Content), 800))
		}
	}

	if origin := r.loadOriginContext(ctx, doc); origin != "" {
		b.WriteString("\n")
		b.WriteString(origin)
	}

	b.WriteString("\n")
	if strings.TrimSpace(userContent) != "" {
		fmt.Fprintf(&b, "【本次追问】%s\n请结合上文作答。", userContent)
	} else {
		b.WriteString("请针对这条批注给出你的回复。")
	}
	return b.String()
}

// loadOriginContext 读取「产出该文档的会话」的摘要 + 近期轮（AR-6 降级链第一级）。
// 归档也不拦（is_deleted 不影响按 id 读）。读不到就跳过，由文档正文 + 工具兜底。
func (r *AnnotationReplier) loadOriginContext(ctx context.Context, doc *model.Document) string {
	if doc.ConversationID == nil || *doc.ConversationID == "" || r.summaryRepo == nil || r.msgRepo == nil {
		return ""
	}
	convID := *doc.ConversationID

	var body strings.Builder

	// 摘要 / 近期轮均为可选增强：读失败按降级处理（打日志后继续），不影响主流程。
	if summary, err := r.summaryRepo.GetLatest(ctx, convID); err != nil {
		log.Printf("annotation_reply: load origin summary conv=%s: %v", convID, err)
	} else if summary != nil && strings.TrimSpace(summary.SummaryText) != "" {
		fmt.Fprintf(&body, "摘要：%s\n", summary.SummaryText)
	}

	if rounds, err := r.msgRepo.ListRecentRounds(ctx, convID, annotationReplyOriginRounds); err != nil {
		log.Printf("annotation_reply: load origin rounds conv=%s: %v", convID, err)
	} else {
		for _, round := range rounds {
			for _, m := range round {
				if m.Role != "user" && m.Role != "assistant" {
					continue
				}
				content := strings.TrimSpace(m.Content)
				if content == "" {
					continue
				}
				who := "用户"
				if m.Role == "assistant" {
					who = "AI"
				}
				fmt.Fprintf(&body, "%s：%s\n", who, truncateRunes(content, 800))
			}
		}
	}

	if body.Len() == 0 {
		return ""
	}
	return "【相关背景 · 来自产出该文档的会话】\n" + body.String()
}

// annotationReplySystemPrompt — 本 run 的静态 system prompt（进请求体的动态内容一律放 user 消息）。
const annotationReplySystemPrompt = `你是 xhanHelper 的文档批注助手。用户在文档里划了一段文字（可能还写了一句批注），需要你针对这条批注作答。

规则：
- 直接回应批注本身，简洁、具体；不要复述原文，也不要客套。
- 需要更多上下文时可调用工具（均为只读）：文档、检索（记忆/笔记/摘要/事件/历史消息）、账单/笔记/提醒、日期时间与计算等，按需调用；简单问题直接作答。
- 划线文本是网页里的纯文本（不含 Markdown 标记，空白已折叠），不要在文档里逐字搜索它。
- 用中文、Markdown 输出，通常 1~3 段；除非用户追问要求展开。
- 不要提及内部工具调用过程。`

// tryKeyedMutex 按键非阻塞加锁（拿不到 = 已有同名请求在跑）。
// ponytail: 单进程有效，键不回收（批注数有界）；多实例部署需换 DB 行锁。
type tryKeyedMutex struct {
	mu sync.Map // key -> *sync.Mutex
}

func (k *tryKeyedMutex) tryLock(key string) (func(), bool) {
	v, _ := k.mu.LoadOrStore(key, &sync.Mutex{})
	m := v.(*sync.Mutex)
	if !m.TryLock() {
		return nil, false
	}
	return m.Unlock, true
}
