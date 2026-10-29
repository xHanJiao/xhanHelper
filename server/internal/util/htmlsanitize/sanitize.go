// Package htmlsanitize — 入库前的 HTML 清洗与校验。
//
// 目标不是"消毒所有东西"：渲染侧还会关掉 JS（见 docs/design/html-artifact §9.2），
// 这里挡的是**没有 JS 也会发生**的事——iframe 框本机文件、外链发请求、meta 跳转。
// 两道闸都得在：清洗挡前者，`javaScriptEnabled = false` 挡清洗漏了的情况。
package htmlsanitize

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

const (
	// MaxBytes — 单条上限，挡误存巨型 HTML。
	MaxBytes = 2 << 20
	// MinBytes — 下限，挡空壳。
	MinBytes = 200
)

// dropTags — 必须删掉的标签。`script` 关 JS 后虽然惰性，留着也是脏数据；
// iframe/object/embed 在 file:// 或任意页面里都能把本机内容框进来，必须删。
var dropTags = map[string]bool{
	"script": true, "iframe": true, "object": true, "embed": true, "applet": true,
	"frame": true, "frameset": true, "link": true, "meta": true, "base": true,
	"form": true, "input": true, "button": true, "select": true, "textarea": true,
}

// urlAttrs — 这些属性的值一律只允许页内锚点或 data:image。
var urlAttrs = map[string]bool{
	"src": true, "href": true, "xlink:href": true, "poster": true,
	"action": true, "formaction": true, "srcset": true, "data": true, "background": true,
}

var cssURLRe = regexp.MustCompile(`(?i)url\(\s*(['"]?)([^'")]*)(['"]?)\s*\)`)

const (
	cspContent = "default-src 'none'; img-src data:; font-src data:; style-src 'unsafe-inline'"
)

// Process — 校验 + 清洗 + 注入 CSP。返回可直接入库的完整 HTML。
//
// 版式基线**不在这里注入**：基线是渲染期的表现层，写在库里就没法回溯
// （改一次版式要求把所有历史页面重新生成）。客户端加载时再插，见
// docs/design/html-artifact/design.md §9.3。
func Process(raw string) (string, error) {
	if len(raw) > MaxBytes {
		return "", fmt.Errorf("HTML 太大（%d 字节，上限 %d）", len(raw), MaxBytes)
	}
	if len(strings.TrimSpace(raw)) < MinBytes {
		return "", fmt.Errorf("HTML 太短（不足 %d 字节），不像一个完整页面", MinBytes)
	}
	// 截断的第一信号：没有收尾标签。项目没有 finish_reason==length 处理，
	// max_tokens 触顶是静默硬截断，半张页面绝不能入库。
	if !strings.Contains(strings.ToLower(raw), "</html>") {
		return "", errors.New("HTML 不完整：缺少 </html> 收尾（可能是输出被截断），请重新生成完整页面")
	}

	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("HTML 解析失败：%w", err)
	}
	cleanNode(doc)

	head := ensureHead(doc)
	// CSP 注入到 head 最前面（版本是服务端契约，和渲染期基线互不影响）。
	head.InsertBefore(textNode("\n"), head.FirstChild)
	head.InsertBefore(&html.Node{Type: html.ElementNode, Data: "meta", Attr: []html.Attribute{
		{Key: "http-equiv", Val: "Content-Security-Policy"},
		{Key: "content", Val: cspContent},
	}}, head.FirstChild)
	head.InsertBefore(textNode("\n"), head.FirstChild)

	var buf strings.Builder
	if err := html.Render(&buf, doc); err != nil {
		return "", fmt.Errorf("HTML 序列化失败：%w", err)
	}
	return buf.String(), nil
}

// cleanNode — 深度优先删除危险节点、清危险属性。style 元素的文本也走一遍 CSS 清洗。
func cleanNode(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode {
			tag := strings.ToLower(c.Data)
			if dropTags[tag] {
				n.RemoveChild(c)
				c = next
				continue
			}
			if tag == "style" {
				// 逐个文本子节点清洗（别只看 FirstChild：漏一个就等于没清）。
				for t := c.FirstChild; t != nil; t = t.NextSibling {
					if t.Type == html.TextNode || t.Type == html.CommentNode {
						t.Data = cleanCSS(t.Data)
					}
				}
			}
			cleanAttrs(c)
			cleanNode(c)
		}
		c = next
	}
}

func cleanAttrs(n *html.Node) {
	keep := n.Attr[:0]
	for _, a := range n.Attr {
		name := strings.ToLower(a.Key)
		switch {
		case strings.HasPrefix(name, "on"):
			continue
		case urlAttrs[name]:
			if !safeURL(a.Val) {
				continue
			}
		case name == "style":
			a.Val = cleanCSS(a.Val)
		}
		keep = append(keep, a)
	}
	n.Attr = keep
}

// safeURL — 只放行页内锚点与 data:image，其余（http/https/协议相对/javascript:…）一律丢弃。
func safeURL(v string) bool {
	s := strings.ToLower(strings.TrimSpace(v))
	if strings.HasPrefix(s, "#") {
		return true
	}
	return strings.HasPrefix(s, "data:image/")
}

// cleanCSS — 去掉 @import 与指向外部的 url()。
func cleanCSS(s string) string {
	s = regexp.MustCompile(`(?i)@import[^;]*;?`).ReplaceAllString(s, "")
	return cssURLRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := cssURLRe.FindStringSubmatch(m)
		if len(sub) < 3 {
			return "none"
		}
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(sub[2])), "data:") {
			return m
		}
		return "none"
	})
}

func ensureHead(doc *html.Node) *html.Node {
	var htmlNode *html.Node
	for c := doc.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && strings.EqualFold(c.Data, "html") {
			htmlNode = c
			break
		}
	}
	if htmlNode == nil {
		htmlNode = &html.Node{Type: html.ElementNode, Data: "html"}
		doc.AppendChild(htmlNode)
	}
	for c := htmlNode.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && strings.EqualFold(c.Data, "head") {
			return c
		}
	}
	head := &html.Node{Type: html.ElementNode, Data: "head"}
	htmlNode.InsertBefore(head, htmlNode.FirstChild)
	return head
}

func textNode(s string) *html.Node {
	return &html.Node{Type: html.TextNode, Data: s}
}
