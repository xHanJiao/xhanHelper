package agent

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
)

// candidateXML / neighborXML / neighborsXML — 决策循环输入块的 XML 结构。
// 一律经 xml.Marshal 输出，不用手拼 xmlEscape：xml.EscapeText 只处理 &<>'" 与 \t\r\n，
// 其他控制字节（0x0B、0x1F…）会漏出去拼出非良构 XML；xml.Marshal 能正确转义/拒绝。
type candidateXML struct {
	XMLName xml.Name `xml:"candidate"`
	Topic   string   `xml:"topic"`
	Summary string   `xml:"summary"`
	Facts   string   `xml:"facts,omitempty"` // JSON 数组文本；无则省略整个元素
}

type neighborXML struct {
	ID      string `xml:"id,attr"`
	Type    string `xml:"type,attr"`
	Topic   string `xml:"topic,attr"`
	Content string `xml:",chardata"`
}

type neighborsXML struct {
	XMLName xml.Name      `xml:"neighbors"`
	Records []neighborXML `xml:"record"`
}

// topicRecordsXML — 该候选 topic 下已有记录的输入块。
// count 是**该主题的总条数**（不是本块条数）：模型要能看见「这个主题已经有 32 条了」，
// 这是治「同一主题碎片化」的信号（见设计文档 §3.5）。块恒发出（空则 records 为空）。
type topicRecordsXML struct {
	XMLName xml.Name      `xml:"topic_records"`
	Count   int           `xml:"count,attr"`
	Records []neighborXML `xml:"record"`
}

// renderCandidateInput — 拼 `<candidate>` + `<neighbors>` + `<topic_records>` 输入块。
// 渲染失败（含非法 XML 字符）向上抛：prompt 是否良构影响本次决策契约，不能降级。
func renderCandidateInput(cand recordCandidate, neighbors []recordNeighbor, topicRecords []recordNeighbor, topicTotal int) (string, error) {
	cx := candidateXML{Topic: cand.Topic, Summary: cand.Summary}
	if len(cand.Facts) > 0 {
		b, err := json.Marshal(cand.Facts)
		if err != nil {
			return "", fmt.Errorf("marshal candidate facts: %w", err)
		}
		cx.Facts = string(b)
	}
	candOut, err := xml.MarshalIndent(cx, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal candidate xml: %w", err)
	}

	nx := neighborsXML{Records: toNeighborXML(neighbors)}
	nbOut, err := xml.MarshalIndent(nx, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal neighbors xml: %w", err)
	}

	tx := topicRecordsXML{Count: topicTotal, Records: toNeighborXML(topicRecords)}
	tpOut, err := xml.MarshalIndent(tx, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal topic records xml: %w", err)
	}
	return string(candOut) + "\n" + string(nbOut) + "\n" + string(tpOut), nil
}

func toNeighborXML(ns []recordNeighbor) []neighborXML {
	out := make([]neighborXML, 0, len(ns))
	for _, n := range ns {
		out = append(out, neighborXML{ID: n.ID, Type: n.Type, Topic: n.Topic, Content: n.Content})
	}
	return out
}
