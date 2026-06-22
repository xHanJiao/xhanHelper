package ctxflow

import (
	"bytes"
	"encoding/xml"
	"log"
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

// FormatBlocks 只渲染检索块 + 额外块。
func (f *XMLFormatter) FormatBlocks(result *RetrieveResult, extraBlocks ...ExtraBlock) string {
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")

	for _, blk := range extraBlocks {
		if blk.Content != "" {
			buf.WriteString(blk.Content)
			buf.WriteByte('\n')
		}
	}

	if result != nil && len(result.Packets) > 0 {
		for _, typ := range typeOrder {
			packets, ok := result.Packets[typ]
			if !ok || len(packets) == 0 {
				continue
			}
			el := buildBlock(typ, packets)
			if el != nil {
				if err := enc.Encode(el); err != nil {
					log.Printf("[xml] encode %s: %v", typ, err)
				}
			}
		}
	}
	enc.Flush()

	return buf.String()
}
