# 浏览器 Tier 3 — 收藏夹 / 浏览历史 / tab 拖拽排序

> 日期：2026-08-04
> 分支：browser-tier1-ux（在现有浏览器上叠加）
> 范围：macOS 客户端。收藏夹（含 HTML 导入/导出）、浏览历史采集与展示、网页 tab 拖拽排序。
> 明确不做：阅读模式（用户未用）、tab 冻结（Chrome discarding，会丢页面状态，节能已覆盖渲染）。

## 一、目标

贴近 Chrome 的浏览器日常体验：

- **收藏夹**：一键收藏当前页（tab 条 ⭐）、收藏夹面板（浏览器内特殊 tab）、支持从 Chrome/Safari 导出的 Netscape HTML 书签导入、以及 HTML 导出。
- **浏览历史**：自动记录访问过的页面，历史面板（浏览器内特殊 tab）可打开/删除/清空。
- **tab 拖拽排序**：网页 tab 之间拖拽重排；特殊 tab（下载/收藏/历史）固定最右、不可拖拽。

## 二、数据层（仿 DownloadHistoryStore，Application Support JSON 持久化）

### BookmarkStore（`bookmarks.json`）
```swift
struct Bookmark: Identifiable, Codable {
    let id: UUID
    var title: String
    var url: String
    var host: String
    var createdAt: Date
}
```
- 扁平列表，上限 ~500（丢最旧）。
- `add(title:url:) / remove(id:) / toggle(title:url:) / isBookmarked(url:) / search(_:)`。
- 去重键：URL lowercased。
- `importFromHTML(data:) -> Int`：解析 Netscape 书签 HTML，`<A HREF>` 提取，仅 http/https，按 URL 去重，返回导入数。
- `exportHTML() -> String`：Netscape 格式导出（可再导入）。

### HistoryStore（`history.json`）
```swift
struct HistoryEntry: Identifiable, Codable {
    let id: UUID
    var title: String
    var url: String
    var host: String
    var visitedAt: Date
}
```
- 上限 ~1000 滚动裁剪。
- `record(title:url:host:)`：仅 http/https；同 URL 已存在则移除并置顶（更新时间）；连续同 URL 天然去重。
- `remove(id:) / clear()`。

## 三、采集钩子

`BrowserToolProvider.handleNavigation`（已由 URL KVO + didFinish 触发）追加 `HistoryStore.shared.record(...)`，历史自动累积，about:blank 被 scheme 过滤排除。

## 四、UI（浏览器内特殊 tab，仿下载 tab）

### BookmarkPanelView
- 头部：标题「收藏夹」+ 搜索框 + 导入 + 导出 + 清空。
- 列表：favicon + 标题 + URL + 相对时间，点击**新 tab 打开**（`BrowserToolProvider.openURL` 立即建 tab），删除。
- 空态：「暂无收藏」。

### HistoryPanelView
- 头部：标题「历史」+ 清空。
- 列表：新→旧平铺，favicon + 标题 + URL + 相对时间，点击新 tab 打开，单条删除。
- 空态：「暂无历史」。

## 五、tab 条入口（固定按钮区）

| 按钮 | 动作 |
|------|------|
| ⭐ | 切换当前页收藏（实心=已收藏） |
| 书签图标 | 打开收藏夹 tab |
| 🕐 | 打开历史 tab |

特殊 tab 固定最右：`openTab` 将新网页 tab 插入到第一个特殊 tab 之前。

## 六、tab 拖拽排序

- 每个网页 tab 按钮 `.draggable(tab.id)` + `.dropDestination(for: String.self)` → `manager.moveTab(from:to:)`。
- `moveTab` 只允许在网页 tab 区域重排（特殊 tab id 被守卫拒绝）。
- `RightSidebarManager.openTab` 改为在特殊 tab 块之前插入（保证特殊 tab 置右）。

## 七、改动文件

| 文件 | 改动 |
|------|------|
| 新增 `Shared/Services/BookmarkStore.swift` | 收藏持久化 + 导入/导出 |
| 新增 `Shared/Services/HistoryStore.swift` | 历史持久化 |
| 新增 `Shared/Views/BookmarkPanelView.swift` | 收藏夹面板 |
| 新增 `Shared/Views/HistoryPanelView.swift` | 历史面板 |
| `Shared/Tools/BrowserToolProvider.swift` | handleNavigation 记历史 + `openURL`（立即开新 tab） |
| `Shared/Views/RightSidebarManager.swift` | 书签/历史特殊 tab + `moveTab` + 特殊 tab 置右 |
| `Shared/Views/RightSidebarView.swift` | ⭐/书签/🕐 按钮 + 拖拽排序 |

## 八、验证

- 收藏：⭐ 收藏当前页 → 实心；书签面板出现该项；打开/删除/搜索。
- 导入：Chrome/Safari 导出 HTML → 导入数 toast；去重；仅 http/https。
- 导出：生成可再导入的 HTML。
- 历史：浏览几个页面 → 历史面板按新→旧记录；打开/删除/清空；同 URL 回访置顶。
- 拖拽：网页 tab 拖拽重排；特殊 tab 不参与、固定最右。
- 重启后收藏/历史仍在（持久化）。
