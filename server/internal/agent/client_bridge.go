package agent

import (
	"encoding/json"
	"fmt"
	"sync"
)

// ClientToolResult — 客户端工具执行结果（由 POST /api/chat/tool-result 回传）
type ClientToolResult struct {
	Result json.RawMessage
	Error  string
}

// pendingCall — 一次进行中的客户端工具调用
type pendingCall struct {
	userID string // 发起调用的用户，回传时校验
	ch     chan ClientToolResult
}

// ClientToolBridge — 客户端工具调用的 pending 路由。
// orchestrator 通过 Register 登记 call_id 并阻塞等待 channel；
// HTTP 回传 handler 通过 OnResult 按 call_id 唤醒等待方。
// 线程安全模型与 mcp/client.go 的 pending map + channel 一致。
type ClientToolBridge struct {
	mu      sync.Mutex
	pending map[string]*pendingCall
}

func NewClientToolBridge() *ClientToolBridge {
	return &ClientToolBridge{
		pending: make(map[string]*pendingCall),
	}
}

// Register 登记一次调用，返回接收结果的 channel。
// 必须在发送 client_tool_call 事件之前调用，避免客户端回传早于登记的竞态。
func (b *ClientToolBridge) Register(callID, userID string) <-chan ClientToolResult {
	ch := make(chan ClientToolResult, 1)
	b.mu.Lock()
	b.pending[callID] = &pendingCall{userID: userID, ch: ch}
	b.mu.Unlock()
	return ch
}

// Unregister 清理登记，幂等。
func (b *ClientToolBridge) Unregister(callID string) {
	b.mu.Lock()
	delete(b.pending, callID)
	b.mu.Unlock()
}

// OnResult 回传路由：校验用户归属后将结果送入等待方 channel。
// unknown call_id 或用户不匹配返回错误（调用方统一回 404，避免探测）。
func (b *ClientToolBridge) OnResult(callID, userID string, r ClientToolResult) error {
	b.mu.Lock()
	pc, ok := b.pending[callID]
	if ok && pc.userID == userID {
		delete(b.pending, callID) // 防重复回传
	}
	b.mu.Unlock()

	if !ok {
		return fmt.Errorf("unknown call_id: %s", callID)
	}
	if pc.userID != userID {
		return fmt.Errorf("call_id %s user mismatch", callID)
	}
	pc.ch <- r // 缓冲 1，不阻塞
	return nil
}
