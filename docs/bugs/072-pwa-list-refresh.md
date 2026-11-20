# 072 — PWA 里灵阅 / 文档列表永远不刷新，也没有刷新入口

- **状态**：已解决
- **发现日期**：2026-11-20
- **影响范围**：`front-end/src/views/ArtifactsView.vue`、`front-end/src/views/DocumentsView.vue`、`front-end/src/components/PageHeader.vue`、`front-end/src/composables/useVisibleRefresh.js`

## 表现

iPhone 主屏 PWA（不杀进程）里，灵阅和文档两个列表页：

1. 在别处（聊天里做了一页灵阅、另一台设备写了文档）产生的新内容，列表里看不到；
2. 界面上**没有任何刷新入口** —— PWA 没有地址栏，iOS 也不给网页下拉刷新，用户只能杀进程重开。

切到别的 Tab 再回来是好的（路由没开 keep-alive，会重新 mount 重拉），所以问题只出在「页面一直停在这一页、进程被 iOS 挂起」这条路径上。

## 根因

1. 两个列表页都只在 `onMounted` 拉一次（`fetchList` / `fetchDocuments`），没有任何重拉路径。
2. 主屏 PWA 被 iOS 挂起时**不销毁进程**，回到前台继续跑同一个页面实例，`onMounted` 不会再执行 —— 列表就停在挂起那天的快照（与 bug 046 的会话列表同一个病根，但那次只修了会话下拉）。
3. `PageHeader` 没有刷新能力，手机端也没有可替代的交互。

## 修复

- 新增 `composables/useVisibleRefresh.js`：监听 `visibilitychange` + `pageshow`（bfcache 恢复），可见且距上次刷新超过 5s 才重拉；`enabled()` 让调用方屏蔽不该刷新的状态（灵阅多选态）。判定抽成纯函数 `canAutoRefresh` 便于单测。
- `PageHeader` 加 `refreshable` / `refreshing` prop 与 `refresh` 事件，按钮固定在动作区**最左**（搜索左边）：手机端 38px 触控区，刷新中图标转圈 + 主色高亮（覆盖 `.icon-btn:disabled` 的 40% 透明度）。`refreshing` 只表示**手动刷新在飞**，加载更多/翻页/搜索仍走各自的 `loading`，图标语义不串。
- 灵阅：`refreshable` 只在非多选时出现；手动刷新弹 `已刷新` toast（`fetchList` 改为返回成功布尔，失败时只留它自己的错误提示）；自动刷新在多选态被 `enabled()` 挡掉，且失败是 `silent`（只 `console.error`，回前台短时无网不弹误报）。
- 文档：刷新**不动 `page`**，翻到第 3 页回前台仍是第 3 页；标签 chip 一起重拉；同样区分手动/自动的错误提示。
- 顺带给 `DocumentsView` 的加载失败补了 `console.error`（原来只有 toast，没有原始 error）。

自动刷新不弹 toast（每次切 app 都弹一条太吵），变化从列表本身看得出来。

## 验证

- `cd front-end && npm test`（新增 `useVisibleRefresh` 四条用例）、`npx vite build` 通过。
- 真机（PWA，不杀进程）：停在灵阅挂起数分钟后回前台 → 列表更新；点刷新图标 → 转圈约一秒 + `已刷新`；多选态下图标消失、回前台不洗勾选；文档翻到第 2 页 → 切 app 再回 → 仍在第 2 页。

## 已知残留

- 笔记 / 账单 / 记忆 / 摘要四个列表页是同一个毛病（只在 `onMounted` 拉一次），本次按需求只改了文档 + 灵阅；接入 `useVisibleRefresh` + `refreshable` 每页两行。
- 智能体列表（`AgentView`）等其它 `page-scroll` 页面同理未动。
- 自动刷新会把灵阅列表复位到第一页（和「切走再回来」的现有行为一致）；不做「保持已加载页数」。
