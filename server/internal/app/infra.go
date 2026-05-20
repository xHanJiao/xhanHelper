// Package app 提供跨包共享的配置结构体与接口定义，作为依赖注入的中枢。
// 各包从 app 引用统一接口，消除 tool ↔ agent 之间的循环依赖和接口重复定义。
package app

import (
	"xhan-helper/internal/llm"

	"gorm.io/gorm"
)

// Infra 基础设施依赖，被几乎所有 Repo 和 Agent 组件共享。
type Infra struct {
	DB          *gorm.DB
	Embedder    *llm.Embedder
	VectorStore VectorStore
}
