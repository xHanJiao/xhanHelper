package tool

import (
	"encoding/json"
	"math"
	"testing"
)

const epsilon = 1e-9

func floatEqual(a, b float64) bool {
	return math.Abs(a-b) < epsilon
}

func TestCalculatorEvaluate(t *testing.T) {
	calc := NewCalculatorTool()

	tests := []struct {
		expr    string
		want    float64
		wantErr bool
	}{
		// 基本运算
		{"2 + 3", 5, false},
		{"10 - 4", 6, false},
		{"3 * 7", 21, false},
		{"15 / 3", 5, false},
		{"2 + 3 * 4", 14, false},
		{"(2 + 3) * 4", 20, false},
		{"10 / 3", 10.0 / 3.0, false},
		{"-5 + 10", 5, false},
		{"3.14 * 2", 6.28, false},
		// 实际用户用例
		{"62+10+110", 182, false},
		{"50+82.5+18+11.5+20+54.3", 236.3, false},
		{"103.93+17.8+29+100+20", 270.73, false},
		{"25.8+26.4", 52.2, false},
		{"62+10+110+50+82.5+18+11.5+20+54.3", 418.3, false},
		// 数学函数
		{"sqrt(144)", 12, false},
		{"sqrt(2.25)", 1.5, false},
		{"pow(2, 10)", 1024, false},
		{"pow(3, 3)", 27, false},
		{"sin(0)", 0, false},
		{"cos(0)", 1, false},
		{"abs(-5)", 5, false},
		{"abs(5)", 5, false},
		{"round(3.7)", 4, false},
		{"round(3.2)", 3, false},
		{"ceil(3.1)", 4, false},
		{"floor(3.9)", 3, false},
		{"log2(8)", 3, false},
		{"log2(1024)", 10, false},
		{"log10(100)", 2, false},
		{"log10(1000)", 3, false},
		// 嵌套函数
		{"sqrt(pow(3, 2) + pow(4, 2))", 5, false},
		{"abs(round(-3.7))", 4, false},
		{"pow(2, round(sqrt(10)))", 8, false},
		// 混合表达式
		{"(abs(-5) + round(2.6)) * pow(2, 3)", 64, false},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			args, _ := json.Marshal(map[string]string{"expression": tt.expr})
			result := calc.Execute(nil, "evaluate", args)

			if tt.wantErr {
				if result.Success {
					t.Errorf("expected error, got success")
				}
				return
			}

			if !result.Success {
				t.Fatalf("unexpected error: %s", result.Error)
			}

			var data struct {
				Result float64 `json:"result"`
			}
			json.Unmarshal(result.Data, &data)
			if !floatEqual(data.Result, tt.want) {
				t.Errorf("got %.6f, want %.6f", data.Result, tt.want)
			}
		})
	}
}

func TestCalculatorEvaluate_Errors(t *testing.T) {
	calc := NewCalculatorTool()

	tests := []struct {
		name string
		expr string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"plain text", "abc + 123"},
		{"unclosed paren", "(1 + 2"},
		{"unopened paren", "1 + 2)"},
		{"double operator", "1 ++ 2"},
		{"trailing operator", "1 + 2 +"},
		{"leading operator", "+ 1 + 2"},
		{"double minus", "1 -- 2"},
		{"asterisk only", "*"},
		{"unknown function", "foo(5)"},
		{"sqrt no args", "sqrt()"},
		{"pow one arg", "pow(2)"},
		{"missing operand", "1 + * 2"},
		{"nested garbage", "abs(foo(5))"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, _ := json.Marshal(map[string]string{"expression": tt.expr})
			result := calc.Execute(nil, "evaluate", args)
			if result.Success {
				t.Errorf("expected error for %q, got success", tt.expr)
			}
		})
	}
}

func TestCalculatorEvaluateBatch(t *testing.T) {
	calc := NewCalculatorTool()

	t.Run("all success", func(t *testing.T) {
		args, _ := json.Marshal(map[string]interface{}{
			"expressions": []string{"62+10+110", "103.93+17.8", "sqrt(144)"},
		})
		result := calc.Execute(nil, "evaluate_batch", args)
		if !result.Success {
			t.Fatalf("unexpected error: %s", result.Error)
		}

		var resp struct {
			Items []struct {
				Result float64 `json:"result"`
				Error  string  `json:"error,omitempty"`
			} `json:"items"`
		}
		json.Unmarshal(result.Data, &resp)
		if len(resp.Items) != 3 {
			t.Fatalf("expected 3 items, got %d", len(resp.Items))
		}

		wants := []float64{182, 121.73, 12}
		for i, want := range wants {
			if !floatEqual(resp.Items[i].Result, want) {
				t.Errorf("item[%d]: got %.6f, want %.6f", i, resp.Items[i].Result, want)
			}
			if resp.Items[i].Error != "" {
				t.Errorf("item[%d] unexpected error: %s", i, resp.Items[i].Error)
			}
		}
	})

	t.Run("partial errors", func(t *testing.T) {
		args, _ := json.Marshal(map[string]interface{}{
			"expressions": []string{"1+1", "invalid", "2*3"},
		})
		result := calc.Execute(nil, "evaluate_batch", args)
		if !result.Success {
			t.Fatalf("expected batch success despite item errors")
		}

		var resp struct {
			Items []struct {
				Error string `json:"error,omitempty"`
			} `json:"items"`
		}
		json.Unmarshal(result.Data, &resp)

		if resp.Items[0].Error != "" {
			t.Errorf("item[0] should have no error, got: %s", resp.Items[0].Error)
		}
		if resp.Items[1].Error == "" {
			t.Error("item[1] should have error")
		}
		if resp.Items[2].Error != "" {
			t.Errorf("item[2] should have no error, got: %s", resp.Items[2].Error)
		}
	})

	t.Run("all error", func(t *testing.T) {
		args, _ := json.Marshal(map[string]interface{}{
			"expressions": []string{"bad1", "bad2", "bad3"},
		})
		result := calc.Execute(nil, "evaluate_batch", args)
		if !result.Success {
			t.Fatal("batch should succeed even when all items fail")
		}
		var resp struct {
			Items []struct {
				Error string `json:"error"`
			}
		}
		json.Unmarshal(result.Data, &resp)
		for i, item := range resp.Items {
			if item.Error == "" {
				t.Errorf("item[%d] should have error", i)
			}
		}
	})

	t.Run("empty array", func(t *testing.T) {
		args, _ := json.Marshal(map[string]interface{}{"expressions": []string{}})
		result := calc.Execute(nil, "evaluate_batch", args)
		if result.Success {
			t.Error("expected error for empty array")
		}
	})
}

func TestCalculatorFunctionsSchema(t *testing.T) {
	fns := NewCalculatorTool().Functions()
	if len(fns) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(fns))
	}
	if fns[0].Name != "evaluate" {
		t.Errorf("expected 'evaluate', got '%s'", fns[0].Name)
	}
	if fns[1].Name != "evaluate_batch" {
		t.Errorf("expected 'evaluate_batch', got '%s'", fns[1].Name)
	}
}

func TestCalculator_MathFunctionsRegistered(t *testing.T) {
	names := []string{"sqrt", "pow", "sin", "cos", "abs", "round", "ceil", "floor", "log2", "log10"}
	if len(calculatorFunctions) != len(names) {
		t.Errorf("expected %d functions, got %d", len(names), len(calculatorFunctions))
	}
	for _, name := range names {
		if _, ok := calculatorFunctions[name]; !ok {
			t.Errorf("function %q not registered", name)
		}
	}
}
