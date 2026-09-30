package tool

import (
	"context"
	"log"
	"regexp"
)

// docImageRefRe 匹配文档正文里引用图床图片的 Markdown 片段：![](/images/<uuid>.<ext>)
// 只认图床路径（/images/ + UUID），外链图片不处理。
var docImageRefRe = regexp.MustCompile(`!\[[^\]]*\]\(/images/([0-9a-fA-F-]{36})\.(?:png|jpg|jpeg|gif|webp)\)`)

// promoteDocImages 扫描正文里引用的图床图片，把它们升级为可公开分享的文档配图
// （public + source=doc + referenced=true），使分享页/PDF 能显示。
// 非本人或不存在、以及不匹配图床路径的引用一律忽略；失败只记日志，不打断文档保存。
func (t *DocumentTool) promoteDocImages(ctx context.Context, userID, content string) {
	if t.imageRepo == nil {
		return
	}
	matches := docImageRefRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return
	}
	seen := make(map[string]bool, len(matches))
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if err := t.imageRepo.PromoteToDoc(ctx, ids, userID); err != nil {
		log.Printf("[document] promote images failed: user=%s count=%d err=%v", userID, len(ids), err)
	}
}
