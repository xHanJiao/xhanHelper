package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"xhan-helper/internal/config"
	"xhan-helper/internal/view"
	"xhan-helper/internal/voice"
)

// VoiceHandler — 语音聊天 HTTP/WebSocket 入口。
//
// TTS 与 ASR 都由服务端代打豆包，API Key 不下发客户端。
// 浏览器 WebSocket 不能带 Authorization 头，所以先用 Bearer 换一次性 ticket 再连 WS。
type VoiceHandler struct {
	cfg     *config.VoiceConfig
	tts     *voice.TTSClient
	tickets *voiceTicketStore

	// C 模式一条连接能占满整个会话（默认 10 分钟），按用户限流，
	// 防止重复开连接把豆包并发和音频时长一起放大。
	continuousMu sync.Mutex
	continuous   map[string]bool
}

// NewVoiceHandler 创建 VoiceHandler。
func NewVoiceHandler(cfg *config.VoiceConfig) *VoiceHandler {
	if cfg == nil {
		panic("voice handler: nil config (wiring bug)")
	}
	return &VoiceHandler{
		cfg:        cfg,
		tts:        voice.NewTTSClient(cfg.APIKey, cfg.TTS),
		tickets:    newVoiceTicketStore(60 * time.Second),
		continuous: map[string]bool{},
	}
}

// Status GET /api/voice/config — 客户端据此决定是否显示麦克风按钮。
func (h *VoiceHandler) Status(c *gin.Context) {
	view.JSONOK(c, gin.H{
		"enabled":     h.cfg.Enabled,
		"ready":       h.cfg.Ready(),
		"speaker":     h.cfg.TTS.Speaker,
		"language":    h.cfg.ASR.Language,
		"max_seconds": h.cfg.ASR.MaxSeconds,
		// C 模式单次会话上限（客户端据此做硬收口；空闲收口由客户端判定）
		"session_seconds": h.cfg.ASR.SessionSeconds,
	})
}

// Ticket POST /api/voice/asr-ticket — 用 Bearer 换一次性 WebSocket ticket。
func (h *VoiceHandler) Ticket(c *gin.Context) {
	if !h.cfg.Ready() {
		view.JSONError(c, http.StatusServiceUnavailable, "语音功能未配置")
		return
	}
	userID := c.GetString("user_id")
	ticket, expiresIn := h.tickets.issue(userID)
	view.JSONOK(c, gin.H{"ticket": ticket, "expires_in": int(expiresIn.Seconds())})
}

// TTS POST /api/voice/tts — 文本转 mp3（整段返回）。
func (h *VoiceHandler) TTS(c *gin.Context) {
	if !h.cfg.Ready() {
		view.JSONError(c, http.StatusServiceUnavailable, "语音功能未配置")
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, http.StatusBadRequest, "invalid request")
		return
	}
	audio, textWords, err := h.tts.Synthesize(c.Request.Context(), req.Text)
	if err != nil {
		log.Printf("[voice] tts failed (chars=%d): %v", len([]rune(req.Text)), err)
		view.JSONError(c, http.StatusBadGateway, "语音合成失败")
		return
	}
	log.Printf("[voice] tts ok (chars=%d, billed=%d, bytes=%d)", len([]rune(req.Text)), textWords, len(audio))
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "audio/mpeg", audio)
}

