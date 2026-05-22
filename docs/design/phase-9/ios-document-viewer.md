# iOS 客户端文档查看功能 + 后端响应格式统一

## 背景

Phase 8 已新增文档系统（`Document`），LLM 可通过 `create_document` 工具生成 Markdown 文档，Web 前端可通过 `/documents` 路由浏览。现在需要在 iOS 客户端也支持浏览文档。

同时发现后端 Document API 响应格式不统一，一并修复。

## 设计决策

| # | 决策点 | 选择 | 理由 |
|---|--------|------|------|
| 1 | Markdown 渲染 | MarkdownUI (Swift Package) | 原生 SwiftUI 组件，支持标准 Markdown 语法，无 WebView 开销 |
| 2 | 后端响应格式 | 统一用 `view.JSONOK` 包装 | 与 Notes/Memories 等 API 保持一致，iOS APIClient 统一解析 |
| 3 | 导航模式 | 双层 push (List → Detail) | 文档内容长，需独立详情页 + 返回按钮 |
| 4 | 列表行内容 | 标题 + 标签 + 更新日期 | 纯文本预览可读性差，保持简洁 |
| 5 | 搜索/筛选 | iOS 不加 | 跟 NoteListView 一致，文档量不大 |
| 6 | 分页加载 | 文档列表 + 笔记列表都加分页 | 现有 NoteListView 只拉 50 条不翻页（一并修复） |
| 7 | 删除 | 左滑 `.swipeActions` 确认删除 | 后端 API 已支持，前端也有删除 |
| 8 | macOS | Shared 代码顺带支持 | View 放 Shared/ 目录，不出明显 bug 不管 |

## 实施范围

### 后端

**`server/internal/controller/document.go`** — 响应格式统一

- `ListDocuments`: `c.JSON(http.StatusOK, gin.H{...})` → `view.JSONOK(c, gin.H{...})`
- `GetDocument`: `c.JSON(http.StatusOK, doc)` → `view.JSONOK(c, doc)`
- `DeleteDocument`: `c.JSON(http.StatusOK, gin.H{"deleted": true})` → `view.JSONOK(c, gin.H{"deleted": true})`

### Web 前端

**`front-end/src/views/DocumentsView.vue`** — 适配响应包装

- 后端改为 `view.JSONOK` 后，`res.documents` 需改为 `res.data.documents`（视前端 API 层拦截器行为）

### iOS

**`client/xHelper/xHelper/Shared/Services/APIClient.swift`**

- 新增 `Document` 模型（id, user_id, title, content, tags, conversation_id, created_at, updated_at）
- 新增 `DocumentListResponse` 模型
- 新增 `listDocuments(tag:keyword:limit:offset:)` → GET `/api/documents`
- 新增 `getDocument(id:)` → GET `/api/documents/:id`
- 新增 `deleteDocument(id:)` → DELETE `/api/documents/:id`

**`client/xHelper/xHelper/Shared/Views/DocumentListView.swift`**（新增）

- NavigationStack push 列表页
- 每行显示：标题 + 标签 + 更新日期
- 上拉加载更多（分页 offset/limit）
- 左滑 `.swipeActions` 删除（带确认）
- 点击行 push 到 DocumentDetailView

**`client/xHelper/xHelper/Shared/Views/DocumentDetailView.swift`**（新增）

- MarkdownUI 渲染 `Document.content`
- NavigationStack 自动提供返回按钮
- 标题显示在 navigationTitle

**`client/xHelper/xHelper/Shared/Views/SettingsView.swift`**

- 「笔记记录」NavigationLink 后新增「文档」NavigationLink
- 使用 `doc.text` SF Symbol

**`client/xHelper/xHelper/Shared/Views/NoteListView.swift`**

- 补分页加载更多（上拉触发 offset 递增）

### 依赖添加

- iOS 项目添加 Swift Package: `https://github.com/gonzalezreal/swift-markdown-ui` (MarkdownUI)

## API 响应格式

### 修改前（Document API 裸返回）

```json
GET /api/documents → {"documents": [...], "total": N}
GET /api/documents/:id → {"id":"...","title":"...","content":"...",...}
DELETE /api/documents/:id → {"deleted": true}
```

### 修改后（统一包装，与其他 API 一致）

```json
GET /api/documents → {"status":"ok","errorCode":0,"errMsg":"","data":{"documents":[...],"total":N}}
GET /api/documents/:id → {"status":"ok","errorCode":0,"errMsg":"","data":{"id":"...","title":"...","content":"...",...}}
DELETE /api/documents/:id → {"status":"ok","errorCode":0,"errMsg":"","data":{"deleted":true}}
```

## 导航流

```
SettingsView
  └─ 「文档」NavigationLink
       └─ DocumentListView (push)
            ├─ 列表行: 标题 + 标签 + 日期
            ├─ 上拉加载更多
            ├─ 左滑删除
            └─ 点击行 → DocumentDetailView (push, 自带返回)
                           └─ MarkdownUI 渲染全文
```
