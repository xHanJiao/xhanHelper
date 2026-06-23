package ctxflow

import (
	"bytes"
	"encoding/xml"
	"log"
	"time"

	"xhan-helper/internal/agent/retrieval"
)

// ContextFormatter 将检索结果格式化为 user 消息内容。
type ContextFormatter interface {
	// FormatUserMessage 将检索结果 + 额外块 + 用户查询拼装为最终的 user 消息。
	FormatUserMessage(result *RetrieveResult, userQuery string, extraBlocks ...ExtraBlock) string

	// FormatBlocks 只渲染检索块 + 额外块，不含 <user_query> 包裹。
	// 供 replay 等场景预构建检索前缀使用。
	FormatBlocks(result *RetrieveResult, extraBlocks ...ExtraBlock) string
}

// XMLFormatter 以 XML 标签格式输出检索上下文。
type XMLFormatter struct{}

// NewXMLFormatter 创建 XML 格式化器。
func NewXMLFormatter() *XMLFormatter {
	return &XMLFormatter{}
}

// FormatUserMessage = FormatBlocks + <user_query> 包裹。
func (f *XMLFormatter) FormatUserMessage(result *RetrieveResult, userQuery string, extraBlocks ...ExtraBlock) string {
	var buf bytes.Buffer
	buf.WriteString(f.FormatBlocks(result, extraBlocks...))
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	enc.Encode(&userQueryBlock{Content: userQuery})
	enc.Flush()
	return buf.String()
}

// FormatBlocks 收集 → 排序 → 编码。
// typed ExtraBlock（Type 在 typeOrder 中）和检索 Packets 统一走 buildBlock → 排序，
// 非 typed ExtraBlock 作为裸 XML 始终在最前面透传。
func (f *XMLFormatter) FormatBlocks(result *RetrieveResult, extraBlocks ...ExtraBlock) string {
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")

	// 1. 收集：typed ExtraBlock + 检索 Packets 统一转 Block
	var blocks []Block
	for _, blk := range extraBlocks {
		if _, ok := typeRank[blk.Type]; ok && blk.Content != "" {
			pkt := retrieval.ContextPacket{Type: blk.Type, Text: blk.Content}
			if b := buildBlock(blk.Type, []retrieval.ContextPacket{pkt}); b != nil {
				blocks = append(blocks, b)
			}
		}
	}
	if result != nil {
		for typ, packets := range result.Packets {
			if _, ok := typeRank[typ]; ok && len(packets) > 0 {
				if b := buildBlock(typ, packets); b != nil {
					blocks = append(blocks, b)
				}
			}
		}
	}

	// 2. 排序
	sortBlocks(blocks)

	// 3. 非 typed ExtraBlock → 裸 XML 透传，始终在最前面
	for _, blk := range extraBlocks {
		if _, ok := typeRank[blk.Type]; !ok && blk.Content != "" {
			buf.WriteString(blk.Content)
			buf.WriteByte('\n')
		}
	}

	// 4. 统一编码
	for _, blk := range blocks {
		if err := enc.Encode(blk); err != nil {
			log.Printf("[xml] encode %s: %v", blk.TypeKey(), err)
		}
	}

	enc.Flush()
	return buf.String()
}

// DateTimeBlock 生成 current_datetime 类型的 ExtraBlock。
// Content 为纯时间字符串，XML 标签由 currentDatetimeBlock 的注解决定。
func DateTimeBlock(t time.Time) ExtraBlock {
	return ExtraBlock{
		Type:    "current_datetime",
		Content: t.Format("2006年1月2日 星期一 15:04 MST"),
	}
}
