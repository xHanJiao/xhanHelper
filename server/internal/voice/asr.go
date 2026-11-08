package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"xhan-helper/internal/config"
)

// ASRResult — 一条识别结果（部分或最终）。
type ASRResult struct {
	Text     string `json:"text"`
	Definite bool   `json:"definite"`
	Final    bool   `json:"final"`
}

// ASRClient 是豆包流式语音识别模型 2.0 的客户端。
// 它只负责「上行 PCM / 下行结果」，鉴权头与二进制协议都封在这里。
type ASRClient struct {
	conn     *websocket.Conn
	writeMu  sync.Mutex
	maxBytes int
	written  int
}

type asrRequest struct {
	User    map[string]string `json:"user,omitempty"`
	Audio   asrAudio          `json:"audio"`
	Request asrRequestOptions `json:"request"`
}

type asrAudio struct {
	Format   string `json:"format"`
	Codec    string `json:"codec,omitempty"`
	Rate     int    `json:"rate"`
	Bits     int    `json:"bits"`
	Channel  int    `json:"channel"`
	Language string `json:"language,omitempty"`
}

type asrRequestOptions struct {
	ModelName       string `json:"model_name"`
	EnableITN       bool   `json:"enable_itn"`
	EnablePunc      bool   `json:"enable_punc"`
	EnableDDC       bool   `json:"enable_ddc"`
	ShowUtterances  bool   `json:"show_utterances"`
	ResultType      string `json:"result_type"`
	EnableNonstream bool   `json:"enable_nonstream"`
	EndWindowSize   int    `json:"end_window_size"`
}

type asrResponse struct {
	Result struct {
		Text       string `json:"text"`
		Utterances []struct {
			Text     string `json:"text"`
			Definite bool   `json:"definite"`
		} `json:"utterances"`
	} `json:"result"`
}

// DialASR 建立豆包 ASR WebSocket 连接并发送首包配置。
func DialASR(ctx context.Context, cfg config.VoiceASRConfig, apiKey, uid string) (*ASRClient, error) {
	headers := map[string][]string{
		"X-Api-Key":         {apiKey},
		"X-Api-Resource-Id": {cfg.ResourceID},
		"X-Api-Request-Id":  {uuid.NewString()},
		"X-Api-Connect-Id":  {uuid.NewString()},
	}
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, resp, err := dialer.DialContext(ctx, cfg.Endpoint, headers)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("dial doubao asr: %w (http %d)", err, resp.StatusCode)
		}
		return nil, fmt.Errorf("dial doubao asr: %w", err)
	}

	req := asrRequest{
		User: map[string]string{"uid": uid},
		Audio: asrAudio{
			Format:   "pcm",
			Codec:    "raw",
			Rate:     16000,
			Bits:     16,
			Channel:  1,
			Language: cfg.Language,
		},
		Request: asrRequestOptions{
			ModelName:       "bigmodel",
			EnableITN:       true,
			EnablePunc:      true,
			EnableDDC:       false,
			ShowUtterances:  true,
			ResultType:      "full",
			EnableNonstream: true,
			EndWindowSize:   800,
		},
	}
	payload, err := json.Marshal(req)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("marshal asr request: %w", err)
	}
	frame, err := encodeFrame(msgFullClientRequest, flagNone, serializationJSON, compressionGzip, payload)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send asr config: %w", err)
	}

	maxBytes := cfg.MaxSeconds * 16000 * 2
	if maxBytes <= 0 {
		maxBytes = 60 * 16000 * 2
	}
	return &ASRClient{conn: conn, maxBytes: maxBytes}, nil
}

// SendAudio 发送一段 16k/16bit/单声道 PCM。
func (c *ASRClient) SendAudio(pcm []byte) error {
	if len(pcm) == 0 {
		return nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.written+len(pcm) > c.maxBytes {
		return fmt.Errorf("录音超过上限（%d 秒）", c.maxBytes/32000)
	}
	frame, err := encodeFrame(msgAudioOnlyRequest, flagNone, serializationNone, compressionGzip, pcm)
	if err != nil {
		return err
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("set asr write deadline: %w", err)
	}
	if err := c.conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return fmt.Errorf("send asr audio: %w", err)
	}
	c.written += len(pcm)
	return nil
}

// Finish 发送最后一包（负包），通知服务端音频结束。
func (c *ASRClient) Finish() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	frame, err := encodeFrame(msgAudioOnlyRequest, flagLastNoSequence, serializationNone, compressionGzip, nil)
	if err != nil {
		return err
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("set asr write deadline: %w", err)
	}
	if err := c.conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return fmt.Errorf("send asr finish: %w", err)
	}
	return nil
}

// Read 阻塞读取识别结果，直到最后一包、错误或连接关闭。
func (c *ASRClient) Read(ctx context.Context, onResult func(ASRResult)) error {
	if deadline, ok := ctx.Deadline(); ok {
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			return fmt.Errorf("set asr read deadline: %w", err)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read asr frame: %w", err)
		}
		f, err := decodeFrame(data)
		if err != nil {
			return err
		}
		if f.Type == msgErrorResponse {
			return fmt.Errorf("doubao asr error %d: %s", f.ErrorCode, f.ErrorMessage)
		}
		if f.Type != msgFullServerResponse {
			continue
		}
		if len(f.Payload) == 0 {
			continue
		}
		var parsed asrResponse
		if err := json.Unmarshal(f.Payload, &parsed); err != nil {
			return fmt.Errorf("parse asr response: %w", err)
		}
		final := f.Flags == flagLastNoSequence || f.Flags == flagLastSequence
		definite := final
		for _, u := range parsed.Result.Utterances {
			if u.Definite {
				definite = true
				break
			}
		}
		if onResult != nil && (parsed.Result.Text != "" || final) {
			onResult(ASRResult{
				Text:     parsed.Result.Text,
				Definite: definite,
				Final:    final,
			})
		}
		if final {
			return nil
		}
	}
}

// Close 关闭连接。
func (c *ASRClient) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
