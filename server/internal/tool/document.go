package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/util"
)

const (
	// maxDocumentContentLen 单篇文档正文上限（字）。超长会被拒绝，请拆分成多篇文档。
	maxDocumentContentLen = 500000
	// maxDocumentTitleLen 文档标题上限（字）。超出会被拒绝。
	maxDocumentTitleLen = 50
	// defaultDocWindowLen get_document 默认返回的窗口大小（字）。
	defaultDocWindowLen = 3000
	// maxDocWindowLen get_document 单次窗口上限（字），对齐客户端工具注入预算。
	maxDocWindowLen = 20000
	// maxDocOutlineEntries outline 最多返回的标题条数。
	maxDocOutlineEntries = 12
	// maxDocOutlineTitleLen outline 单条标题截断长度（字）。
	maxDocOutlineTitleLen = 30
	// defaultDocPageSize 列表/查询类函数的默认每页条数。
	defaultDocPageSize = 20
	// maxDocPageSize 列表/查询类函数的每页条数上限，超限报错；压制单次大输出。
	maxDocPageSize = 50
	// docPreviewLen list/query 的 preview（正文摘要）截断长度（字）。
	docPreviewLen = 100
)

// resolvePaging 解析列表类函数的 limit/offset：不传用默认值，负数或超上限报错（传 0 表示只取总数、返回空列表）。
func resolvePaging(limit, offset *int) (int, int, string) {
	l, o := defaultDocPageSize, 0
	if limit != nil {
		if *limit < 0 {
			return 0, 0, "limit 不能为负数"
		}
		if *limit > maxDocPageSize {
			return 0, 0, fmt.Sprintf("limit 上限 %d", maxDocPageSize)
		}
		l = *limit
	}
	if offset != nil {
		if *offset < 0 {
			return 0, 0, "offset 不能为负数"
		}
		o = *offset
	}
	return l, o, ""
}

// docListFields 列表/查询类函数可选的返回字段白名单。
var docListFields = map[string]bool{
	"id": true, "title": true, "tags": true, "preview": true,
	"created_at": true, "updated_at": true, "conversation_id": true,
}

// docDefaultFields list/query 共用的默认返回字段（不含 preview，避免默认就堆大输出）。
var docDefaultFields = []string{"id", "title", "tags", "created_at", "updated_at", "conversation_id"}

// errFieldsShape fields 类型/形态不合法时的统一提示。
const errFieldsShape = "fields 需为逗号分隔字符串或字符串数组"

// parseFields 解析 fields 字符串：逗号分隔、去空格、白名单校验。
// 未指定（空串）时 explicit=false，由调用方套用默认字段集。
func parseFields(s string, allowed map[string]bool) (sel map[string]bool, explicit bool, err error) {
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !allowed[f] {
			// 字段名里出现 JSON 标点，多半是把整个数组/对象当字段名传了，给形态提示而非"未知字段"
			if strings.ContainsAny(f, `[]{}"`) {
				return nil, false, fmt.Errorf("%s", errFieldsShape)
			}
			names := make([]string, 0, len(allowed))
			for n := range allowed {
				names = append(names, n)
			}
			sort.Strings(names)
			return nil, false, fmt.Errorf("未知字段 %q，可选：%s", f, strings.Join(names, ","))
		}
		if sel == nil {
			sel = map[string]bool{}
		}
		sel[f] = true
	}
	return sel, len(sel) > 0, nil
}

// parseFieldsRaw 解析 fields 原始 JSON：接受字符串（逗号分隔）或字符串数组，其余类型报错。
// 空/null 表示未指定（explicit=false）。
func parseFieldsRaw(raw json.RawMessage, allowed map[string]bool) (sel map[string]bool, explicit bool, err error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false, nil
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, false, fmt.Errorf("%s: %w", errFieldsShape, err)
		}
		return parseFieldsString(s, allowed)
	case '[':
		var arr []string
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, false, fmt.Errorf("%s: %w", errFieldsShape, err)
		}
		return parseFields(strings.Join(arr, ","), allowed)
	default:
		return nil, false, fmt.Errorf("%s", errFieldsShape)
	}
}

