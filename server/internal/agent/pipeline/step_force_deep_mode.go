package pipeline

import "context"

// ForceDeepModeStep 强制启用 deep mode（pro 模型 + thinking），绕过请求参数。
// 可作为管线第一步，确保所有对话都使用 pro 模型。
type ForceDeepModeStep struct{}

func NewForceDeepModeStep() *ForceDeepModeStep { return &ForceDeepModeStep{} }

func (s *ForceDeepModeStep) Name() string                       { return "forceDeepMode" }
func (s *ForceDeepModeStep) Requires() []string                 { return nil }
func (s *ForceDeepModeStep) Produces() []string                 { return []string{"force_deep_mode"} }
func (s *ForceDeepModeStep) ShouldRun(ctx context.Context) bool { return true }

func (s *ForceDeepModeStep) Run(ctx context.Context) (context.Context, error) {
	pc := GetPreContext(ctx)
	pc.ForceDeepMode = true
	return ctx, nil
}
