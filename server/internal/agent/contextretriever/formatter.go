package contextretriever

import (
	"bytes"
	"encoding/xml"
	"log"
)

// ContextFormatter 将检索结果格式化为 user 消息内容。
type ContextFormatter interface {
	// FormatUserMessage 将检索结果 + 额外块 + 用户查询拼装为最终的 user 消息。
	// extraBlocks 为可选的附加内容块（如 conversation_summary），
	// formatter 根据 ExtraBlock.Type 决定放置策略。
	// result 为 nil 或 Packets 为空时，只输出 "<user_query>原始文本</user_query>"。
	FormatUserMessage(result *RetrieveResult, userQuery string, extraBlocks ...ExtraBlock) string
}

// XMLFormatter 以 XML 标签格式输出检索上下文。
type XMLFormatter struct{}

// NewXMLFormatter 创建 XML 格式化器。
func NewXMLFormatter() *XMLFormatter {
	return &XMLFormatter{}
}

// FormatUserMessage 用 xml.Encoder 构建完整的 user 消息。
func (f *XMLFormatter) FormatUserMessage(result *RetrieveResult, userQuery string, extraBlocks ...ExtraBlock) string {
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")

	// 额外块（如 conversation_summary）：直接写入已格式化的 XML
	for _, blk := range extraBlocks {
		if blk.Content != "" {
			buf.WriteString(blk.Content)
			buf.WriteByte('\n')
		}
	}

	// 检索结果：按固定顺序，每个 type 编码为 XML 元素
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

	// <user_query> 包裹用户原始文本，文本内容做 XML 转义
	buf.WriteString("<user_query>")
	xml.EscapeText(&buf, []byte(userQuery))
	buf.WriteString("</user_query>")

	return buf.String()
}