// ASR GET /api/voice/asr?ticket=... — 浏览器/客户端音频流入口。
func (h *VoiceHandler) ASR(c *gin.Context) {
	if !h.cfg.Ready() {
		view.JSONError(c, http.StatusServiceUnavailable, "语音功能未配置")
		return
	}
	userID, ok := h.tickets.consume(c.Query("ticket"))
	if !ok {
		view.JSONError(c, http.StatusUnauthorized, "invalid or expired ticket")
		return
	}

	upgrader := websocket.Upgrader{
		HandshakeTimeout: 10 * time.Second,
		ReadBufferSize:   16 << 10,
		WriteBufferSize:  16 << 10,
		// 同源策略交由 token 保护；原生客户端不带 Origin，不能按 Origin 拒绝。
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	client, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("[voice] asr upgrade failed (user=%s): %v", userID, err)
		return
	}
	defer client.Close()
	// 单帧上限：200ms PCM 只有 6.4KB，64KB 足够；防止有效 ticket 被用来塞大帧
	client.SetReadLimit(64 << 10)

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	handshakeTimeout := time.Duration(h.cfg.ASR.TimeoutSeconds) * time.Second
	if handshakeTimeout <= 0 {
		handshakeTimeout = 15 * time.Second
	}
	// C 模式（连续对话）：一条连接连着送音频，按分句增量返回（result_type=single），
	// 客户端每收到一条 sentence 就提交一轮对话，不再等服务端 ready→录音→end。
	continuous := c.Query("mode") == "continuous"
	log.Printf("[voice] asr ws opened (user=%s, continuous=%v)", userID, continuous)
	if continuous {
		if !h.acquireContinuous(userID) {
			log.Printf("[voice] continuous session already active (user=%s)", userID)
			_ = client.WriteJSON(gin.H{"type": "error", "error": "已有进行中的语音会话"})
			return
		}
		defer h.releaseContinuous(userID)
	}
	asrCfg := h.cfg.ASR
	if continuous {
		asrCfg.ResultType = "single"
		// 连续对话一条连接顶到整个会话上限；超时/断开由客户端重连
		asrCfg.MaxSeconds = h.cfg.ASR.SessionSeconds
	}
	log.Printf("[voice] asr dialing (user=%s, result_type=%s, end_window=%d, max_seconds=%d)",
		userID, asrCfg.ResultType, asrCfg.EndWindowSize, asrCfg.MaxSeconds)
	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, handshakeTimeout)
	asr, err := voice.DialASR(handshakeCtx, asrCfg, h.cfg.APIKey, userID)
	handshakeCancel()
	if err != nil {
		log.Printf("[voice] asr dial failed (user=%s): %v", userID, err)
		_ = client.WriteJSON(gin.H{"type": "error", "error": "语音识别连接失败"})
		return
	}
	defer asr.Close()

	var writeMu sync.Mutex
	send := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return client.WriteJSON(v)
	}
	if err := send(gin.H{"type": "ready"}); err != nil {
		log.Printf("[voice] asr send ready failed (user=%s): %v", userID, err)
		return
	}
	log.Printf("[voice] asr ready sent (user=%s)", userID)
	startedAt := time.Now()

	// 客户端下行：二进制 PCM / 文本控制帧（end、cancel）。
	go func() {
		var audioBytes int
		var firstAudioLogged bool
		var lastAudioLog = time.Now()
		for {
			mt, data, err := client.ReadMessage()
			if err != nil {
				cancel()
				// 关闭豆包连接，否则主 goroutine 的 Read 会一直阻塞到读超时
				_ = asr.Close()
				return
			}
			switch mt {
			case websocket.BinaryMessage:
				if !firstAudioLogged {
					firstAudioLogged = true
					log.Printf("[voice] asr first audio frame (user=%s, bytes=%d)", userID, len(data))
				}
				audioBytes += len(data)
				if time.Since(lastAudioLog) >= 5*time.Second {
					lastAudioLog = time.Now()
					log.Printf("[voice] asr upstream (user=%s, audio=%.1fs, bytes=%d)",
						userID, float64(audioBytes)/32000, audioBytes)
				}
				if err := asr.SendAudio(data); err != nil {
					if errors.Is(err, voice.ErrASRDurationLimit) {
						// 到上限不要丢结果：先发提示，再 Finish 让服务端把已识别文本吐完
						_ = send(gin.H{"type": "limit", "message": err.Error()})
						if finishErr := asr.Finish(); finishErr != nil {
							log.Printf("[voice] asr finish after limit failed (user=%s): %v", userID, finishErr)
						}
						return
					}
					log.Printf("[voice] asr send audio failed (user=%s): %v", userID, err)
					_ = send(gin.H{"type": "error", "error": err.Error()})
					cancel()
					_ = asr.Close()
					return
				}
			case websocket.TextMessage:
				var msg struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal(data, &msg); err != nil {
					log.Printf("[voice] asr control parse failed (user=%s): %v", userID, err)
					continue
				}
				switch msg.Type {
				case "end":
					log.Printf("[voice] asr end received (user=%s, bytes=%d)", userID, audioBytes)
					if err := asr.Finish(); err != nil {
						log.Printf("[voice] asr finish failed (user=%s): %v", userID, err)
						_ = send(gin.H{"type": "error", "error": "结束录音失败"})
						cancel()
						_ = asr.Close()
					}
				case "cancel":
					log.Printf("[voice] asr cancel received (user=%s, bytes=%d)", userID, audioBytes)
					cancel()
					_ = asr.Close()
					return
				}
			}
		}
	}()

	readTimeout := time.Duration(asrCfg.MaxSeconds+30) * time.Second
	readCtx, readCancel := context.WithTimeout(ctx, readTimeout)
	defer readCancel()
	var committer voice.SentenceCommitter
	var resultCount int
	// 上一条发过的「有人在说话」文本：豆包会对同一句重复吐结果（SentenceCommitter
	// 就是为此去重），不去重会让静音期的重复结果一直给空闲收口续命。
	var lastVoiceText string
	err = asr.Read(readCtx, func(result voice.ASRResult) {
		resultCount++
		// 识别文本本身不进日志（设计约束）：只记长度、是否判停、时间戳
		if result.Definite || result.Final || resultCount%20 == 1 {
			log.Printf("[voice] asr result #%d (user=%s, chars=%d, definite=%v, final=%v, end_time=%d)",
				resultCount, userID, len([]rune(result.Text)), result.Definite, result.Final, result.EndTime)
		}
		if continuous {
			// 只下发已判停的分句；客户端按 sentence 逐轮提交。
			// 但「ASR 听到人声」要单独告诉客户端（不带文本）：空闲收口靠它顺延，
			// 客户端用麦克风电平猜会被环境噪声骗（详见 051）。
			// 文本有变化才发（partial 会随说话增长，长句不受影响）：重复结果不能一直续命。
			if text := strings.TrimSpace(result.Text); text != "" && text != lastVoiceText {
				lastVoiceText = text
				if err := send(gin.H{"type": "voice"}); err != nil {
					log.Printf("[voice] asr send voice failed (user=%s): %v", userID, err)
					readCancel()
				}
			}
			text := committer.Commit(result)
			if text == "" {
				return
			}
			log.Printf("[voice] asr sentence out (user=%s, chars=%d, end_time=%d)",
				userID, len([]rune(text)), result.EndTime)
			if err := send(gin.H{"type": "sentence", "text": text}); err != nil {
				log.Printf("[voice] asr send sentence failed (user=%s): %v", userID, err)
				readCancel()
			}
			return
		}
		if err := send(gin.H{
			"type":     "result",
			"text":     result.Text,
			"definite": result.Definite,
			"final":    result.Final,
		}); err != nil {
			log.Printf("[voice] asr send result failed (user=%s): %v", userID, err)
			readCancel()
		}
	})
	elapsed := time.Since(startedAt).Round(time.Second)
	if err != nil && ctx.Err() == nil {
		log.Printf("[voice] asr read failed (user=%s, elapsed=%s): %v", userID, elapsed, err)
		_ = send(gin.H{"type": "error", "error": "语音识别失败"})
	} else {
		log.Printf("[voice] asr session closed (user=%s, continuous=%v, results=%d, elapsed=%s)",
			userID, continuous, resultCount, elapsed)
	}
}