// parseFieldsString 解析「字符串形态」的 fields；容忍字符串里嵌了 JSON 数组字面量（模型偶发二次编码），
// 直接归一化，省一次纠错往返。
func parseFieldsString(s string, allowed map[string]bool) (map[string]bool, bool, error) {
	ts := strings.TrimSpace(s)
	if strings.HasPrefix(ts, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(ts), &arr); err != nil {
			return nil, false, fmt.Errorf("%s: %w", errFieldsShape, err)
		}
		return parseFields(strings.Join(arr, ","), allowed)
	}
	return parseFields(s, allowed)
}

// fieldSet 把默认字段名列表转成集合，供 projectFields 使用。
func fieldSet(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// projectFields 只保留 sel 中选中的字段。
func projectFields(full map[string]interface{}, sel map[string]bool) map[string]interface{} {
	out := make(map[string]interface{}, len(sel))
	for k := range sel {
		if v, ok := full[k]; ok {
			out[k] = v
		}
	}
	return out
}

// docPreviewHint list/query 默认不返回 preview 时随结果下发的提醒。
const docPreviewHint = "未返回 preview（正文摘要）；需要时用 fields=preview 重新调用。"

// docWindowHint 大文档窗口读的固定提示，防止整读整写浪费 token。
const docWindowHint = "文档较长，仅返回部分内容。继续读请传 char_offset；修改请用 old_text/new_text 局部替换，不要整读整写。"

// DocumentTool — 文档管理工具。仅在用户明确要求时使用，不要主动创建。
type DocumentTool struct {
	docRepo  *service.DocumentRepo
	annoRepo *service.DocumentAnnotationRepo
	// mu 按文档 ID 串行化「读快照→局部替换→整列写回」，避免并发 update 互相覆盖。
	mu keyedMutex
}

func NewDocumentTool(docRepo *service.DocumentRepo, annoRepo *service.DocumentAnnotationRepo) *DocumentTool {
	return &DocumentTool{docRepo: docRepo, annoRepo: annoRepo}
}

func (t *DocumentTool) Name() string { return "document" }

func (t *DocumentTool) Description() string {
	return "管理用户的结构化 Markdown 文档。仅在用户明确要求生成/保存/修改/查询/删除文章、报告、总结等文档时使用。不要主动创建或修改文档。日常的信息记录请用 note 工具。"
}

func (t *DocumentTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "文档。写/查/改/删 Markdown 文档，看用户划的线和批注。用户明确要求才用。",
		Funcs:     []string{"get_document", "update_document", "list_annotations", "create_document"},
		Others:    "删除、文档列表、标签 等",
	}, true
}

