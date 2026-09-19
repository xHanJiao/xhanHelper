package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupDocumentTool(t *testing.T) (*DocumentTool, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Document{}, &model.ContentVersion{}, &model.DocumentAnnotation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// :memory: 每个连接一个独立库，限制单连接让并发测试共用同一内存库
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	docRepo := service.NewDocumentRepo(db)
	annoRepo := service.NewDocumentAnnotationRepo(db)
	return NewDocumentTool(docRepo, annoRepo), func() {}
}

func TestDocumentGetAndUpdate(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ctx := context.Background()
	userID := "user-1"

	// create
	createArgs := mustMarshal(t, map[string]interface{}{
		"title": "Title", "content": "Hello World", "tags": "go",
	})
	ec := &ExecuteContext{Ctx: ctx, UserID: userID}
	res := tool.Execute(ec, "create_document", createArgs)
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	var created map[string]interface{}
	json.Unmarshal(res.Data, &created)
	docID := created["id"].(string)

	// get_document: 应该返回完整内容
	res = tool.Execute(ec, "get_document", mustMarshal(t, map[string]string{"id": docID}))
	if !res.Success {
		t.Fatalf("get failed: %s", res.Error)
	}
	var got map[string]interface{}
	json.Unmarshal(res.Data, &got)
	if got["content"] != "Hello World" {
		t.Errorf("expected content 'Hello World', got %v", got["content"])
	}

	// update
	res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
		"id": docID, "content": "Updated Content",
	}))
	if !res.Success {
		t.Fatalf("update failed: %s", res.Error)
	}

	// 更新后内容生效
	res = tool.Execute(ec, "get_document", mustMarshal(t, map[string]string{"id": docID}))
	if !res.Success {
		t.Fatalf("get after update failed: %s", res.Error)
	}
	json.Unmarshal(res.Data, &got)
	if got["content"] != "Updated Content" {
		t.Errorf("expected updated content, got %v", got["content"])
	}
}

func TestDocumentUpdateReplace(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ctx := context.Background()
	ec := &ExecuteContext{Ctx: ctx, UserID: "user-1"}

	res := tool.Execute(ec, "create_document", mustMarshal(t, map[string]interface{}{
		"title": "T", "content": "first line\nsecond line\nthird line", "tags": "",
	}))
	var created map[string]interface{}
	json.Unmarshal(res.Data, &created)
	docID := created["id"].(string)

	t.Run("replace success", func(t *testing.T) {
		res := tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
			"id": docID, "old_text": "second line", "new_text": "SECOND",
		}))
		if !res.Success {
			t.Fatalf("replace failed: %s", res.Error)
		}
		res = tool.Execute(ec, "get_document", mustMarshal(t, map[string]string{"id": docID}))
		var got map[string]interface{}
		json.Unmarshal(res.Data, &got)
		want := "first line\nSECOND\nthird line"
		if got["content"] != want {
			t.Errorf("expected %q, got %q", want, got["content"])
		}
	})

	t.Run("replace to empty deletes fragment", func(t *testing.T) {
		res := tool.Execute(ec, "update_document", mustMarshal(t, map[string]interface{}{
			"id": docID, "old_text": "\nthird line", "new_text": "",
		}))
		if !res.Success {
			t.Fatalf("delete-fragment failed: %s", res.Error)
		}
		res = tool.Execute(ec, "get_document", mustMarshal(t, map[string]string{"id": docID}))
		var got map[string]interface{}
		json.Unmarshal(res.Data, &got)
		want := "first line\nSECOND"
		if got["content"] != want {
			t.Errorf("expected %q, got %q", want, got["content"])
		}
	})

	t.Run("old_text not found", func(t *testing.T) {
		res := tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
			"id": docID, "old_text": "no such text", "new_text": "x",
		}))
		if res.Success {
			t.Error("expected failure for missing old_text")
		}
	})

	t.Run("old_text ambiguous", func(t *testing.T) {
		res := tool.Execute(ec, "create_document", mustMarshal(t, map[string]interface{}{
			"title": "Dup", "content": "dup text here and dup there",
		}))
		var created map[string]interface{}
		json.Unmarshal(res.Data, &created)
		res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
			"id": created["id"].(string), "old_text": "dup", "new_text": "x",
		}))
		if res.Success {
			t.Error("expected failure for multi-match old_text")
		}
	})

	t.Run("replace multiple with count", func(t *testing.T) {
		res := tool.Execute(ec, "create_document", mustMarshal(t, map[string]interface{}{
			"title": "Cnt", "content": "a-b-a-b-a",
		}))
		var created map[string]interface{}
		json.Unmarshal(res.Data, &created)
		id := created["id"].(string)
		res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]interface{}{
			"id": id, "old_text": "a", "new_text": "X", "count": 3,
		}))
		if !res.Success {
			t.Fatalf("count replace failed: %s", res.Error)
		}
		res = tool.Execute(ec, "get_document", mustMarshal(t, map[string]string{"id": id}))
		var got map[string]interface{}
		json.Unmarshal(res.Data, &got)
		if got["content"] != "X-b-X-b-X" {
			t.Errorf("expected 'X-b-X-b-X', got %q", got["content"])
		}
	})

	t.Run("count exceeds matches", func(t *testing.T) {
		res := tool.Execute(ec, "update_document", mustMarshal(t, map[string]interface{}{
			"id": docID, "old_text": "SECOND", "new_text": "x", "count": 5,
		}))
		if res.Success {
			t.Error("expected failure when count exceeds matches")
		}
	})

	t.Run("count zero or negative", func(t *testing.T) {
		for _, c := range []int{0, -1} {
			res := tool.Execute(ec, "update_document", mustMarshal(t, map[string]interface{}{
				"id": docID, "old_text": "SECOND", "new_text": "x", "count": c,
			}))
			if res.Success {
				t.Errorf("expected failure for count=%d", c)
			}
		}
	})

	t.Run("old_text conflicts with content", func(t *testing.T) {
		res := tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
			"id": docID, "old_text": "a", "new_text": "b", "content": "c",
		}))
		if res.Success {
			t.Error("expected failure for old_text + content")
		}
	})

	t.Run("old_text without new_text", func(t *testing.T) {
		res := tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
			"id": docID, "old_text": "a",
		}))
		if res.Success {
			t.Error("expected failure for missing new_text")
		}
	})
}

