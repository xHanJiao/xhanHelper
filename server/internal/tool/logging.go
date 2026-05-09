package tool

import (
	"encoding/json"
	"log"
	"time"
)

// LoggingWrapper — 工具日志装饰器，记录 Execute 的完整输入输出
type LoggingWrapper struct {
	inner  Tool
	logger *log.Logger
}

func NewLoggingWrapper(inner Tool, logger *log.Logger) *LoggingWrapper {
	return &LoggingWrapper{inner: inner, logger: logger}
}

func (w *LoggingWrapper) Name() string            { return w.inner.Name() }
func (w *LoggingWrapper) Description() string     { return w.inner.Description() }
func (w *LoggingWrapper) Functions() []FuncSchema { return w.inner.Functions() }

func (w *LoggingWrapper) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	start := time.Now()
	w.logger.Printf("[%s.%s] INPUT: %s", w.Name(), funcName, string(args))
	result := w.inner.Execute(ec, funcName, args)
	elapsed := float64(time.Since(start).Microseconds()) / 1000
	dataStr := string(result.Data)
	w.logger.Printf("[%s.%s] OUTPUT (%.2fms) success=%v data=%s error=%q",
		w.Name(), funcName, elapsed, result.Success, dataStr, result.Error)
	return result
}
