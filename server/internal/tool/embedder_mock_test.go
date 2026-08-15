package tool

import (
	"context"

	"xhan-helper/internal/app"
)

// mockEmbedder 测试用固定向量嵌入器（原定义于 memory_search_test.go，该文件已随工具下线删除）。
type mockEmbedder struct{}

func (m *mockEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	// 返回确定性向量：简单字符哈希 → 3 维
	v := make([]float64, 3)
	for i, c := range text {
		v[i%3] += float64(c)
	}
	return v, nil
}

func (m *mockEmbedder) ModelName() string { return "mock-embedder" }

var _ app.Embedder = (*mockEmbedder)(nil)
