package agent

import "errors"

// 预定义错误，调用方用 fmt.Errorf("...: %w", ErrXxx) 包裹后返回。
// chat.go 通过 UserErrorMessage(err) 将错误转为用户友好文案。
var (
	ErrPromptBuild      = errors.New("prompt build failed")
	ErrLLMApi           = errors.New("llm api error")
	ErrMaxIterations    = errors.New("max iterations reached")
	ErrInternal         = errors.New("internal error")
	ErrContextCancelled = errors.New("request cancelled")
	ErrPlanModeBlocked  = errors.New("plan mode blocks write operation")
)

// UserErrorMessage 将 sentinel error 映射为用户可读的中文提示。
// 匹配失败时返回兜底文案，不暴露内部错误细节。
func UserErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrPromptBuild):
		return "服务繁忙，请重试"
	case errors.Is(err, ErrLLMApi):
		return "AI 服务暂时不可用，请重试"
	case errors.Is(err, ErrMaxIterations):
		return "任务未完成，请简化后重试"
	case errors.Is(err, ErrContextCancelled):
		return "请求已取消"
	case errors.Is(err, ErrPlanModeBlocked):
		return "计划模式下禁止执行写操作"
	default:
		return "服务繁忙，请重试"
	}
}
