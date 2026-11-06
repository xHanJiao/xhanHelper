# M2 · 聊天成品化、灵阅、图片

> 前置：`01-m1-shell-mobile.md`
> 目标：让聊天像 macOS 一样有卡片、能跳文档、能发照片、能看灵阅。
> 预估：14–20h（拆成三段）

## 〇、分册与状态（2026-11-01）

| 段 | 内容 | 状态 |
|---|---|---|
| **M2a** 输入框补齐 | 选图 / 粘贴 / 拖拽 → 压缩 → 上传 → 托盘 → 发送 → 历史图片条 + 大图 | ✅ 已实现 |
| **M2b** 卡片与灵阅 | 灵阅卡 + 多卡横排 `＋N`、`done.artifacts`、灵阅列表/详情（分享/删除）、文档最小阅读页、Markdown 内链/表格 | ✅ 已实现（v1） |
| **M2c** 会话开关 | 计划模式 pill、无痕 pill（新会话 `init_mode`/`ephemeral`，已有会话 PATCH） | ✅ 已实现 |

图片链路被提到卡片之前：手机端发照片是最常用的真实动作，比卡片更核心。

M2b 的 v1 只做「能看见、能点开」；Markdown 代码块复制已于 2026-11-02 补齐。
仍未做：消息卡片里没有图片/文件卡（只有灵阅卡），灵阅详情没有标签词云。

### M1 输入框对照 macOS 的取舍

| macOS 按钮 | Web | 说明 |
|---|---|---|
| 附件 `attach` | ✅ 回形针 | iOS 上 `accept="image/*"` 自带「照片图库 / 拍照 / 选择文件」 |
| 计划模式 `mode` | ✅ | 服务端已有 `PATCH /conversations/:id/mode` |
| 无痕 `ephemeral` | ✅ | 新会话走 `ephemeral`，已有会话走 PATCH（默认会话禁止，服务端 400 透出原因） |
| 完全信任 `trust` | ❌ | 本地 fs/shell 专属，Web 无对应能力 |
| 模型 / 精炼 / 接力 | ❌ | macOS 端已下架（`visibility: .hidden`） |
| 上下文占用圆环 | ❌ | 明确不做（见 `ui-decisions.md`） |

## 一、范围

- 消息卡片：文档引用卡、灵阅卡、图片缩略图条
- 一轮多卡横排；单卡保持整卡
- Markdown 增强：linkify、外链、内链、图片、表格、代码复制
- 灵阅：列表 → 详情 iframe → 删除/分享
- 图片：选图/拍照/粘贴 → 压缩 → 上传 → 发送；历史图片渲染 + Lightbox
- 流式态：停止、回到最新、底部渐隐、工具/校验 pill、错误态
- 后端：`done` 事件带本轮 artifacts

## 二、数据契约

### 消息字段（已有）

`GET /api/conversations/:id/messages-v2` 返回的消息含：

```json
{
  "id": "...",
  "role": "assistant",
  "content": "...",
  "artifacts": [
    { "id": "...", "type": "html", "title": "10.18 手账", "summary": "..." }
  ],
  "image_ids": "[\"img1\",\"img2\"]"
}
```

- `artifacts` 由控制器 `attachArtifacts` 批量展开；正文不在消息里。
- `image_ids` 是 JSON 数组字符串，只出现在 user 消息。

### SSE

- `tool_result` 已经带 `{artifact_id,title,summary}`，可用于流式中先插卡。
- `done` 目前只有 `conversation_id/message_id/user_message_id/memory_ids/note_ids/context_tokens/context_window`。
- **M2 增加**：`done.artifacts` 为完整 refs（`id/type/title/summary`），流结束即可渲染，不用等第二次请求。

实现要点：

| 文件 | 改动 |
|---|---|
| `server/internal/agent/orchestrator.go` | `artifactRefsFromResults` 从工具结果里补 `Title/Summary`；`emitDone` 增加 `artifacts` 参数并输出 `"artifacts":[...]` |
| 同上 | `SetArtifactsJSON` 落库时只写 `{id,type}`，不要冗余标题摘要（保持现有注释的约定） |
| `server/internal/controller/chat.go` | 不用改；done 原样转发 |
| `front-end/src/composables/useChatSSE.js` | 收到 `done` 后用 `d.artifacts` 挂到最终 assistant 消息；没有则回退到 `messages-v2?limit=1` 合并 |

