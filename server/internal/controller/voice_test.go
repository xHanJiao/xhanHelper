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

func TestVoiceTTSHandlerSpeaker(t *testing.T) {
	var gotSpeaker string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ReqParams struct {
				Speaker string `json:"speaker"`
			} `json:"req_params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotSpeaker = req.ReqParams.Speaker
		_, _ = w.Write([]byte(`{"code":0,"data":"YXVkaW8="}` + "\n"))
		_, _ = w.Write([]byte(`{"code":20000000}` + "\n"))
	}))
	defer upstream.Close()

	gin.SetMode(gin.TestMode)
	cfg := config.VoiceConfig{Enabled: true, APIKey: "test-key"}
	cfg.ApplyDefaults()
	cfg.TTS.Endpoint = upstream.URL
	cfg.TTS.Speaker = "default-speaker"
	h := NewVoiceHandler(&cfg)
	r := gin.New()
	r.POST("/tts", h.TTS)

	cases := []struct {
		name     string
		body     string
		expected string
	}{
		{"explicit curated", `{"text":"你好","speaker":"zh_male_m191_uranus_bigtts"}`, "zh_male_m191_uranus_bigtts"},
		{"unknown falls back", `{"text":"你好","speaker":"no-such-voice"}`, "default-speaker"},
		{"empty uses default", `{"text":"你好"}`, "default-speaker"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotSpeaker = ""
			req := httptest.NewRequest(http.MethodPost, "/tts", strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
			}
			if gotSpeaker != c.expected {
				t.Fatalf("upstream speaker = %q, want %q", gotSpeaker, c.expected)
			}
		})
	}
}

func TestVoiceStatusIncludesSpeakers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.VoiceConfig{}
	cfg.ApplyDefaults()
	h := NewVoiceHandler(&cfg)
	r := gin.New()
	r.GET("/config", h.Status)

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body struct {
		Data struct {
			Speaker  string `json:"speaker"`
			Speakers []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"speakers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body json: %v", err)
	}
	if len(body.Data.Speakers) < 2 {
		t.Fatalf("speakers = %+v", body.Data.Speakers)
	}
	found := false
	for _, s := range body.Data.Speakers {
		if s.ID == body.Data.Speaker {
			found = true
		}
	}
	if !found {
		t.Fatalf("default speaker %q missing from list", body.Data.Speaker)
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
