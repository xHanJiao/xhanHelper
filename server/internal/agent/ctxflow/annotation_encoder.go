package ctxflow

import (
	"encoding/json"
	"encoding/xml"
	"io"
)

// AnnotationEncoder — 注解驱动编码器，读取 struct tag 序列化 Block。
// 统一 xml.Encoder 和 json.Encoder 的方法签名差异。
type AnnotationEncoder interface {
	Encode(v any) error
	SetIndent(prefix, indent string)
	Flush() error
}

// xmlAdapter 将 xml.Encoder 适配为 AnnotationEncoder。
type xmlAdapter struct{ *xml.Encoder }

func (a *xmlAdapter) SetIndent(pre, ind string) { a.Encoder.Indent(pre, ind) }

// jsonAdapter 将 json.Encoder 适配为 AnnotationEncoder。
type jsonAdapter struct{ *json.Encoder }

func (a *jsonAdapter) SetIndent(pre, ind string) { a.Encoder.SetIndent(pre, ind) }
func (a *jsonAdapter) Flush() error              { return nil }

// EncoderFactory 创建 AnnotationEncoder，决定输出格式。
type EncoderFactory func(w io.Writer) AnnotationEncoder

// XMLEncoder 工厂：xml.NewEncoder。
func XMLEncoder(w io.Writer) AnnotationEncoder {
	return &xmlAdapter{Encoder: xml.NewEncoder(w)}
}

// JSONEncoder 工厂：json.NewEncoder。
func JSONEncoder(w io.Writer) AnnotationEncoder {
	return &jsonAdapter{Encoder: json.NewEncoder(w)}
}