### 灵阅接口（已有）

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/artifacts?limit=&offset=` | 列表 |
| GET | `/api/artifacts/tags` | 标签 |
| GET | `/api/artifacts/:id` | 单条（含 html） |
| GET | `/api/artifacts/:id/html` | 裸 HTML（带 ETag/CSP） |
| DELETE | `/api/artifacts/:id` | 删除 |
| POST | `/api/artifacts/batch-delete` | 批量删除 |
| POST/DELETE | `/api/artifacts/:id/share` | 分享/撤销 |

### 图片接口（已有）

| 方法 | 路径 | 用途 |
|---|---|---|
| POST | `/api/images/upload` | multipart `file` 或裸字节；返回 `{image_id,url,mime,bytes}` |
| GET | `/api/images/:id/file` | 鉴权读图，**不能直接给 `<img src>`** |
| GET | `/api/images?limit=&offset=` | 图片库 |
| DELETE | `/api/images/:id` | 删除 |

## 三、组件拆分

```
components/
├── chat/
│   ├── MessageCard.vue          # 通用卡片壳（标题/摘要/整卡可点）
│   ├── DocumentRefCard.vue      # 文档引用
│   ├── ArtifactCard.vue         # 灵阅引用
│   ├── CardStrip.vue            # 多卡横排 + “＋N” + 自动收起
│   ├── MessageImageStrip.vue    # 用户消息图片条
│   ├── MarkdownRenderer.vue     # markdown-it 统一配置
│   └── ThinkingBlock.vue        # 复用现有
├── media/
│   ├── AuthImage.vue            # fetch + blob URL
│   ├── Lightbox.vue             # 图片大图
│   └── AttachmentTray.vue       # 草稿图片托盘
└── artifact/
    ├── ArtifactGrid.vue         # 列表卡片
    └── ArtifactFrame.vue        # iframe sandbox + srcdoc
```

### 卡片尺寸

对齐 macOS：

- 单卡宽 220（窄屏 200），固定高 96，圆角 11，底 `--input`。
- 标题 13.5px semibold，1 行；摘要 11.5px，`--text` 60%，最多 2 行（卡片不允许被摘要撑高）。
- 整卡可点，0.5px 描边；hover 提亮背景并加深描边，不加位移。
- 一轮多卡：固定 220×96 横排，塞不下时最后显示窄的「＋N」；点开展开成横向可滑，**8 秒后自动收起**。
- 自动收起实现：展开时启动 8s 定时器；鼠标进入条子、焦点在条内或正在横向拖动时暂停，离开后重新计时。收起不改变滚动位置。
- 用户消息用**气泡**（浅色淡绿底 / 深色淡绿底、圆角 16），不是 macOS 的纯文本；长消息仍可长按选中。

## 四、Markdown

`markdown-it` 配置：

```js
new MarkdownIt({
  html: false,
  breaks: false,
  linkify: true,
  typographer: false
})
```

自定义规则：

1. `link_open`：
   - `xhanhelper://document/<id>` → `router.push('/documents/' + id)`，不打开新窗。
   - 其他 `http/https` → `target="_blank" rel="noopener noreferrer"`。
2. `image`：
   - `image:<id>` 或 `/api/images/...` → 用 `AuthImage` 渲染；Markdown-it 默认 `<img>` 不带鉴权头，必须替换成组件。
   - 普通远程图片保留，但加 `loading="lazy"`。
3. `table`：外面包 `.table-wrap { overflow-x: auto; }`。
4. `fence`：加复制按钮；代码块横向滚动。
5. `linkify`：裸 URL 可点。

样式：

- 图片 `max-width: 100%; height: auto; border-radius: 8px`。
- 表格 `border-collapse; min-width: 480px`（窄屏横滑）。
- 行内代码 `--tool` 底，代码块 `--input` 底，圆角 8。
- 链接颜色 `--app-accent`，下划线只在 hover。

## 五、链接跳转

两条路都要做：

1. **卡片**：`DocumentRefCard` 点击 → `/documents/:id`（手机全屏页；桌面也全屏页，因为不做右栏）。
2. **正文内链**：模型写 `[标题](xhanhelper://document/<id>)` → 上面的 markdown 规则拦截。

文档页在 M2 只做最小阅读：

