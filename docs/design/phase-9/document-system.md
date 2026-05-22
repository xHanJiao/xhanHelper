# 文档系统

## 背景

用户发现「笔记」概念存在定义脑裂：

- **笔记 (Note)**：Agent 在对话中主动记录的碎片化记忆点，带向量嵌入，注入 LLM 上下文，是上下文工程的一部分
- **文档 (Document)**：用户明确要求 LLM 生成的、给人看的、结构化的完整 Markdown 文章

原有的 Markdown 下载工具（`data/markdown/*.md` 临时文件 + `/api/files/:filename` 下载链接）功能不足：临时文件 1 小时后清理，无网页阅读入口，无标签组织，无持久化。

本文档系统**取代**旧 Markdown 下载工具。

## 决策

| 维度 | 决定 |
|------|------|
| 概念边界 | 笔记 = LLM 上下文碎片；文档 = 给人看的完整结构化文章 |
| 旧 markdown 工具 | 淘汰、删除 |
| 创建者 | 仅 LLM tool，网页只读 |
| 元数据 | 标题、标签、所属会话(可选)、创建/更新时间 |
| 修改 | LLM 可编辑 |
| 存储 | DB TEXT 字段 |
| 向量化 | 暂不需要 |
| LLM 触发规则 | 用户明确要求才调用，不走主动记录。规则写在 tool description 里 |
| 前端 UI | 卡片列表（复用 ListItemCard），点击展开阅读 |

## 数据模型

```go
type Document struct {
    ID             string     `gorm:"primaryKey;size:36" json:"id"`
    UserID         string     `gorm:"index;size:36;not null" json:"user_id"`
    Title          string     `gorm:"size:500;not null" json:"title"`
    Content        string     `gorm:"type:text;not null" json:"content"`
    Tags           string     `gorm:"size:500" json:"tags"`
    ConversationID *string    `gorm:"size:36" json:"conversation_id,omitempty"`
    CreatedAt      time.Time  `json:"created_at"`
    UpdatedAt      time.Time  `json:"updated_at"`
}
```

## LLM Tool

工具名 `"document"`，四个函数：

| 函数 | 说明 | 参数 |
|------|------|------|
| `create_document` | 创建文档（仅用户明确要求时） | title(必填), content(必填,markdown), tags(可选) |
| `update_document` | 修改已有文档 | id(必填), title, content, tags（部分更新） |
| `query_documents` | 查询文档列表 | tag(可选), keyword(可选) |
| `delete_document` | 删除文档 | id(必填) |

Tool Description 划线：
> 「仅在用户明确要求生成/保存文章、报告、总结等结构化文档时使用。**不要**主动创建。日常信息记录请用 note 工具。」

## API

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/documents?tag=&keyword=&limit=&offset=` | 分页列表 |
| `GET` | `/api/documents/:id` | 单篇详情 |
| `DELETE` | `/api/documents/:id` | 删除 |

无需 create/update API（LLM tool 完成）。

## 前端

- 路由 `/documents`，单个页面即可
- 复用 `ListItemCard` 组件，新增 `contentType` prop（`'text'` | `'markdown'`），markdown 时用 `markdown-it` 渲染
- 点击卡片展开，全文阅读，不需要单独详情页
- 布局参考 `NotesView`

## 变更清单

### 后端

| 操作 | 文件 |
|------|------|
| 新增 | `server/internal/model/document.go` |
| 新增 | `server/internal/service/document_repo.go` |
| 新增 | `server/internal/tool/document.go` |
| 新增 | `server/internal/controller/document.go` |
| 修改 | `server/internal/controller/router.go` |
| 修改 | `server/cmd/xhan-helper/main.go` |
| 删除 | `server/internal/tool/markdown.go` |
| 删除 | `server/internal/controller/markdown_download.go` |

### 前端

| 操作 | 文件 |
|------|------|
| 修改 | `front-end/src/components/ListItemCard.vue` |
| 新增 | `front-end/src/views/DocumentsView.vue` |
| 修改 | `front-end/src/router/` |
