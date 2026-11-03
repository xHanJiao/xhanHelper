# ListItemCard 通用卡片组件设计

## 概述

为智能体、记忆、笔记、提示词等列表页面提供统一的卡片展示组件，兼顾桌面端与移动端体验。

---

## 设计原则

1. **风格统一**：所有列表页面使用相同的卡片样式
2. **响应式适配**：桌面端与移动端分别优化
3. **深色/浅色主题**：使用 CSS 变量自动适配
4. **简洁交互**：桌面端 hover 显示操作，移动端弹出菜单

---

## 组件 API

### Props

| 属性 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `title` | String | 是 | - | 卡片标题 |
| `content` | String | 否 | `''` | 卡片内容（可截断） |
| `tags` | Array | 否 | `[]` | 标签数组 |
| `metadata` | String | 否 | `''` | 底部元数据（时间、模型等） |
| `actions` | Array | 否 | `[]` | 右上角操作按钮配置 |
| `primaryActions` | Array | 否 | `[]` | 底部主操作按钮配置 |
| `hoverable` | Boolean | 否 | `true` | 是否启用悬停效果 |
| `maxContentHeight` | String | 否 | `'120px'` | 内容区域最大高度 |

### Actions 配置格式

```javascript
[
  {
    key: 'edit',           // 唯一标识
    label: '编辑',         // 按钮文案
    type: 'default',       // 按钮类型：default/primary/error 等
    onClick: () => { ... }, // 点击回调
    confirm: '确认删除?'   // 可选，需要确认时的提示文案
  }
]
```

---

## 卡片结构

```
┌─────────────────────────────────────────┐
│ 标题              [操作区]  ← 桌面端hover显示 │
├─────────────────────────────────────────┤
│  内容区域（可截断，max-height限制）      │
├─────────────────────────────────────────┤
│ [标签] [标签]                           │
├─────────────────────────────────────────┤
│ 元数据（时间、模型等）  [主操作按钮]    │
└─────────────────────────────────────────┘
```

---

## 交互设计

### 桌面端
- 默认状态：操作按钮隐藏（`opacity: 0`）
- Hover 状态：
  - 操作按钮淡入显示
  - 卡片阴影加深
  - NaiveUI 默认的 `hoverable` 效果

### 移动端
- 默认状态：显示 `⋯` 更多按钮
- 点击 `⋯`：弹出 `NDropdown` 下拉菜单
- 下拉菜单项支持确认弹窗

---

## 样式规范

### 颜色变量
使用全局 CSS 变量自动适配深色/浅色模式：

| 用途 | 变量 |
|------|------|
| 卡片背景 | `var(--color-surface)` |
| 边框 | `var(--color-border)` |
| 主文本 | `var(--color-text)` |
| 次要文本 | `var(--color-text-secondary)` |
| 辅助文本 | `var(--color-text-muted)` |
| 圆角 | `var(--radius-lg)` |
| 间距 | `var(--space-sm/md/lg)` |

### 样式细节

```css
.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.card-title {
  font-weight: 500;
  font-size: var(--font-size-md);
}

.desktop-actions {
  opacity: 0;
  transition: opacity 0.2s ease;
}

.list-item-card:hover .desktop-actions {
  opacity: 1;
}

.card-content {
  font-size: 14px;
  line-height: 1.6;
  overflow: hidden;
}
```

---

## 使用示例

### 智能体列表

```vue
<ListItemCard
  :title="agent.name"
  :content="agent.system_prompt"
  :metadata="`模型：${agent.model} · ${formatDate(agent.created_at)}`"
  :actions="[
    { key: 'edit', label: '编辑', onClick: () => openEdit(agent) },
    { key: 'delete', label: '删除', type: 'error', confirm: '确认删除此智能体？', onClick: () => handleDelete(agent) },
    { key: 'chat', label: '开始对话', type: 'primary', onClick: () => handleStartChat(agent) }
  ]"
/>
```

### 笔记列表

```vue
<ListItemCard
  :title="note.topic || '无标题'"
  :content="truncate(note.content, 300)"
  :tags="parseTags(note.tags)"
  :metadata="formatDateTime(note.created_at)"
  :actions="[
    { key: 'delete', label: '删除', type: 'error', confirm: '确认删除此笔记？', onClick: () => handleDelete(note) }
  ]"
  max-content-height="160px"
/>
```

### 记忆列表

```vue
<ListItemCard
  :title="mem.memory_key || '记忆'"
  :content="mem.memory_value"
  :metadata="`来源：${sourceMap[mem.source] || mem.source} · ${formatDateTime(mem.created_at)}`"
  :actions="[
    { key: 'delete', label: '删除', type: 'error', confirm: '确认删除此记忆？', onClick: () => handleDelete(mem) }
  ]"
  max-content-height="160px"
/>
```

---

## 迁移计划

按以下顺序逐个页面迁移：

1. ✅ 创建 `ListItemCard.vue` 组件
2. ✅ 修复并完善 `ListItemCard.vue` 组件
   - 完善移动端 dropdown 逻辑（用 NModal 替代 window.confirm）
   - 增加 `primaryActions` prop
3. ✅ 迁移 `AgentView.vue`（智能体管理）
4. ✅ 迁移 `NotesView.vue`（笔记管理）- 去掉组件外的 truncate
5. ✅ 迁移 `MemoryView.vue`（记忆管理）
6. ✅ 迁移 `PromptView.vue`（Prompt 管理）- 去掉特殊边框样式
7. ⏸️ `ToolView.vue`（工具管理）- 暂不迁移
8. ⏸️ `BillsView.vue`（账单管理）- 保持表格展示

---

## 注意事项

1. **账单页面**：账单使用表格展示，不强制迁移，可保持现状
2. **确认弹窗**：删除操作务必加确认
3. **内容截断**：过长内容通过 `maxContentHeight` 限制，不要在组件外截断；底部淡出用 `mask-image` 让文字本身淡出（不用覆盖色，半透明卡片/深色主题都对得上），且只在实测溢出（`scrollHeight > clientHeight`）时才加
4. **移动端测试**：务必在真实设备或模拟器上测试交互
