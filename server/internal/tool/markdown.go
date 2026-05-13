package tool

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MarkdownTool — 创建/追加 Markdown 文件，支持下载
type MarkdownTool struct {
	dir string // data/markdown/
}

// NewMarkdownTool 创建 Markdown 工具
func NewMarkdownTool(dir string) *MarkdownTool {
	os.MkdirAll(dir, 0755)
	return &MarkdownTool{dir: dir}
}

func (t *MarkdownTool) Name() string { return "markdown" }
func (t *MarkdownTool) Description() string {
	return "创建或追加 Markdown 文档，生成下载链接"
}

func (t *MarkdownTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "create",
			Description: "创建新的 Markdown 文档，返回下载链接",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title":   map[string]interface{}{"type": "string", "description": "文档标题（也用作文件名）"},
					"content": map[string]interface{}{"type": "string", "description": "Markdown 格式的文档内容"},
				},
				"required": []string{"title", "content"},
			},
		},
		{
			Name:        "append",
			Description: "追加内容到已有的 Markdown 文档",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"file_id": map[string]interface{}{"type": "string", "description": "create 返回的 file_id"},
					"content": map[string]interface{}{"type": "string", "description": "要追加的 Markdown 内容"},
				},
				"required": []string{"file_id", "content"},
			},
		},
	}
}

func (t *MarkdownTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "create":
		return t.create(args)
	case "append":
		return t.append(args)
	default:
		return &Result{Success: false, Error: "unknown function: " + funcName}
	}
}

var safeSlugRe = regexp.MustCompile(`[^\p{Han}\w\-.]`)

func safeSlug(s string) string {
	s = safeSlugRe.ReplaceAllString(s, "")
	if len([]rune(s)) > 80 {
		s = string([]rune(s)[:80])
	}
	return strings.TrimSpace(s)
}

func (t *MarkdownTool) create(args json.RawMessage) *Result {
	var req struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if req.Title == "" || req.Content == "" {
		return &Result{Success: false, Error: "title and content required"}
	}

	slug := safeSlug(req.Title)
	if slug == "" {
		slug = "document"
	}
	fileID := uuid.New().String()[:8] + "-" + slug
	filename := fileID + ".md"
	path := filepath.Join(t.dir, filename)

	if err := os.WriteFile(path, []byte(req.Content), 0644); err != nil {
		return &Result{Success: false, Error: "write file: " + err.Error()}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"file_id":  fileID,
		"filename": slug + ".md",
		"url":      "/api/files/" + filename,
		"message":  "文档已创建，点击链接下载",
	})
	return &Result{Success: true, Data: data}
}

func (t *MarkdownTool) append(args json.RawMessage) *Result {
	var req struct {
		FileID  string `json:"file_id"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if req.FileID == "" || req.Content == "" {
		return &Result{Success: false, Error: "file_id and content required"}
	}

	filename := req.FileID + ".md"
	path := filepath.Join(t.dir, filename)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return &Result{Success: false, Error: "file not found: " + req.FileID}
		}
		return &Result{Success: false, Error: "open file: " + err.Error()}
	}
	defer f.Close()

	if _, err := f.WriteString("\n\n" + req.Content); err != nil {
		return &Result{Success: false, Error: "write file: " + err.Error()}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"file_id": req.FileID,
		"message": "内容已追加",
	})
	return &Result{Success: true, Data: data}
}

// CleanOldMarkdownFiles 清理超过 maxAge 的 markdown 文件
func CleanOldMarkdownFiles(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(dir, e.Name())
			os.Remove(path)
			log.Printf("[markdown] cleaned: %s", e.Name())
		}
	}
}
