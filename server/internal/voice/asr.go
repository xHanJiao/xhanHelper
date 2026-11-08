package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"xhan-helper/internal/config"
)

// ErrASRDurationLimit 表示本轮录音超过配置上限；调用方应 Finish 取回已识别结果，
// 而不是直接取消连接。
var ErrASRDurationLimit = errors.New("录音超过上限")

// ASRResult — 一条识别结果（部分或最终）。
type ASRResult struct {
	Text     string `json:"text"`
	Definite bool   `json:"definite"`
	Final    bool   `json:"final"`
	// EndTime 分句结束时间（ms），用于去重：enable_nonstream 会把同一句重复下发，
	// 但「对。对。」这种连着两句一样的话 end_time 不同，不能按文本判重。
	EndTime int64 `json:"end_time,omitempty"`
}

// SentenceCommitter 把 continuous 模式（result_type=single）的结果流折算成"可提交的新分句"。
// single 模式下每条结果的 Text 只含当前分句；退出时 final 只带最后一句，也需要补提交。
type SentenceCommitter struct {
	last    string
	lastEnd int64
}

// Commit 返回需要提交的新分句；空串表示这条结果不用提交（未判停 / 重复）。
func (c *SentenceCommitter) Commit(r ASRResult) string {
	if !r.Definite || r.Text == "" {
		return ""
	}
	if r.Text == c.last {
		// 同一句：任一路缺时间戳时只能按文本判重（会吞掉一模一样的连续两句，属已知退化），
		// 并把后到的时间戳补记下来，避免下一帧又当成新分句。
		if r.EndTime == 0 || c.lastEnd == 0 || r.EndTime == c.lastEnd {
			if c.lastEnd == 0 {
				c.lastEnd = r.EndTime
			}
			return ""
		}
	}
	c.last, c.lastEnd = r.Text, r.EndTime
	return r.Text
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
			EndTime  int64  `json:"end_time"`
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
	handshake := time.Duration(cfg.TimeoutSeconds) * time.Second
	if handshake <= 0 {
		handshake = 15 * time.Second
	}
	dialer := websocket.Dialer{HandshakeTimeout: handshake}
	conn, resp, err := dialer.DialContext(ctx, cfg.Endpoint, headers)
	if err != nil {
		if resp != nil {
			// 401/403 的真实原因（资源未开通 / resource id 不对）只在响应体里，别吞掉
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			if readErr != nil {
				return nil, fmt.Errorf("dial doubao asr: %w (http %d, read body: %v)", err, resp.StatusCode, readErr)
			}
			return nil, fmt.Errorf("dial doubao asr: %w (http %d: %s)", err, resp.StatusCode, strings.TrimSpace(string(body)))
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
			ShowUtterances:  true,
			ResultType:      cfg.ResultType,
			EnableNonstream: true,
			EndWindowSize:   cfg.EndWindowSize,
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
		return fmt.Errorf("%w（%d 秒）", ErrASRDurationLimit, c.maxBytes/32000)
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
		mt, data, err := c.conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read asr frame: %w", err)
		}
		f, err := decodeFrame(data)
		if err != nil {
			// 帧解析失败时带上 ws 消息类型和帧头：区分「服务端发了文本帧/事件帧」
			// 与「真正的二进制帧字段错位」，只看错误字符串分不出来。
			return fmt.Errorf("decode asr frame (ws_type=%d, len=%d, head=%x): %w",
				mt, len(data), headHex(data, 32), err)
		}
		if f.Type == msgErrorResponse {
			return fmt.Errorf("doubao asr error %d: %s", f.ErrorCode, f.ErrorMessage)
		}
		if f.Type != msgFullServerResponse {
			continue
		}
		// 先判 final 再判空 payload：协议允许负包不带内容，不能因为空 payload 就永远等下去
		final := f.Flags == flagLastNoSequence || f.Flags == flagLastSequence
		if len(f.Payload) == 0 {
			if final {
				return nil
			}
			continue
		}
		var parsed asrResponse
		if err := json.Unmarshal(f.Payload, &parsed); err != nil {
			return fmt.Errorf("parse asr response: %w", err)
		}
		if final {
			definite := true
			if onResult != nil {
				onResult(ASRResult{Text: parsed.Result.Text, Definite: definite, Final: true,
					EndTime: lastDefiniteEndTime(parsed.Result.Utterances)})
			}
			return nil
		}
		definite := false
		for _, u := range parsed.Result.Utterances {
			if u.Definite {
				definite = true
				break
			}
		}
		if onResult != nil && parsed.Result.Text != "" {
			onResult(ASRResult{
				Text:     parsed.Result.Text,
				Definite: definite,
				EndTime:  lastDefiniteEndTime(parsed.Result.Utterances),
			})
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

// lastDefiniteEndTime 取最后一条已判停分句的结束时间；拿不到返回 0。
func lastDefiniteEndTime(utterances []struct {
	Text     string `json:"text"`
	Definite bool   `json:"definite"`
	EndTime  int64  `json:"end_time"`
}) int64 {
	var end int64
	for _, u := range utterances {
		if u.Definite && u.EndTime > end {
			end = u.EndTime
		}
	}
	return end
}

// headHex 取帧头前 n 字节的十六进制，用于解析失败的诊断日志。
func headHex(data []byte, n int) []byte {
	if len(data) > n {
		return data[:n]
	}
	return data
}
