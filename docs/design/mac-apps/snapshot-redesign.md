# 快照重设计：稳定句柄模型（阶段 1）

> 日期：2026-08-04
> 分支：feat/browser-polish
> 状态：阶段 1（稳定句柄核心）已实施并测试闭环；阶段 2/3 规划见文末。

## 一、背景问题

原有 `snapshot` 的 ref（`@eN`）是**位置索引**——"第 N 个满足快照过滤条件的元素"。它的四个硬伤：

1. **前 60 个限制**：`LIMIT` 默认 60，且**无分页/offset**，密集页面后面的元素永远进不了快照。
2. **ref 过期**：快照后 DOM 一插入/重排，`@eN` 就指向别的元素；`refProbe` 只能"校验失败报错"，不能找回目标。
3. **位置悬垂（Bad Case 15）**：元素被销毁后，旧索引可能"悬垂"到位置相同的新节点上，误操作错误元素。
4. **覆盖面窄**：自定义组件（无 role）、纯 JS 绑定点击的元素、shadow DOM、iframe 内的元素不可见。

## 二、调研：业界怎么做

| 方案 | 做法 | 我们的取舍 |
|------|------|-----------|
| **Chrome CDP `Accessibility`** | `enable()` 后 AXNodeId 跨调用稳定；每个 AX 节点带 **`backendDOMNodeId`**（DOM 变动后仍稳定）；`getFullAXTree` 全量、`queryAXTree` 按 name/role 定向 | WKWebView 无 CDP，用 **JS WeakMap 注册表模拟 backendNodeId** |
| **browser-use（最流行开源浏览器 agent）** | 模型看 `selector_index`，点击用内部 `backend_node_id`（每次重新序列化）；**滚动驱动 + viewport 阈值**（默认 1000px）；识别分页按钮；AX 树 + `getEventListeners` 检测纯 JS 点击目标；`pierce:true` 穿透 shadow/iframe | 我们的 WeakMap **跨交互持久**（比它强，不需每次重新序列化）；滚动驱动模型采纳进阶段 2；JS 监听检测用 addEventListener 包装近似 |
| **Playwright locator** | 每次操作前按 selector 重新解析 | 签名重定位思路（阶段 3 采纳） |

**JS 环境的三个做不到（如实记录）**：
- 纯 JS 监听器绑定检测 → 用 **addEventListener 包装脚本**近似（阶段 1 已埋，阶段 3 接入收集）。
- 跨域 iframe → JS 够不到（见"未来路径"）；同源 iframe 阶段 3 纳入；跨域用 cgclick 兜底。
- 闭合 shadow root → 无 JS 路径，接受跳过（实践中极罕见）。

## 三、稳定句柄模型（阶段 1 核心）

### 3.1 机制

- `makeWebView` 注入 `a11yRegistryScript`（documentStart，forMainFrameOnly）：
  - `window.__xhanA11y = { id: WeakMap 注册表 }` —— `a11y.id(el)` 首次遇到分配单调 id、之后恒定（JS 版 backendNodeId）。
  - **addEventListener 包装**：记录元素绑定的点击监听（Gap 1 基础）。
- **`@eN` 语义升级**：N 从"第 N 个元素"变"稳定 id"。首次快照按 DOM 序分配 1..N（表现与旧版一致），DOM 变动后 id 不变。
- 交互工具用 `locateElJS(handle)` 在**当前 DOM** 里按 `a11y.id(el) === handle` 定位，不依赖位置。

### 3.2 关键性质

- **跨快照稳定**：同一元素（节点未重建）跨多次快照 id 不变。
- **对插入/重排免疫**：顶部插广告、列表重排，`@e6` 仍指向原元素。
- **不悬垂**：元素销毁 → id 消失 → 报"ref @eN 不存在，页面已变化，请重新 snapshot"，**不会指向位置相同的新节点**（Bad Case 15 根治）。
- **页面跳转自然重置**：documentStart 重新建注册表，id 重新分配。

### 3.3 迁移

- `refIndex`（位置）→ `refHandle`（稳定 id）；移除位置签名校验 `refMismatchError`（稳定 id 本身就是身份）。
- 全部交互工具（click/fill/type/select/check/get_text/get_value/cgclick/cgDrag）改为 `collectEls + locateElJS` 定位。
- `snapshotItem/snapshotText` 按稳定 id 反查（确认条摘要/指示器 ref→文本复用）。

## 四、阶段 1 测试报告（T1-T7 第二轮，全绿）

环境：浏览器重置后从零开始。结果：

