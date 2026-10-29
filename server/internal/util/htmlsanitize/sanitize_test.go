package htmlsanitize

import (
	"strings"
	"testing"
)

const sample = `<!DOCTYPE html><html><head><title>t</title></head><body>
<h1>对比</h1><p>内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容内容</p>
</body></html>`

func TestProcessInjectsCSPWithoutBaseline(t *testing.T) {
	out, err := Process(sample)
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
	out2, err := Process(model)
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
	out, err := Process(raw)
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
	if _, err := Process(`<html><body><p>` + strings.Repeat("字", 300)); err == nil {
		t.Fatal("缺少 </html> 应报错（静默截断是这里唯一的拦截点）")
	}
}

func TestProcessRejectsTiny(t *testing.T) {
	if _, err := Process(`<html><body>x</body></html>`); err == nil {
		t.Fatal("过短应报错")
	}
}

func TestProcessKeepsDataImage(t *testing.T) {
	raw := strings.Replace(sample, "<h1>", `<img src="data:image/png;base64,iVBORw0KGgo="><h1>`, 1)
	out, err := Process(raw)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !strings.Contains(out, "data:image/png;base64") {
		t.Fatal("data:image 应保留（自包含单文件的前提）")
	}
}
