package voice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"xhan-helper/internal/config"
)

func TestASRReadsEmptyFinalFrame(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		partial, _ := json.Marshal(map[string]any{"result": map[string]any{"text": "你好"}})
		frame, _ := encodeFrame(msgFullServerResponse, flagNone, serializationJSON, compressionGzip, partial)
		_ = conn.WriteMessage(websocket.BinaryMessage, frame)

		// 负包允许不带内容：Read 必须据此结束，不能等到读超时
		frame, _ = encodeFrame(msgFullServerResponse, flagLastNoSequence, serializationJSON, compressionGzip, nil)
		_ = conn.WriteMessage(websocket.BinaryMessage, frame)
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := DialASR(ctx, config.VoiceASRConfig{
		Endpoint: "ws" + strings.TrimPrefix(server.URL, "http"), ResourceID: "volc.seedasr.sauc.duration",
		Language: "zh-CN", MaxSeconds: 10, TimeoutSeconds: 5,
	}, "test-key", "u1")
	if err != nil {
		t.Fatalf("DialASR: %v", err)
	}
	defer client.Close()

	var results []ASRResult
	if err := client.Read(ctx, func(r ASRResult) { results = append(results, r) }); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(results) != 1 || results[0].Text != "你好" {
		t.Fatalf("results = %+v", results)
	}
}

func TestASRReadsPartialAndFinal(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		if r.Header.Get("X-Api-Key") != "test-key" {
			t.Errorf("X-Api-Key = %q", r.Header.Get("X-Api-Key"))
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read config: %v", err)
			return
		}
		f, err := decodeFrame(data)
		if err != nil {
			t.Errorf("decode config: %v", err)
			return
		}
		var req asrRequest
		if err := json.Unmarshal(f.Payload, &req); err != nil {
			t.Errorf("config json: %v", err)
			return
		}
		if req.Audio.Format != "pcm" || req.Audio.Rate != 16000 {
			t.Errorf("unexpected audio config: %+v", req.Audio)
		}
		partial, _ := json.Marshal(map[string]any{"result": map[string]any{"text": "你"}})
		frame, _ := encodeFrame(msgFullServerResponse, flagNone, serializationJSON, compressionGzip, partial)
		_ = conn.WriteMessage(websocket.BinaryMessage, frame)

		finalBody, _ := json.Marshal(map[string]any{"result": map[string]any{"text": "你好世界"}})
		frame, _ = encodeFrame(msgFullServerResponse, flagLastNoSequence, serializationJSON, compressionGzip, finalBody)
		_ = conn.WriteMessage(websocket.BinaryMessage, frame)
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := DialASR(ctx, config.VoiceASRConfig{
		Endpoint: "ws" + strings.TrimPrefix(server.URL, "http"), ResourceID: "volc.seedasr.sauc.duration",
		Language: "zh-CN", MaxSeconds: 10, TimeoutSeconds: 5,
	}, "test-key", "u1")
	if err != nil {
		t.Fatalf("DialASR: %v", err)
	}
	defer client.Close()

	var results []ASRResult
	err = client.Read(ctx, func(r ASRResult) { results = append(results, r) })
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	if results[0].Text != "你" || results[0].Final {
		t.Fatalf("partial = %+v", results[0])
	}
	if results[1].Text != "你好世界" || !results[1].Final {
		t.Fatalf("final = %+v", results[1])
	}
}

// TestASRDurationLimit — 超上限必须回 ErrASRDurationLimit，调用方据此 Finish 取回结果
// 而不是把已识别文本一起丢掉。
func TestASRDurationLimit(t *testing.T) {
	c := &ASRClient{maxBytes: 10}
	err := c.SendAudio(make([]byte, 11))
	if !errors.Is(err, ErrASRDurationLimit) {
		t.Fatalf("err = %v, want ErrASRDurationLimit", err)
	}
}

// TestDialASRSendsSpeakerOptions — 声纹/说话人分离参数必须进首包；关闭时保持原请求不变。
func TestDialASRSendsSpeakerOptions(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read config: %v", err)
			return
		}
		f, err := decodeFrame(data)
		if err != nil {
			t.Errorf("decode config: %v", err)
			return
		}
		var req asrRequest
		if err := json.Unmarshal(f.Payload, &req); err != nil {
			t.Errorf("config json: %v", err)
			return
		}
		if !req.Request.EnableSpeakerInfo || req.Request.SSDMode != 2 {
			t.Errorf("speaker options = %+v", req.Request)
		}
		if len(req.Request.Voiceprints) != 1 || req.Request.Voiceprints[0].ID != "vp-1" {
			t.Errorf("voiceprints = %+v", req.Request.Voiceprints)
		}
		// 负包让 Read 正常退出
		frame, _ := encodeFrame(msgFullServerResponse, flagLastNoSequence, serializationJSON, compressionGzip, nil)
		_ = conn.WriteMessage(websocket.BinaryMessage, frame)
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := DialASR(ctx, config.VoiceASRConfig{
		Endpoint: "ws" + strings.TrimPrefix(server.URL, "http"), ResourceID: "volc.seedasr.sauc.duration",
		Language: "zh-CN", MaxSeconds: 10, TimeoutSeconds: 5,
		EnableSpeakerInfo: false, SSDMode: 2, VoiceprintID: "vp-1",
	}, "test-key", "u1")
	if err != nil {
		t.Fatalf("DialASR: %v", err)
	}
	defer client.Close()
	if err := client.Read(ctx, nil); err != nil {
		t.Fatalf("Read: %v", err)
	}
}

