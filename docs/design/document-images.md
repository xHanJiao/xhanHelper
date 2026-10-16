# 文档"有图"能力（v1）— 图床 + 图表入档 + 搬运图

> 状态：**设计已定稿，待实现**（2026-08-07 访谈 + 决策完成）
> 范围：server（Go）+ macOS/iOS 客户端（SwiftUI）+ Web 前端（Vue）+ nginx

---

## 一、背景与发心（产品角度）

文档功能当前是纯 Markdown。用户（唯一使用者）的反馈：

- **大同攻略**：纯文字"干巴巴"，小红书攻略图文并茂，自己的只有文字。
- **房地产统计**：只有文字，想要折线图/饼图等统计图。

用户访谈结论（2026-08-07）：

1. **文档是"作品"，不是备忘**。除了自己回看，也会叫同居的 CJ 直接来看（本地 macOS app 内查看为主）。
2. 最低要求：**内容清楚 + 有图点缀**；理想态：小红书帖子式图文混排（v2 再做排版）。
3. 图的两种角色：
   - **装饰美化**（攻略配图）→ 真实图片搬运；
   - **辅助理解**（统计数据）→ 折线/柱状/饼图。
4. **不要 AI 生图**（明确排除）。
5. 图的来源不设限：**全部由 LLM 通过工具获取**（记账统计 / 用户提供数据 / 浏览器浏览拿图）。

### 产品切割

- **v1：让文档"有图"** —— 图床 + 图表入档 + 搬运图 + 全端渲染。
- **v2：让文档"好看"** —— 封面图、卡片式排版、帖子感阅读体验（另立任务）。

---

## 二、决策记录

| 编号 | 决策 | 结论 |
|---|---|---|
| D1 | 图表怎么入档 | ✅ **扩展 macOS `create_chart`**（SwiftUI Charts）→ `ImageRenderer` 渲染 PNG → 上传图床 → 返回 URL。局限：仅 macOS 会话生成的文档能带图表（web 聊天无图表工具，v1 接受，如实记录） |
| D2 | 搬运图走哪条腿 | ✅ **推荐 + 备选都做**：<br>① 服务端 `save_image(url, referer?)` 直拉（跨端、简单，带 UA+referer 破热链；登录/强热链图拉不到）<br>② 客户端浏览器下载 + 上传（复用 `runDownload` 的 UA/Referer/Cookie 构造，带完整会话，登录/热链图能转；macOS 专属） |
| D3 | 配图主动性 | ✅ **主动配图**（统计类必配图表、素材图克制；配不上如实说明，不硬凑） |

---

## 三、总体流程

```
LLM 写文档/回复（chat）
 ├─ 统计类数据 ──► create_chart（macOS）→ SwiftUI Charts 渲染 PNG ─► 上传图床 ─► /images/<uuid>.png
 ├─ 素材图（浏览器浏览）─► save_image(url)（服务端直拉）──► 图床 ─► /images/<uuid>.png
 └─ 素材图（需会话/登录）─► 客户端浏览器下载+上传 ─────────────► 图床 ─► /images/<uuid>.png
                                                        │
                               markdown 引用：![](/images/<uuid>.png)
                                                        │
    ┌───────────────┬───────────────┬───────────────────┬──────────────────┐
    │ macOS 客户端    │ Web 分享页     │ Web PDF 导出       │ macOS PDF 导出     │
    │ MarkdownUI     │ markdown-it   │ useExportPDF      │ DocumentPDFExporter│
    └───────────────┴───────────────┴───────────────────┴──────────────────┘
```

---

## 四、图床（服务端，两条入口共用）

### 4.1 配置

`conf/config.yaml` 增加：

```yaml
storage:
  image_dir: data/images   # 图片根目录（相对 server 运行目录）
```

dev/prod/test 环境 yaml 同步补 `storage` 段。

### 4.2 静态提供：`GET /images/:file`

- **放鉴权组外**（`<img>` 标签无法携带 Bearer 头；UUID 文件名本身即"能力凭证"，与现有分享链接同模型）。
- 只接受匹配 UUID 模式的文件名（`filepath.Base` 防目录穿越）。
- `Cache-Control: public, max-age=31536000, immutable`（文件名不可变）。
- `http.ServeFile` 从 `storage.image_dir` 提供。
- 新文件：`internal/controller/image.go`；在 `controller/router.go` 注册（鉴权组外）。

