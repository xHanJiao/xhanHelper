package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"xhan-helper/internal/idgen"
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
	// maxAnnotationReplyLen 单条批注回复上限（字），与 controller 侧一致。
	maxAnnotationReplyLen = 4000
	// maxRepliesPerAnnotation 单条批注回复条数上限。
	maxRepliesPerAnnotation = 100
	// annotationRepliesPreview list_annotations 每条批注回带回复的条数上限。
	annotationRepliesPreview = 20
	// annotationScanLimit 「待回复」清单的全量扫描上限，对齐 controller 单文档批注配额（maxAnnotationsPerDoc=500）。
	annotationScanLimit = 500
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
	"audience": true,
}

// docAudienceParamDesc create/update 共用的 audience 参数说明：把「读者是谁」这条唯一判据写死，
// 避免模型按可见性/length 猜。
const docAudienceParamDesc = "文档读者层级，必填判断：给用户阅读的成品填 finished；给助手后续工作用的材料（调研底稿、交接、采集清单、证据池）填 internal。internal 默认不出现在用户列表，但工具检索/读取、拿到链接仍可见。"

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

// docLinkHint 内部跳链约定：客户端拦截 xhanhelper://document/<id> 并跳到该文档。
// id 必须真实存在（先查再写），编造的 id 点开只会报错。
const docLinkHint = "正文中引用其他文档时，写成 [文档标题](xhanhelper://document/<文档id>)；id 必须先用 query_documents 或 list_documents 查到，禁止编造。"

// DocumentTool — 文档管理工具。仅在用户明确要求时使用，不要主动创建。
type DocumentTool struct {
	docRepo   *service.DocumentRepo
	annoRepo  *service.DocumentAnnotationRepo
	replyRepo *service.DocumentAnnotationReplyRepo
	imageRepo *service.ImageAssetRepo
	// mu 按文档 ID 串行化「读快照→局部替换→整列写回」，避免并发 update 互相覆盖。
	mu keyedMutex
}

func NewDocumentTool(docRepo *service.DocumentRepo, annoRepo *service.DocumentAnnotationRepo, replyRepo *service.DocumentAnnotationReplyRepo, imageRepo *service.ImageAssetRepo) *DocumentTool {
	return &DocumentTool{docRepo: docRepo, annoRepo: annoRepo, replyRepo: replyRepo, imageRepo: imageRepo}
}

func (t *DocumentTool) Name() string { return "document" }

func (t *DocumentTool) Description() string {
	return "管理用户的结构化 Markdown 文档。逐日流水/台账（睡眠、体重、饮酒、运动、时间日志等按天记的读数）归这里：先查有没有对应台账，有就追加、没有就建一篇。用户明确要求生成/保存/修改/查询/删除文章、报告、总结等时也用这里。除流水台账外，不要主动创建或修改文档。创建时必须声明 audience：给用户读的成品填 finished，供助手后续工作用的材料（调研底稿、交接、采集清单、证据池）填 internal；internal 默认不出现在用户列表，但工具检索/读取仍可见。需要配图时用 image.list_images 找用户上传的图，把返回的 markdown 原样嵌入正文（如图片描述为空，说明尚未生成，可稍后重试）。" + docLinkHint
}

func (t *DocumentTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "文档。写/查/改/删 Markdown 文档，看用户划的线和批注。逐日流水/台账归这里（有就追加、没有就建）；其余用户明确要求才用。创建时须声明 audience（finished=给用户读 / internal=给助手后续用）。",
		Funcs:     []string{"get_document", "update_document", "list_annotations", "create_document"},
		Others:    "删除、文档列表、标签 等",
	}, true
}