// TestASRReadsSpeakerInfo — speaker_id 兼容 additions 内与顶层两种布局，并保留分句列表。
func TestASRReadsSpeakerInfo(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		body, _ := json.Marshal(map[string]any{"result": map[string]any{
			"text": "你好。再见。",
			"utterances": []any{
				map[string]any{"text": "你好。", "definite": true, "end_time": 1000,
					"additions": map[string]any{"speaker_id": "spk-a"}},
				map[string]any{"text": "再见。", "definite": true, "end_time": 2000, "speaker_id": "spk-b"},
			},
		}})
		frame, _ := encodeFrame(msgFullServerResponse, flagNone, serializationJSON, compressionGzip, body)
		_ = conn.WriteMessage(websocket.BinaryMessage, frame)
		frame, _ = encodeFrame(msgFullServerResponse, flagLastNoSequence, serializationJSON, compressionGzip, nil)
		_ = conn.WriteMessage(websocket.BinaryMessage, frame)
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := DialASR(ctx, config.VoiceASRConfig{
		Endpoint: "ws" + strings.TrimPrefix(server.URL, "http"), ResourceID: "volc.seedasr.sauc.duration",
		Language: "zh-CN", MaxSeconds: 10, TimeoutSeconds: 5, EnableSpeakerInfo: true,
	}, "test-key", "u1")
	if err != nil {
		t.Fatalf("DialASR: %v", err)
	}
	defer client.Close()

	var results []ASRResult
	if err := client.Read(ctx, func(r ASRResult) { results = append(results, r) }); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}
	r := results[0]
	if r.SpeakerID != "spk-b" {
		t.Errorf("SpeakerID = %q, want spk-b", r.SpeakerID)
	}
	if len(r.Utterances) != 2 || r.Utterances[0].SpeakerID != "spk-a" || r.Utterances[1].SpeakerID != "spk-b" {
		t.Errorf("utterances = %+v", r.Utterances)
	}
}

// TestSentenceCommitter — C 模式下只提交已判停的新分句；重复文本与未判停结果不提交，
// 退出时 final（只含最后一句）能补出残句。
func TestSentenceCommitter(t *testing.T) {
	var c SentenceCommitter
	cases := []struct {
		in   ASRResult
		want string
	}{
		{ASRResult{Text: "第一句"}, ""},
		{ASRResult{Text: "第一句。", Definite: true}, "第一句。"},
		{ASRResult{Text: "第二"}, ""},
		{ASRResult{Text: "第一句。", Definite: true}, ""}, // 重复下发不重复提交
		{ASRResult{Text: "第二句。", Definite: true}, "第二句。"},
		{ASRResult{Text: "第二句。"}, ""},
	}
	for i, tc := range cases {
		if got := c.Commit(tc.in); got != tc.want {
			t.Errorf("case %d: Commit(%+v) = %q, want %q", i, tc.in, got, tc.want)
		}
	}

	// 有 end_time 时按时间判重：连着说两句一模一样的话不能被吞
	var timed SentenceCommitter
	if got := timed.Commit(ASRResult{Text: "对。", Definite: true, EndTime: 1000}); got != "对。" {
		t.Fatalf("first 对 = %q", got)
	}
	if got := timed.Commit(ASRResult{Text: "对。", Definite: true, EndTime: 1000}); got != "" {
		t.Fatalf("duplicate 对 = %q, want empty", got)
	}
	if got := timed.Commit(ASRResult{Text: "对。", Definite: true, EndTime: 2500}); got != "对。" {
		t.Fatalf("second 对 = %q, want 对。", got)
	}

	// 先来没时间戳的同文结果、再来带时间戳的重复：不能提交两次
	var mixed SentenceCommitter
	if got := mixed.Commit(ASRResult{Text: "好。", Definite: true}); got != "好。" {
		t.Fatalf("first 好 = %q", got)
	}
	if got := mixed.Commit(ASRResult{Text: "好。", Definite: true, EndTime: 1200}); got != "" {
		t.Fatalf("mixed duplicate 好 = %q, want empty", got)
	}
	if got := mixed.Commit(ASRResult{Text: "好。", Definite: true, EndTime: 1200}); got != "" {
		t.Fatalf("timed duplicate 好 = %q, want empty", got)
	}
	if got := mixed.Commit(ASRResult{Text: "好。", Definite: true, EndTime: 2600}); got != "好。" {
		t.Fatalf("second timed 好 = %q, want 好。", got)
	}
}
