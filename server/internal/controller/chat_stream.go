package controller

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/monitor"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// 一轮对话的事件留档：P0 客户端挂起/断线后 -> P1 回前台按 seq 续传。
//
// 为什么要有它：assistant 消息是「一轮 LLM 调用结束才落库」，挂起期间拉历史
// 看不到正在生成的那段文字；实时文本只存在于那条 TCP 连接里。本 hub 把本轮
// 已经发出的帧留一份，让回前台的新连接能补发 + 继续跟流。
const (
	maxTurnBufferBytes = 4 << 20          // 单轮 buffer 上限；超出从头部丢，重连方收到 reset
	turnSubQueue       = 256              // 订阅者通道深度；跟不上（死连接）就踢掉
	turnTTL            = 10 * time.Minute // 轮结束后保留多久（覆盖「挂起很久才回来」）
	streamHeartbeat    = 15 * time.Second // 心跳间隔；两端解析器都忽略注释行
	streamWriteTimeout = 30 * time.Second // 单帧写超时；死连接靠它变成 clientGone
)

// 内存只有「单轮」上限，没有全局上限：最坏是 4 MiB × 并发会话数，TTL 10 分钟内不回收。
// 单机个人使用够用；若将来并发会话数量级上涨，需要在这里加总量配额 + 淘汰。

// SSEFrame 一帧：seq 是同轮内单调递增的续传锚点（每轮从 1 开始，不跨轮）。
type SSEFrame struct {
	Seq   int64
	Event string
	Data  string
}

type subState int

const (
	subOK subState = iota
	subIdle
	subReset
)

// TurnStream 是「这一轮」的句柄。Publish/Finish 都挂在句柄上而不是按 convID 查表：
// 同会话新轮 Begin 之后，旧 handler 还可能发完 error/done，按 convID 查会串到新轮 buffer。
type TurnStream struct {
	mu        sync.Mutex
	convID    string
	turnID    string
	seq       int64
	frames    []SSEFrame
	bytes     int
	oldestSeq int64
	subs      map[chan SSEFrame]struct{}
	done      bool
	touchedAt time.Time
}

type turnHub struct {
	mu    sync.Mutex
	turns map[string]*TurnStream
}

var chatStreams = &turnHub{turns: map[string]*TurnStream{}}

// Begin 开始新一轮并替换同会话旧轮（旧轮句柄仍归旧 handler 所有）。
// 第一帧（seq=1）是 meta：客户端据此拿到 turn_id / conversation_id
// —— 懒创建的会话在 done 之前也就能续传了。
func (h *turnHub) Begin(convID string) *TurnStream {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old, ok := h.turns[convID]; ok {
		old.finish()
	}
	t := &TurnStream{
		convID:    convID,
		turnID:    strconv.FormatInt(time.Now().UnixNano(), 36),
		subs:      map[chan SSEFrame]struct{}{},
		touchedAt: time.Now(),
	}
	b, err := json.Marshal(map[string]string{"turn_id": t.turnID, "conversation_id": convID})
	if err != nil {
		// 编码固定两个字符串不会失败；真失败了也别丢整轮，只是客户端续传不了
		log.Printf("[chat_stream] marshal meta error: conv=%s err=%v", convID, err)
	} else {
		t.publish("meta", string(b))
	}
	h.turns[convID] = t
	return t
}

func (h *turnHub) Get(convID string) *TurnStream {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.turns[convID]
}

// Sweep 只回收已结束且过了 TTL 的轮。活轮永不回收：心跳不算 Publish，
// 静默十几分钟（verify/子任务）是常态，删掉会逼着重连方拿到错位的 seq。
func (h *turnHub) Sweep() {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, t := range h.turns {
		t.mu.Lock()
		expired := t.done && now.Sub(t.touchedAt) > turnTTL
		t.mu.Unlock()
		if expired {
			delete(h.turns, id)
		}
	}
}

// StartChatStreamSweeper 由 appinit 用 bgCtx 起，跟 CronRunner 同一套生命周期。
func StartChatStreamSweeper(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				chatStreams.Sweep()
			}
		}
	}()
}

// Meta 返回本轮第一帧（seq=1，Begin 时写入）。原连接要把这一帧也写给客户端，
// 否则它拿不到 turn_id / 懒创建会话的 conversation_id，回前台就续不上。
func (t *TurnStream) Meta() (SSEFrame, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.frames) == 0 {
		return SSEFrame{}, false
	}
	return t.frames[0], true
}

// Publish 入 buffer + 广播；返回刚分配的帧（调用方拿 seq 写原连接）。
func (t *TurnStream) Publish(event, data string) SSEFrame { return t.publish(event, data) }