func (t *DocumentTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "create_document",
			Display:     "创建文档",
			Description: "创建一篇新的 Markdown 文档。仅当用户明确说写一篇、生成文档、保存为文档、总结成文章等时调用。不要主动调用。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title":   map[string]interface{}{"type": "string", "description": fmt.Sprintf("文档标题，≤%d 字，超长会被拒绝", maxDocumentTitleLen)},
					"content": map[string]interface{}{"type": "string", "description": fmt.Sprintf("Markdown 格式的文档正文，≤%d 字，超长会被拒绝（请拆分成多篇）", maxDocumentContentLen)},
					"tags":    map[string]interface{}{"type": "string", "description": "逗号分隔的标签（半角逗号），用于分类检索，如 技术,Go,后端；自动去重"},
				},
				"required": []string{"title", "content"},
			},
		},
		{
			Name:        "get_document",
			Display:     "获取文档",
			Description: fmt.Sprintf("获取文档内容（按窗口返回，默认前 %d 字）。长文档会附带 outline（标题+偏移）并提示续读方式：先看 outline 定位，再用 char_offset 读需要的片段，不要反复整读。修改文档前只需读相关片段即可。", defaultDocWindowLen),
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":          map[string]interface{}{"type": "string", "description": "文档 ID"},
					"char_offset": map[string]interface{}{"type": "integer", "description": "从第几个字符开始读（0 起，默认 0）"},
					"char_limit":  map[string]interface{}{"type": "integer", "description": fmt.Sprintf("本次返回的最大字符数，默认 %d，上限 %d", defaultDocWindowLen, maxDocWindowLen)},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "list_annotations",
			Display:     "查看划线批注",
			Description: "查看用户在文档正文上划的线和写的批注。修改文档前先看一眼，能避开用户标出来的段落，改完也不容易把划线弄失活。note 为空表示用户只划了线没写批注。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"document_id": map[string]interface{}{"type": "string", "description": "文档 ID"},
					"limit":       map[string]interface{}{"type": "integer", "description": fmt.Sprintf("每页条数，默认 %d，上限 %d；传 0 只取总数返回空列表", defaultDocPageSize, maxDocPageSize)},
					"offset":      map[string]interface{}{"type": "integer", "description": "偏移量，默认 0，不能为负"},
				},
				"required": []string{"document_id"},
			},
		},
		{
			Name:        "update_document",
			Display:     "修改文档",
			Description: "修改已有文档。局部小改动优先用 old_text/new_text 指定替换片段（无需传全文，省 token 且不会误覆盖其他内容）；只有重写全文时才传 content。old_text 必须与文档当前内容精确匹配且唯一。仅更新传入的非空字段。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":       map[string]interface{}{"type": "string", "description": "文档 ID"},
					"title":    map[string]interface{}{"type": "string", "description": fmt.Sprintf("新的文档标题（可选），≤%d 字，超长会被拒绝", maxDocumentTitleLen)},
					"content":  map[string]interface{}{"type": "string", "deprecated": true, "description": fmt.Sprintf("（已废弃）新的 Markdown 正文，全量覆盖，≤%d 字。优先用 old_text/new_text 局部替换，仅整篇重写时才传此参数", maxDocumentContentLen)},
					"tags":     map[string]interface{}{"type": "string", "description": "新的标签（可选，半角逗号分隔、自动去重）。传空字符串表示清空标签"},
					"old_text": map[string]interface{}{"type": "string", "description": "要被替换的原文片段，需精确匹配且在文档中唯一（与 content 互斥）"},
					"new_text": map[string]interface{}{"type": "string", "description": "替换后的新文本（可为空字符串表示删除片段），须与 old_text 搭配传入"},
					"count":    map[string]interface{}{"type": "integer", "description": "要替换的匹配数量（可选）。不传时要求 old_text 唯一匹配并只替换 1 处；传入时替换前 count 处，不能超过实际匹配数"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "query_documents",
			Display:     "查询文档",
			Description: fmt.Sprintf("查询文档列表，可按标签或关键词筛选；total 为符合筛选的文档数。默认只回 id/title/tags/时间，不含正文摘要 preview；需要正文前 %d 字判断相关性时传 fields=preview，只要目录信息可传 fields=id,title,tags。", docPreviewLen),
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"tag":     map[string]interface{}{"type": "string", "description": "按标签筛选（可选）"},
					"keyword": map[string]interface{}{"type": "string", "description": "按标题和内容搜索关键词（可选）"},
					"fields":  map[string]interface{}{"type": "string", "description": "返回字段（可选），逗号分隔字符串或字符串数组，可选 id,title,tags,preview,created_at,updated_at,conversation_id。默认不含 preview（正文摘要），需要时加上 preview"},
					"limit":   map[string]interface{}{"type": "integer", "description": fmt.Sprintf("每页条数，默认 %d，上限 %d；传 0 只取总数返回空列表", defaultDocPageSize, maxDocPageSize)},
					"offset":  map[string]interface{}{"type": "integer", "description": "偏移量，默认 0，不能为负"},
				},
			},
		},
		{
			Name:        "list_documents",
			Display:     "文档列表",
			Description: fmt.Sprintf("获取文档列表（按更新时间倒序），不支持筛选（按标签/关键词筛请用 query_documents）；total 为全部文档数。默认只回 id/title/tags/时间，不含正文摘要 preview；需要正文前 %d 字判断相关性时传 fields=preview，只要目录信息可传 fields=id,title,tags。", docPreviewLen),
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"fields": map[string]interface{}{"type": "string", "description": "返回字段（可选），逗号分隔字符串或字符串数组，可选 id,title,tags,preview,created_at,updated_at,conversation_id。默认不含 preview（正文摘要），需要时加上 preview"},
					"limit":  map[string]interface{}{"type": "integer", "description": fmt.Sprintf("每页条数，默认 %d，上限 %d；传 0 只取总数返回空列表", defaultDocPageSize, maxDocPageSize)},
					"offset": map[string]interface{}{"type": "integer", "description": "偏移量，默认 0，不能为负"},
				},
			},
		},
		{
			Name:        "list_tags",
			Display:     "标签列表",
			Description: "获取当前用户所有文档中使用过的标签列表。用于了解有哪些标签可供 query_documents 筛选。",
		},
		{
			Name:        "delete_document",
			Display:     "删除文档",
			Description: "删除一篇文档。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "要删除的文档 ID"},
				},
				"required": []string{"id"},
			},
		},
	}
}