// TestDocumentRemovedFuncs 版本/回滚能力已下掉（实测 0 调用），调用应返回 unknown function
func TestDocumentRemovedFuncs(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	for _, fn := range []string{"get_document_versions", "rollback_document"} {
		res := tool.Execute(ec, fn, mustMarshal(t, map[string]interface{}{"id": "x", "version": 1}))
		if res.Success {
			t.Errorf("%s should be removed, got success", fn)
		}
		if !strings.Contains(res.Error, "unknown function") {
			t.Errorf("%s: expected unknown function error, got %q", fn, res.Error)
		}
	}
}

func TestDocumentNotFound(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "get_document", mustMarshal(t, map[string]string{"id": "nonexistent"}))
	if res.Success {
		t.Error("expected failure for nonexistent document")
	}

	res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{"id": "nonexistent", "content": "x"}))
	if res.Success {
		t.Error("expected failure for nonexistent document")
	}
}

func TestDocumentCreateValidation(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	// 缺 title
	res := tool.Execute(ec, "create_document", mustMarshal(t, map[string]string{"content": "c"}))
	if res.Success {
		t.Error("expected failure without title")
	}

	// 缺 content
	res = tool.Execute(ec, "create_document", mustMarshal(t, map[string]string{"title": "t"}))
	if res.Success {
		t.Error("expected failure without content")
	}
}

// createDocForTest 建一篇文档并返回 id
func createDocForTest(t *testing.T, tool *DocumentTool, ec *ExecuteContext, title, content string) string {
	t.Helper()
	res := tool.Execute(ec, "create_document", mustMarshal(t, map[string]interface{}{
		"title": title, "content": content,
	}))
	if !res.Success {
		t.Fatalf("create document failed: %s", res.Error)
	}
	var created map[string]interface{}
	json.Unmarshal(res.Data, &created)
	return created["id"].(string)
}

// getDocForTest 调用 get_document 并解析成 map
func getDocForTest(t *testing.T, tool *DocumentTool, ec *ExecuteContext, args map[string]interface{}) map[string]interface{} {
	t.Helper()
	res := tool.Execute(ec, "get_document", mustMarshal(t, args))
	if !res.Success {
		t.Fatalf("get_document failed: %s", res.Error)
	}
	var got map[string]interface{}
	json.Unmarshal(res.Data, &got)
	return got
}

func TestDocumentGetWindowSmallDocUnchanged(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	content := "短文档内容"
	id := createDocForTest(t, tool, ec, "小文档", content)
	got := getDocForTest(t, tool, ec, map[string]interface{}{"id": id})

	if got["content"] != content {
		t.Errorf("small doc should be returned in full, got %q", got["content"])
	}
	if got["truncated"] != false {
		t.Errorf("expected truncated=false, got %v", got["truncated"])
	}
	if _, ok := got["next_char_offset"]; ok {
		t.Error("next_char_offset should be absent when not truncated")
	}
	if _, ok := got["outline"]; ok {
		t.Error("outline should be absent when not truncated")
	}
	if got["total_chars"].(float64) != float64(len([]rune(content))) {
		t.Errorf("expected total_chars=%d, got %v", len([]rune(content)), got["total_chars"])
	}
}

func TestDocumentGetWindowBoundary(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	t.Run("exactly window size is not truncated", func(t *testing.T) {
		content := strings.Repeat("字", defaultDocWindowLen)
		id := createDocForTest(t, tool, ec, "恰好", content)
		got := getDocForTest(t, tool, ec, map[string]interface{}{"id": id})
		if got["truncated"] != false {
			t.Errorf("exactly %d chars should not be truncated", defaultDocWindowLen)
		}
		if got["content"] != content {
			t.Error("content mismatch at exact boundary")
		}
	})

	t.Run("window+1 is truncated by 1", func(t *testing.T) {
		content := strings.Repeat("字", defaultDocWindowLen+1)
		id := createDocForTest(t, tool, ec, "超一", content)
		got := getDocForTest(t, tool, ec, map[string]interface{}{"id": id})
		if got["truncated"] != true {
			t.Fatal("expected truncated=true")
		}
		if n := got["next_char_offset"].(float64); n != float64(defaultDocWindowLen) {
			t.Errorf("expected next_char_offset=%d, got %v", defaultDocWindowLen, n)
		}
		if len([]rune(got["content"].(string))) != defaultDocWindowLen {
			t.Errorf("expected window of %d chars", defaultDocWindowLen)
		}
	})
}

