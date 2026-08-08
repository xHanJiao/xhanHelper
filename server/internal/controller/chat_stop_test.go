package controller

import "testing"

// Register 应收起同一会话的旧循环：再次注册同 conv 时，旧 channel 被关闭。
// 对应事故场景：旧 ReAct 循环（客户端工具超时等待）未结束，用户重发消息
// 启动新循环——旧循环必须被取消，否则两个循环并发写库产生孤儿 tool 消息。
func TestChatStopRegistry_RegisterReplacesOld(t *testing.T) {
	r := &ChatStopRegistry{stops: map[string]chan struct{}{}}

	oldCh := r.Register("conv-1")
	select {
	case <-oldCh:
		t.Fatal("newly registered channel should not be closed")
	default:
	}

	newCh := r.Register("conv-1")
	// 旧循环的 channel 应已被关闭
	select {
	case <-oldCh:
	default:
		t.Fatal("old channel should be closed after re-register for same conv")
	}
	// 新 channel 不受影响
	select {
	case <-newCh:
		t.Fatal("new channel should not be closed by re-register")
	default:
	}
}

// Unregister 只能回收自己注册的 channel：旧 handler 的 defer 在
// 新循环注册之后执行时，不能把新循环的注册一起删掉。
func TestChatStopRegistry_UnregisterOnlyOwnChannel(t *testing.T) {
	r := &ChatStopRegistry{stops: map[string]chan struct{}{}}

	ch1 := r.Register("conv-1")
	ch2 := r.Register("conv-1") // 旧循环被替换

	// 旧 handler 结束（defer）时 try 回收 ch1——不应影响 ch2
	r.Unregister("conv-1", ch1)

	r.mu.Lock()
	cur, ok := r.stops["conv-1"]
	r.mu.Unlock()
	if !ok || cur != ch2 {
		t.Fatal("Unregister of stale channel removed the new registration")
	}

	// 新 handler 结束，正常回收
	r.Unregister("conv-1", ch2)
	r.mu.Lock()
	_, ok = r.stops["conv-1"]
	r.mu.Unlock()
	if ok {
		t.Fatal("expected conv-1 unregistered after own Unregister")
	}
}
