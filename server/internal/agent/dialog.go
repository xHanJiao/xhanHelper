package agent

import (
	"encoding/xml"
	"fmt"
	"strings"

	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
)

// turnXML / dialogXML — 对话块的 XML 结构（记忆抽取 / 事实抽取共用，经 xml.Marshal 输出）。
type turnXML struct {
	XMLName xml.Name `xml:"turn"`
	Role    string   `xml:"role,attr"`
	At      string   `xml:"at,attr"`
	Content string   `xml:",chardata"`
}

type dialogXML struct {
	XMLName xml.Name  `xml:"对话"`
	Turns   []turnXML `xml:"turn"`
}

// renderDialog 把消息渲染成 <对话> XML 块。role 取实际值（user / assistant）——
// 助手的话必须明确标注为 assistant，不能被当成用户的话。
// 每个 turn 带发生时间（北京时间）：用户消息里的相对日期由管线补成「原词（日期）」，
// assistant 消息不经管线，靠这个属性提供时间锚点。
// Content 为空的 turn 跳过（只发 tool call 的 assistant 消息 content 为空）。
// 无内容返回 ""（调用方据此不追加对话块）。渲染失败向上抛。
func renderDialog(msgs []model.Message) (string, error) {
	doc := dialogXML{Turns: make([]turnXML, 0, len(msgs))}
	for _, m := range msgs {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		doc.Turns = append(doc.Turns, turnXML{
			Role:    m.Role,
			At:      m.CreatedAt.In(config.BJ).Format("2006-01-02 15:04"),
			Content: content,
		})
	}
	if len(doc.Turns) == 0 {
		return "", nil
	}
	b, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal dialog xml: %w", err)
	}
	return string(b), nil
}

// postChatDialogSystemPrompt — 输入是「用户 + 助手」对话的后处理链路专用 system prompt。
// 不复用 postChatSharedSystemPrompt：那条写死了「输入均为用户角色的发言」，
// 与喂 user + assistant 的对话矛盾，且改共享常量会动其他后处理链路的缓存前缀。
const postChatDialogSystemPrompt = `你是一个对话后处理助手。输入是一段用户与助手的对话记录，按时间顺序排列，每个 turn 带发生时间。`
