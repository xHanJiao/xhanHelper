package htmlsanitize

import (
	"errors"
	"strings"
	"testing"
)

const sample = `<!DOCTYPE html><html><head><title>t</title></head><body>
<h1>对比</h1><p>内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容</p>
</body></html>`

func TestProcessInjectsCSPWithoutBaseline(t *testing.T) {
	out, err := ProcessWithImages(sample, nil)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !strings.Contains(out, "Content-Security-Policy") {
		t.Fatal("缺 CSP meta")
	}
	// 基线是渲染期的事（客户端注入），存进库就没法随版本更新了
	if strings.Contains(out, "-webkit-font-smoothing") || strings.Contains(out, "min(760px,100%)") {
		t.Fatal("版式基线不应写进存储的 HTML")
	}
	// 模型自己的样式必须原样保留（注入点不在这里，别顺手改了别人的样式）
	model := strings.Replace(sample, "</head>", "<style>h1{color:#c00}</style></head>", 1)
	out2, err := ProcessWithImages(model, nil)
	if err != nil {
		t.Fatalf("Process model style: %v", err)
	}
	if !strings.Contains(out2, "color:#c00") {
		t.Fatal("模型样式应保留")
	}
}

func TestProcessDropsDangerousNodes(t *testing.T) {
	raw := `<html><head></head><body>
<script>alert(1)</script>
<iframe src="file:///etc/passwd"></iframe>
<object data="x"></object>
<img src="https://evil.example/a.png" onerror="alert(1)">
<a href="javascript:alert(1)">x</a>
<a href="https://evil.example">y</a>
<a href="#ok">z</a>
<style>@import url(https://evil.example/a.css); .a{background:url(https://evil.example/b.png)}</style>
<p>正文正文正文正文正文正文正文正文正文正文正文正文正文正文正文正文正文</p>
</body></html>`
	out, err := ProcessWithImages(raw, nil)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	for _, bad := range []string{"<script", "<iframe", "<object", "onerror", "javascript:", "evil.example", "@import"} {
		if strings.Contains(out, bad) {
			t.Errorf("应被清掉但仍存在: %q", bad)
		}
	}
	if !strings.Contains(out, `href="#ok"`) {
		t.Error("页内锚点应保留")
	}
}

func TestProcessRejectsTruncated(t *testing.T) {
	if _, err := ProcessWithImages(`<html><body><p>`+strings.Repeat("字", 300), nil); err == nil {
		t.Fatal("缺少 </html> 应报错（静默截断是这里唯一的拦截点）")
	}
}

func TestProcessRejectsTiny(t *testing.T) {
	if _, err := ProcessWithImages(`<html><body>x</body></html>`, nil); err == nil {
		t.Fatal("过短应报错")
	}
}

func TestProcessKeepsDataImage(t *testing.T) {
	raw := strings.Replace(sample, "<h1>", `<img src="data:image/png;base64,iVBORw0KGgo="><h1>`, 1)
	out, err := ProcessWithImages(raw, nil)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !strings.Contains(out, "data:image/png;base64") {
		t.Fatal("data:image 应保留（自包含单文件的前提）")
	}
}

func TestProcessInlinesImageReferences(t *testing.T) {
	page := strings.Replace(sample, "</body>", `<img src="image:abc" alt="图"><img src="https://evil/x.png"></body>`, 1)
	out, err := ProcessWithImages(page, func(id string) (string, error) {
		if id != "abc" {
			return "", errors.New("unknown id")
		}
		return "data:image/png;base64,AAAA", nil
	})
	if err != nil {
		t.Fatalf("ProcessWithImages: %v", err)
	}
	if !strings.Contains(out, `src="data:image/png;base64,AAAA"`) {
		t.Fatal("image: 引用应被换成 data: URI")
	}
	if strings.Contains(out, "image:abc") {
		t.Fatal("不应残留 image: 引用")
	}
	if strings.Contains(out, "evil") {
		t.Fatal("外链图片仍应被剥掉")
	}
}

func TestProcessRejectsUnresolvableImage(t *testing.T) {
	page := strings.Replace(sample, "</body>", `<img src="image:gone"></body>`, 1)
	_, err := ProcessWithImages(page, func(id string) (string, error) {
		return "", errors.New("图片不存在或已被清理")
	})
	if err == nil {
		t.Fatal("解析失败应整页失败，不能静默少图")
	}
	if !strings.Contains(err.Error(), "gone") {
		t.Fatalf("错误应点名 id: %v", err)
	}
}

// CSS 背景图不支持：必须报错，不能让 cleanCSS 静默换成 none。
func TestProcessRejectsCSSBackgroundImage(t *testing.T) {
	resolve := func(id string) (string, error) { return "data:image/png;base64,AAAA", nil }

	styleBlock := strings.Replace(sample, "</head>", `<style>.hero{background-image:url(image:abc)}</style></head>`, 1)
	if _, err := ProcessWithImages(styleBlock, resolve); err == nil {
		t.Fatal("style 块里的背景图引用应报错")
	}

	inlineAttr := strings.Replace(sample, "</body>", `<div style="background-image:url('image:abc')"></div></body>`, 1)
	if _, err := ProcessWithImages(inlineAttr, resolve); err == nil {
		t.Fatal("style 属性里的背景图引用应报错")
	}
}

// 整页内联总量超过 MaxInlineBytes 必须拒，而不是存一条超大记录。
func TestProcessRejectsOversizedInlineTotal(t *testing.T) {
	big := "data:image/png;base64," + strings.Repeat("A", 5<<20)
	page := strings.Replace(sample, "</body>", `<img src="image:a"><img src="image:b"></body>`, 1)
	_, err := ProcessWithImages(page, func(id string) (string, error) { return big, nil })
	if err == nil {
		t.Fatal("两张大图叠起来超过整页上限应报错")
	}
	if !strings.Contains(err.Error(), "太大") {
		t.Fatalf("错误应说明体积超限: %v", err)
	}
}

func TestTooShortIsSentinel(t *testing.T) {
	_, err := ProcessWithImages(`<html><body></body></html>`, func(string) (string, error) { return "", nil })
	if !errors.Is(err, ErrTooShort) {
		t.Fatalf("短页面应能 errors.Is(ErrTooShort)，实际 %v", err)
	}
}