| # | 用例 | 结果 | 关键证据 |
|---|------|------|---------|
| T1 | 快照基本格式 | ✅ | baidu 首行标注 + @e1-@e31 连续递增 |
| T2 | 稳定 id 跨快照 | ✅ | 「换一换」整组替换：存活元素 @e1-@e16/@e27-@e31 保持原 id；新节点从 @e32 起 |
| T3 | 旧 ref 安全 | ✅ | 已销毁 @e18 → 正确报错不悬垂；存活 @e16 直接命中 |
| T4 | S1 脱敏 | ✅ | get_value 密码框返回「敏感字段不可读取」；fill 假值后 snapshot 零泄露 |
| T5 | 工具回归 | ✅ | fill→click→nav、get_text、check/uncheck 双向全正常 |
| T6/T7 | L3 确认条 | ✅ | 支付宝高敏域点击弹确认条（人话摘要）、确认后执行 |

**核心收益**：稳定 id 机制验证通过；顺带解决 Bad Case 15 位置悬垂（本轮最大收益）。

## 五、缺陷清单（阶段 1 修复闭环）

| # | 缺陷 | 根因 | 修复 |
|---|------|------|------|
| ① | L3 确认条显示 `ref=@e10` 非「点击「帮助」」 | `confirmationBody` 未实现 + `awaitToolConfirmation` 的 argsSummary 未接它（仍是原始参数转储） | 实现 `confirmationBody`（全工具人话摘要：ref→元素文本/href、密码脱敏、跨域标注、敏感页 ⚠）；`bridgeToBrowser` 时 argsSummary 走它，聊天/浏览器条同享；body lineLimit(3) |
| ② | get_value 密码框报「未找到元素」而非「敏感字段不可读取」 | `handleGetValue` guard 吞掉 JS err | 改用 `dictErr` 透出 JS 错误 |
| ③ | 指示器完成态绿字绿底看不清 | 绿勾叠浅 accent 底 | 完成态整条转浅绿反馈（bg green 0.15 + 绿描边 + 白勾圆） |

**新观察（已修复）**：必应 @e10「换一批」_blank 拦截错误缺目标 URL——非 `<a>` 元素无 `href`。blank guard 改为 `closest('a')` 向上找 href + 附元素文本。

## 六、未来路径

### 6.1 跨域 iframe（研究项，不本期做）

- 现状：JS 够不到跨域 frame；支付/登录/嵌入多为跨域 iframe，agent 不可结构化操作，只能 cgclick 兜底。
- 未来方向：WKWebView `evaluateJavaScript(_:in: contentWorld:)` 可按 WKFrameInfo 定向执行——通过 `decidePolicyFor` 的 `targetFrame` 收集子 frame 句柄，跨域也可能注入（内容拦截器同款机制）。需原型验证（有不确定性）。
- 与产品策略一致：支付/登录用户自己来，不交给 LLM。

### 6.2 桌面小宠物（潜在复用）

viewport 空间感知（阶段 2 的可见性模型）为桌面宠物提供"屏幕上有什么"的感知基础。

## 七、阶段 2 实施与测试（已完成）

### 阶段 2：可见性模型 + 滚动驱动

已实现：
- `snapshot` 参数：`scope=viewport|document`（默认 viewport）、`offset`、`limit`（兼容 `max_items`）、`filter`。
- **viewport scope**：只列视口内 + 阈值（1000px）元素，`getComputedStyle` 滤 visibility:hidden/opacity:0（display:none 已被 rect=0 捕获）；返回 `hidden_below`（下方交互元素 + 距几屏，上限 10）+ `hidden_below_count`。
- **分页按钮识别**：next/prev/页码文本模式 → `pagination`（带稳定句柄），**viewport/document 都输出**（覆盖视口外底部按钮）。
- **document scope**：扁平 offset/limit 全量扫描，响应带 `total/offset/has_more`，快照页脚提示翻页。
- **交互增强**：`refClickDownJS`/`refCenterJS` 元素不在视口时先 `scrollIntoView` 居中（document scope 下可直接点视口外元素）。

### 阶段 2 测试报告（P1-P8，全绿）

| 用例 | 结果 | 关键证据 |
|---|---|---|
| P1 viewport 默认 | ✅ | 百度一屏显示完无提示行；搜狐 `hidden_below_count=10` + 「视口下方还有 10+ 个交互元素」带距离标注 |
| P2 滚动驱动 | ✅ | scroll 后 @e32/@e34/@e46/@e67/@e68 全部 id 不变；hidden_below 元素进入视口有 @eN；懒加载 total 87→123 |
| P3 document 翻页 | ✅ | 第1页 @e1-@e60 / 第2页 @e61-@e120 无重复不冲突；total=447 恒定 |
| P4 filter | ✅ | 登录 1/28、图片 @e2（id 不重排）、0 命中语义清晰 |
| P5 分页识别 | ✅ | 分页 @e108-@e113 [page] + 结构化 `pagination`；click @e108 → nav:true + pn=10 |
| P6 视口外操作 | ✅ | get_text @e180（下 5 屏）成功；click 视口外按钮自动滚动 |
| P7 回归 | ✅ | fill→click→nav:true 跳结果页；filter 命中 25 条 |
| P8 销毁检测 | ✅ | 换一换销毁后旧 @e17 → 「ref 不存在，页面已变化」不悬垂 |

