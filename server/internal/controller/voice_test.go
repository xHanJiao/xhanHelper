package controller

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/config"
)

func TestVoiceTicketSingleUse(t *testing.T) {
	store := newVoiceTicketStore(time.Minute)
	ticket, ttl := store.issue("u1")
	if ticket == "" || ttl <= 0 {
		t.Fatalf("issue returned empty ticket/ttl")
	}
	user, ok := store.consume(ticket)
	if !ok || user != "u1" {
		t.Fatalf("consume = %q, %v", user, ok)
	}
	if _, ok := store.consume(ticket); ok {
		t.Fatal("ticket should be single-use")
	}
}

func TestVoiceTicketExpires(t *testing.T) {
	store := newVoiceTicketStore(10 * time.Millisecond)
	ticket, _ := store.issue("u1")
	time.Sleep(20 * time.Millisecond)
	if _, ok := store.consume(ticket); ok {
		t.Fatal("ticket should expire")
	}
}

func TestVoiceTTSHandler(t *testing.T) {
	audio := []byte("mp3-bytes")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "test-key" {
			t.Errorf("X-Api-Key = %q", r.Header.Get("X-Api-Key"))
		}
		_, _ = w.Write([]byte(`{"code":0,"data":"` + base64.StdEncoding.EncodeToString(audio) + `"}` + "\n"))
		_, _ = w.Write([]byte(`{"code":20000000}` + "\n"))
	}))
	defer upstream.Close()

	gin.SetMode(gin.TestMode)
	cfg := config.VoiceConfig{Enabled: true, APIKey: "test-key"}
	cfg.ApplyDefaults()
	cfg.TTS.Endpoint = upstream.URL
	cfg.TTS.Speaker = "speaker"
	h := NewVoiceHandler(&cfg)

	r := gin.New()
	r.POST("/tts", h.TTS)
	req := httptest.NewRequest(http.MethodPost, "/tts", strings.NewReader(`{"text":"你好"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("content-type = %q", w.Header().Get("Content-Type"))
	}
	if w.Body.String() != string(audio) {
		t.Fatalf("body = %q", w.Body.Bytes())
	}
}

func TestVoiceTTSHandlerDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.VoiceConfig{}
	cfg.ApplyDefaults()
	h := NewVoiceHandler(&cfg)
	r := gin.New()
	r.POST("/tts", h.TTS)
	req := httptest.NewRequest(http.MethodPost, "/tts", strings.NewReader(`{"text":"你好"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body json: %v", err)
	}
}
