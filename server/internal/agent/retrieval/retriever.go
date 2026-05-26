package retrieval

import (
	"context"
	"log"
	"strings"
)

// Retriever — 上下文检索器，遍历多个 ContextSource 汇总结果
type Retriever struct {
	sources []ContextSource
}

// NewRetriever 创建检索器
func NewRetriever(sources ...ContextSource) *Retriever {
	return &Retriever{sources: sources}
}

// Retrieve 遍历所有数据源，汇总 ContextPacket，返回记忆 ID 列表
func (r *Retriever) Retrieve(ctx context.Context, queryVec []float64,
	query, userID, convID string, threshold float64, limit int) ([]ContextPacket, []string, []string) {

	if limit <= 0 {
		limit = 8
	}
	if threshold <= 0 {
		threshold = 0.5
	}

	var allPackets []ContextPacket
	var memIDs, noteIDs []string

	for _, src := range r.sources {
		packets, err := src.Fetch(ctx, queryVec, query, userID, convID, threshold, limit)
		if err != nil {
			log.Printf("[retriever] %s fetch error: %v", src.Name(), err)
			continue
		}
		if len(packets) == 0 {
			continue
		}
		if src.Name() == "memory" {
			for _, p := range packets {
				memIDs = append(memIDs, p.ID)
			}
		}
		if src.Name() == "note" {
			for _, p := range packets {
				noteIDs = append(noteIDs, p.ID)
			}
		}
		allPackets = append(allPackets, packets...)
	}
	return allPackets, memIDs, noteIDs
}

// FormatContext 将 ContextPacket 列表格式化为 prompt 注入文本
func (r *Retriever) FormatContext(packets []ContextPacket) string {
	if len(packets) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, p := range packets {
		sb.WriteString(p.Text)
	}
	return sb.String()
}
