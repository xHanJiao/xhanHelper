package tool

import (
	"encoding/json"
	"log"
	"time"

	"xhan-helper/internal/monitor"
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
func (w *LoggingWrapper) ReadOnlyFuncs() map[string]bool {
	return w.inner.ReadOnlyFuncs()
}

// Catalog 透传 CatalogProvider（若 inner 实现），保证被 wrapper 包裹的工具目录信息可用。
func (w *LoggingWrapper) Catalog() (CatalogEntry, bool) {
	if p, ok := w.inner.(CatalogProvider); ok {
		return p.Catalog()
	}
	return CatalogEntry{}, false
}

func (w *LoggingWrapper) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	start := time.Now()
	w.logger.Printf("[%s.%s] INPUT: %s", w.Name(), funcName, string(args))
	result := w.inner.Execute(ec, funcName, args)
	elapsed := float64(time.Since(start).Microseconds()) / 1000
	monitor.IncToolCallWithLatency(w.Name(), funcName, !result.Success, int64(elapsed))
	dataStr := string(result.Data)
	w.logger.Printf("[%s.%s] OUTPUT (%.2fms) success=%v data=%s error=%q",
		w.Name(), funcName, elapsed, result.Success, dataStr, result.Error)
	return result
}
