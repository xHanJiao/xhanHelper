package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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
}

// NewVoiceHandler 创建 VoiceHandler。
func NewVoiceHandler(cfg *config.VoiceConfig) *VoiceHandler {
	if cfg == nil {
		panic("voice handler: nil config (wiring bug)")
	}
	return &VoiceHandler{
		cfg:     cfg,
		tts:     voice.NewTTSClient(cfg.APIKey, cfg.TTS),
		tickets: newVoiceTicketStore(60 * time.Second),
	}
}

// Status GET /api/voice/config — 客户端据此决定是否显示麦克风按钮。
func (h *VoiceHandler) Status(c *gin.Context) {
	view.JSONOK(c, gin.H{
		"enabled":  h.cfg.Enabled,
		"ready":    h.cfg.Ready(),
		"speaker":  h.cfg.TTS.Speaker,
		"language": h.cfg.ASR.Language,
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
		ReadBufferSize:  16 << 10,
		WriteBufferSize: 16 << 10,
		// 同源策略交由 token 保护；原生客户端不带 Origin，不能按 Origin 拒绝。
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	client, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("[voice] asr upgrade failed (user=%s): %v", userID, err)
		return
	}
	defer client.Close()

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, 15*time.Second)
	asr, err := voice.DialASR(handshakeCtx, h.cfg.ASR, h.cfg.APIKey, userID)
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

	// 客户端下行：二进制 PCM / 文本控制帧（end、cancel）。
	go func() {
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
				if err := asr.SendAudio(data); err != nil {
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
					if err := asr.Finish(); err != nil {
						log.Printf("[voice] asr finish failed (user=%s): %v", userID, err)
						_ = send(gin.H{"type": "error", "error": "结束录音失败"})
						cancel()
						_ = asr.Close()
					}
				case "cancel":
					cancel()
					_ = asr.Close()
					return
				}
			}
		}
	}()

	readTimeout := time.Duration(h.cfg.ASR.MaxSeconds+30) * time.Second
	readCtx, readCancel := context.WithTimeout(ctx, readTimeout)
	defer readCancel()
	err = asr.Read(readCtx, func(result voice.ASRResult) {
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
	if err != nil && ctx.Err() == nil {
		log.Printf("[voice] asr read failed (user=%s): %v", userID, err)
		_ = send(gin.H{"type": "error", "error": "语音识别失败"})
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
