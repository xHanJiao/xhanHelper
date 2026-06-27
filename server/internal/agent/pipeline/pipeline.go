package pipeline

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"xhan-helper/internal/monitor"
)

// PreContext — 管线中流转的所有预处理状态
type PreContext struct {
	RawMsg             string  // 原始用户消息
	NormalizedMsg      string  // 日期标准化后的消息
	SkipRetrieval      bool    // 是否跳过记忆检索
	IsShort            bool    // 是否短输入
	UserID             string  // 用户 ID
	AgentID            string  // 匹配的智能体 ID
	AgentSystemPrompt  string  // 匹配的智能体 system prompt
	AgentName          string  // 匹配的智能体名称
	Confidence         float64 // 匹配置信度
	UserSpecifiedAgent bool    // 用户显式指定了 agent_id
	ForceDeepMode      bool    // 强制使用 deep mode（pro 模型+thinking）
}

// preCtxKey — ctx 中存储 PreContext 的私有 key
type preCtxKey struct{}

// WithPreContext 将 PreContext 注入 ctx
func WithPreContext(ctx context.Context, pc *PreContext) context.Context {
	return context.WithValue(ctx, preCtxKey{}, pc)
}

// GetPreContext 从 ctx 取出 PreContext，不存在时返回空的 PreContext（仅测试用）
func GetPreContext(ctx context.Context) *PreContext {
	pc, _ := ctx.Value(preCtxKey{}).(*PreContext)
	if pc == nil {
		return &PreContext{}
	}
	return pc
}

// Step — 管线中的一个预处理步骤
type Step interface {
	Name() string
	Requires() []string                               // 依赖的 ctx key 名（拓扑排序用）
	Produces() []string                               // 产出的 ctx key 名（拓扑排序用）
	ShouldRun(ctx context.Context) bool               // 基于当前 ctx 判断是否执行
	Run(ctx context.Context) (context.Context, error) // 通过 GetPreContext(ctx) 读写状态，返回新 ctx
}

// Pipeline — 声明式预处理管线
type Pipeline struct {
	steps []Step
}

// NewPipeline 创建管线并做拓扑排序验证
func NewPipeline(steps ...Step) (*Pipeline, error) {
	if err := validateDAG(steps); err != nil {
		return nil, fmt.Errorf("invalid pipeline: %w", err)
	}
	return &Pipeline{steps: steps}, nil
}

// Run 顺序执行所有 Step，ctx 链式传递
func (p *Pipeline) Run(ctx context.Context) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	stepNames := make([]string, 0, len(p.steps))
	startAll := time.Now()

	for _, s := range p.steps {
		stepStart := time.Now()
		log.Printf("[pipeline] Step %q START", s.Name())

		if !s.ShouldRun(ctx) {
			log.Printf("[pipeline] Step %q SKIP (%dms)", s.Name(), time.Since(stepStart).Milliseconds())
			stepNames = append(stepNames, s.Name()+"(skip)")
			continue
		}

		var err error
		ctx, err = s.Run(ctx)
		elapsed := time.Since(stepStart).Milliseconds()
		if err != nil {
			log.Printf("[pipeline] Step %q ERROR (%dms): %v", s.Name(), elapsed, err)
			return ctx, fmt.Errorf("step %q: %w", s.Name(), err)
		}

		log.Printf("[pipeline] Step %q DONE (%dms)", s.Name(), elapsed)
		monitor.RecordPipelineStep(s.Name(), elapsed)
		stepNames = append(stepNames, s.Name())
	}

	log.Printf("[pipeline] 链路: %s (%dms)", strings.Join(stepNames, " → "), time.Since(startAll).Milliseconds())
	return ctx, nil
}

// validateDAG 用 Kahn 算法做拓扑排序 + 循环依赖检测
func validateDAG(steps []Step) error {
	// 构建名称 → Step 映射
	byName := make(map[string]Step, len(steps))
	for _, s := range steps {
		name := s.Name()
		if _, exists := byName[name]; exists {
			return fmt.Errorf("duplicate step name: %s", name)
		}
		byName[name] = s
	}

	// 构建 produces → step 映射（用于解析依赖）
	producesBy := make(map[string]string) // key → step name
	for _, s := range steps {
		for _, p := range s.Produces() {
			if existing, ok := producesBy[p]; ok {
				return fmt.Errorf("key %q produced by both %q and %q", p, existing, s.Name())
			}
			producesBy[p] = s.Name()
		}
	}

	inDegree := make(map[string]int, len(steps))
	edges := make(map[string][]string) // from step → to step names

	for _, s := range steps {
		inDegree[s.Name()] = 0
	}

	for _, s := range steps {
		for _, req := range s.Requires() {
			producer, ok := producesBy[req]
			if !ok {
				continue // 外部注入的 key，不是内部依赖
			}
			if producer == s.Name() {
				return fmt.Errorf("step %q requires %q that it also produces (self-loop)", s.Name(), req)
			}
			edges[producer] = append(edges[producer], s.Name())
			inDegree[s.Name()]++
		}
	}

	// Kahn 算法
	queue := make([]string, 0)
	for _, s := range steps {
		if inDegree[s.Name()] == 0 {
			queue = append(queue, s.Name())
		}
	}

	sorted := make([]string, 0, len(steps))
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		sorted = append(sorted, n)

		for _, to := range edges[n] {
			inDegree[to]--
			if inDegree[to] == 0 {
				queue = append(queue, to)
			}
		}
	}

	if len(sorted) != len(steps) {
		remaining := make([]string, 0)
		for _, s := range steps {
			if inDegree[s.Name()] > 0 {
				remaining = append(remaining, s.Name())
			}
		}
		return fmt.Errorf("circular dependency detected among: %s", strings.Join(remaining, ", "))
	}

	return nil
}
