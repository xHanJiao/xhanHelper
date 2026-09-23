package agent

import (
	"context"
	"fmt"
	"log"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// SessionSnapshot — 一个已结束 session 的完整快照，所有 PostChatWorker 共享同一份。
// 消息含 user + assistant（不含 tool / internal），按时间升序。
// 不包含任何格式化产物（如 convText）——各 worker 按自己的格式需求自行构建。
type SessionSnapshot struct {
	ConvID string
	UserID string
	msgs   []model.Message // user + assistant（不含 tool / internal），时间升序
}

// GetUserMessages 返回快照中的 user 消息（画像 / 状态 worker 用——它们只看用户发言）。
func (s *SessionSnapshot) GetUserMessages() []model.Message {
	out := make([]model.Message, 0, len(s.msgs))
	for _, m := range s.msgs {
		if m.Role == "user" {
			out = append(out, m)
		}
	}
	return out
}

// GetAllMessages 返回快照中的全部消息（user + assistant，抽取段用）。
func (s *SessionSnapshot) GetAllMessages() []model.Message {
	return s.msgs
}

// PostChatWorker — 对话后处理子任务。
// 每个 worker 在构造时注入自己的依赖（LLM/Repo/提取器等），
// Process 只消费 SessionSnapshot，各自独立完成 LLM 调用与持久化。
// 单个 worker 失败不影响其他 worker 继续执行（不回滚）。
type PostChatWorker interface {
	Name() string
	Process(ctx context.Context, sess *SessionSnapshot) error
}

// EphemeralPurger — 无痕会话销毁能力。由 EphemeralDestroyer 实现，测试可注入 fake。
type EphemeralPurger interface {
	IsEphemeral(ctx context.Context, convID string) (bool, error)
	BeginDestroy(ctx context.Context, convID, userID string) error // Stage A + 入队 Stage B
	RunPurge(ctx context.Context, convID string) error             // Stage B（cron 调用）
}

// PostChatWorkerGroup — 对话后处理协调器。
// 取代原 post_chat（记忆+主题）与 summary_generation（摘要）两条独立定时任务路径：
// 统一读取 session 窗口内 user 消息，构建共享文本，然后分发给各 worker。
// 由 CronRunner 的 session_end 延迟任务驱动。
type PostChatWorkerGroup struct {
	workers []PostChatWorker
	msgRepo *service.MessageRepo
	purge   EphemeralPurger
}

// NewPostChatWorkerGroup 创建后处理组。workers 按注册顺序依次执行。
func NewPostChatWorkerGroup(workers []PostChatWorker, msgRepo *service.MessageRepo, purge EphemeralPurger) *PostChatWorkerGroup {
	return &PostChatWorkerGroup{
		workers: workers,
		msgRepo: msgRepo,
		purge:   purge,
	}
}

// Process 处理一个已结束的 session：
//  1. 读 lastMsg 的 SessionStartMsgID，无标记则跳过（非 session 边界）
//  2. 按时间窗口拉取 session 内所有 user 消息
//  3. 逐个分发 worker（独立失败不回滚，仅记录日志）
//
// 返回的 error 仅表示"消息拉取阶段"失败（应触发任务重试）；
// worker 执行失败不会作为 error 返回。
func (g *PostChatWorkerGroup) Process(ctx context.Context, convID, userID, lastMsgID string) error {
	if g.msgRepo == nil {
		panic("post_chat_group: msgRepo is nil (wiring bug)")
	}
	if g.purge == nil {
		panic("post_chat_group: purge is nil (wiring bug)")
	}

	// 无痕会话：窗口结束后不跑任何后处理（否则记忆/摘要/画像会留下痕迹），
	// 改为执行 Stage A（隐藏 + 入队 Stage B 物理销毁）。
	if ephemeral, err := g.purge.IsEphemeral(ctx, convID); err != nil {
		return fmt.Errorf("check ephemeral: %w", err)
	} else if ephemeral {
		log.Printf("[post_chat_group] ephemeral conv=%s → destroy (skip all workers)", convID[:8])
		return g.purge.BeginDestroy(ctx, convID, userID)
	}

	lastMsg, err := g.msgRepo.GetByID(ctx, lastMsgID)
	if err != nil {
		return err
	}
	if lastMsg == nil {
		log.Printf("[post_chat_group] lastMsg %s not found", lastMsgID)
		return nil
	}

	sessionStart := GetSessionStart(lastMsg)
	if sessionStart == "" {
		// 没有 session 起始标记，说明不是新 session 的边界消息，跳过
		return nil
	}

	startMsg, err := g.msgRepo.GetByID(ctx, sessionStart)
	if err != nil {
		return err
	}
	if startMsg == nil {
		log.Printf("[post_chat_group] sessionStart %s not found", sessionStart)
		return nil
	}

	endMsg, err := g.msgRepo.GetByID(ctx, lastMsgID)
	if err != nil {
		return err
	}
	if endMsg == nil {
		log.Printf("[post_chat_group] endMsg %s not found", lastMsgID)
		return nil
	}

	msgs, err := g.msgRepo.ListChatMessagesBetween(ctx, convID, startMsg.CreatedAt, endMsg.CreatedAt, 0)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil
	}

	log.Printf("[post_chat_group] session_end: conv=%s loading %d chat msgs", convID[:8], len(msgs))

	sess := &SessionSnapshot{
		ConvID: convID,
		UserID: userID,
		msgs:   msgs,
	}

	for _, w := range g.workers {
		if err := w.Process(ctx, sess); err != nil {
			log.Printf("[post_chat_group] worker %s error: conv=%s err=%v", w.Name(), convID[:8], err)
		}
	}
	return nil
}