### 4.3 上传：`POST /api/images/upload`（鉴权组内）

供客户端（图表 PNG / 浏览器转存）上传图片：

- 请求：multipart 或原始字节均可，携带 `filename`（可选，仅用于推断扩展名）。
- 校验：
  - 大小 ≤ 10MB；
  - content-type / 魔数必须是 `png | jpeg | webp | gif`（**拒绝 svg**，防脚本）；危险类型（可执行/压缩/HTML）直接拒绝；
  - 魔数校验防伪造扩展名。
- 落盘：`data/images/<uuid>.<ext>`。
- 返回：`{"url": "/images/<uuid>.<ext>"}`。

---

## 五、搬运图入口（D2）

### 5.1 服务端直拉：`internal/tool/image.go`（新工具 `ImageTool`）

函数 `save_image(url, referer?, alt?)`：

- 校验：scheme 必须 http/https；**防 SSRF**（解析后的 host 拦 127.0.0.1/10.x/172.16-31.x/192.168.x/link-local/0.0.0.0）。
- 请求特征：浏览器 UA + 可选 `referer` 参数（LLM 从浏览页面得知 referer，破 Referer 型热链）。
- 超时 15s；流式读取限制 10MB。
- 校验 content-type 白名单（png/jpeg/webp/gif，拒 svg）+ 魔数；按实际类型定扩展名。
- 写图床 → 返回 `{"image_url": "/images/<uuid>.<ext>"}`。
- 注册：`appinit/tools.go`。
- 单测：`httptest` 覆盖 成功 / 非图片 / 超限 / 内网拦截 / referer 热链。

### 5.2 客户端浏览器下载 + 上传（macOS，兜底登录/热链图）

复用 `BrowserToolProvider.runDownload` 的**请求构造**（BrowserToolProvider.swift:833-853）：
真实 UA（页面 JS 读取）+ Referer（当前页）+ 域匹配 Cookie（`matchingCookies`），加到新工具。

- 新客户端工具（建议命名 `agent_image_save`，挂在 BrowserToolProvider 或独立 Provider）：
  1. 按 `runDownload` 的 UA/Referer/Cookie 构造请求，下载图片字节（**不落盘**，内存或临时文件）；
  2. 魔数校验为允许的图片类型；
  3. `POST {baseURL}/api/images/upload`（带 Bearer token，走 `APIClient`）；
  4. 返回服务器相对 URL。
- 注册到 `LocalToolRegistry`（App.swift `.task` 里，与 BrowserToolProvider 同级）。
- 服务端无需改动（复用 4.3 上传接口）。

> 服务端直拉失败（404/403/登录墙）时，LLM 可改用客户端 `agent_image_save`；两者都用不了则如实告知用户。

---

## 六、图表入档（D1）

扩展 macOS `ChartTool`（`client/xHelper/xHelper/Shared/Tools/ChartTool.swift`）：

- `create_chart` 增加 `save_to_image: bool`（默认 false），或新增 `create_chart_image` 函数。
- `save_to_image=true` 时：
  1. 用 SwiftUI `ImageRenderer` 把图表视图（ChartBubble 内容，ChartBubble.swift:60-127）渲染为 PNG；
  2. `POST /api/images/upload`（带 token）；
  3. 工具结果返回 `{"image_url": "/images/<uuid>.png"}`（LLM 拿它嵌入文档）。
- `isReadOnly`：`create_chart` 保留只读；**保存图片路径视为写**（plan 模式拦截）。
- 数据来源不设限：LLM 通过工具拿到的任何数据（`get_bill_stats` / 用户提供 / 浏览获取）。

> 局限（v1 如实记录）：图表工具是 macOS 客户端工具，**web 聊天生成的文档无法带图表**。若后续需要，可加服务端 Go 图表库（go-chart/gonum/plot）渲染 PNG 兜底。

---

## 七、LLM 引导（D3）

- `document` 工具描述（`internal/tool/document.go` Description / Functions）追加配图规则：
  - 生成文档时**主动**判断是否需要配图；
  - 统计数据 → 调图表工具；攻略/故事 → 转存素材图；
  - 图片一律用相对路径 `![](/images/xxx.png)` 嵌入正文；
  - 配图失败如实说明，不硬凑、不编 URL。
