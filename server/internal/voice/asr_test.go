package voice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"xhan-helper/internal/config"
)

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