// voiceTicketStore 是一次性 WebSocket ticket 的内存账本。
type voiceTicketStore struct {
	mu   sync.Mutex
	ttl  time.Duration
	data map[string]voiceTicket
}

type voiceTicket struct {
	userID  string
	expires time.Time
}

func newVoiceTicketStore(ttl time.Duration) *voiceTicketStore {
	return &voiceTicketStore{ttl: ttl, data: make(map[string]voiceTicket)}
}

func (s *voiceTicketStore) issue(userID string) (string, time.Duration) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败属于系统级异常，直接 panic 比发一个可预测 ticket 安全
		panic(fmt.Sprintf("voice ticket: rand failed: %v", err))
	}
	ticket := hex.EncodeToString(buf)
	now := time.Now()
	s.mu.Lock()
	for k, v := range s.data {
		if now.After(v.expires) {
			delete(s.data, k)
		}
	}
	s.data[ticket] = voiceTicket{userID: userID, expires: now.Add(s.ttl)}
	s.mu.Unlock()
	return ticket, s.ttl
}

func (s *voiceTicketStore) consume(ticket string) (string, bool) {
	if ticket == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.data[ticket]
	if !ok {
		return "", false
	}
	delete(s.data, ticket)
	if time.Now().After(item.expires) {
		return "", false
	}
	return item.userID, true
}

// acquireContinuous 抢占用户的连续对话槽位；同一用户只允许一条。
func (h *VoiceHandler) acquireContinuous(userID string) bool {
	h.continuousMu.Lock()
	defer h.continuousMu.Unlock()
	if h.continuous[userID] {
		return false
	}
	h.continuous[userID] = true
	return true
}

func (h *VoiceHandler) releaseContinuous(userID string) {
	h.continuousMu.Lock()
	defer h.continuousMu.Unlock()
	delete(h.continuous, userID)
}