- 顶部：返回、标题、分享/下载。
- 正文：`MarkdownRenderer` 渲染 `doc.content`。
- 目录/批注放 M3。

## 六、灵阅

### 列表页 `/artifacts`

- 顶栏：标题「灵阅」、搜索、标签筛选（M3 可加词云）。
- 卡片网格：手机 1 列、桌面 2–3 列；卡片显示标题、摘要 2–3 行、标签、日期。
- 点卡片进详情；卡片右上 `⋯`：分享、删除。
- 分页 30 条，滚动加载或「加载更多」。

### 详情页 `/artifacts/:id`

- 顶部：返回、标题、分享、删除。
- 正文：`ArtifactFrame`。
- 加载：
  1. `fetch('/api/artifacts/:id/html', { headers: { Authorization } })`
  2. 拿文本 → `iframe srcdoc`
  3. `sandbox=""`（等同关 JS + 禁同源），`referrerpolicy="no-referrer"`
- 失败：错误态 + 重试，不白屏。
- 404：提示「该页面已被删除」，返回列表。
- 分享：`POST /api/artifacts/:id/share` → 拼 origin + `/api/share/artifacts/:token`；撤销走 DELETE。

### 流式卡片去重

- `tool_result` 带 `artifact_id` 时先插一张临时卡。
- `done.artifacts` 到了以后按 `artifact_id` 去重，只保留最终卡。
- 同一条消息多张卡按 id 去重。

## 七、图片（M2a，已实现）

### 发送

- 入口：输入区回形针、`paste` 事件、桌面拖拽进输入区。**不做**单独的相机按钮——iOS 的 `<input type="file" accept="image/*">` 已经会弹「照片图库 / 拍照 / 选择文件」。
- **选完立刻上传**（并发 2），发送时不用等；失败的单张在托盘里可重试。
  - 好处：发送瞬间不卡、失败可单张重试；没被引用的图由服务端 `image_orphan_ttl_days`（3 天）自动清。
- 压缩（`front-end/src/utils/image.js`），参数对齐 macOS `ImageCompressor`：
  - 长边 2048，JPEG 0.85，PNG 保留 PNG（alpha），GIF 原样不重编码（重编码只剩第一帧）。
  - 已经 ≤2048 且 ≤4 MiB 的 jpeg/png/webp 直接原样上传，省一次代损。
  - HEIC：Safari 能 `createImageBitmap` 解码 → 自动转 JPEG；桌面 Chrome 解不了时按原字节上传，由服务端按魔数拒绝并回中文原因（不做 WASM 解码）。
  - 压完反而更大时保留原图（除非原格式服务端不收）。
- 去重：压缩后字节的 SHA-256 前 16 字节；**LAN http 下 `crypto.subtle` 不可用**，退回 `name|size|lastModified`。
- 上限 20 张（超出给 warning，不静默丢），单图服务端上限 32 MiB。
- 只发图不写字也允许（服务端 `message` 与 `attachments` 至少一个）。

### 展示

- `normalizeMessage` 解析 `image_ids`（JSON 字符串）→ `imageIds`；本地刚发出的消息直接带 `imageIds` 数组，不额外请求。
- `MessageBubble` 在气泡内先渲染图片条再渲染正文：单图 168×168，多图 84×84 自动换行；正文为空时不渲染空 `<p>`。
- `AuthImage`：`/api/images/:id/file` 要 Bearer，先 `fetch` 成 blob 再 `URL.createObjectURL`；**不做跨组件缓存**（同一张图重复出现在一轮对话里的概率极低，省掉 revoke 引用计数），组件卸载时 revoke。
- `ImageLightbox`：`Teleport to body`，点击遮罩或 Esc 关闭。
- 发送后立即回收草稿的 objectURL，消息里的图改走鉴权接口读，避免一处泄漏。

### 已知限制

- 桌面 Chrome 选 HEIC 会走到「原字节上传 → 服务端拒绝」这一条；iPhone Safari 正常。
- 没有多图 `+N` 折叠（M2b 的卡片条才需要）；草稿托盘就是横滑。

## 八、流式态

复用现有 `useChatSSE`，补：