// ReadOnlyFuncs — 查看/查询/标签类只读，增删改为写（plan 模式拦截）
func (t *DocumentTool) ReadOnlyFuncs() map[string]bool {
	return map[string]bool{
		"get_document":     true,
		"list_annotations": true,
		"query_documents":  true,
		"list_documents":   true,
		"list_tags":        true,
	}
}

func (t *DocumentTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "create_document":
		return t.createDocument(ec, args)
	case "get_document":
		return t.getDocument(ec, args)
	case "list_annotations":
		return t.listAnnotations(ec, args)
	case "update_document":
		return t.updateDocument(ec, args)
	case "query_documents":
		return t.queryDocuments(ec, args)
	case "delete_document":
		return t.deleteDocument(ec, args)
	case "list_documents":
		return t.listDocuments(ec, args)
	case "list_tags":
		return t.listTags(ec, args)
	default:
		return &Result{Success: false, Error: "unknown function: " + funcName}
	}
}

func (t *DocumentTool) createDocument(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Title   string `json:"title"`
		Content string `json:"content"`
		Tags    string `json:"tags"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if req.Title == "" || req.Content == "" {
		return &Result{Success: false, Error: "title and content required"}
	}
	if utf8.RuneCountInString(req.Content) > maxDocumentContentLen {
		return &Result{Success: false, Error: fmt.Sprintf("文档正文超长（≤%d 字），请拆分成多篇文档", maxDocumentContentLen)}
	}
	if utf8.RuneCountInString(req.Title) > maxDocumentTitleLen {
		return &Result{Success: false, Error: fmt.Sprintf("文档标题超长（≤%d 字），请精简", maxDocumentTitleLen)}
	}

	doc := &model.Document{
		ID:      uuid.New().String(),
		UserID:  ec.UserID,
		Title:   req.Title,
		Content: req.Content,
		Tags:    service.NormalizeTags(req.Tags),
	}
	if ec.ConvID != "" {
		convID := ec.ConvID
		doc.ConversationID = &convID
	}

	if err := t.docRepo.Create(ec.Ctx, doc); err != nil {
		return &Result{Success: false, Error: "create document: " + err.Error()}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"id":      doc.ID,
		"title":   doc.Title,
		"message": "文档已保存",
	})
	return &Result{Success: true, Data: data}
}

func (t *DocumentTool) getDocument(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ID         string `json:"id"`
		CharOffset int    `json:"char_offset"`
		CharLimit  int    `json:"char_limit"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if req.ID == "" {
		return &Result{Success: false, Error: "id required"}
	}
	if req.CharOffset < 0 {
		req.CharOffset = 0
	}
	if req.CharLimit <= 0 {
		req.CharLimit = defaultDocWindowLen
	}
	if req.CharLimit > maxDocWindowLen {
		req.CharLimit = maxDocWindowLen
	}

	doc, err := t.docRepo.GetByID(ec.Ctx, req.ID, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "document not found: " + err.Error()}
	}

	runes := []rune(doc.Content)
	total := len(runes)
	if req.CharOffset > total {
		req.CharOffset = total
	}
	end := req.CharOffset + req.CharLimit
	if end > total {
		end = total
	}
	truncated := end < total

	result := map[string]interface{}{
		"id":          doc.ID,
		"title":       doc.Title,
		"content":     string(runes[req.CharOffset:end]),
		"tags":        doc.Tags,
		"created_at":  doc.CreatedAt,
		"updated_at":  doc.UpdatedAt,
		"char_offset": req.CharOffset,
		"char_limit":  req.CharLimit,
		"total_chars": total,
		"total_lines": strings.Count(doc.Content, "\n") + 1,
		"truncated":   truncated,
	}
	if truncated {
		result["next_char_offset"] = end
		result["outline"] = docOutline(doc.Content)
		result["hint"] = docWindowHint
	}
	if doc.ConversationID != nil {
		result["conversation_id"] = *doc.ConversationID
	}

	data, _ := json.Marshal(result)
	return &Result{Success: true, Data: data}
}

