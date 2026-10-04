# 文档配图（用用户上传的图）— 方案

> 状态：**已实现**（服务端 / Web / macOS / iOS 渲染 + ImageListView 上传与描述）
> 范围：server（Go）+ macOS 客户端（SwiftUI）+ Web 前端（Vue）+ nginx
> 关联：图床基建来自 `../multimodal-images.md`（已实现，聊天图片输入）；总图景见 `../document-images.md`（已定稿，图表/搬运图尚未实现）。**本文只做"用户上传的图能进文档"这一件事。**

---

## 一、一句话

用户上传的图（现在只能从聊天里传）会进图片列表，以后能直接写进文档，全端能显示、能分享、能导 PDF。

---

## 二、要解决的真问题

现在文档只能放 mermaid 图。想要的是：

1. 用户把一张图传上来（聊天里传，或图片列表里传）；
2. 这张图出现在**图片列表**里，有一句**文字描述**；
3. 让 LLM 挑一张图写进文档（`![](/images/<id>.png)`）；
4. macOS、Web、分享页、PDF 都能看到这张图。

**为什么非要"描述"？** 因为让 LLM 看图很贵。

- 每张图最多约 1024 token，且图片 token 不吃文本前缀缓存；
- 如果 LLM 每次挑图都得逐张识图，N 张就是 N×1024；
- 而历史重放（最近 10 轮带图消息）会反复烧这些 token。

有了描述，LLM 读文字就能筛图（几乎零成本），只在真正需要"看细节"时才把图喂给模型。

> 本期**不**用描述替换历史重放里的图（那会让模型没法回答"图里第三行写了啥"这类追问），只给"列表挑图"用。

---

## 三、现状（已核实）

| 东西 | 现状 |
|------|------|
| 图床 `ImageAsset` + 上传/读/列表/删除/清理 | **已有**，但只服务聊天：`source=chat`、`visibility=private` |
| `ImageSourceDoc`、`ImageVisibilityPublic` | 代码里预留了，**从没被用过** |
| 图片描述 | **没有**这个字段 |
| `image` 工具（让 LLM 列图） | **没有** |
| 文档写入 | 只能 LLM 工具写（`document.create_document`/`update_document`），内容原样存 Markdown，不做任何图校验 |
| 全端渲染 | 基本不通：macOS WKWebView `baseURL: nil`、iOS 无 `imageBaseURL`、Web 无 `/images` 代理、nginx 无 `/images/`、PDF 不重写 URL |

---

## 四、拍板的四个决定

| # | 问题 | 结论 |
|---|------|------|
| D1 | 描述啥时候生成 | **上传后异步生成**。落库后起后台任务调一次视觉模型，失败只记日志（best-effort）。 |
| D2 | 图片可见性 | **被文档引用时自动转 public**。聊天图继续私有，只有真写进文档的才公开可寻址。 |
| D3 | LLM 怎么接触图片列表 | **新增 `image` 工具 `list_images`**，返回描述 + markdown URL + 分页。 |
| D4 | 附加范围 | **ImageListView 显示描述** + **ImageListView 加上传按钮**。 |

---

## 五、流程

```
用户传图（聊天 / 图片列表）
   │
   ▼
POST /api/images/upload  → 落盘 + 写 ImageAsset(private, chat)
   │
   ├─ 后台异步：EnsureFileID → 视觉模型 → 回写 Description
   │
   ▼
LLM 调 image.list_images → 看到 id + 描述 + markdown URL
   │
   ▼
LLM 写文档：![](/images/<id>.<ext>)
   │
   ▼
document 工具保存时扫描引用：
   校验 owner → 升级 public + source=doc + referenced=true + 刷新 last_used_at
   │
   ▼
全端渲染（macOS / iOS / Web / 分享页 / PDF）
```

---

## 六、具体做什么

### 6.1 服务端

**描述**
- `model/image_asset.go` 加两列：`Description string`（描述文本）、`DescriptionAt *time.Time`（已尝试过的时间，用来避免失败图被无限重试）。
- `service/image_store.go`：照现有的 `FileUploader` 范式，加一个**可选**依赖接口 `ImageDescriber`（nil 就跳过）。`Upload` 落库后起 goroutine 调它。
- `agent/` 里实现 `ImageDescriber`：`EnsureFileID` 拿到 `file_id` → `ChatSync` 问"用 1-2 句中文描述这张图" → 回写。走 `llmcost` 场景记账。

**`image` 工具**
- 新增 `tool/image.go`：`image.list_images`，返回 `id / description / markdown(![]/images/<id>.<ext>) / source / created_at`，分页；`ReadOnlyFuncs` 含它；实现 `CatalogProvider`；在 `appinit/tools.go` 注册。