- `done` 后把最终消息落列表，清 streaming。
- `scrollToBottom`：用户离底超过 0.75 屏时显示浮钮；新消息/流式增量时只有贴底才自动滚。
- 底部 24px 渐隐 mask，避免内容硬裁在输入区下。
- 工具 pill：流式中显示 `tool_call`/`tool_result` 摘要；历史消息不显示（与 macOS 一致）。
- 校验 pill：`verify` 的 checking/done/retry。
- 错误态：`error` 事件显示在消息流末尾，带「重新加载」（SSE 断开不终止服务端，重拉通常能拿到完整结果）。
- 停止按钮先断本地 SSE，再调 `POST /api/chat/stop`；新会话首条消息流式时前端还没有 `conversation_id`，只能断流。
- `plan_done`/`subagent`/`client_tool_call`/`reminders_changed`：M2 先忽略，不报错；M3 再接计划便签/子任务。

## 九、后端小改

### done 带 artifacts（已实现）

`emitDone` 增加参数 `artifacts []model.ArtifactRef`，输出：

```json
{"conversation_id":"...","message_id":"...","user_message_id":"...","artifacts":[{"id":"...","type":"html","title":"...","summary":"..."}]}
```

- `artifactRefsFromResults` 解析 `title/summary`；同一轮 patch 同一页按 id 合并，标题取最后一次（`mergeArtifactRefs`）。
- 落库 `SetArtifactsJSON` 前用 `compactArtifactRefs` 剥掉 title/summary，只存 id/type。
- 单测：`artifactRefsFromResults` 带出标题摘要、`mergeArtifactRefs` 去重、`compactArtifactRefs` 裁剪（`artifact_refs_test.go`）。

### 可选：artifact ref 批量接口

如果不想改 `emitDone`，备选是 done 后拉 `messages-v2?limit=1` 合并；但要注意 `artifacts_json` 是在 done 之后落库的竞态，所以**推荐 done 带 refs**。

## 十、验收

- 手机/桌面：聊天里出现文档卡、灵阅卡、图片条；点卡能进对应页。
- 正文 `xhanhelper://document/<id>` 能跳；外链新窗口；裸 URL 可点。
- 灵阅列表/详情/分享/删除可用；iframe 里脚本不执行。
- 手机拍照/选图/粘贴上传成功；发送后消息显示缩略图；刷新后历史图片还在。
- 计划模式/无痕 pill：新会话首条消息落库即生效；已有会话 PATCH 后刷新仍保持；默认会话开无痕要报错而不是静默失败。
- 流式中停止、回底、错误态正常；done 后卡片不重复。
- `npm run build` + `go build ./...` + `go test ./...` 通过。

## 十一、风险

- HEIC 转码是最大不确定项，必须真机验（Safari 能解、桌面 Chrome 不能）。
- `iframe srcdoc` 的体积可能到几 MB，移动端要注意加载态和内存。
- 图片 blob URL 不 revoke 会漏内存；列表要用懒加载。
- `done` 改后端要确认所有调用点（两处 `emitDone`）都传 artifacts。

## 十二、后续变更（2026-11-02）

- 补 M2 遗留：停止按钮接 `POST /api/chat/stop`（新会话首条消息除外）、错误态「重新加载」、代码块复制按钮（聊天/文档/分享页共用，导出 PDF 不带按钮）。
- 键盘处理落到 `useKeyboardInset` + `--keyboard-inset`（见 `01-*.md`），真机仍需复验。
- 图片库 `/images` 决定本次不做，「我的」和桌面「功能」菜单入口已隐藏，路由保留但无入口；同时移除「我的」里「无痕会话」「快捷操作说明」两个点进去没有内容的入口。

## 十三、后续变更（2026-11-06）

- 流式加 120s 空闲看门狗：`utils/sse.js` 的 `createIdleWatchdog` 在 `api.stream()` 之前 arm，覆盖「请求发出 → 响应头 → 流结束」整条链路；超时先把 UI 收尾（`failStream` + `isStreaming=false`）再 `abort`，已 abort 过（用户停止）则只复位状态、不报假超时。`cancel()` 也直接复位 `isStreaming`。
- 起因：移动端 PWA 被挂起后可能留下半开连接（reader 既不返回也不报错），`chat.isStreaming` 会永远为真，回前台既不同步历史也发不出消息、只能杀进程。
- 服务端没有心跳帧，阈值不能再压低；要更灵敏需服务端在静默时发 `: ping`（SSE 注释行，`createSSEParser` 已能忽略）。