func TestDocumentGetWindowContinue(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	// 构造 2.5 个窗口，含多字节与 emoji，验证续读拼接能还原全文且不劈字
	content := strings.Repeat("文档内容abc😀", 1500)
	id := createDocForTest(t, tool, ec, "长文档", content)

	var sb strings.Builder
	offset := 0
	for i := 0; i < 10; i++ {
		got := getDocForTest(t, tool, ec, map[string]interface{}{"id": id, "char_offset": offset})
		chunk := got["content"].(string)
		if !utf8.ValidString(chunk) {
			t.Fatal("window content is not valid UTF-8")
		}
		sb.WriteString(chunk)
		if got["truncated"] != true {
			break
		}
		next := int(got["next_char_offset"].(float64))
		if next <= offset {
			t.Fatalf("next_char_offset did not advance: %d -> %d", offset, next)
		}
		offset = next
	}

	if sb.String() != content {
		t.Errorf("concatenated windows do not reconstruct the document (got %d runes, want %d)",
			len([]rune(sb.String())), len([]rune(content)))
	}
}

func TestDocumentGetWindowClamp(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	content := strings.Repeat("x", maxDocWindowLen+1000)
	id := createDocForTest(t, tool, ec, "clamp", content)

	t.Run("char_limit over max is clamped", func(t *testing.T) {
		got := getDocForTest(t, tool, ec, map[string]interface{}{"id": id, "char_limit": maxDocWindowLen + 99999})
		if got["char_limit"].(float64) != float64(maxDocWindowLen) {
			t.Errorf("expected char_limit=%d, got %v", maxDocWindowLen, got["char_limit"])
		}
	})

	t.Run("non-positive char_limit falls back to default", func(t *testing.T) {
		for _, l := range []int{0, -5} {
			got := getDocForTest(t, tool, ec, map[string]interface{}{"id": id, "char_limit": l})
			if got["char_limit"].(float64) != float64(defaultDocWindowLen) {
				t.Errorf("char_limit=%d: expected default %d, got %v", l, defaultDocWindowLen, got["char_limit"])
			}
		}
	})

	t.Run("negative offset clamps to zero", func(t *testing.T) {
		got := getDocForTest(t, tool, ec, map[string]interface{}{"id": id, "char_offset": -10})
		if got["char_offset"].(float64) != 0 {
			t.Errorf("expected char_offset=0, got %v", got["char_offset"])
		}
		if !strings.HasPrefix(got["content"].(string), "x") {
			t.Error("expected content from the beginning")
		}
	})
}

func TestDocumentGetWindowOffsetOverflow(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	id := createDocForTest(t, tool, ec, "overflow", "短内容")
	got := getDocForTest(t, tool, ec, map[string]interface{}{"id": id, "char_offset": 9999})

	if got["content"] != "" {
		t.Errorf("expected empty content past the end, got %q", got["content"])
	}
	if got["truncated"] != false {
		t.Errorf("past-the-end read should not be truncated, got %v", got["truncated"])
	}
}

func TestDocumentOutline(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	t.Run("headings with rune offsets", func(t *testing.T) {
		content := "# 一级标题\n正文\n## 二级标题\n更多正文"
		// 补足到超窗口，触发 outline
		content += "\n" + strings.Repeat("填充", defaultDocWindowLen)
		id := createDocForTest(t, tool, ec, "outline", content)
		got := getDocForTest(t, tool, ec, map[string]interface{}{"id": id})

		outline, _ := got["outline"].(string)
		if !strings.Contains(outline, "0: # 一级标题") {
			t.Errorf("outline missing first heading with offset 0: %q", outline)
		}
		// "# 一级标题\n正文\n" = 6+1+2+1 = 10 runes 后是 "## 二级标题"
		if !strings.Contains(outline, "10: ## 二级标题") {
			t.Errorf("outline missing second heading with offset 10: %q", outline)
		}
	})

	t.Run("capped entries and truncated titles", func(t *testing.T) {
		longTitle := strings.Repeat("标", maxDocOutlineTitleLen+20)
		var sb strings.Builder
		for i := 0; i < maxDocOutlineEntries+5; i++ {
			sb.WriteString("## ")
			sb.WriteString(longTitle)
			sb.WriteString("\n")
		}
		sb.WriteString(strings.Repeat("填充", defaultDocWindowLen))
		id := createDocForTest(t, tool, ec, "many", sb.String())
		got := getDocForTest(t, tool, ec, map[string]interface{}{"id": id})

		outline, _ := got["outline"].(string)
		lines := strings.Split(outline, "\n")
		if len(lines) != maxDocOutlineEntries {
			t.Errorf("expected %d outline entries, got %d", maxDocOutlineEntries, len(lines))
		}
		for _, line := range lines {
			idx := strings.Index(line, ": ")
			if idx < 0 {
				t.Errorf("outline line missing offset separator: %q", line)
				continue
			}
			title := strings.TrimLeft(line[idx+2:], "# ")
			if len([]rune(title)) > maxDocOutlineTitleLen+3 { // +3 为截断省略号
				t.Errorf("outline title not truncated: %q", title)
			}
		}
	})
}

func TestDocumentTitleTooLong(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	longTitle := strings.Repeat("题", maxDocumentTitleLen+1)

	res := tool.Execute(ec, "create_document", mustMarshal(t, map[string]string{
		"title": longTitle, "content": "c",
	}))
	if res.Success || !strings.Contains(res.Error, "标题超长") {
		t.Fatalf("expected create to reject long title, got success=%v err=%q", res.Success, res.Error)
	}

	// 边界：恰好 50 字通过
	okTitle := strings.Repeat("题", maxDocumentTitleLen)
	res = tool.Execute(ec, "create_document", mustMarshal(t, map[string]string{
		"title": okTitle, "content": "c",
	}))
	if !res.Success {
		t.Fatalf("at-limit title should pass, got: %s", res.Error)
	}
	var created map[string]interface{}
	json.Unmarshal(res.Data, &created)
	id := created["id"].(string)

	// update 传超长 title 被拒
	res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
		"id": id, "title": longTitle,
	}))
	if res.Success || !strings.Contains(res.Error, "标题超长") {
		t.Fatalf("expected update to reject long title, got success=%v err=%q", res.Success, res.Error)
	}

	// update 不传 title 时不受影响
	res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
		"id": id, "content": "new content",
	}))
	if !res.Success {
		t.Fatalf("update without title should pass, got: %s", res.Error)
	}
}

