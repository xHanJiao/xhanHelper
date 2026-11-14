# 067 · 阅读器模板新增 `report()` 覆盖了已有的同名函数，文档阅读器的查找静默失效

## 表现

`check-reader-html.sh` 报 `FAIL 抛出异常: Cannot read properties of undefined (reading 'count')`，只有一条断言挂（`占位文字不进查找/批注锚点`），前面 160 条全绿。

真机症状会隐蔽得多：文档阅读器里 `find()` 仍然会滚到命中处，但**不再回报 `findResult`** —— 查找栏不显示「1/3」、上下箭头不生效，也没有任何报错。

## 根因

`DocumentReaderView.swift` 的模板是**一个 IIFE**，里面本来就有：

```js
function report() { post({ type: 'findResult', count: findRanges.length, index: findIdx }); }
```

加导航诊断时又声明了 `function report(kind, extra)`。函数声明会提升，**后声明的同名函数把前一个整体覆盖**，于是 `find()` 末尾的 `report()` 调用变成发一条 `{type:'diag'}`，`findResult` 再也不会出现。脚本不报错，因为两个函数都"能跑"。

## 处理

诊断函数改名 `navDiag(kind, extra)`。没有别的改动 —— 覆盖一消失，`findResult` 自动恢复。

## 教训（这条是重点）

模板是单 IIFE、同一作用域，加任何 `function x()` / `var x` 之前**先 `rg` 一遍同名**。同名不是报错而是静默覆盖，症状会落在**别的功能**上，极难联想到。

现有自查脚本能抓到这类问题，前提是**两个都跑**：

- `check-reader-html.sh`（文档模式 176 条）—— 这次就是它抓到的；
- `book-reader-probe.sh`（书模式 15 条 × 2 宽度）—— 只跑这个看不出问题，因为书模式不走 `findResult` 的断言。

（同一条纪律在 AGENTS.md 的「UI 预览」段里已经写过一次，是针对 mockup 的 HTML 预览；WKWebView 模板同理。）