- 聊天 System Prompt（`conf/config.yaml` global_default_role 或 builder）同步配图指引，让非文档回复也能插图。

---

## 八、全端渲染

### 8.1 macOS / iOS 客户端（MarkdownUI 2.4.1）

当前 `Markdown(...)` 均未传 imageBaseURL，相对路径图加载不了。需给以下位置传 `imageBaseURL: URL(string: deviceManager.baseURL)`（或注入环境 `markdownImageBaseURL`）：

- `DocumentFullView`：DocumentListView.swift:148 与 :156（含 Mermaid 分段后的 markdown 段）
- `ChatBubble`：ChatBubble.swift:167 与 :176
- 其他渲染文档/笔记/记忆内容的 `Markdown(...)` 视需要统一处理

### 8.2 Web 前端

- `.markdown-body img { max-width: 100%; height: auto; }`：ListItemCard.vue、SharedDocumentView.vue、MessageBubble.vue。
- dev `vite.config.js` proxy 增加：`'/images': { target: 'http://localhost:8080', changeOrigin: true }`。

### 8.3 PDF 导出（×2，相对路径都必须重写为绝对 URL）

- **Web** `front-end/src/composables/useExportPDF.js`：`md.render` 前把 `](/images/` 重写为 `](${window.location.origin}/images/`。
- **macOS** `DocumentPDFExporter`（client/.../Services/DocumentPDFExporter.swift:132 `loadHTMLString(html, baseURL: baseURL)`）：`baseURL` 由调用方给服务端地址，正文里的 `/images/...` 因此解析成绝对 URL。

### 8.4 分享页

`SharedDocumentView.vue`（web）同源渲染，`/images/...` 自动可显示，**零改动**。

---

## 九、部署

生产前端由 nginx 独立托管，**必须**为图床公开读加反代；否则 `/images/<uuid>.<ext>` 会落到
SPA fallback 回吐首页 HTML（详见 `docs/bugs/031`）。配置在 `server/nginx-helper.conf`
（线上 `/etc/nginx/sites-enabled/helper.xhanx.cn` 的版本化副本）：

```nginx
location /images/ {
    proxy_pass http://xhan_helper_backend;  # upstream 块定义在 nginx-helper.conf 顶层（见 docs/bugs/035）
    proxy_set_header Host $host;
}
# 单图上限 32 MiB（storage.max_image_bytes），留余量；默认 1M 会让大图上传 413
client_max_body_size 40M;
```

- 缓存头由 Go 的 `serve()` 下发（`public, max-age=31536000, immutable`），nginx 不重复写，
  否则会出现两条 `Cache-Control`。
- `make deploy*` 会把 `nginx-helper.conf` 留档到服务器 `$P/`，但**不会自动安装**（安装需 root）；
  改动后需手动覆盖到 `/etc/nginx` 并 `sudo nginx -t && sudo systemctl reload nginx`。
- Go 是唯一事实来源：dev 直连 `localhost:8088` 即可用，prod 经 nginx 转发。

---

## 十、验收

1. 服务端：`go build ./...` + `go test ./...` 全绿（图床上传/静态路由/save_image 校验单测）。
2. 客户端不执行编译，由用户在 Xcode 验证；前端 `vite build`。
3. 手工场景：
   - ① 让 LLM 写"大同攻略"，观察是否主动配图，macOS 客户端 + Web 都能显示；
   - ② 写"房产统计"，LLM 生成折线/饼图并嵌入文档；
   - ③ 文档导出 PDF（Web `useExportPDF` + macOS `DocumentPDFExporter`）图片可见；
   - ④ 分享链接打开图片正常；
   - ⑤ 热链/登录图走客户端 `agent_image_save` 兜底成功。

---

## 十一、不做（v1 边界）

- ❌ AI 生图（用户明确排除）
- ❌ 封面图 / 卡片式 / 帖子感排版（v2）
- ❌ 图片 DB 记录与孤儿清理（v1 纯文件；删文档清理孤儿图后补）
- ❌ mermaid 在 Web/PDF 的渲染（独立旧缺口，另立任务）
- ❌ Web 聊天生成图表（依赖 macOS 客户端工具，后议）
