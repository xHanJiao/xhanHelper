package tool

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// CalculatorTool — 安全表达式求值
type CalculatorTool struct{}

func NewCalculatorTool() *CalculatorTool { return &CalculatorTool{} }
func (t *CalculatorTool) Name() string   { return "calculator" }
func (t *CalculatorTool) Description() string {
	return "安全的数学表达式计算器，支持加减乘除和括号"
}

func (t *CalculatorTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "evaluate",
			Description: "计算数学表达式，如 '2 + 3 * 4' 或 '(10 - 5) / 2'",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"expression": map[string]interface{}{
						"type":        "string",
						"description": "数学表达式",
					},
				},
				"required": []interface{}{"expression"},
			},
		},
	}
}

func (t *CalculatorTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	var req struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(args, &req); err != nil || req.Expression == "" {
		return &Result{Success: false, Error: "expression is required"}
	}

	result, err := evalExpr(req.Expression)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"expression": req.Expression,
		"result":     result,
	})
	return &Result{Success: true, Data: data}
}

// --- 简单表达式求值器 ---

type parser struct {
	s   string
	pos int
}

func evalExpr(s string) (float64, error) {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	if s == "" {
		return 0, fmt.Errorf("empty expression")
	}
	p := &parser{s: s}
	val, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	if p.pos < len(p.s) {
		return 0, fmt.Errorf("unexpected character at position %d", p.pos)
	}
	return val, nil
}

func (p *parser) parseExpr() (float64, error) {
	left, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for p.pos < len(p.s) {
		op := p.s[p.pos]
		if op != '+' && op != '-' {
			break
		}
		p.pos++
		right, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			left += right
		} else {
			left -= right
		}
	}
	return left, nil
}

func (p *parser) parseTerm() (float64, error) {
	left, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for p.pos < len(p.s) {
		op := p.s[p.pos]
		if op != '*' && op != '/' {
			break
		}
		p.pos++
		right, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		if op == '*' {
			left *= right
		} else {
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left /= right
		}
	}
	return left, nil
}

func (p *parser) parseFactor() (float64, error) {
	if p.pos >= len(p.s) {
		return 0, fmt.Errorf("unexpected end")
	}

	if p.s[p.pos] == '(' {
		p.pos++
		val, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		if p.pos >= len(p.s) || p.s[p.pos] != ')' {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return val, nil
	}

	if p.s[p.pos] == '-' && p.pos+1 < len(p.s) && isDigitOrDot(p.s[p.pos+1]) {
		p.pos++
		val, err := p.parseNumber()
		if err != nil {
			return 0, err
		}
		return -val, nil
	}

	return p.parseNumber()
}

func (p *parser) parseNumber() (float64, error) {
	start := p.pos
	for p.pos < len(p.s) && (isDigitOrDot(p.s[p.pos])) {
		p.pos++
	}
	if start == p.pos {
		return 0, fmt.Errorf("expected number at position %d", start)
	}
	return strconv.ParseFloat(p.s[start:p.pos], 64)
}

func isDigitOrDot(c byte) bool {
	return (c >= '0' && c <= '9') || c == '.'
}
