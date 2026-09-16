// Package app 提供跨包共享的配置结构体与接口定义，作为依赖注入的中枢。
// 各包从 app 引用统一接口，消除 tool ↔ agent 之间的循环依赖和接口重复定义。
package app

import (
	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"

	"gorm.io/gorm"
)

// Infra 基础设施依赖，被几乎所有 Repo 和 Agent 组件共享。
type Infra struct {
	DB          *gorm.DB
	Embedder    *llm.Embedder
	VectorStore VectorStore
	// ImageStore — 图床（文档配图 + 聊天图片输入共用）。控制器负责读写，编排器负责懒补 file_id。
	ImageStore *service.ImageStore
}