// docOutline 提取 Markdown 标题行，返回紧凑目录串 "rune偏移: 标题"，供模型定位后按 char_offset 续读。
func docOutline(content string) string {
	var sb strings.Builder
	count := 0
	offset := 0
	for _, line := range strings.Split(content, "\n") {
		if count >= maxDocOutlineEntries {
			break
		}
		if title, ok := markdownHeading(line); ok {
			if count > 0 {
				sb.WriteString("\n")
			}
			fmt.Fprintf(&sb, "%d: %s", offset, util.TruncateContent(title, maxDocOutlineTitleLen))
			count++
		}
		offset += utf8.RuneCountInString(line) + 1
	}
	return sb.String()
}

// markdownHeading 判断是否为 Markdown 标题行（#~###### + 空格），返回含标记的标题行（如 "## 小节"）。
func markdownHeading(line string) (string, bool) {
	if !strings.HasPrefix(line, "#") {
		return "", false
	}
	trimmed := strings.TrimLeft(line, "#")
	level := len(line) - len(trimmed)
	if level > 6 || !strings.HasPrefix(trimmed, " ") {
		return "", false
	}
	return strings.TrimRight(line, " \t"), true
}

func (t *DocumentTool) updateDocument(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ID      string  `json:"id"`
		Title   string  `json:"title"`
		Content string  `json:"content"`
		Tags    *string `json:"tags"`
		OldText string  `json:"old_text"`
		NewText *string `json:"new_text"`
		Count   *int    `json:"count"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if req.ID == "" {
		return &Result{Success: false, Error: "id required"}
	}
	// 同一文档的并发局部替换必须串行，否则后写者用旧快照整列覆盖前一次改动。
	unlock := t.mu.lock(req.ID)
	defer unlock()
	if req.OldText != "" && req.Content != "" {
		return &Result{Success: false, Error: "old_text 与 content 互斥，局部替换用 old_text，全量重写才用 content"}
	}
	if req.OldText != "" && req.NewText == nil {
		return &Result{Success: false, Error: "old_text 需要搭配 new_text 使用"}
	}

	// 局部替换需要读当前内容做匹配
	current, err := t.docRepo.GetByID(ec.Ctx, req.ID, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "document not found: " + err.Error()}
	}

	updates := map[string]interface{}{}
	if req.OldText != "" {
		n := strings.Count(current.Content, req.OldText)
		limit := 1
		if req.Count != nil {
			if *req.Count <= 0 {
				return &Result{Success: false, Error: "count 必须为正整数"}
			}
			if *req.Count > n {
				return &Result{Success: false, Error: fmt.Sprintf("old_text 在文档中仅匹配 %d 处，无法替换 %d 处", n, *req.Count)}
			}
			limit = *req.Count
		} else {
			if n == 0 {
				return &Result{Success: false, Error: "old_text 未在文档中找到，请先用 get_document 核对原文（注意精确匹配）"}
			}
			if n > 1 {
				return &Result{Success: false, Error: fmt.Sprintf("old_text 在文档中匹配 %d 处，请加长片段使其唯一，或用 count 指定替换数量", n)}
			}
		}
		updates["content"] = strings.Replace(current.Content, req.OldText, *req.NewText, limit)
	} else if req.Content != "" {
		updates["content"] = req.Content
	}
	if c, ok := updates["content"].(string); ok && utf8.RuneCountInString(c) > maxDocumentContentLen {
		return &Result{Success: false, Error: fmt.Sprintf("文档正文超长（≤%d 字），请拆分成多篇文档", maxDocumentContentLen)}
	}
	if req.Title != "" {
		if utf8.RuneCountInString(req.Title) > maxDocumentTitleLen {
			return &Result{Success: false, Error: fmt.Sprintf("文档标题超长（≤%d 字），请精简", maxDocumentTitleLen)}
		}
		updates["title"] = req.Title
	}
	if req.Tags != nil {
		updates["tags"] = service.NormalizeTags(*req.Tags)
	}
	if len(updates) == 0 {
		return &Result{Success: true, Data: []byte(`{"message":"nothing to update"}`)}
	}

	if err := t.docRepo.Update(ec.Ctx, req.ID, ec.UserID, updates); err != nil {
		return &Result{Success: false, Error: "update document: " + err.Error()}
	}

	out := map[string]interface{}{
		"id":      req.ID,
		"message": "文档已更新",
	}
	// 正文变了就重算用户划线批注的锚点，把失活情况如实报回去
	if newContent, ok := updates["content"].(string); ok {
		affected, orphaned, err := t.annoRepo.RelocateAll(ec.Ctx, req.ID, ec.UserID, newContent)
		if err != nil {
			// 不影响正文已更新的结果，就地记日志
			log.Printf("[document] RelocateAll doc=%s user=%s: %v", req.ID, ec.UserID, err)
		} else if affected > 0 {
			out["annotations_affected"] = affected
			out["annotations_orphaned"] = orphaned
			if orphaned > 0 {
				out["message"] = fmt.Sprintf("文档已更新；本次改动使 %d 条用户划线批注失效，可在文档批注列表里看到", orphaned)
			}
		}
	}

	data, _ := json.Marshal(out)
	return &Result{Success: true, Data: data}
}

func (t *DocumentTool) listAnnotations(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		DocumentID string `json:"document_id"`
		Limit      *int   `json:"limit"`
		Offset     *int   `json:"offset"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if req.DocumentID == "" {
		return &Result{Success: false, Error: "document_id required"}
	}
	limit, offset, errMsg := resolvePaging(req.Limit, req.Offset)
	if errMsg != "" {
		return &Result{Success: false, Error: errMsg}
	}

	list, total, err := t.annoRepo.List(ec.Ctx, req.DocumentID, ec.UserID, limit, offset)
	if err != nil {
		return &Result{Success: false, Error: "list annotations: " + err.Error()}
	}

	type item struct {
		Quote    string `json:"quote"`
		Note     string `json:"note"`
		Orphaned bool   `json:"orphaned"`
		Created  string `json:"created_at"`
	}
	items := make([]item, 0, len(list))
	for _, a := range list {
		items = append(items, item{
			Quote:    a.Quote,
			Note:     a.Note,
			Orphaned: a.Orphaned,
			Created:  a.CreatedAt.Format("2006-01-02 15:04"),
		})
	}

	data, _ := json.Marshal(map[string]interface{}{
		"annotations": items,
		"total":       total,
		"hint":        "note 为空表示用户只划了线没写批注；orphaned 为 true 表示原文已被改写、这条划线已失活",
	})
	return &Result{Success: true, Data: data}
}

