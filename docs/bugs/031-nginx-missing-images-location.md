# 031 · 云端文档配图 404（nginx 缺 `/images/` 反代 + 配置漂移）

## 问题表现

- 文档里嵌入的图床图片 `https://helper.xhanx.cn/images/<uuid>.jpg` 在**本地可用、部署到阿里云后打不开**。
- 直接用浏览器/`<img>` 请求该 URL 得到的是 **SPA 首页**（`200` + `Content-Type: text/html`，约 2903 字节），不是图片。

## 根因

1. **线上 nginx 缺 `location /images/`**。生产前端由 nginx 独立托管，其
   `/etc/nginx/sites-enabled/helper.xhanx.cn` 只有 `location /api/`，`/images/<uuid>.jpg`
   落到 `location / { try_files $uri $uri/ /index.html; }`（SPA fallback），
   于是回吐首页 HTML。图片实际由 Go 的公开读路由 `GET /images/:file`
   （`server/internal/controller/router.go:87` → `image.go` 的 `ServePublic`）提供，
   必须显式反代到 `127.0.0.1:8088`。
2. **仓库配置漂移，放大了问题**。`server/nginx-helper.conf` 是一份**残缺副本**
   （缺 `root` / `location /`，cert 路径、超时、body 上限都与线上不同），
   而 `server/Makefile` 的 `deploy*` **从不上传它**。线上配置长期靠手改，
   于是「某次加了 `/images/` 到仓库、线上没同步」这种漏配必然发生。
3. **本地不暴露**：`make dev` 走 Vite，`front-end/vite.config.js` 有 `/images` 代理；
   `make local` 是 Go 直接托管前端，同源天然可达。只有云端的 nginx 需要显式加块。

## 排查弯路（易复发，记下来）

### 弯路 1：`curl -I` 是 HEAD，被 Gin 的 18 字节 404 误导

加完 nginx 块后，用 `curl -sSI`（HEAD）探测，得到 `404 text/plain`、`Content-Length: 18`，
一度误判为「Go 里没有该路由 / 跑的是旧进程」。真相：

- Gin `r.GET` **不注册 HEAD**（`gin@v1.12.0/routergroup.go:116`，`GET`/`HEAD` 是分开的）。
- `gin.Engine.HandleMethodNotAllowed` **默认 false**（`gin.go:213`）→ 路径存在但方法不符时，
  不走 405，直接落 NoRoute 默认 body `404 page not found`（`gin.go:33`，**正好 18 字节**）。
- 所以 **HEAD 探测永远 404**；真正的浏览器是 GET，加块后其实立刻就好了。
- **结论：验证图片用 `curl -sS -o /dev/null -w '%{http_code} %{content_type}' <url>`（GET），
  不要用 `-I`。**

### 弯路 2：误判"老进程没被杀"

一度怀疑 `control-xhan-helper.sh` 的杀进程正则 `[.]/$APP_NAME$` 匹配不到进程、
新二进制被旧进程占着 8088。用下面三条即可证伪，别凭猜：

```bash
sudo ss -ltnp | grep ':8088'                 # 谁在听
ps -o pid=,lstart=,cmd= -p <pid>             # 启动时间：早于本次部署才是旧进程
sudo readlink -f /proc/<pid>/exe             # 结尾带 "(deleted)" 才是"二进制被换、进程没重启"
```

本次三者都正常（进程 08:27:59 启动、exe 未 deleted），排除。

## 最终方案

（2026-10-08）

1. **线上 nginx**（`/etc/nginx/sites-enabled/helper.xhanx.cn`）补块 + 调 body 上限：

   ```nginx
   location /images/ {
       proxy_pass http://127.0.0.1:8088;
       proxy_set_header Host $host;
   }
   # 单图上限 32 MiB（storage.max_image_bytes），留余量；默认 1M 会让大图上传 413
   client_max_body_size 40M;
   ```

   改后 `sudo nginx -t && sudo systemctl reload nginx`。
   > 缓存头由 Go 的 `serve()` 下发（`image.go`：`public, max-age=31536000, immutable`），
   > nginx 不再写 `expires`/`add_header`，避免出现两条 `Cache-Control`。

2. **仓库同步**：`server/nginx-helper.conf` 回填为线上真实整份（含 `root`/`location /`、
   cert 路径、`/images/`、`40M`），顶部注明「手动安装到 `/etc/nginx`」。
3. **部署留档**：`server/Makefile` 的 `deploy` / `deploy-backend` / `deploy-only`
   增加 `scp nginx-helper.conf $U@$H:$P/`，并在末尾打印提醒。**不**自动 `sudo` 写 `/etc/nginx`
   （部署脚本不应要 root）。

