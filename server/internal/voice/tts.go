package voice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/config"
)

const (
	ttsSuccessCode = 0
	ttsEndCode     = 20000000
	ttsMaxAudio    = 20 << 20 // 单次合成音频上限，防止异常响应吃满内存
)

// TTSClient — 豆包语音合成模型 2.0（HTTP Chunked 单向流式）。
type TTSClient struct {
	apiKey string
	cfg    config.VoiceTTSConfig
	http   *http.Client
}

// NewTTSClient 创建 TTS 客户端。
func NewTTSClient(apiKey string, cfg config.VoiceTTSConfig) *TTSClient {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &TTSClient{
		apiKey: apiKey,
		cfg:    cfg,
		http:   &http.Client{Timeout: timeout},
	}
}

type ttsRequest struct {
	ReqParams ttsReqParams `json:"req_params"`
}

type ttsReqParams struct {
	Text        string         `json:"text"`
	Speaker     string         `json:"speaker"`
	AudioParams ttsAudioParams `json:"audio_params"`
}

type ttsAudioParams struct {
	Format                string `json:"format"`
	SampleRate            int    `json:"sample_rate"`
	SpeechRate            int    `json:"speech_rate,omitempty"`
	DisableMarkdownFilter bool   `json:"disable_markdown_filter"`
	DisableEmojiFilter    bool   `json:"disable_emoji_filter"`
}

type ttsLine struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
	Usage   struct {
		TextWords int `json:"text_words"`
	} `json:"usage"`
}

// Synthesize 合成一段文本，返回 mp3 字节与实际计费字符数（服务端返回时）。
func (c *TTSClient) Synthesize(ctx context.Context, text string) ([]byte, int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, 0, fmt.Errorf("tts: 文本为空")
	}
	if c.cfg.MaxChars > 0 && len([]rune(text)) > c.cfg.MaxChars {
		return nil, 0, fmt.Errorf("tts: 文本超过 %d 字符", c.cfg.MaxChars)
	}

	payload, err := json.Marshal(ttsRequest{
		ReqParams: ttsReqParams{
			Text:    text,
			Speaker: c.cfg.Speaker,
			AudioParams: ttsAudioParams{
				Format:                c.cfg.Format,
				SampleRate:            c.cfg.SampleRate,
				SpeechRate:            c.cfg.SpeechRate,
				DisableMarkdownFilter: true,
				DisableEmojiFilter:    true,
			},
		},
	})
	if err != nil {
		return nil, 0, fmt.Errorf("tts: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("tts: create request: %w", err)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("X-Api-Resource-Id", c.cfg.ResourceID)
	req.Header.Set("X-Api-Request-Id", uuid.NewString())
	req.Header.Set("X-Control-Require-Usage-Tokens-Return", "*")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("tts: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, 0, fmt.Errorf("tts: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	audio := make([]byte, 0, 32<<10)
	textWords := 0
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var item ttsLine
		if err := json.Unmarshal(line, &item); err != nil {
			return nil, 0, fmt.Errorf("tts: parse stream line: %w", err)
		}
		if item.Usage.TextWords > 0 {
			textWords = item.Usage.TextWords
		}
		switch {
		case item.Code == ttsSuccessCode:
			if item.Data == "" {
				continue
			}
			chunk, err := base64.StdEncoding.DecodeString(item.Data)
			if err != nil {
				return nil, 0, fmt.Errorf("tts: decode audio chunk: %w", err)
			}
			if len(audio)+len(chunk) > ttsMaxAudio {
				return nil, 0, fmt.Errorf("tts: 音频超过 %d MB", ttsMaxAudio>>20)
			}
			audio = append(audio, chunk...)
		case item.Code == ttsEndCode:
			return audio, textWords, nil
		default:
			return nil, 0, fmt.Errorf("tts: code %d: %s", item.Code, item.Message)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, fmt.Errorf("tts: read stream: %w", err)
	}
	if len(audio) == 0 {
		return nil, 0, fmt.Errorf("tts: 未返回音频")
	}
	return audio, textWords, nil
}