func (t *TurnStream) publish(event, data string) SSEFrame {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	f := SSEFrame{Seq: t.seq, Event: event, Data: data}
	t.frames = append(t.frames, f)
	t.bytes += len(data) + len(event)
	if t.oldestSeq == 0 {
		t.oldestSeq = 1
	}
	for t.bytes > maxTurnBufferBytes && len(t.frames) > 1 {
		t.bytes -= len(t.frames[0].Data) + len(t.frames[0].Event)
		t.frames = t.frames[1:]
		t.oldestSeq = t.frames[0].Seq
	}
	for ch := range t.subs {
		select {
		case ch <- f:
		default:
			// 订阅者跟不上（死连接）：踢掉，让它按 seq 重连，别拖住整轮
			delete(t.subs, ch)
			close(ch)
		}
	}
	t.touchedAt = time.Now()
	return f
}

// Finish 标记本轮结束（幂等）：关闭订阅者通道，buffer 留给迟到的重连，TTL 后回收。
func (t *TurnStream) Finish() { t.finish() }

func (t *TurnStream) finish() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.done = true
	t.touchedAt = time.Now()
	for ch := range t.subs {
		delete(t.subs, ch)
		close(ch)
	}
}

// Subscribe 按 from 补发并（必要时）挂成订阅者。
// turnID 为空表示老客户端/未收到 meta：不做轮次校验，只按 seq 判断连续性。
func (t *TurnStream) Subscribe(from int64, turnID string) (replay []SSEFrame, ch <-chan SSEFrame, cancel func(), state subState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if turnID != "" && turnID != t.turnID {
		// 手里拿的是另一轮（另一台设备刚发了新消息把自己的轮取消了）
		return nil, nil, nil, subReset
	}
	if from+1 < t.oldestSeq || from > t.seq {
		return nil, nil, nil, subReset
	}
	for _, f := range t.frames {
		if f.Seq > from {
			replay = append(replay, f)
		}
	}
	if t.done {
		return replay, nil, func() {}, subOK
	}
	c := make(chan SSEFrame, turnSubQueue)
	t.subs[c] = struct{}{}
	return replay, c, func() {
		t.mu.Lock()
		if _, ok := t.subs[c]; ok {
			delete(t.subs, c)
		}
		t.mu.Unlock()
	}, subOK
}

// newDeadlineSetter 返回「下一次写之前重设 30s 写截止时间」的闭包。
// 不支持 SetWriteDeadline 的底层（测试用 ResponseRecorder）只记一次日志后放过。
func newDeadlineSetter(w http.ResponseWriter, convID string) func() {
	broken := false
	return func() {
		if broken {
			return
		}
		if err := view.SetWriteTimeout(w, streamWriteTimeout); err != nil {
			broken = true
			log.Printf("[chat_stream] set write deadline failed: conv=%s err=%v", convID, err)
		}
	}
}

// chatStream 续传接口：GET /api/chat/stream?conversation_id=X&turn=T&from=N
func chatStream(convRepo *service.ConversationRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Query("conversation_id")
		if convID == "" {
			view.JSONError(c, 400, "conversation_id required")
			return
		}
		if _, ok := requireConversationOwner(c, convRepo, convID); !ok {
			return
		}
		from, err := strconv.ParseInt(c.Query("from"), 10, 64)
		if err != nil || from < 0 {
			from = 0
		}

		w := c.Writer
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			log.Printf("[chat_stream] streaming not supported: conv=%s", convID)
			view.JSONError(c, 500, "streaming not supported")
			return
		}

		turn := chatStreams.Get(convID)
		if turn == nil {
			if werr := view.WriteSSE(w, flusher, "idle", `{"reason":"no running turn"}`); werr != nil {
				log.Printf("[chat_stream] idle write failed: conv=%s err=%v", convID, werr)
			}
			return
		}
		replay, ch, cancel, state := turn.Subscribe(from, c.Query("turn"))
		switch state {
		case subIdle:
			if werr := view.WriteSSE(w, flusher, "idle", `{"reason":"no running turn"}`); werr != nil {
				log.Printf("[chat_stream] idle write failed: conv=%s err=%v", convID, werr)
			}
			return
		case subReset:
			if werr := view.WriteSSE(w, flusher, "reset", `{"reason":"continuity lost"}`); werr != nil {
				log.Printf("[chat_stream] reset write failed: conv=%s err=%v", convID, werr)
			}
			return
		}

		monitor.IncSSE()
		defer monitor.DecSSE()
		defer cancel()

		setDeadline := newDeadlineSetter(w, convID)

		for _, f := range replay {
			setDeadline()
			if werr := view.WriteSSEID(w, flusher, f.Seq, f.Event, f.Data); werr != nil {
				return
			}
		}
		if ch == nil {
			return
		}

		ticker := time.NewTicker(streamHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case f, ok := <-ch:
				if !ok {
					return
				}
				setDeadline()
				if werr := view.WriteSSEID(w, flusher, f.Seq, f.Event, f.Data); werr != nil {
					return
				}
			case <-ticker.C:
				setDeadline()
				if werr := view.WriteComment(w, flusher, "ping"); werr != nil {
					return
				}
			case <-c.Request.Context().Done():
				return
			}
		}
	}
}