func (t *DocumentTool) queryDocuments(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Tag     string          `json:"tag"`
		Keyword string          `json:"keyword"`
		Fields  json.RawMessage `json:"fields"`
		Limit   *int            `json:"limit"`
		Offset  *int            `json:"offset"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	limit, offset, errMsg := resolvePaging(req.Limit, req.Offset)
	if errMsg != "" {
		return &Result{Success: false, Error: errMsg}
	}
	sel, explicit, err := parseFieldsRaw(req.Fields, docListFields)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if !explicit {
		sel = fieldSet(docDefaultFields...)
	}

	list, total, err := t.docRepo.List(ec.Ctx, ec.UserID, req.Tag, req.Keyword, nil, limit, offset)
	if err != nil {
		return &Result{Success: false, Error: "query documents: " + err.Error()}
	}

	result := make([]map[string]interface{}, 0, len(list))
	for _, d := range list {
		full := map[string]interface{}{
			"id":         d.ID,
			"title":      d.Title,
			"tags":       d.Tags,
			"created_at": d.CreatedAt,
			"updated_at": d.UpdatedAt,
			"preview":    util.TruncateContent(d.Content, docPreviewLen),
		}
		if d.ConversationID != nil {
			full["conversation_id"] = *d.ConversationID
		}
		result = append(result, projectFields(full, sel))
	}

	out := map[string]interface{}{
		"documents": result,
		"total":     total,
	}
	if !explicit && len(result) > 0 {
		out["hint"] = docPreviewHint
	}
	data, _ := json.Marshal(out)
	return &Result{Success: true, Data: data}
}

func (t *DocumentTool) deleteDocument(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if req.ID == "" {
		return &Result{Success: false, Error: "id required"}
	}

	rows, err := t.docRepo.Delete(ec.Ctx, req.ID, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "delete document: " + err.Error()}
	}
	if rows == 0 {
		return &Result{Success: false, Error: "document not found"}
	}

	data, _ := json.Marshal(map[string]interface{}{"id": req.ID, "message": "文档已删除"})
	return &Result{Success: true, Data: data}
}

func (t *DocumentTool) listDocuments(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		Fields json.RawMessage `json:"fields"`
		Limit  *int            `json:"limit"`
		Offset *int            `json:"offset"`
	}
	if err := decodeArgs(args, &req); err != nil {
		if strings.Contains(err.Error(), `unknown field "tag"`) || strings.Contains(err.Error(), `unknown field "keyword"`) {
			return &Result{Success: false, Error: "list_documents 不支持筛选（tag/keyword），请改用 query_documents"}
		}
		return &Result{Success: false, Error: err.Error()}
	}
	limit, offset, errMsg := resolvePaging(req.Limit, req.Offset)
	if errMsg != "" {
		return &Result{Success: false, Error: errMsg}
	}
	sel, explicit, err := parseFieldsRaw(req.Fields, docListFields)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if !explicit {
		sel = fieldSet(docDefaultFields...)
	}

	list, total, err := t.docRepo.List(ec.Ctx, ec.UserID, "", "", nil, limit, offset)
	if err != nil {
		return &Result{Success: false, Error: "list documents: " + err.Error()}
	}

	result := make([]map[string]interface{}, 0, len(list))
	for _, d := range list {
		full := map[string]interface{}{
			"id":         d.ID,
			"title":      d.Title,
			"tags":       d.Tags,
			"created_at": d.CreatedAt,
			"updated_at": d.UpdatedAt,
			"preview":    util.TruncateContent(d.Content, docPreviewLen),
		}
		if d.ConversationID != nil {
			full["conversation_id"] = *d.ConversationID
		}
		result = append(result, projectFields(full, sel))
	}

	out := map[string]interface{}{
		"documents": result,
		"total":     total,
	}
	if !explicit && len(result) > 0 {
		out["hint"] = docPreviewHint
	}
	data, _ := json.Marshal(out)
	return &Result{Success: true, Data: data}
}

func (t *DocumentTool) listTags(ec *ExecuteContext, args json.RawMessage) *Result {
	if err := decodeArgs(args, &struct{}{}); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	tags, err := t.docRepo.ListTags(ec.Ctx, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "list tags: " + err.Error()}
	}
	data, _ := json.Marshal(map[string]interface{}{"tags": tags})
	return &Result{Success: true, Data: data}
}
