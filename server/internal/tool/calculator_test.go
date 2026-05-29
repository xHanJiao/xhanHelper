package tool

import (
	"encoding/json"
	"testing"
)

func TestCalculatorEvaluate(t *testing.T) {
	tool := NewCalculatorTool()

	tests := []struct {
		expr    string
		want    float64
		wantErr bool
	}{
		{"2 + 3", 5, false},
		{"10 - 4", 6, false},
		{"3 * 7", 21, false},
		{"15 / 3", 5, false},
		{"2 + 3 * 4", 14, false},
		{"(2 + 3) * 4", 20, false},
		{"10 / 3", 10.0 / 3.0, false},
		{"-5 + 10", 5, false},
		{"3.14 * 2", 6.28, false},
		{"1 / 0", 0, true},
		{"", 0, true},
		{"abc", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			args, _ := json.Marshal(map[string]string{"expression": tt.expr})
			result := tool.Execute(nil, "evaluate", args)

			if tt.wantErr {
				if result.Success {
					t.Errorf("expected error, got success")
				}
				return
			}

			if !result.Success {
				t.Errorf("unexpected error: %s", result.Error)
				return
			}

			var data map[string]interface{}
			json.Unmarshal(result.Data, &data)
			got, ok := data["result"].(float64)
			if !ok {
				t.Errorf("result not a float64: %v", data["result"])
				return
			}
			if got != tt.want {
				t.Errorf("%q = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}