func TestDocumentQueryPaging(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	for i := 0; i < 5; i++ {
		createDocForTest(t, tool, ec, "page-"+string(rune('a'+i)), "共享关键词内容")
	}

	res := tool.Execute(ec, "query_documents", mustMarshal(t, map[string]interface{}{
		"keyword": "共享关键词", "limit": 2, "offset": 0,
	}))
	var page struct {
		Documents []map[string]interface{} `json:"documents"`
		Total     int                      `json:"total"`
	}
	json.Unmarshal(res.Data, &page)
	if page.Total != 5 {
		t.Errorf("expected total 5, got %d", page.Total)
	}
	if len(page.Documents) != 2 {
		t.Errorf("expected 2 docs on first page, got %d", len(page.Documents))
	}

	res = tool.Execute(ec, "query_documents", mustMarshal(t, map[string]interface{}{
		"keyword": "共享关键词", "limit": 2, "offset": 2,
	}))
	var page2 struct {
		Documents []map[string]interface{} `json:"documents"`
	}
	json.Unmarshal(res.Data, &page2)
	if len(page2.Documents) != 2 {
		t.Fatalf("expected 2 docs on second page, got %d", len(page2.Documents))
	}
	if page.Documents[0]["id"] == page2.Documents[0]["id"] {
		t.Error("second page should not repeat the first page's documents")
	}
}

func TestDocumentListAndQuery(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	// 创建 3 篇文档
	for i, item := range []struct{ title, content, tags string }{
		{"项目管理笔记", "记录了一些项目管理的方法论和实践经验", "项目管理,笔记"},
		{"Go 后端设计", "后端架构设计文档，包括 API 规范", "技术,Go,后端"},
		{"周报 2026-05", "本周完成了默认会话和历史检索两个大功能", "周报"},
	} {
		res := tool.Execute(ec, "create_document", mustMarshal(t, map[string]interface{}{
			"title": item.title, "content": item.content, "tags": item.tags,
		}))
		if !res.Success {
			t.Fatalf("create doc %d failed: %s", i, res.Error)
		}
	}

	t.Run("list_documents_default_no_preview", func(t *testing.T) {
		res := tool.Execute(ec, "list_documents", mustMarshal(t, map[string]interface{}{}))
		if !res.Success {
			t.Fatalf("list_documents failed: %s", res.Error)
		}
		var resp struct {
			Documents []map[string]interface{} `json:"documents"`
			Total     int                      `json:"total"`
			Hint      string                   `json:"hint"`
		}
		json.Unmarshal(res.Data, &resp)
		if resp.Total != 3 {
			t.Errorf("expected total 3, got %d", resp.Total)
		}
		if len(resp.Documents) != 3 {
			t.Fatalf("expected 3 documents, got %d", len(resp.Documents))
		}
		for _, d := range resp.Documents {
			if _, ok := d["preview"]; ok {
				t.Errorf("默认不应返回 preview，got %v", d)
			}
		}
		if resp.Hint == "" {
			t.Error("默认应附带 preview 提醒 hint")
		}
	})

	t.Run("list_documents_fields_projection", func(t *testing.T) {
		res := tool.Execute(ec, "list_documents", mustMarshal(t, map[string]interface{}{
			"fields": "id,title,tags",
		}))
		if !res.Success {
			t.Fatalf("list_documents fields failed: %s", res.Error)
		}
		var resp struct {
			Documents []map[string]interface{} `json:"documents"`
			Hint      string                   `json:"hint"`
		}
		json.Unmarshal(res.Data, &resp)
		if len(resp.Documents) != 3 {
			t.Fatalf("expected 3 documents, got %d", len(resp.Documents))
		}
		for _, d := range resp.Documents {
			if len(d) != 3 {
				t.Errorf("fields=id,title,tags 应只回 3 个键，got %v", d)
			}
			for _, k := range []string{"id", "title", "tags"} {
				if _, ok := d[k]; !ok {
					t.Errorf("缺少字段 %s: %v", k, d)
				}
			}
		}
		if resp.Hint != "" {
			t.Errorf("显式传 fields 时不应带 hint，got %q", resp.Hint)
		}
	})

	t.Run("list_documents_with_preview", func(t *testing.T) {
		res := tool.Execute(ec, "list_documents", mustMarshal(t, map[string]interface{}{
			"fields": "id,title,tags,preview",
		}))
		var resp struct {
			Documents []map[string]interface{} `json:"documents"`
		}
		json.Unmarshal(res.Data, &resp)
		if len(resp.Documents) == 0 {
			t.Fatal("expected documents")
		}
		// preview 应截断在 50 字以内
		preview, _ := resp.Documents[0]["preview"].(string)
		if preview == "" {
			t.Fatalf("显式请求 preview 应返回，got %v", resp.Documents[0])
		}
		if len([]rune(preview)) > docPreviewLen {
			t.Errorf("preview too long: %d chars", len([]rune(preview)))
		}
	})

	t.Run("unknown_field_errors", func(t *testing.T) {
		res := tool.Execute(ec, "list_documents", mustMarshal(t, map[string]interface{}{
			"fields": "id,body",
		}))
		if res.Success {
			t.Error("未知字段应报错")
		}
	})

	t.Run("list_documents_pagination", func(t *testing.T) {
		res := tool.Execute(ec, "list_documents", mustMarshal(t, map[string]interface{}{
			"limit": 2, "offset": 0,
		}))
		var resp struct {
			Documents []struct{} `json:"documents"`
			Total     float64    `json:"total"`
		}
		json.Unmarshal(res.Data, &resp)
		if len(resp.Documents) != 2 {
			t.Errorf("expected 2 docs, got %d", len(resp.Documents))
		}
		if int(resp.Total) != 3 {
			t.Errorf("total should still be 3, got %v", resp.Total)
		}
	})

	t.Run("query_documents_by_tag", func(t *testing.T) {
		res := tool.Execute(ec, "query_documents", mustMarshal(t, map[string]string{"tag": "项目管理"}))
		var resp struct {
			Documents []map[string]interface{} `json:"documents"`
			Total     int                      `json:"total"`
		}
		json.Unmarshal(res.Data, &resp)
		if resp.Total != 1 {
			t.Errorf("expected 1 doc with tag '项目管理', got %d", resp.Total)
		}
	})

	t.Run("query_documents_by_keyword", func(t *testing.T) {
		res := tool.Execute(ec, "query_documents", mustMarshal(t, map[string]string{"keyword": "后端"}))
		var resp struct {
			Documents []map[string]interface{} `json:"documents"`
			Total     int                      `json:"total"`
		}
		json.Unmarshal(res.Data, &resp)
		if resp.Total != 1 {
			t.Errorf("expected 1 doc with keyword '后端', got %d", resp.Total)
		}
	})

	t.Run("query_no_match", func(t *testing.T) {
		res := tool.Execute(ec, "query_documents", mustMarshal(t, map[string]string{"keyword": "不存在的内容xyz"}))
		var resp struct {
			Documents []interface{} `json:"documents"`
			Total     int           `json:"total"`
		}
		json.Unmarshal(res.Data, &resp)
		if resp.Total != 0 {
			t.Errorf("expected 0 matches, got %d", resp.Total)
		}
	})

	t.Run("query_default_no_preview", func(t *testing.T) {
		res := tool.Execute(ec, "query_documents", mustMarshal(t, map[string]interface{}{}))
		var resp struct {
			Documents []map[string]interface{} `json:"documents"`
			Hint      string                   `json:"hint"`
		}
		json.Unmarshal(res.Data, &resp)
		if len(resp.Documents) == 0 {
			t.Fatal("expected documents")
		}
		for _, d := range resp.Documents {
			if _, ok := d["preview"]; ok {
				t.Errorf("默认不应返回 preview，got %v", d)
			}
		}
		if resp.Hint == "" {
			t.Error("默认应附带 preview 提醒 hint")
		}
	})

	t.Run("query_fields_projection", func(t *testing.T) {
		res := tool.Execute(ec, "query_documents", mustMarshal(t, map[string]interface{}{
			"fields": "id,title,preview",
		}))
		var resp struct {
			Documents []map[string]interface{} `json:"documents"`
		}
		json.Unmarshal(res.Data, &resp)
		if len(resp.Documents) == 0 {
			t.Fatal("expected documents")
		}
		for _, d := range resp.Documents {
			if len(d) != 3 {
				t.Errorf("fields=id,title,preview 应只回 3 个键，got %v", d)
			}
		}
	})
}

