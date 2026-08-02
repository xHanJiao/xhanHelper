package controller

import "sync"

// ChatStopRegistry 管理运行中的 chat 的终止信号。
// 每个 chat handler 开始时注册一个 stop channel，结束时回收；
// 停止接口通过关闭 channel 传播信号（幂等）。
type ChatStopRegistry struct {
	mu    sync.Mutex
	stops map[string]chan struct{}
}

var chatStops = &ChatStopRegistry{stops: map[string]chan struct{}{}}

// Register 注册一个运行中的 chat，返回其终止信号 channel
func (r *ChatStopRegistry) Register(convID string) chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	stopCh := make(chan struct{})
	r.stops[convID] = stopCh
	return stopCh
}

// Unregister 回收指定 chat 的注册（chat handler 结束或取消时调用）
func (r *ChatStopRegistry) Unregister(convID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.stops, convID)
}

// Stop 向指定 chat 发送终止信号（幂等关闭 channel）。
// 返回 false 表示该会话当前没有运行中的 chat。
func (r *ChatStopRegistry) Stop(convID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.stops[convID]
	if !ok {
		return false
	}
	select {
	case <-ch:
		// 已关闭，幂等
	default:
		close(ch)
	}
	return true
}