### 阶段 2 观察点（已处理）

1. **filter 消耗 id 分配**（文档化，不修）：filter 快照先遇到的元素拿小 id，后续全量保持该 id、其他顺延，输出出现 `@e14→@e1` 跳变。这是稳定句柄模型的固有性质（id 按首次遇到顺序分配、非 DOM 序）；"修"只有重编号（破坏稳定）或按 id 排序（破坏空间序）两条死路。ref 定位不受影响。
2. **分页段 viewport 默认也输出**（已修）：分页识别覆盖视口外候选项（hidden 带句柄），viewport 默认快照可直接点底部页按钮。

## 阶段 3（已完成）：内容质量

已实现：
- **可访问名升级**（`accName` 简化版）：`aria-label` > `aria-labelledby`（解析引用的元素文本）> `label[for]`（CSS.escape 安全查询）> `placeholder` > `title` > `textContent`。**不使用 value**——密码等敏感值永不进入（S1 更彻底，表单值不是名字）。
- **状态字段**：`expanded`（aria-expanded）/ `selected`（aria-selected）/ `disabled`（disabled|aria-disabled）/ `tabindex` / `contenteditable`，快照行渲染 `disabled/expanded/selected/tabindex=n/editable`。
- **点击信号**（小页面 ≤3000 元素全量扫描）：监听器表（addEventListener 包装）+ `onclick` 属性 + `tabindex≥0` + `cursor:pointer`；非语义元素也纳入快照与交互定位。大页面退化语义集（限成本）。
- **开放 shadow DOM**：手动递归 `element.shadowRoot`（querySelectorAll 不穿透）。
- **同源 iframe**：递归 `iframe.contentDocument`；交互点击/取中心用 `viewportRect` 逐级累加 iframe 偏移换算到主视口坐标。**跨域 iframe 保持缺口**（访问抛错跳过，cgclick 兜底）。
- **ref 签名重定位兜底**：`locateElJS` 按稳定 id 未命中时，用快照签名（tag/type/id/name/text）在活 DOM 找回（元素被重建/WeakMap 丢失时）。

## 阶段 4（已完成）：工具描述加固 + 指示器配色

- **工具描述加固**：全部 ref 工具（click/fill/type/select/check/uncheck/get_text/get_value/cgclick/drag）描述强化——`ref（@eN 稳定句柄，须先 snapshot 获取，DOM 变化后仍有效）优先；selector 仅用于 snapshot 无法捕获的元素（自定义/动态组件）；密码等敏感字段禁止用 selector 定位（必须用 ref）`。
- **指示器配色（方案 B）**：活动条改为**实色 accent 条**（浅色=蓝 #0a8dda、深色=浅青绿 #92c8b4）+ **对比自适应文字**（浅色=白字加粗、深色=近黑字，`colorScheme` 判断）——解决浅色下"同色相叠分不清"的问题；勾号/转圈同样自适应。弃用绿色。

## 阶段 4 全量回归（R1-R7，待测）
- R1 快照基础 / R2 S1 密码脱敏 / R3 S2 高敏域 L3 + 确认条人话 / R4 G2 指示器 / R5 G3 内容拦截 / R6 工具描述 ref 优先 / R7 稳定句柄终检。

## 八、改动文件

- **阶段 1**：`BrowserToolProvider.swift`（a11y 注册表、collectEls/locateElJS、交互迁移、snapshotItem/snapshotText 按 handle、blank guard、handleGetValue dictErr）、`BrowserActivityCenter.swift`（confirmationBody）、`ChatViewModel.swift`（接 confirmationBody）、`BrowserAgentBar.swift`（完成态浅绿 + lineLimit(3)）、`ChatView.swift`（lineLimit(3)）。
- **阶段 2**：`BrowserToolProvider.swift`（collectSnapshotJS scope/offset/limit/filter/hidden_below/pagination、handleSnapshot 重写、scrollIntoView、snapshot 工具描述）。

## 后续变更

- **2026-11-13**：`cg_click` / `cg_drag` 已删除（用户从未使用；CEF 引擎上本来也不可用），本文里「跨域 iframe 用 cgclick 兜底」的口径作废 —— 跨域 iframe 现在是**明确的缺口**（访问 `contentDocument` 抛错就跳过）。影响 `Shared/Tools/BrowserToolProvider.swift`。