func (t *DocumentTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "create_document",
			Display:     "创建文档",
			Description: "创建一篇新的 Markdown 文档。仅当用户明确说写一篇、生成文档、保存为文档、总结成文章等时调用。" + docLinkHint,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title":    map[string]interface{}{"type": "string", "description": fmt.Sprintf("文档标题，≤%d 字，超长会被拒绝", maxDocumentTitleLen)},
					"content":  map[string]interface{}{"type": "string", "description": fmt.Sprintf("Markdown 格式的文档正文，≤%d 字，超长会被拒绝（请拆分成多篇）", maxDocumentContentLen)},
					"tags":     map[string]interface{}{"type": "string", "description": "逗号分隔的标签（半角逗号），用于分类检索，如 技术,Go,后端；自动去重"},
					"audience": map[string]interface{}{"type": "string", "enum": []string{model.DocumentAudienceFinished, model.DocumentAudienceInternal}, "description": docAudienceParamDesc},
				},
				"required": []string{"title", "content", "audience"},
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
			Description: "查看用户在文档正文上划的线和写的批注。修改文档前先看一眼，能避开用户标出来的段落，改完也不容易把划线弄失活。note 为空表示用户只划了线没写批注。传 pending=true 只返回「等待 AI 回复」的批注（用户写了批注，且还没被回复、或用户又追问了），按最近活跃倒序——用于批量回复 / 继续回应用户的追问。注意：只有用户在批注抽屉里的追问会写进批注线程；在主聊天里说的追问不会写入线程，因而不会出现在 pending 清单里。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"document_id": map[string]interface{}{"type": "string", "description": "文档 ID"},
					"pending":     map[string]interface{}{"type": "boolean", "description": "只返回等待 AI 回复的批注（note 非空，且无回复或最新一条回复是用户），按最近活跃倒序；用于批量回复 / 继续回应用户追问。默认 false 返回全部"},
					"limit":       map[string]interface{}{"type": "integer", "description": fmt.Sprintf("每页条数，默认 %d，上限 %d；传 0 只取总数返回空列表", defaultDocPageSize, maxDocPageSize)},
					"offset":      map[string]interface{}{"type": "integer", "description": "偏移量，默认 0，不能为负"},
				},
				"required": []string{"document_id"},
			},
		},
		{
			Name:        "reply_annotation",
			Display:     "回复批注",
			Description: "针对用户在文档里划的某条线/写的批注，写一条 AI 回复进它的讨论线程。仅当用户要求你逐条回复批注时调用；annotation_id 从 list_annotations 里取。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"annotation_id": map[string]interface{}{"type": "string", "description": "批注 ID（来自 list_annotations）"},
					"content":       map[string]interface{}{"type": "string", "description": fmt.Sprintf("你要对这条批注说的话，≤%d 字", maxAnnotationReplyLen)},
				},
				"required": []string{"annotation_id", "content"},
			},
		},
		{
			Name:        "update_document",
			Display:     "修改文档",
			Description: "修改已有文档。局部小改动优先用 old_text/new_text 指定替换片段（无需传全文，省 token 且不会误覆盖其他内容）；只有重写全文时才传 content。old_text 必须与文档当前内容精确匹配且唯一。仅更新传入的非空字段。局部替换时不要弄丢正文里已有的内部链接。" + docLinkHint,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":       map[string]interface{}{"type": "string", "description": "文档 ID"},
					"title":    map[string]interface{}{"type": "string", "description": fmt.Sprintf("新的文档标题（可选），≤%d 字，超长会被拒绝", maxDocumentTitleLen)},
					"content":  map[string]interface{}{"type": "string", "deprecated": true, "description": fmt.Sprintf("（已废弃）新的 Markdown 正文，全量覆盖，≤%d 字。优先用 old_text/new_text 局部替换，仅整篇重写时才传此参数", maxDocumentContentLen)},
					"tags":     map[string]interface{}{"type": "string", "description": "新的标签（可选，半角逗号分隔、自动去重）。传空字符串表示清空标签"},
					"audience": map[string]interface{}{"type": "string", "enum": []string{model.DocumentAudienceFinished, model.DocumentAudienceInternal}, "description": "调整文档读者层级（可选，不传保持不变）。" + docAudienceParamDesc},
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
			Description: "把一篇文档移入回收站（7 天内可由用户在客户端恢复，超期自动物理删除）。",
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
	case "reply_annotation":
		return t.replyAnnotation(ec, args)
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
		Title    string `json:"title"`
		Content  string `json:"content"`
		Tags     string `json:"tags"`
		Audience string `json:"audience"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	if req.Title == "" || req.Content == "" {
		return &Result{Success: false, Error: "title and content required"}
	}
	if !model.ValidDocumentAudience(req.Audience) {
		return &Result{Success: false, Error: "audience 必填，且必须为 finished（给用户读）或 internal（给助手后续工作用）"}
	}
	if utf8.RuneCountInString(req.Content) > maxDocumentContentLen {
		return &Result{Success: false, Error: fmt.Sprintf("文档正文超长（≤%d 字），请拆分成多篇文档", maxDocumentContentLen)}
	}
	if utf8.RuneCountInString(req.Title) > maxDocumentTitleLen {
		return &Result{Success: false, Error: fmt.Sprintf("文档标题超长（≤%d 字），请精简", maxDocumentTitleLen)}
	}

	doc := &model.Document{
		ID:       uuid.New().String(),
		UserID:   ec.UserID,
		Title:    req.Title,
		Content:  req.Content,
		Tags:     service.NormalizeTags(req.Tags),
		Audience: req.Audience,
	}
	if ec.ConvID != "" {
		convID := ec.ConvID
		doc.ConversationID = &convID
	}

	if err := t.docRepo.Create(ec.Ctx, doc); err != nil {
		return &Result{Success: false, Error: "create document: " + err.Error()}
	}
	t.promoteDocImages(ec.Ctx, ec.UserID, req.Content)

	data, _ := json.Marshal(map[string]interface{}{
		"id":       doc.ID,
		"title":    doc.Title,
		"audience": doc.Audience,
		"message":  "文档已保存",
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
		ID       string  `json:"id"`
		Title    string  `json:"title"`
		Content  string  `json:"content"`
		Tags     *string `json:"tags"`
		Audience *string `json:"audience"`
		OldText  string  `json:"old_text"`
		NewText  *string `json:"new_text"`
		Count    *int    `json:"count"`
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
	if req.Audience != nil {
		if !model.ValidDocumentAudience(*req.Audience) {
			return &Result{Success: false, Error: "audience 必须为 finished 或 internal"}
		}
		updates["audience"] = *req.Audience
	}
	if len(updates) == 0 {
		return &Result{Success: true, Data: []byte(`{"message":"nothing to update"}`)}
	}

	if err := t.docRepo.Update(ec.Ctx, req.ID, ec.UserID, updates); err != nil {
		return &Result{Success: false, Error: "update document: " + err.Error()}
	}

	// 回填写后的权威 title/tags/preview，供客户端文档卡片展示，避免模型未传 title 时卡片退化成占位标题
	newTitle := current.Title
	if v, ok := updates["title"].(string); ok {
		newTitle = v
	}
	newTags := current.Tags
	if v, ok := updates["tags"].(string); ok {
		newTags = v
	}
	newContent := current.Content
	if v, ok := updates["content"].(string); ok {
		newContent = v
	}
	newAudience := current.Audience
	if v, ok := updates["audience"].(string); ok {
		newAudience = v
	}
	out := map[string]interface{}{
		"id":       req.ID,
		"message":  "文档已更新",
		"title":    newTitle,
		"tags":     newTags,
		"audience": newAudience,
		"preview":  util.TruncateContent(newContent, docPreviewLen),
	}
	// 正文变了就重算用户划线批注的锚点，把失活情况如实报回去
	if newContent, ok := updates["content"].(string); ok {
		t.promoteDocImages(ec.Ctx, ec.UserID, newContent)
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
		Pending    bool   `json:"pending"`
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
	// 文档不存在或已在回收站时不暴露批注
	if _, err := t.docRepo.GetByID(ec.Ctx, req.DocumentID, ec.UserID); err != nil {
		return &Result{Success: false, Error: "document not found"}
	}

	var (
		list  []model.DocumentAnnotation
		total int64
		err   error
	)
	if req.Pending {
		list, total, err = t.pendingAnnotations(ec, req.DocumentID, limit, offset)
	} else {
		list, total, err = t.annoRepo.List(ec.Ctx, req.DocumentID, ec.UserID, limit, offset)
	}
	if err != nil {
		return &Result{Success: false, Error: "list annotations: " + err.Error()}
	}

	// 回复摘要 + 每条批注最近若干条回复（一次批量查，无 N+1）
	ids := make([]string, 0, len(list))
	for _, a := range list {
		ids = append(ids, a.ID)
	}
	counts, err := t.replyRepo.CountsByAnnotationIDs(ec.Ctx, ids, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "list annotation replies: " + err.Error()}
	}
	recent, err := t.replyRepo.ListRecentByAnnotationIDs(ec.Ctx, ids, ec.UserID, annotationRepliesPreview)
	if err != nil {
		return &Result{Success: false, Error: "list annotation replies: " + err.Error()}
	}

	type replyItem struct {
		Role    string `json:"role"`
		Content string `json:"content"`
		Created string `json:"created_at"`
	}
	type item struct {
		ID               string      `json:"id"`
		Quote            string      `json:"quote"`
		Note             string      `json:"note"`
		Orphaned         bool        `json:"orphaned"`
		Created          string      `json:"created_at"`
		ReplyCount       int64       `json:"reply_count"`
		LastReplyRole    string      `json:"last_reply_role"`
		LastReplyAt      string      `json:"last_reply_at"`
		Replies          []replyItem `json:"replies"`
		RepliesTruncated bool        `json:"replies_truncated"`
	}
	items := make([]item, 0, len(list))
	for _, a := range list {
		reps := recent[a.ID]
		replyItems := make([]replyItem, 0, len(reps))
		for _, rep := range reps {
			replyItems = append(replyItems, replyItem{
				Role:    rep.Role,
				Content: rep.Content,
				Created: rep.CreatedAt.Format("2006-01-02 15:04"),
			})
		}
		// recent 按时间正序，末条即最新回复；无回复时留空
		lastRole, lastAt := "", ""
		if n := len(reps); n > 0 {
			lastRole = reps[n-1].Role
			lastAt = reps[n-1].CreatedAt.Format("2006-01-02 15:04:05")
		}
		items = append(items, item{
			ID:               a.ID,
			Quote:            a.Quote,
			Note:             a.Note,
			Orphaned:         a.Orphaned,
			Created:          a.CreatedAt.Format("2006-01-02 15:04"),
			ReplyCount:       counts[a.ID],
			LastReplyRole:    lastRole,
			LastReplyAt:      lastAt,
			Replies:          replyItems,
			RepliesTruncated: counts[a.ID] > int64(len(replyItems)),
		})
	}

	hint := "note 为空表示用户只划了线没写批注；orphaned 为 true 表示原文已被改写、这条划线已失活；replies 是该批注下已有的讨论；last_reply_role 是该批注最新一条回复的角色"
	if req.Pending {
		hint += "。pending=true 只返回「等待 AI 回复」的批注（note 非空，且没回复或最新一条回复是用户），按最近活跃倒序，用于批量回复 / 继续回应用户追问。注意：只有用户在批注抽屉里的追问会进入批注线程；在主聊天里的追问不会写入线程，因此不在此清单体现"
	}

	data, _ := json.Marshal(map[string]interface{}{
		"annotations": items,
		"total":       total,
		"hint":        hint,
	})
	return &Result{Success: true, Data: data}
}

// pendingAnnotations 返回「等待 AI 回复」的批注：note 非空，且没有回复、或最新一条回复是用户。
// 覆盖「新批注待首答」与「用户追问后待续答」两种场景；按最近活跃（最新回复时间，无回复则批注创建时间）倒序，再分页。
// ponytail: 全量扫描（受单文档批注配额 annotationScanLimit 约束）后在内存过滤，避免 MySQL/SQLite 方言相关 SQL。
func (t *DocumentTool) pendingAnnotations(ec *ExecuteContext, docID string, limit, offset int) ([]model.DocumentAnnotation, int64, error) {
	all, totalAll, err := t.annoRepo.List(ec.Ctx, docID, ec.UserID, annotationScanLimit, 0)
	if err != nil {
		return nil, 0, err
	}
	// 扫描达到上限说明批注数已越过配额（历史/导入数据，或配额被上调），待回复清单会漏项。
	// 这里是有意降级，但必须留痕，不能静默截断。
	if totalAll > int64(len(all)) {
		log.Printf("pendingAnnotations: 扫描达上限 doc=%s user=%s scanned=%d total=%d，待回复清单可能漏项", docID, ec.UserID, len(all), totalAll)
	}
	ids := make([]string, 0, len(all))
	for _, a := range all {
		ids = append(ids, a.ID)
	}
	latest, err := t.replyRepo.LatestByAnnotationIDs(ec.Ctx, ids, ec.UserID)
	if err != nil {
		return nil, 0, err
	}

	activity := func(a model.DocumentAnnotation) time.Time {
		at := a.CreatedAt
		if rep, ok := latest[a.ID]; ok {
			at = rep.CreatedAt
		}
		return at
	}
	pending := make([]model.DocumentAnnotation, 0, len(all))
	for _, a := range all {
		if strings.TrimSpace(a.Note) == "" {
			continue // 纯划线不回复
		}
		if rep, ok := latest[a.ID]; ok && rep.Role != "user" {
			continue // 最新一条已是 AI 回复 → 等用户，不待回复
		}
		pending = append(pending, a)
	}
	sort.SliceStable(pending, func(i, j int) bool {
		return activity(pending[i]).After(activity(pending[j]))
	})

	total := int64(len(pending))
	if limit == 0 || offset >= len(pending) {
		return []model.DocumentAnnotation{}, total, nil
	}
	end := offset + limit
	if end > len(pending) {
		end = len(pending)
	}
	return pending[offset:end], total, nil
}

// replyAnnotation — 向某条批注写入一条 AI 回复（指令/批量路径，Source=chat）。
// 校验批注归属 / content 长度 / 回复数上限（与按钮路径同一套配额）。
func (t *DocumentTool) replyAnnotation(ec *ExecuteContext, args json.RawMessage) *Result {
	var req struct {
		AnnotationID string `json:"annotation_id"`
		Content      string `json:"content"`
	}
	if err := decodeArgs(args, &req); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.AnnotationID == "" || req.Content == "" {
		return &Result{Success: false, Error: "annotation_id and content required"}
	}
	if utf8.RuneCountInString(req.Content) > maxAnnotationReplyLen {
		return &Result{Success: false, Error: fmt.Sprintf("回复超长（≤%d 字）", maxAnnotationReplyLen)}
	}

	anno, err := t.annoRepo.GetByID(ec.Ctx, req.AnnotationID, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "annotation not found"}
	}
	// ponytail: 该配额校验与按钮路径的 AnnotationReplier.tryKeyedMutex 是两套锁，不互串；
	// 「聊天工具回复 + 按钮回复」并发时最多超出上限一个并发数。量小且两端共用同一上限，先接受。
	n, err := t.replyRepo.CountByAnnotation(ec.Ctx, req.AnnotationID, ec.UserID)
	if err != nil {
		return &Result{Success: false, Error: "count replies: " + err.Error()}
	}
	if n >= maxRepliesPerAnnotation {
		return &Result{Success: false, Error: "该批注回复数量已达上限"}
	}

	rep := model.DocumentAnnotationReply{
		ID:           idgen.Next(),
		AnnotationID: req.AnnotationID,
		DocumentID:   anno.DocumentID,
		UserID:       ec.UserID,
		Role:         "assistant",
		Content:      req.Content,
		Source:       "chat",
	}
	if err := t.replyRepo.CreateBatch(ec.Ctx, []model.DocumentAnnotationReply{rep}); err != nil {
		return &Result{Success: false, Error: "save reply: " + err.Error()}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"id":            rep.ID,
		"annotation_id": rep.AnnotationID,
		"created_at":    rep.CreatedAt.Format("2006-01-02 15:04:05"),
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

	list, total, err := t.docRepo.List(ec.Ctx, ec.UserID, req.Tag, req.Keyword, nil, "", limit, offset)
	if err != nil {
		return &Result{Success: false, Error: "query documents: " + err.Error()}
	}

	result := make([]map[string]interface{}, 0, len(list))
	for _, d := range list {
		full := map[string]interface{}{
			"id":         d.ID,
			"title":      d.Title,
			"tags":       d.Tags,
			"audience":   d.Audience,
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

	data, _ := json.Marshal(map[string]interface{}{"id": req.ID, "message": "文档已移入回收站（7 天内可恢复）"})
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

	list, total, err := t.docRepo.List(ec.Ctx, ec.UserID, "", "", nil, "", limit, offset)
	if err != nil {
		return &Result{Success: false, Error: "list documents: " + err.Error()}
	}

	result := make([]map[string]interface{}, 0, len(list))
	for _, d := range list {
		full := map[string]interface{}{
			"id":         d.ID,
			"title":      d.Title,
			"tags":       d.Tags,
			"audience":   d.Audience,
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
	tags, err := t.docRepo.ListTags(ec.Ctx, ec.UserID, "")
	if err != nil {
		return &Result{Success: false, Error: "list tags: " + err.Error()}
	}
	data, _ := json.Marshal(map[string]interface{}{"tags": tags})
	return &Result{Success: true, Data: data}
}
