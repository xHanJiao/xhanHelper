package ctxflow

import (
	"bytes"
	"log"

	"xhan-helper/internal/agent/retrieval"
)

// ContextFormatter 将检索结果格式化为 user 消息内容。
type ContextFormatter interface {
	FormatUserMessage(packets map[string][]retrieval.ContextPacket, userQuery string) string
}

// formatter — 编码器可插拔的格式化实现。
type formatter struct {
	newEncoder EncoderFactory
}

// NewXMLFormatter 创建 XML 格式化器。
func NewXMLFormatter() ContextFormatter {
	return &formatter{newEncoder: XMLEncoder}
}

// NewJSONFormatter 创建 JSON 格式化器。
func NewJSONFormatter() ContextFormatter {
	return &formatter{newEncoder: JSONEncoder}
}

// FormatUserMessage = FormatPackets + user_query 包裹。
func (f *formatter) FormatUserMessage(packets map[string][]retrieval.ContextPacket, userQuery string) string {
	var buf bytes.Buffer
	buf.WriteString(f.formatPackets(packets))
	enc := f.newEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.Encode(&userQueryBlock{Content: userQuery})
	enc.Flush()
	return buf.String()
}

// formatPackets 收集 → 排序 → 编码。
func (f *formatter) formatPackets(packets map[string][]retrieval.ContextPacket) string {
	var buf bytes.Buffer
	enc := f.newEncoder(&buf)
	enc.SetIndent("", "  ")

	// 1. 收集检索 Packets 统一转 Block
	var blocks []Block
	for typ, pkts := range packets {
		if _, ok := typeRank[typ]; ok && len(pkts) > 0 {
			if b := buildBlock(typ, pkts); b != nil {
				blocks = append(blocks, b)
			}
		}
	}

	// 2. 排序
	sortBlocks(blocks)

	// 3. 统一编码
	for _, blk := range blocks {
		if err := enc.Encode(blk); err != nil {
			log.Printf("[formatter] encode %s: %v", blk.TypeKey(), err)
		}
	}

	enc.Flush()
	if len(blocks) > 0 {
		buf.WriteByte('\n')
	}
	return buf.String()
}