func TestDocumentListTags(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	// 初始无标签
	res := tool.Execute(ec, "list_tags", mustMarshal(t, map[string]interface{}{}))
	var resp struct {
		Tags []string `json:"tags"`
	}
	json.Unmarshal(res.Data, &resp)
	if len(resp.Tags) != 0 {
		t.Errorf("expected 0 tags initially, got %d: %v", len(resp.Tags), resp.Tags)
	}

	// 创建带标签的文档
	tool.Execute(ec, "create_document", mustMarshal(t, map[string]string{
		"title": "doc1", "content": "c1", "tags": "Go, 后端, 技术",
	}))
	tool.Execute(ec, "create_document", mustMarshal(t, map[string]string{
		"title": "doc2", "content": "c2", "tags": "项目管理, 技术",
	}))

	res = tool.Execute(ec, "list_tags", mustMarshal(t, map[string]interface{}{}))
	json.Unmarshal(res.Data, &resp)
	// 应去重：Go, 后端, 技术, 项目管理 = 4 个
	if len(resp.Tags) != 4 {
		t.Errorf("expected 4 unique tags, got %d: %v", len(resp.Tags), resp.Tags)
	}
	expected := map[string]bool{"Go": true, "后端": true, "技术": true, "项目管理": true}
	for _, tag := range resp.Tags {
		if !expected[tag] {
			t.Errorf("unexpected tag: %s", tag)
		}
	}
}

func TestDocumentDelete(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	// create
	res := tool.Execute(ec, "create_document", mustMarshal(t, map[string]string{
		"title": "to-delete", "content": "content",
	}))
	var created map[string]interface{}
	json.Unmarshal(res.Data, &created)
	docID := created["id"].(string)

	// delete
	res = tool.Execute(ec, "delete_document", mustMarshal(t, map[string]string{"id": docID}))
	if !res.Success {
		t.Fatalf("delete failed: %s", res.Error)
	}

	// 验证不存在
	res = tool.Execute(ec, "get_document", mustMarshal(t, map[string]string{"id": docID}))
	if res.Success {
		t.Error("expected failure after delete")
	}

	// delete 不存在的应明确报错（否则调用方无法区分"真删了"和"本来就没有"）
	res = tool.Execute(ec, "delete_document", mustMarshal(t, map[string]string{"id": "nonexistent"}))
	if res.Success {
		t.Error("expected failure for deleting nonexistent doc")
	}
}

