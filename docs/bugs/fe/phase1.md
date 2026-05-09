# Phase 1 Bug 记录

## 001 — 用户消息气泡文字上下垂直空白过高

- **状态**：已解决
- **发现日期**：2026-05-09
- **影响范围**：`MessageBubble.vue` 用户消息气泡

### 复现

用户在 ChatView 中发送任意短文本，渲染后的用户消息气泡内文字上下出现明显多余空白。

### 原因

markdown-it 渲染消息内容时自动包裹 `<p>` 标签，`<p>` 的默认 `margin` 导致额外垂直空白。

### 修复

在用户气泡容器上添加 CSS 规则 `p { margin: 0; }` 清除 markdown-it 生成的段落 margin。

## 002 — 中文输入法下 Enter 发送吞字 + 输入框不清空

- **状态**：已解决
- **发现日期**：2026-05-09
- **影响范围**：`ChatView.vue` 输入框 `@keydown` 事件

### 复现

使用中文输入法（拼音）输入文字，按 Enter 键发送。拼音组合中的内容（如 "x h"）被吞掉，未发送到后端，且输入框内容未被清空。

### 原因

`@keydown` 监听 `e.key === 'Enter'` 时未检测 IME composition 状态。当 IME 处于组合态时，Enter 键同时触发"确认组合"和"发送消息"，此时 `inputValue` 尚未包含组合后的文字，导致拼音部分丢失。`inputValue.value = ''` 清空后 IME 又插入了组合文字，导致看起来没清空。

### 修复

`@keydown` 处理中增加 `!e.isComposing` 条件，IME 组合中按 Enter 不触发发送。