## 验证

- `curl -sS -o /dev/null -w '%{http_code} %{content_type}\n' https://helper.xhanx.cn/images/<uuid>.jpg`
  → `200 image/jpeg`（本次实测 200 + `image/jpeg`，208 KB）。
- 传一张 >1MB 的图不应再 413。

## 防复发

- 改 nginx 行为时，**同一 PR 更新 `server/nginx-helper.conf`**，并记得手动装到线上。
- `docs/design/document-images.md` §九、`docs/front-end/plan.md` 的 nginx 示例已同步补上
  `/images/` 与 `client_max_body_size`。

## 后续变更

### 2026-10-08 · macOS 客户端持久缓存把图 URL 污染成裂图

nginx 修好后，Mac app 里旧文档配图仍裂。根因是 **WKWebView 的持久 HTTP 缓存**：

- WebKit NetworkCache Record（`~/Library/Caches/xhanx.cn.xHelperMac/WebKit/NetworkCache/…/Resource/…`）
  在 nginx 修好**之前**把 `https://helper.xhanx.cn/images/<uuid>.jpg` 缓存成了
  `Content-Type: text/html` + SPA 首页 body（`Date: Wed, 07 Oct 2026 23:58:52 GMT`）。
- 该响应**没有 `Cache-Control`** → WebKit 按启发式新鲜度估算，约 11h 内不再回源，
  于是 nginx 已修好也继续吐缓存的 HTML。清客户端缓存（`rm -rf ~/Library/Caches/xhanx.cn.xHelperMac`，
  先退 app）或历史面板「清缓存」按钮即可恢复。

**根因修复（本次）**：文档阅读器与 PDF 导出器的 WKWebView 改用**非持久 store**，
不再把文档 HTML/图片写进持久 HTTP 缓存：

- `client/xHelper/xHelper/Shared/Views/DocumentReaderView.swift`：`config.websiteDataStore = .nonPersistent()`
- `client/xHelper/xHelper/Shared/Services/DocumentPDFExporter.swift`：同上
- 文档图是 public、无需鉴权，非持久化无副作用；不碰内置浏览器的 Cookie/登录态。

### 2026-10-08 · 历史面板按钮 tooltip 不显示

- 现象：历史面板头部「清缓存」「清空全部历史」两个按钮 hover 无提示。
- 根因：代码里其实已有 `.help(...)`（系统原生 tooltip），但在本项目**自绘/透明窗口**里不渲染。
- 方案：改用项目自绘的 `hoverTooltip`（`Views/HoverTooltip.swift`，与顶栏/输入框统一），
  涉及 `client/xHelper/xHelper/Shared/Views/HistoryPanelView.swift` 头部两按钮 + 行内「从历史移除」。
- **约定：自绘窗口内一律用 `hoverTooltip`，不要用 `.help`**（其它面板若有 `.help` 存在同样隐患）。

### 2026-10-12 · 上文示例里的 `proxy_pass http://127.0.0.1:8088` 已改指上游

后来发现「每请求直连后端」会持续产 loopback TIME_WAIT（云监控 NON_ESTABLISHED 告警），
改成了 `upstream xhan_helper_backend { keepalive 32; }` + `proxy_set_header Connection "";`。
本文件上文的 `/images/` 片段只作历史记录，**以 `server/nginx-helper.conf` 为准**，详见 `docs/bugs/035`。

### 2026-10-12 · 恢复 macOS 文档图持久缓存 + 灯箱补缓存

上面的「改用非持久 store」是给 nginx 漏配打的补丁，代价是文档配图每次进文档都重下。
根因（本文件已修：`server/nginx-helper.conf` 的 `location /images/`）消失后恢复缓存：

- `client/xHelper/xHelper/Shared/Views/DocumentReaderView.swift` 的 `ReaderWebView`：
  删掉 `config.websiteDataStore = .nonPersistent()`，回到默认持久 store —— WebKit 磁盘缓存
  命中 `immutable` + 一年 max-age 的文档图，换篇/重进/重启都不再重下。污染窗口当时也只有
  ~11h 启发式（SPA 响应无 `Cache-Control`），不是一年，风险可接受。
- 同文件的 `DocumentImageLightbox`（此前**没有**任何缓存）：加 `NSCache<NSString, NSImage>`
  按 src 缓存解码结果（重开瞬时），并用自有 `URLSession` + `URLCache(64MB 内存/512MB 磁盘,
  diskPath: "doc-images")` 取字节 —— WebKit 缓存与 URLSession 不共享，默认
  `URLCache.shared`（512KB/20MB）也放不下大图。
- `DocumentPDFExporter` 仍保持非持久 store（导出是一次性动作，无缓存收益）。