func TestDocumentTagsNormalizeAndClear(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	// 全角逗号 + 空格 + 末尾空项 + 重复项 → 归一为半角、去重
	id := createDocForTest(t, tool, ec, "标签", "正文")
	res := tool.Execute(ec, "update_document", mustMarshal(t, map[string]interface{}{
		"id": id, "tags": "a， a , b,",
	}))
	if !res.Success {
		t.Fatalf("update tags failed: %s", res.Error)
	}
	if got := getDocForTest(t, tool, ec, map[string]interface{}{"id": id})["tags"]; got != "a,b" {
		t.Errorf("tags should be normalized to %q, got %v", "a,b", got)
	}

	// tags 传空字符串 = 清空
	res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]interface{}{"id": id, "tags": ""}))
	if !res.Success {
		t.Fatalf("clear tags failed: %s", res.Error)
	}
	if got := getDocForTest(t, tool, ec, map[string]interface{}{"id": id})["tags"]; got != "" {
		t.Errorf("tags should be cleared, got %v", got)
	}

	// 不传 tags = 不动
	res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]interface{}{"id": id, "tags": "x，y"}))
	if !res.Success {
		t.Fatalf("update tags failed: %s", res.Error)
	}
	res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]interface{}{"id": id, "title": "改名"}))
	if !res.Success {
		t.Fatalf("update title failed: %s", res.Error)
	}
	if got := getDocForTest(t, tool, ec, map[string]interface{}{"id": id})["tags"]; got != "x,y" {
		t.Errorf("tags should survive title-only update, got %v", got)
	}
}

func TestDocumentPagingStrict(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	for _, title := range []string{"p1", "p2", "p3"} {
		createDocForTest(t, tool, ec, title, "c")
	}

	// limit=0：返回空列表但 total 照常，且空结果不带 hint
	for _, fn := range []string{"list_documents", "query_documents"} {
		res := tool.Execute(ec, fn, mustMarshal(t, map[string]interface{}{"limit": 0}))
		if !res.Success {
			t.Fatalf("%s limit=0 failed: %s", fn, res.Error)
		}
		var got struct {
			Documents []interface{} `json:"documents"`
			Total     int64         `json:"total"`
			Hint      string        `json:"hint"`
		}
		json.Unmarshal(res.Data, &got)
		if len(got.Documents) != 0 {
			t.Errorf("%s limit=0 should return no docs, got %d", fn, len(got.Documents))
		}
		if got.Total != 3 {
			t.Errorf("%s limit=0 should still report total=3, got %d", fn, got.Total)
		}
		if got.Hint != "" {
			t.Errorf("%s limit=0 空结果不应带 hint，got %q", fn, got.Hint)
		}
	}

	// 负数：明确报错，不静默落默认值
	for _, fn := range []string{"list_documents", "query_documents"} {
		for _, args := range []map[string]interface{}{{"limit": -1}, {"offset": -1}} {
			if res := tool.Execute(ec, fn, mustMarshal(t, args)); res.Success {
				t.Errorf("%s %v should fail", fn, args)
			}
		}
	}

	// 超上限：报错，不静默钳制到上限
	for _, fn := range []string{"list_documents", "query_documents"} {
		res := tool.Execute(ec, fn, mustMarshal(t, map[string]interface{}{"limit": maxDocPageSize + 1}))
		if res.Success {
			t.Errorf("%s limit=%d 应报错", fn, maxDocPageSize+1)
		}
		if res := tool.Execute(ec, fn, mustMarshal(t, map[string]interface{}{"limit": maxDocPageSize})); !res.Success {
			t.Errorf("%s limit=%d 应成功: %s", fn, maxDocPageSize, res.Error)
		}
	}
}

func TestDocumentFieldsParamType(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}
	createDocForTest(t, tool, ec, "T", "正文内容")

	// 字符串数组归一化
	res := tool.Execute(ec, "list_documents", mustMarshal(t, map[string]interface{}{
		"fields": []string{"id", "title"},
	}))
	if !res.Success {
		t.Fatalf("数组 fields 应被接受: %s", res.Error)
	}
	var got struct {
		Documents []map[string]interface{} `json:"documents"`
		Hint      string                   `json:"hint"`
	}
	json.Unmarshal(res.Data, &got)
	if len(got.Documents) != 1 || len(got.Documents[0]) != 2 {
		t.Fatalf("数组 fields 应只回 2 个键: %+v", got.Documents)
	}
	if _, ok := got.Documents[0]["id"]; !ok {
		t.Errorf("缺少 id: %+v", got.Documents[0])
	}
	if got.Hint != "" {
		t.Errorf("显式传 fields 不应带 hint")
	}

	// 非字符串/数组类型一律报错（不静默走默认）
	for _, bad := range []interface{}{123, true, map[string]interface{}{"a": 1}} {
		res := tool.Execute(ec, "list_documents", mustMarshal(t, map[string]interface{}{"fields": bad}))
		if res.Success {
			t.Errorf("fields=%v 应报错", bad)
		}
		if !strings.Contains(res.Error, "逗号分隔字符串") {
			t.Errorf("fields=%v 报错应含形态提示，got %q", bad, res.Error)
		}
	}

	// 字符串里嵌 JSON 数组字面量（模型偶发二次编码）→ 归一化成功，不再报"未知字段 [..]"
	res = tool.Execute(ec, "list_documents", mustMarshal(t, map[string]interface{}{
		"fields": `["id","title"]`,
	}))
	if !res.Success {
		t.Fatalf("二次编码的数组字符串应被归一化: %s", res.Error)
	}
	got = struct {
		Documents []map[string]interface{} `json:"documents"`
		Hint      string                   `json:"hint"`
	}{}
	json.Unmarshal(res.Data, &got)
	if len(got.Documents) != 1 || len(got.Documents[0]) != 2 {
		t.Fatalf("归一化后应只回 2 个键: %+v", got.Documents)
	}

	// 数组里混入非字符串 → 形态提示，而非把 JSON 串当字段名
	res = tool.Execute(ec, "list_documents", mustMarshal(t, map[string]interface{}{
		"fields": []interface{}{"id", 123},
	}))
	if res.Success || !strings.Contains(res.Error, "逗号分隔字符串") {
		t.Errorf("非法数组元素应给形态提示，got success=%v err=%q", res.Success, res.Error)
	}
}

