package ctxflow

import "xhan-helper/internal/agent/retrieval"

// RetrieveConfig — 检索源阈值/数量集中配置。
type RetrieveConfig struct {
	Memory  retrieval.SourceConfig
	Note    retrieval.SourceConfig
	History retrieval.SourceConfig
	Theme   retrieval.SourceConfig
	Summary retrieval.SourceConfig
	Event   retrieval.SourceConfig
}

// DefaultRetrieveConfig 返回生产环境默认配置。
func DefaultRetrieveConfig() RetrieveConfig {
	return RetrieveConfig{
		Memory:  retrieval.SourceConfig{Threshold: 0.5, Limit: 8},
		Note:    retrieval.SourceConfig{Threshold: 0.5, Limit: 5},
		History: retrieval.SourceConfig{Threshold: 0.8, Limit: 1},
		Theme:   retrieval.SourceConfig{Threshold: 0.6, Limit: 10},
		Summary: retrieval.SourceConfig{Threshold: 1.0, Limit: 0},
		Event:   retrieval.SourceConfig{Threshold: 0.5, Limit: 20},
	}
}
