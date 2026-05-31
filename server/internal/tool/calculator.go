package tool

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/Knetic/govaluate"
)

// CalculatorTool — 安全表达式求值（基于 govaluate）
type CalculatorTool struct{}

func NewCalculatorTool() *CalculatorTool { return &CalculatorTool{} }

func (t *CalculatorTool) Name() string { return "calculator" }

func (t *CalculatorTool) Description() string {
	return "安全的数学表达式计算器，支持加减乘除、括号、以及 sqrt/pow/sin/cos/abs/round/ceil/floor/log2/log10 函数"
}

// calculatorFunctions 注册的数学函数
func arg1(args []interface{}, fn func(float64) float64) (interface{}, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("expected 1 argument, got 0")
	}
	v, ok := args[0].(float64)
	if !ok {
		return nil, fmt.Errorf("argument must be a number")
	}
	return fn(v), nil
}

func arg2(args []interface{}, fn func(float64, float64) float64) (interface{}, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("expected 2 arguments, got %d", len(args))
	}
	a, ok := args[0].(float64)
	if !ok {
		return nil, fmt.Errorf("first argument must be a number")
	}
	b, ok := args[1].(float64)
	if !ok {
		return nil, fmt.Errorf("second argument must be a number")
	}
	return fn(a, b), nil
}

var calculatorFunctions = map[string]govaluate.ExpressionFunction{
	"sqrt":  func(args ...interface{}) (interface{}, error) { return arg1(args, math.Sqrt) },
	"sin":   func(args ...interface{}) (interface{}, error) { return arg1(args, math.Sin) },
	"cos":   func(args ...interface{}) (interface{}, error) { return arg1(args, math.Cos) },
	"abs":   func(args ...interface{}) (interface{}, error) { return arg1(args, math.Abs) },
	"round": func(args ...interface{}) (interface{}, error) { return arg1(args, math.Round) },
	"ceil":  func(args ...interface{}) (interface{}, error) { return arg1(args, math.Ceil) },
	"floor": func(args ...interface{}) (interface{}, error) { return arg1(args, math.Floor) },
	"log2":  func(args ...interface{}) (interface{}, error) { return arg1(args, math.Log2) },
	"log10": func(args ...interface{}) (interface{}, error) { return arg1(args, math.Log10) },
	"pow":   func(args ...interface{}) (interface{}, error) { return arg2(args, math.Pow) },
}

func (t *CalculatorTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name: "evaluate",
			Description: "计算数学表达式。支持四则运算、括号，以及数学函数：" +
				"sqrt(开方)、" +
				"pow(a,b)乘方、" +
				"sin/cos(三角函数)、" +
				"abs(绝对值)、" +
				"round(四舍五入)、" +
				"ceil(向上取整)、" +
				"floor(向下取整)、" +
				"log2/log10(对数)。" +
				"例: '(2+3)*4'、'sqrt(144)'、'pow(2,10)'、'round(3.7)'",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"expression": map[string]interface{}{
						"type":        "string",
						"description": "数学表达式，支持 + - * / () 和 sqrt/pow/sin/cos/abs/round/ceil/floor/log2/log10",
					},
				},
				"required": []interface{}{"expression"},
			},
		},
		{
			Name:        "evaluate_batch",
			Description: "批量计算多个数学表达式。同样支持四则运算和 sqrt/pow/sin/cos/abs/round/ceil/floor/log2/log10。传入 expressions 字符串数组",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"expressions": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "数学表达式数组",
					},
				},
				"required": []interface{}{"expressions"},
			},
		},
	}
}

func (t *CalculatorTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "evaluate_batch":
		return t.evaluateBatch(args)
	default:
		return t.evaluate(args)
	}
}

func (t *CalculatorTool) evaluate(args json.RawMessage) *Result {
	var req struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(args, &req); err != nil || req.Expression == "" {
		return &Result{Success: false, Error: "expression is required"}
	}

	val, err := evalExpr(req.Expression)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"expression": req.Expression,
		"result":     val,
	})
	return &Result{Success: true, Data: data}
}

func (t *CalculatorTool) evaluateBatch(args json.RawMessage) *Result {
	var req struct {
		Expressions []string `json:"expressions"`
	}
	if err := json.Unmarshal(args, &req); err != nil || len(req.Expressions) == 0 {
		return &Result{Success: false, Error: "expressions array is required and must not be empty"}
	}

	type itemResult struct {
		Expression string  `json:"expression"`
		Result     float64 `json:"result"`
		Error      string  `json:"error,omitempty"`
	}
	results := make([]itemResult, len(req.Expressions))
	for i, expr := range req.Expressions {
		results[i] = itemResult{Expression: expr}
		val, err := evalExpr(expr)
		if err != nil {
			results[i].Error = err.Error()
		} else {
			results[i].Result = val
		}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"items": results,
	})
	return &Result{Success: true, Data: data}
}

func evalExpr(s string) (val float64, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("计算错误: %v", r)
		}
	}()

	expr, err := govaluate.NewEvaluableExpressionWithFunctions(s, calculatorFunctions)
	if err != nil {
		return 0, err
	}
	result, err := expr.Evaluate(map[string]interface{}{})
	if err != nil {
		return 0, err
	}
	v, ok := result.(float64)
	if !ok {
		return 0, fmt.Errorf("unexpected result type")
	}
	return v, nil
}
