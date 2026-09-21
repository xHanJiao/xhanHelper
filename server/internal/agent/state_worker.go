package agent

import (
	"context"
)

// StateWorker — 主题状态更新子任务（PostChatWorker）。
// 包装 StateUpdater，基于 session 快照的 user 消息匹配活跃主题并增量更新状态页。
type StateWorker struct {
	updater *StateUpdater
}

// StateWorkerConfig — StateWorker 依赖配置
type StateWorkerConfig struct {
	Updater *StateUpdater
}

// NewStateWorker 创建主题更新 worker。
func NewStateWorker(cfg *StateWorkerConfig) *StateWorker {
	return &StateWorker{
		updater: cfg.Updater,
	}
}

// Name 实现 PostChatWorker。
func (w *StateWorker) Name() string { return "state" }

// Process 匹配活跃主题并逐个更新状态页。StateUpdater 内部处理 trace 与失败降级。
func (w *StateWorker) Process(ctx context.Context, sess *SessionSnapshot) error {
	if w.updater == nil {
		panic("state_worker: updater is nil (wiring bug)")
	}
	userMsgs := sess.GetUserMessages()
	w.updater.UpdateThemesFromConversation(ctx, sess.UserID, userMsgs, buildMsgBlock(userMsgs))
	return nil
}