**文档引用校验 + 升级**
- `tool/document.go` 的 create/update 落库前，扫描最终内容里的 `![...](/images/<uuid>.(png|jpg|jpeg|gif|webp))`：
  - 有效且属于本人 → 升级 `public + source=doc + referenced=true + last_used_at=now`；
  - 无效 id → **只记日志，不硬失败**（不打断文档保存）。

**清理豁免**
- `image_store.go::Cleanup` 跳过 `source=doc`。文档图会被 15 天 TTL 误删，必须豁免。
  - `ponytail:` 文档删除后不回收其图片（孤儿检测不做）。个人图床，磁盘增长可接受；真要回收再加"扫全库引用"。

### 6.2 全端渲染（引用一律用 `/images/<id>.<ext>`）

- **macOS** `DocumentReaderView.swift`：`ReaderHTML.build` 把 `](/images/` 重写为绝对 `baseURL`；改模板后跑 `scripts/reader-template.py` + 两个自查脚本（`check-reader-html.sh` / `snapshot-reader.sh`）。
- **Web**：`DocumentsView`/`ListItemCard`/`SharedDocumentView` 加 `img{max-width:100%}`；`vite.config.js` 加 `/images` 代理；`useExportPDF.js` 重写绝对 URL。
- **nginx**：`server/nginx-helper.conf` 加 `/images/` location（对齐 `document-images.md` §9）。
- **PDF**：`DocumentPDFExporter.swift` 相对路径重写为绝对。
- **iOS**：只补 `Markdown(..., imageBaseURL:)` 一行渲染修复，保持可编译，不加新功能。

### 6.3 Swift 客户端

- `ImageListView`：显示 `description`；加"上传"按钮（复用 `APIClient.uploadImage` + `ImageCompressor`）。
- `APIClient.swift`：`ImageAssetSummary` 补 `description` 字段解码。

---

## 七、验收

1. 服务端 `go build ./... && go test ./...` 全绿，补描述生成/引用升级的单测。
2. Web 改渲染则 `vite build`。
3. 真机（你在 Xcode 验证）：
   - ① 传一张图 → 稍后图片列表出现描述；
   - ② 让 LLM 用 `image.list_images` 找到它并写进文档；
   - ③ macOS / Web / 分享页 / PDF 都能看到图；
   - ④ 聊天图（没进文档）仍是 private，不能匿名访问。

---

## 八、明确不做

- ❌ 用描述替换历史重放的图（另做决策）
- ❌ 图表入档 / 搬运图 / 截图工具（`document-images.md` 的 v1，另立任务）
- ❌ 多模态 embedding、AI 生图、票据 OCR
- ❌ 文档删除后回收图片
- ❌ iOS 新功能（仅保持可编译）

---

## 九、风险

| # | 风险 | 兜底 |
|---|------|------|
| 1 | 清理豁免 `source=doc` 改动影响聊天图清理 | 单测覆盖两类保留期，确认不误伤 |
| 2 | 描述生成失败（模型/文件异常）会反复重试 | `DescriptionAt` 非空即视为已尝试；失败率高再加次数上限 |
| 3 | `ChatSync` 带 `file_id` 的真实返回格式不确定 | 先写一条探针验证，再定解析写法 |
| 4 | 相对路径在各渲染端解析不一致 | 引用统一 `/images/<id>.<ext>`，各端各自重写为绝对 URL |
| 5 | iOS 无法在 agent 侧编译验证 | 只保证源码可编译，真机编译归你 |

---

## 十、粗估

- **面**：8（服务端 4 + 渲染 3 + 客户端 1）
- **缝**：3（描述异步接线落点、WKWebView 相对→绝对方案、文档引用解析边界）
- **探针**：1（`ChatSync` 带 `file_id` 的描述返回格式）
- **真机往返**：2-3（macOS reader、ImageListView、iOS 编译）
- **待拍决定**：0
- **区间**：约 5–8 小时（不含你真机验证）

---

## 后续变更

- **2026-10-04**：macOS 阅读器补「点图放大」。`DocumentReaderView` 的 JS 对 `#content` 里 `<img>` 点击回传 `{type:'image', src}`，原生 present `DocumentImageLightbox`（data URI / http URL 都接，复用 `AttachmentPreviewView`）。mermaid 图同样可点（按当前主题那张）。属旧 MarkdownUI 路径点击放大能力的回归补齐，详见 `docs/bugs/026-macos-doc-image-no-zoom.md`。影响文件：`client/xHelper/xHelper/Shared/Views/DocumentReaderView.swift`、`client/xHelper/scripts/check-reader-html.sh`。
