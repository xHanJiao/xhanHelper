# 018 浏览器图标串味：所有网站都显示成同一个图标

## 现象

内置浏览器里，同一标签页切换不同网站，favicon 换不下来；多个不相干的站点都显示成同一张图（用户看到的是抖音）。收藏夹里新加的条目也是这张图。

## 怎么发现的

直接读本机 prod 缓存目录 `~/Library/Application Support/xhanHelper/favicons/`：

```
platform.deepseek.com.png  3244B  md5 1eeb68be… ┐
www.douyin.com.png         3244B  md5 1eeb68be… ├ 三个不同 host，字节完全相同
www.xiaohongshu.com.png    3244B  md5 1eeb68be… ┘
```

同一个图标被写进了多个 host 的 key，说明问题在写入端，不是显示端。

## 根因（一句话）

**图标提取得太早、且提取结果跟 host 没做一致性校验，于是「上一页的图标」被存进了「新网站」的名字下；又因为缓存一旦写入就不再覆盖，错图被永久锁死。**

拆开看是两件事：

1. **提取时机**：`BrowserToolProvider` 用 URL KVO（`webView.observe(\.url)`）监听导航，URL 一变就跑 `handleNavigation → refreshFavicon`。此刻新 URL 已经是新 host，但页面的 DOM 往往还是上一页的。`faviconJS` 读的是 `document.querySelectorAll('link[rel~="icon"]')`，读到的自然还是上一页的图标，然后以新 host 为 key 存了下去。
2. **异步缝隙**：`refreshFavicon` 里 `host` 在 `await` 之前取，`href` / `pageURL` 在 `await` 之后取，中间页面若又跳走，两边对不上也没人拦。
3. **放大效应**：`FaviconStore.loadFavicon` 开头的 `guard images[key] == nil` 让首次写入（错误的）成为终局——等 `didFinish` 再跑一次时直接跳过；磁盘也一并固化，重启照样错。

收藏夹图标读的是同一个 `FaviconStore.image(forHost:)`，所以跟着一起错。

## 修法

分三层，都在客户端：

### 1. 提取时机挪到「页面就绪」

`onURLChanged` 回调加一个 `pageReady` 标志，区分两个触发点：

- URL KVO（URL 刚变、DOM 还是旧的）→ `pageReady = false`：只更新 tab 的 host 和浏览历史，**不碰图标**。
- `didFinish`（新文档加载完）→ `pageReady = true`：这时 DOM 已经是新页面，才去提取 favicon。

### 2. 关掉异步缝隙

`refreshFavicon` 在跳到后台前先把 `host` 和 `pageURL` 一起抓下来；JS 返回后校验 `session.webView.url?.host == host`，页面中途跳走就丢弃结果，绝不把上一页的图写到别的 host。

### 3. 存量脏缓存自愈

已经落盘的错图会被 `loadDiskCache` 当权威加载，光修代码不解决问题。给缓存目录加 schema 版本号，结构变成 `favicons/v2/<host>.png`；启动时把非当前版本的旧缓存删掉：

- 旧版没有版本子目录、图片直接落在 `favicons/` 根下 → 删这些散落的 `*.png`；
- 历史遗留的 `favicons/vN/` 目录 → 一并删除。

删的是纯缓存，删失败只记一条 warning，下次启动重试，不影响启动。以后 key 规则或落盘结构再变，版本号 +1 即可复用这套自愈。

## 改动文件

| 文件 | 改动 |
|------|------|
| `client/xHelper/xHelper/Shared/Services/FaviconStore.swift` | 缓存目录加 `v2`、启动清理旧版本；更新头部注释 |
| `client/xHelper/xHelper/Shared/Tools/BrowserToolProvider.swift` | `onURLChanged` 加 `URLChangePhase`（`.provisional` / `.finished`）；`handleNavigation` / `refreshFavicon` 按上面改 |

## 验证

- 编译自检通过。
- 真机：连续切换 3~4 个站点，确认 `~/Library/Application Support/xhanHelper/favicons/v2/*.png` 的 md5 各不相同；把其中任意一个加入收藏夹，图标正确。
- 首次启动后确认旧目录里的散落 `*.png` 已被清掉，`v2/` 随浏览逐步建立。

## 复盘补充（code review）

首版只清了存量脏图，没降低**未来**再被锁死的代价，且降级路径无日志。补了这些：

- **解除「首次写入终局」**：`loadFavicon` 去掉 `images[key] == nil`，只保留 `loading` 防同 host 并发——后续正确提取可覆盖内存 + 磁盘。代价是每次 didFinish 重新拉一次，`URLSession.shared` 的 HTTP 缓存兜住（304/磁盘命中），量很小。这样同类错误无需再 bump schema 自愈。
- **降级必留痕**：`purgeStaleCache` 枚举/读类型失败、`loadDiskCache` 枚举/读文件失败、URLSession 拉取失败、`persistToDisk` 写失败都补了 `LogBuffer` warning；favicon 提取的「无 icon」与「页面已跳走丢弃」两条正常降级补 diag，方便下次直接看日志而不是比磁盘 md5。
- **布尔旗标换枚举**：`onURLChanged` 第二参由 `Bool` 改成嵌套的 `URLChangePhase`（`.provisional` / `.finished`），两个触发点语义自解释，不再是魔法值。

有意不做（记录取舍）：

- `purgeStaleCache` 在 `@MainActor init` 同步跑：首次启动几十 ms 一次性成本，可接受。
- `didFailProvisionalNavigation` 路径没有 `didFinish`，加载失败的新 host 拿不到图标，显示占位（不会错显别人的图），可接受。

## 状态

已修（未提交）。