func TestDocumentStrictArgs(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}
	id := createDocForTest(t, tool, ec, "T", "c")

	// 未知参数一律报错，不再静默忽略
	cases := []struct {
		fn   string
		args map[string]interface{}
	}{
		{"list_documents", map[string]interface{}{"nonexistent_param": 1}},
		{"query_documents", map[string]interface{}{"nonexistent_param": 1}},
		{"get_document", map[string]interface{}{"id": id, "nonexistent_param": 1}},
		{"update_document", map[string]interface{}{"id": id, "nonexistent_param": 1}},
		{"list_annotations", map[string]interface{}{"document_id": id, "nonexistent_param": 1}},
		{"delete_document", map[string]interface{}{"id": id, "nonexistent_param": 1}},
		{"create_document", map[string]interface{}{"title": "t", "content": "c", "nonexistent_param": 1}},
	}
	for _, c := range cases {
		if res := tool.Execute(ec, c.fn, mustMarshal(t, c.args)); res.Success {
			t.Errorf("%s 传未知参数应报错", c.fn)
		}
	}

	// list_documents 不支持筛选，报错并指向 query_documents
	for _, key := range []string{"tag", "keyword"} {
		res := tool.Execute(ec, "list_documents", mustMarshal(t, map[string]interface{}{key: "x"}))
		if res.Success {
			t.Fatalf("list_documents 传 %s 应报错", key)
		}
		if !strings.Contains(res.Error, "query_documents") {
			t.Errorf("报错应指向 query_documents，got %q", res.Error)
		}
	}

	// list_tags 无参数，传任何参数都报错；无参数正常
	if res := tool.Execute(ec, "list_tags", mustMarshal(t, map[string]interface{}{"limit": 1})); res.Success {
		t.Error("list_tags 传参数应报错")
	}
	if res := tool.Execute(ec, "list_tags", mustMarshal(t, map[string]interface{}{})); !res.Success {
		t.Errorf("list_tags 无参数应成功: %s", res.Error)
	}
}

func TestDocumentDefaultFieldsUnified(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1", ConvID: "conv-1"}
	createDocForTest(t, tool, ec, "T", "正文")

	for _, fn := range []string{"list_documents", "query_documents"} {
		res := tool.Execute(ec, fn, mustMarshal(t, map[string]interface{}{}))
		if !res.Success {
			t.Fatalf("%s: %s", fn, res.Error)
		}
		var got struct {
			Documents []map[string]interface{} `json:"documents"`
		}
		json.Unmarshal(res.Data, &got)
		if len(got.Documents) != 1 {
			t.Fatalf("%s expected 1 doc, got %d", fn, len(got.Documents))
		}
		d := got.Documents[0]
		if _, ok := d["preview"]; ok {
			t.Errorf("%s 默认不应含 preview", fn)
		}
		if _, ok := d["conversation_id"]; !ok {
			t.Errorf("%s 默认应含 conversation_id: %+v", fn, d)
		}
	}
}

func TestDocumentUnknownFunction(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "nonexistent_func", mustMarshal(t, map[string]interface{}{}))
	if res.Success {
		t.Error("expected failure for unknown function")
	}
}

func TestDocumentContentTooLong(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}
	tooLong := strings.Repeat("字", maxDocumentContentLen+1)

	// create 超长应被友好拒绝
	res := tool.Execute(ec, "create_document", mustMarshal(t, map[string]string{
		"title": "过长", "content": tooLong,
	}))
	if res.Success {
		t.Fatal("expected create to fail for over-limit content")
	}
	if !strings.Contains(res.Error, "超长") {
		t.Errorf("expected friendly over-limit error, got: %s", res.Error)
	}

	// 建一篇正常文档，供 update 场景使用
	res = tool.Execute(ec, "create_document", mustMarshal(t, map[string]string{
		"title": "正常", "content": "短内容",
	}))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	var created map[string]interface{}
	json.Unmarshal(res.Data, &created)
	docID := created["id"].(string)

	// 全量重写超长
	res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
		"id": docID, "content": tooLong,
	}))
	if res.Success || !strings.Contains(res.Error, "超长") {
		t.Fatalf("expected update to fail for over-limit content, got success=%v err=%s", res.Success, res.Error)
	}

	// old_text/new_text 替换后撑爆也应被拒绝
	res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
		"id": docID, "old_text": "短内容", "new_text": tooLong,
	}))
	if res.Success || !strings.Contains(res.Error, "超长") {
		t.Fatalf("expected replacement-induced over-limit to fail, got success=%v err=%s", res.Success, res.Error)
	}

	// 边界：恰好等于上限应放行
	atLimit := strings.Repeat("字", maxDocumentContentLen)
	res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
		"id": docID, "content": atLimit,
	}))
	if !res.Success {
		t.Fatalf("expected at-limit content to succeed, got: %s", res.Error)
	}
}

