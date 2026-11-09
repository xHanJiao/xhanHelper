package voice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"xhan-helper/internal/config"
)

func TestSynthesizeStreamsBase64Chunks(t *testing.T) {
	audio := []byte("fake-mp3")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Api-Key"); got != "test-key" {
			t.Errorf("X-Api-Key = %q", got)
		}
		if got := r.Header.Get("X-Api-Resource-Id"); got != "seed-tts-2.0" {
			t.Errorf("X-Api-Resource-Id = %q", got)
		}
		var req ttsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if req.ReqParams.Text != "你好" || req.ReqParams.Speaker != "speaker" {
			t.Errorf("unexpected request: %+v", req.ReqParams)
		}
		if !req.ReqParams.AudioParams.DisableMarkdownFilter {
			t.Error("markdown filter should be enabled")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"OK","data":"` + base64.StdEncoding.EncodeToString(audio[:3]) + `"}` + "\n"))
		_, _ = w.Write([]byte(`{"code":0,"message":"OK","data":"` + base64.StdEncoding.EncodeToString(audio[3:]) + `","usage":{"text_words":2}}` + "\n"))
		_, _ = w.Write([]byte(`{"code":20000000,"message":"OK"}` + "\n"))
	}))
	defer server.Close()

	client := NewTTSClient("test-key", config.VoiceTTSConfig{
		Endpoint: server.URL, ResourceID: "seed-tts-2.0", Speaker: "speaker",
		Format: "mp3", SampleRate: 24000, MaxChars: 100, TimeoutSeconds: 5,
	})
	got, words, err := client.Synthesize(context.Background(), " 你好 ", "")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if string(got) != string(audio) {
		t.Fatalf("audio = %q", got)
	}
	if words != 2 {
		t.Fatalf("words = %d", words)
	}
}

func TestSynthesizeErrorCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":45000001,"message":"bad param"}` + "\n"))
	}))
	defer server.Close()
	client := NewTTSClient("k", config.VoiceTTSConfig{Endpoint: server.URL, Speaker: "s", MaxChars: 100, TimeoutSeconds: 5})
	_, _, err := client.Synthesize(context.Background(), "你好", "")
	if err == nil || !strings.Contains(err.Error(), "bad param") {
		t.Fatalf("err = %v", err)
	}
}

func TestSynthesizeRejectsTooLong(t *testing.T) {
	client := NewTTSClient("k", config.VoiceTTSConfig{Endpoint: "http://127.0.0.1:1", MaxChars: 2, TimeoutSeconds: 1})
	if _, _, err := client.Synthesize(context.Background(), "一二三", ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestSynthesizeUsesExplicitSpeaker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ttsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if req.ReqParams.Speaker != "requested-speaker" {
			t.Errorf("speaker = %q", req.ReqParams.Speaker)
		}
		_, _ = w.Write([]byte(`{"code":0,"data":"` + base64.StdEncoding.EncodeToString([]byte("mp3")) + `"}` + "\n"))
		_, _ = w.Write([]byte(`{"code":20000000}` + "\n"))
	}))
	defer server.Close()

	client := NewTTSClient("k", config.VoiceTTSConfig{
		Endpoint: server.URL, Speaker: "default-speaker", MaxChars: 100, TimeoutSeconds: 5,
	})
	if _, _, err := client.Synthesize(context.Background(), "你好", "requested-speaker"); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
}
