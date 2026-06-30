package ctxflow

import (
	"bytes"
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

// FormatUserMessage = FormatBlocks + user_query 包裹。
func (f *formatter) FormatUserMessage(result *RetrieveResult, userQuery string, extraBlocks ...ExtraBlock) string {
	var buf bytes.Buffer
	buf.WriteString(f.FormatBlocks(result, extraBlocks...))
	enc := f.newEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.Encode(&userQueryBlock{Content: userQuery})
	enc.Flush()
	return buf.String()
}

// FormatBlocks 收集 → 排序 → 编码。
// typed ExtraBlock（Type 在 typeRank 中）和检索 Packets 统一走 buildBlock → 排序，
// 非 typed ExtraBlock 作为裸 XML 始终在最前面透传。
func (f *formatter) FormatBlocks(result *RetrieveResult, extraBlocks ...ExtraBlock) string {
	var buf bytes.Buffer
	enc := f.newEncoder(&buf)
	enc.SetIndent("", "  ")

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
			log.Printf("[formatter] encode %s: %v", blk.TypeKey(), err)
		}
	}

	enc.Flush()
	// 确保编码块末尾换行，避免 FormatUserMessage 追加 user_query 时粘在同一行
	if len(blocks) > 0 {
		buf.WriteByte('\n')
	}
	return buf.String()
}

var weekdaysCN = [...]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}

// DateTimeBlock 生成 current_datetime 类型的 ExtraBlock。
// Content 为纯时间字符串，标签由 currentDatetimeBlock 的注解决定。
func DateTimeBlock(t time.Time) ExtraBlock {
	return ExtraBlock{
		Type:    "current_datetime",
		Content: t.Format("2006年1月2日 ") + weekdaysCN[t.Weekday()] + t.Format(" 15:04 MST"),
	}
}
