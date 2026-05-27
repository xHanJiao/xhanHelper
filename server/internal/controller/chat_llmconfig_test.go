package controller

import (
	"testing"

	"xhan-helper/internal/model"
)

func ptrF(v float64) *float64 { return &v }
func ptrI(v int) *int         { return &v }
func ptrB(v bool) *bool       { return &v }

func TestMergeAgentAndUserParams(t *testing.T) {
	tests := []struct {
		name           string
		agentTemp      *float64
		agentMaxTokens *int
		agentThinking  *bool
		userTemp       *float64
		userMaxTokens  *int
		userThinking   *bool
		wantTemp       float64 // 0 means nil
		wantMaxTokens  int     // 0 means nil
		wantThinking   bool
		nilThinkingOK  bool // 都未设置时 thinking 允许为 nil
	}{
		{
			name:           "用户参数覆盖 agent 参数",
			agentTemp:      ptrF(0.5),
			agentMaxTokens: ptrI(1000),
			agentThinking:  ptrB(false),
			userTemp:       ptrF(0.9),
			userMaxTokens:  ptrI(2048),
			userThinking:   ptrB(true),
			wantTemp:       0.9,
			wantMaxTokens:  2048,
			wantThinking:   true,
		},
		{
			name:           "agent 参数作为默认值",
			agentTemp:      ptrF(0.3),
			agentMaxTokens: ptrI(500),
			agentThinking:  ptrB(false),
			userTemp:       nil,
			userMaxTokens:  nil,
			userThinking:   nil,
			wantTemp:       0.3,
			wantMaxTokens:  500,
			wantThinking:   false,
		},
		{
			name:           "无 agent 时使用用户参数",
			agentTemp:      nil,
			agentMaxTokens: nil,
			agentThinking:  nil,
			userTemp:       ptrF(0.7),
			userMaxTokens:  ptrI(3000),
			userThinking:   ptrB(true),
			wantTemp:       0.7,
			wantMaxTokens:  3000,
			wantThinking:   true,
		},
		{
			name:           "都没有时保持 nil",
			agentTemp:      nil,
			agentMaxTokens: nil,
			agentThinking:  nil,
			userTemp:       nil,
			userMaxTokens:  nil,
			userThinking:   nil,
			wantTemp:       0,
			wantMaxTokens:  0,
			wantThinking:   false,
			nilThinkingOK:  true, // 都没设置时 thinking 为 nil，由调用方提供默认值
		},
		{
			name:           "部分覆盖：用户只覆盖 temperature",
			agentTemp:      ptrF(0.5),
			agentMaxTokens: ptrI(1000),
			agentThinking:  ptrB(true),
			userTemp:       ptrF(0.9),
			userMaxTokens:  nil,
			userThinking:   nil,
			wantTemp:       0.9,
			wantMaxTokens:  1000,
			wantThinking:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTemp, gotMaxTokens, gotThinking := mergeAgentAndUserParams(
				tt.agentTemp, tt.agentMaxTokens, tt.agentThinking,
				tt.userTemp, tt.userMaxTokens, tt.userThinking,
			)

			if tt.wantTemp == 0 {
				if gotTemp != nil {
					t.Errorf("temperature = %v, want nil", *gotTemp)
				}
			} else {
				if gotTemp == nil || *gotTemp != tt.wantTemp {
					t.Errorf("temperature = %v, want %v", gotTemp, tt.wantTemp)
				}
			}

			if tt.wantMaxTokens == 0 {
				if gotMaxTokens != nil {
					t.Errorf("maxTokens = %v, want nil", *gotMaxTokens)
				}
			} else {
				if gotMaxTokens == nil || *gotMaxTokens != tt.wantMaxTokens {
					t.Errorf("maxTokens = %v, want %v", gotMaxTokens, tt.wantMaxTokens)
				}
			}

			if gotThinking == nil {
				if !tt.nilThinkingOK {
					t.Errorf("thinking = nil, want %v", tt.wantThinking)
				}
			} else if *gotThinking != tt.wantThinking {
				t.Errorf("thinking = %v, want %v", *gotThinking, tt.wantThinking)
			}
		})
	}
}

