package controller

import (
	"log"
	"sync"
)

// ChatStopRegistry 管理运行中的 chat 的终止信号。
// 每个 chat handler 开始时注册一个 stop channel，结束时回收；
// 停止接口通过关闭 channel 传播信号（幂等）。
type ChatStopRegistry struct {
	mu    sync.Mutex
	stops map[string]chan struct{}
}

var chatStops = &ChatStopRegistry{stops: map[string]chan struct{}{}}

// Register 注册一个运行中的 chat，返回其终止信号 channel。
// 若该会话已有运行中的 chat，先关闭旧 channel（取消旧 ReAct 循环）再注册新的，
// 防止同一会话并发执行两个 ReAct 循环交叉写库（历史教训：旧循环工具结果晚到、
// 新请求的 user 消息已经写入，产生 assistant(tool_calls)→user→tool 错位结构，
// 后续所有请求因 role=tool 找不到前置 tool_calls 而被 DeepSeek 400 拒绝，会话报废）。
func (r *ChatStopRegistry) Register(convID string) chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.stops[convID]; ok {
		select {
		case <-old:
			// 已关闭，幂等
		default:
			close(old)
			log.Printf("[chat_stop] replacing running chat for conv %s (old loop cancelled)", convID)
		}
	}
	stopCh := make(chan struct{})
	r.stops[convID] = stopCh
	return stopCh
}

// Unregister 回收指定 chat 的注册（chat handler 结束或取消时调用）。
// 仅当 map 中保存的仍是本次注册的 channel 时才删除：旧 handler 的 defer
// 可能在晚到的新循环已注册后执行，直接按 convID 删除会误删新循环的注册。
func (r *ChatStopRegistry) Unregister(convID string, stopCh chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.stops[convID]; ok && cur == stopCh {
		delete(r.stops, convID)
	}
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
