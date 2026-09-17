# 文档问 AI（P5）真机两例：存批注后窄条不消失 / 长批注浮层一滚就被关掉

P5「问 AI」代码写完、自查脚本全绿之后就上真机了，用户当场抓到两个。两个都不在自查脚本的覆盖里 —— 一个是「动作做完没清状态」，一个是「监听器把后代事件也收下了」。记在这，免得下一个人重新踩。

## 一、点「存为批注」之后，那条窄条一直不消失

### 问题表现

文档开着，点窄条上的「存为批注」→ 批注浮层正常弹出、预填也对（用户还确认了「追加到已有批注」那条路），但聊天栏那条「把这段回答存为批注？」**一直在**，点几次它就再弹几次浮层。

### 根因

窄条的显示条件是「引用还在」（`viewModel.docQuote != nil` + 引用之后又出过一轮回答），而 `ChatView.storeQuoteAsNote` 的两条分支**都没清引用**：

```swift
if let reader = DocumentAIBridge.shared.reader, reader.documentID == quote.documentID {
    reader.beginNoteFromAI(quote: quote, note: answer)
    return                    // ← 交完就 return，docQuote 原封不动
}
Task { ... createAnnotation(...) }   // ← 成功之后也只 toast
```

交浮层那一支尤其容易漏：动作是「异步完成」的（用户还要在浮层里改、再点保存），写的时候下意识觉得"还没存完，先别清"。

### 修复

两条路都清，位置各不相同：

- 交浮层那支：`beginNoteFromAI` 之后**立刻** `docQuote = nil` —— 回答已经进了编辑框，留着这条只会重复存；用户取消浮层就等于放弃这次存
- 直接 POST 那支：**成功之后**再清（失败不清，还能重试）

### 钉子

`check-reader-html.sh` 里没有这条（它是 Swift 侧的 vm 状态，JS 断言够不着）。真机验收清单 §十一 补了「存完窄条即刻消失」；同一段再问再存要**追加**不新建，这条有 JS 断言钉住（`openNoteFromAI` 同 quote → 预填追加 → 保存发 `update`）。

## 二、长批注的 hover 浮层：一滚就被关掉，看着像「只有文档在滚」

### 问题表现

AI 的长回答存成批注后，hover 那条线弹出的卡片上千字。用户原话：**「文字无法完整展示，拖动滚动条却会滚动文档而非批注」**。

第一次只修了一半（卡片没有 `max-height`，`position: fixed` 超出视口那截永远拿不到）→ 加了封顶 `min(62vh, 520px)` + `overflow-y: auto`。结果用户回来说：**「卡片是小了，但上下滚动还是文档在滚，即使指针在卡片内部」**。

### 根因：capture 阶段的 window scroll 监听把后代滚动也收下了

```js
window.addEventListener('scroll', function () { hideAll(); hidePopover(); }, true);
```

`scroll` 事件**不冒泡**，但**capture 阶段的监听器照样会命中后代元素**（`e.target` 就是被滚的那个元素）。浮层自己成了滚动容器之后：

1. 指针在卡片里滚 → 浏览器先滚卡片（`overflow-y: auto` 生效）
2. 这一滚派发 scroll 事件 → 冒到 window 的 capture 监听 → **`hidePopover()` 把卡片关掉**
3. 卡片没了，滚轮自然落到正文上 → 用户看到的是"文档在滚"，而且长批注永远读不完

**次生问题**：`hidePopover()` 不重置 `hoverKey`（只有 `scheduleHidePopover()` 重置）。卡片被这条路径关掉后，光标只要还在 `#content` 里，再 hover 同一处会走 `mousemove` 处理里的 `if (key === hoverKey) return;` **提前返回 → 浮层再也开不出来**（要先把光标移出正文再移回去）。同一个坑在写自查脚本时就踩到过一次，当时用「先往空白处虚晃一下清 hoverKey」绕过去了 —— 那其实是把这个真 bug 绕进了测试里。

### 修复

```js
window.addEventListener('scroll', function (e) {
  if (popover.contains(e.target)) return;   // 浮层自己的内滚不算「页面滚动」
  hideAll(); hidePopover();
}, true);

function hidePopover() { hoverKey = ''; popover.classList.remove('open'); }
```

- 页面滚动照旧关浮层（P3 的语义不变：滚动后浮层不该停在旧坐标上）
- `hoverKey` 的复位收进 `hidePopover()`，任何隐藏路径都不再留下卡住的 key（`scheduleHidePopover` 里那句随之删掉，状态只维护一处）
- `.popover` 上早就写着的 `overscroll-behavior: contain` 这时才真正派上用场：滚到卡片底部不把滚动串给正文，也就不会派发页面 scroll 把卡片关掉
- 顺带按用户要求**卡片里不显示滚动条**（`scrollbar-width: none` + `.popover::-webkit-scrollbar { width: 0 }`），内容照样滚

### 钉子

- `check-reader-html.sh` 3 条：内滚的 scroll 事件不关卡片 / 页面滚动的 scroll 事件仍然关 / 关掉后再 hover 同一处还能开。**变异验证**：把两处改回旧写法 → `open=false scrollTop=0` 立刻红
- `snapshot-reader.sh` 新增 `marks-longnote`（封顶布局）与 `marks-longnote-scroll`（内滚后采真实状态）两个真引擎 job。真引擎里：改后 `innerScroll=open scrollTop=200 scrollHeight=2576`；改前 `innerScroll=closed scrollTop=0` —— 和用户看到的一模一样
- 写这个 job 时又踩到一次「hoverKey 卡住」（这次把它当回归测了）：所以 job 里**必须把上一个 job 留着的补 hover 定时器停掉**，否则它会把被滚掉的浮层重新开起来，内滚那一步就测不出来

## 留给以后的

- **`window` 上的 capture 监听要当心后代事件**：这页里还有 `mouseup` / `mousedown` / `keydown` 三个 `document` 监听，它们都靠 `e.target.closest('.ui')` 白名单排除浮层。以后再往浮层里加可交互元素（按钮、输入框），记得同时在白名单里放行，别用「事件从哪儿冒上来」的直觉去判
- **浮动 UI 想让它自己滚，就得同时处理三件事**：封顶（`max-height`）+ 到底不串滚（`overscroll-behavior: contain`）+ 别被页面滚动的副作用关掉（本例的 capture 监听）。少一件都会表现成「滚不动」
- 自查脚本覆盖不到 Swift 侧的 vm 状态（例一），这类只能靠真机验收清单；清单在 design.md §十一
