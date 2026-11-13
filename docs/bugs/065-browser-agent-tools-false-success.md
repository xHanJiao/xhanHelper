# 065 · 浏览器 agent 工具的四个谎报（download / check / select / fill）

> 发现于 2026-11-12 的浏览器工具回归（CEF 迁移期间做全量回归时顺带查出，属**引擎无关**的工具层 bug）。

## 表现

| 工具 | 症状 |
|---|---|
| `download` | 恒报「agent 调用写/交互工具必须显式指定 tabId」，但它的签名里**没有** tabId → 死循环，工具完全不可用 |
| `check` / `uncheck` | 拿一个普通链接（`<a>`）调 `check` → 返回 `checked:true`、`nav:true`。**页面真的跳走了**：没有类型校验，`e.click()` 打在链接上就是一次真点击 |
| `select` | 传一个不存在的 value（`999`）→ 返回 `selected:true`，回读 `get_value` 却是空串：只写 `e.value`，不校验有没有这个 option |
| `fill` | 打到链接上抛 `元素交互失败：JS 异常`（`HTMLInputElement.prototype` 的 setter 用在 `<a>` 上），错误信息不可用 |
| `read(mode=filter)` | 关键词没命中时返回 `matched:false` + 空 content，**不报错** → LLM 误判「页面没内容」 |

## 根因

1. `download` 被列进 `userTabWriteTools`，而那个集合的唯一作用就是「agent 调用必须显式 tabId」的闸门；`download` 实际不操作任何标签页（走 URLSession + 来源页 Cookie/Referer），签名自然没有 tabId。
2. `check/uncheck`、`select`、`fill` 三个 JS 都是**只做动作、不做前置校验**：不判断元素类型、不判断 option 是否存在，然后无条件 `return true`。
3. `read(filter)` 把「没匹配到」当成正常空结果返回，没有告诉模型原因。

## 改法（`client/xHelper/xHelper/Shared/Tools/BrowserToolProvider.swift`）

- `download` 从 `userTabWriteTools` 移除，并把它的 dispatch 提到 tabId 闸门之前（与 `list_tabs` / `close` 同级）。
- 抽出三段共用 JS，ref 与 selector 两条路径都走它们，**失败带原因**：
  - `fillGuardJS`：只允许 `input`（排除 checkbox/radio/button/submit/reset/file/image/hidden）、`textarea`、`contenteditable`，否则报「元素不可填写（<a>）」。
  - `selectAssignJS`：必须是 `<select>`；value/text 必须命中某个 option，否则报可选项列表（前 20 个 + 总数）。
  - `checkToggleJS`：必须是 `input[type=checkbox|radio]` 或 `role=checkbox`；radio 拒绝 `uncheck`；点完**回读状态**，没变成目标状态就报「该控件可能被页面脚本接管」。
- `fill/select/check` 的 selector 版 JS 从 `return true/false` 改成回 JSON `{ok, err}`，Swift 侧统一走 `interpretRefResult`（以前 `jsBool` 会把 JS 异常和「没找到元素」混成一句话）。
- `read(mode=filter)` 没命中改成明确的 serverError（给出可用替代动作），不再静默回空。
- `get_text` 全量模式 JS 失败也改成报错（上一批已改，同一类问题）。

## 自检

三段守卫 JS 用 node 跑桩对象自检 17/17：链接拒绝 check、checkbox 双向切换、radio 拒绝 uncheck、select 非 select / 值不存在 / 按 value 命中 / 按文本命中、fill 各类型分级。

## 后续变更（2026-11-13，代码评审补丁）

- 交互相应结果改走 `jsJSONResult`（`(value, jsError)`）+ `interpretRefResult(_:jsError:successKey:)`：
  「JS 求值超时 / 页面还没准备好 / 渲染进程不可用」不再被糊成「元素交互失败：JS 异常」，
  与原来「只做动作、失败没原因」是同一类问题（传输层错误同样不能吞）。
- `checkToggleJS` 只在状态真的变了才派发 `change`（原实现在「本来就已是目标状态」时也派发，
  会让页面重复校验/重渲染）。
- selector 版 `selectJS` 找不到元素时错误里带上选择器文本（原实现只有 ref 版带位置信息）。

## 遗留

`click_text` 用模糊文本匹配仍有语义风险（命中多个只按 index 取），文档里已建议优先用 `ref`；`fill` 后 DOM 重建会让 `@eN` 失效，需重新 snapshot —— 都是设计边界，不在这条修复范围内。
