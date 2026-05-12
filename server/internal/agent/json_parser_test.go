package agent

import (
	"encoding/json"
	"testing"
)

func TestParseFlashJSON(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		var v struct {
			Name string `json:"name"`
		}
		prefix, err := ParseFlashJSON(`{"name":"test"}`, &v)
		if err != nil {
			t.Fatal(err)
		}
		if prefix != "" {
			t.Errorf("prefix = %q, want empty", prefix)
		}
		if v.Name != "test" {
			t.Errorf("name = %q, want test", v.Name)
		}
	})

	t.Run("nested json", func(t *testing.T) {
		var v struct {
			Data struct {
				Deep string `json:"deep"`
			} `json:"data"`
		}
		_, err := ParseFlashJSON(`{"data":{"deep":"ok"}}`, &v)
		if err != nil {
			t.Fatal(err)
		}
		if v.Data.Deep != "ok" {
			t.Errorf("deep = %q, want ok", v.Data.Deep)
		}
	})

	t.Run("brace inside string value", func(t *testing.T) {
		var v struct {
			Code string `json:"code"`
		}
		raw := `{"code":"function() { return 1; }"}`
		prefix, err := ParseFlashJSON(raw, &v)
		if err != nil {
			t.Fatal(err)
		}
		if prefix != "" {
			t.Errorf("prefix = %q, want empty", prefix)
		}
		if v.Code != "function() { return 1; }" {
			t.Errorf("code = %q, want function() { return 1; }", v.Code)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		var v map[string]interface{}
		prefix, err := ParseFlashJSON("", &v)
		if err != nil {
			t.Fatal(err)
		}
		if prefix != "" {
			t.Errorf("prefix = %q, want empty", prefix)
		}
	})

	t.Run("no json at all", func(t *testing.T) {
		var v map[string]interface{}
		prefix, err := ParseFlashJSON("just plain text", &v)
		if err != nil {
			t.Fatal(err)
		}
		if prefix != "" {
			t.Errorf("prefix = %q, want empty", prefix)
		}
	})

	t.Run("unclosed brace", func(t *testing.T) {
		var v map[string]interface{}
		_, err := ParseFlashJSON(`{"a":1`, &v)
		if err == nil {
			t.Fatal("expected error for unclosed brace")
		}
		_, ok := err.(*json.SyntaxError)
		if !ok {
			t.Logf("error type: %T, message: %v", err, err)
		}
	})

	t.Run("prefix text before json", func(t *testing.T) {
		var v struct {
			Reply string `json:"reply"`
		}
		prefix, err := ParseFlashJSON("ok let me do this\n{\"reply\":\"hello\"}", &v)
		if err != nil {
			t.Fatal(err)
		}
		if prefix != "ok let me do this" {
			t.Errorf("prefix = %q, want 'ok let me do this'", prefix)
		}
		if v.Reply != "hello" {
			t.Errorf("reply = %q, want hello", v.Reply)
		}
	})

	t.Run("text after json", func(t *testing.T) {
		var v struct {
			Reply string `json:"reply"`
		}
		_, err := ParseFlashJSON(`{"reply":"ok"} and more text here`, &v)
		if err != nil {
			t.Fatal(err)
		}
		if v.Reply != "ok" {
			t.Errorf("reply = %q, want ok", v.Reply)
		}
	})

	t.Run("markdown code block", func(t *testing.T) {
		var v struct {
			Reply string `json:"reply"`
		}
		_, err := ParseFlashJSON("```json\n{\"reply\":\"hi\"}\n```", &v)
		if err != nil {
			t.Fatal(err)
		}
		if v.Reply != "hi" {
			t.Errorf("reply = %q, want hi", v.Reply)
		}
	})
}