func TestBuildLLMConfigResponse(t *testing.T) {
	// 无 agent 时默认值
	resp := buildLLMConfigResponse(nil, nil, nil)
	if resp["deep_mode"] != false {
		t.Errorf("default deep_mode = %v, want false", resp["deep_mode"])
	}
	if resp["temperature"] != 1.0 {
		t.Errorf("default temperature = %v, want 1.0", resp["temperature"])
	}
	if resp["max_tokens"] != 4096 {
		t.Errorf("default max_tokens = %v, want 4096", resp["max_tokens"])
	}
	if resp["thinking"] != true {
		t.Errorf("default thinking = %v, want true", resp["thinking"])
	}

	// agent 有 deep_mode: true
	agent := &model.Agent{ParamsJSON: `{"temperature":0.3,"max_tokens":500,"thinking":false,"deep_mode":true}`}
	resp = buildLLMConfigResponse(agent, nil, nil)
	if resp["deep_mode"] != true {
		t.Errorf("agent deep_mode = %v, want true", resp["deep_mode"])
	}
	if resp["temperature"] != 0.3 {
		t.Errorf("agent temperature = %v, want 0.3", resp["temperature"])
	}
	if resp["max_tokens"] != 500 {
		t.Errorf("agent max_tokens = %v, want 500", resp["max_tokens"])
	}
	if resp["thinking"] != false {
		t.Errorf("agent thinking = %v, want false", resp["thinking"])
	}

	// agent 无 deep_mode 时保持默认 false
	agent = &model.Agent{ParamsJSON: `{"temperature":0.8}`}
	resp = buildLLMConfigResponse(agent, nil, nil)
	if resp["deep_mode"] != false {
		t.Errorf("agent without deep_mode = %v, want false", resp["deep_mode"])
	}
	if resp["temperature"] != 0.8 {
		t.Errorf("agent temperature = %v, want 0.8", resp["temperature"])
	}

	// 配置默认值覆盖
	customTemp := 0.7
	customTokens := 8000
	resp = buildLLMConfigResponse(nil, &customTemp, &customTokens)
	if resp["temperature"] != 0.7 {
		t.Errorf("custom temperature = %v, want 0.7", resp["temperature"])
	}
	if resp["max_tokens"] != 8000 {
		t.Errorf("custom max_tokens = %v, want 8000", resp["max_tokens"])
	}

	// agent 参数优先级高于配置默认值
	agent = &model.Agent{ParamsJSON: `{"max_tokens":2000}`}
	resp = buildLLMConfigResponse(agent, &customTemp, &customTokens)
	if resp["max_tokens"] != 2000 {
		t.Errorf("agent max_tokens over config = %v, want 2000", resp["max_tokens"])
	}
	if resp["temperature"] != 0.7 {
		t.Errorf("config temperature fallback = %v, want 0.7", resp["temperature"])
	}
}

func TestParseAgentParams(t *testing.T) {
	// 完整参数
	paramsJSON := `{"temperature":0.5,"max_tokens":3000,"thinking":true,"deep_mode":true}`
	temp, tokens, thinking, deepMode, err := parseAgentParams(paramsJSON)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if temp == nil || *temp != 0.5 {
		t.Errorf("temp = %v, want 0.5", temp)
	}
	if tokens == nil || *tokens != 3000 {
		t.Errorf("tokens = %v, want 3000", tokens)
	}
	if thinking == nil || *thinking != true {
		t.Errorf("thinking = %v, want true", thinking)
	}
	if deepMode == nil || *deepMode != true {
		t.Errorf("deepMode = %v, want true", deepMode)
	}

	// 仅 deep_mode
	paramsJSON = `{"deep_mode":false}`
	temp, tokens, thinking, deepMode, err = parseAgentParams(paramsJSON)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if temp != nil {
		t.Errorf("temp = %v, want nil", temp)
	}
	if tokens != nil {
		t.Errorf("tokens = %v, want nil", tokens)
	}
	if thinking != nil {
		t.Errorf("thinking = %v, want nil", thinking)
	}
	if deepMode == nil || *deepMode != false {
		t.Errorf("deepMode = %v, want false", deepMode)
	}

	// 空字符串
	_, _, _, _, err = parseAgentParams("")
	if err == nil {
		t.Error("expected error for empty string")
	}

	// 无效 JSON
	_, _, _, _, err = parseAgentParams("{invalid}")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseAgentParamsOnlyDeepMode(t *testing.T) {
	paramsJSON := `{"deep_mode":true}`
	temp, tokens, thinking, deepMode, err := parseAgentParams(paramsJSON)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if temp != nil || tokens != nil || thinking != nil {
		t.Error("other params should be nil when not present")
	}
	if deepMode == nil || *deepMode != true {
		t.Errorf("deepMode = %v, want true", deepMode)
	}
}
