# 剩余优化项

> 日期: 2026-05-09 | 状态: 待排期

Phase 1~4 功能开发已全部完成，以下是代码审查和体验自查中发现的可优化点，不影响核心功能，按优先级排列。

## 中优先级

### 1. PlanGraph 深色模式颜色适配

- **文件**: `PlanGraph.vue`
- **问题**: 节点状态徽章使用硬编码的浅色背景（`#e8f0fe`、`#e8f5e9`、`#fff0f0`），在深色表面上看会发白/发亮
- **方向**: 为 running/done/failed/pending 状态增加 `[data-theme="dark"]` 覆盖

### 2. ConversationList SVG 描边无主题适配

- **文件**: `ConversationList.vue`
- **问题**: 会话图标和操作按钮图标用 `stroke="#666"`，深色模式下与 `#222244` 表面对比度偏低（~3.1:1）
- **方向**: 改为 `stroke="currentColor"` 或 `stroke="var(--color-text-secondary)"`

### 3. `--color-border` 深色模式对比度偏低

- **文件**: `style.css`
- **问题**: `--color-border: #333355` 在 `--color-surface: #222244` 上，边框几乎不可见
- **方向**: 调整到 `#3d3d5c` 或 `#444466`

## 低优先级

### 4. `markdown-it` 每个组件实例化一次

- **文件**: `MessageBubble.vue`
- **问题**: `const md = new MarkdownIt()` 在每个气泡实例中重复创建。聊天 50 条消息就是 50 个实例
- **方向**: 移到模块顶层（`<script setup>` 外面）

### 5. `auth.js` 未使用的 `watch` 导入

- **文件**: `stores/auth.js`
- **问题**: `import { ref, watch } from 'vue'`，`watch` 未使用

### 6. 死代码 `HelloWorld.vue` + Vite 脚手架资源

- **文件**: `components/HelloWorld.vue`、`assets/hero.png`、`assets/vite.svg`、`assets/vue.svg`
- **问题**: Vite 默认脚手架文件，未被任何组件引用

### 7. ConversationList 双重 hover 机制冗余

- **文件**: `ConversationList.vue`
- **问题**: 同一元素同时绑定了 `@mouseenter`/`@mouseleave`（设置 `hoveredId`）和 `@mouseover`/`@mouseout`（直接操作 `element.style.background`），两种机制会互相覆盖
- **方向**: 统一为 Vue 响应式的 `hoveredId` 方案，去掉 DOM 直接操作

## 体验微调

### 8. MemoryView/NotesView 无数据时加载状态

- **问题**: 首次进入时空白区域较长才显示 "还没有记忆/笔记"
- **方向**: 加 `NSpin` 包裹或 skeleton 占位