func mustMarshal(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// 改正文 → 划线失活 → list_annotations 能看见 → 改回来 → 自动恢复
func TestDocumentAnnotationsSurviveUpdate(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ctx := context.Background()
	ec := &ExecuteContext{Ctx: ctx, UserID: "user-1"}

	res := tool.Execute(ec, "create_document", mustMarshal(t, map[string]interface{}{
		"title": "T", "content": "第一段。\n\n重点句在这里。\n\n第三段。",
	}))
	if !res.Success {
		t.Fatalf("create: %s", res.Error)
	}
	var created map[string]interface{}
	if err := json.Unmarshal(res.Data, &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	docID := created["id"].(string)

	const seedContent = "第一段。\n\n重点句在这里。\n\n第三段。"
	seedAx := service.NewAnchorer(seedContent)
	anchor, ok := seedAx.Locate("重点句在这里", "", "")
	if !ok {
		t.Fatal("定位失败")
	}
	pfx, sfx := seedAx.Context(anchor)
	if err := tool.annoRepo.Create(ctx, &model.DocumentAnnotation{
		ID: "anno-1", UserID: "user-1", DocumentID: docID,
		Quote: anchor.Quote, Prefix: pfx, Suffix: sfx,
		StartOffset: anchor.Start, EndOffset: anchor.End,
	}); err != nil {
		t.Fatalf("seed anno: %v", err)
	}

	// 改写划线所在句子 → 应报失活
	res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
		"id": docID, "old_text": "重点句在这里。", "new_text": "整句被换掉了。",
	}))
	if !res.Success {
		t.Fatalf("update: %s", res.Error)
	}
	var upd map[string]interface{}
	if err := json.Unmarshal(res.Data, &upd); err != nil {
		t.Fatalf("unmarshal update: %v", err)
	}
	if upd["annotations_orphaned"] != float64(1) {
		t.Fatalf("应报 1 条失活，got %v", upd)
	}

	// list_annotations 能看到它，且带 orphaned 标记
	res = tool.Execute(ec, "list_annotations", mustMarshal(t, map[string]string{"document_id": docID}))
	if !res.Success {
		t.Fatalf("list_annotations: %s", res.Error)
	}
	var listed struct {
		Annotations []struct {
			Quote    string `json:"quote"`
			Orphaned bool   `json:"orphaned"`
		} `json:"annotations"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(res.Data, &listed); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if listed.Total != 1 || len(listed.Annotations) != 1 || !listed.Annotations[0].Orphaned {
		t.Fatalf("list 结果不符：%+v", listed)
	}

	// 改回原文 → 恢复
	res = tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
		"id": docID, "old_text": "整句被换掉了。", "new_text": "重点句在这里。",
	}))
	if !res.Success {
		t.Fatalf("update back: %s", res.Error)
	}
	var back map[string]interface{}
	if err := json.Unmarshal(res.Data, &back); err != nil {
		t.Fatalf("unmarshal back: %v", err)
	}
	if back["annotations_orphaned"] != float64(0) {
		t.Fatalf("恢复后不该有失活，got %v", back)
	}
	a, err := tool.annoRepo.GetByID(ctx, "anno-1", "user-1")
	if err != nil || a.Orphaned {
		t.Fatalf("应已恢复：err=%v orphaned=%v", err, a.Orphaned)
	}
}

// 注册表启动校验：Catalog/ReadOnlyFuncs 里引用的函数名必须真实存在，否则服务起不来
func TestDocumentToolRegistryValidation(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	reg := NewRegistry()
	reg.Register(tool)
	reg.ValidateNativeFC()
	reg.ValidateReadOnlyFuncs()
	reg.ValidateCatalog()
}

// 并发局部替换不同片段：无锁时后写者用旧快照整列覆盖会丢改动，有锁则全部保留
func TestDocumentConcurrentUpdateNoLostUpdate(t *testing.T) {
	tool, cleanup := setupDocumentTool(t)
	defer cleanup()
	ctx := context.Background()
	userID := "user-1"
	ec := &ExecuteContext{Ctx: ctx, UserID: userID}

	const n = 8
	markers := make([]string, n)
	var sb strings.Builder
	for i := range markers {
		markers[i] = fmt.Sprintf("MARK%d", i)
		sb.WriteString(markers[i])
		sb.WriteString("\n")
	}

	res := tool.Execute(ec, "create_document", mustMarshal(t, map[string]string{
		"title": "并发", "content": sb.String(),
	}))
	if !res.Success {
		t.Fatalf("create failed: %s", res.Error)
	}
	var created map[string]interface{}
	json.Unmarshal(res.Data, &created)
	docID := created["id"].(string)

	var wg sync.WaitGroup
	for i := range markers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := tool.Execute(ec, "update_document", mustMarshal(t, map[string]string{
				"id": docID, "old_text": markers[i], "new_text": markers[i] + "_done",
			}))
			if !r.Success {
				t.Errorf("update %d failed: %s", i, r.Error)
			}
		}(i)
	}
	wg.Wait()

	res = tool.Execute(ec, "get_document", mustMarshal(t, map[string]string{"id": docID}))
	var got map[string]interface{}
	json.Unmarshal(res.Data, &got)
	final := got["content"].(string)
	for i, m := range markers {
		if !strings.Contains(final, m+"_done") {
			t.Errorf("marker %d 的改动被覆盖：%s", i, final)
		}
	}
}
