# Plan DAG 可视化方案

> 日期: 2026-05-09 | 状态: 设计阶段

## 背景

ReAct Agent 在一次迭代中可能并发调用多个工具，后端通过 SSE 推送 `plan_node_start` / `plan_node_done` / `plan` 事件描述本轮的工具执行 DAG。前端需要可视化展示节点执行状态和节点间的依赖关系。

本方案的目标：手写轻量实现，不引入 dagre/d3 等图库。

## 技术决策：手写 vs 图库

| 方案 | 优点 | 缺点 |
|------|------|------|
| dagre/d3 | 成熟布局算法 | 50KB+ minified，对 2~5 节点的小图杀鸡用牛刀，学习成本高 |
| **手写** | 零依赖、完全可控、代码量小 | 仅适合小规模 DAG |

本项目工具调用链路最多 2~5 个节点，手写布局算法不到 30 行，不引入额外依赖。

## 实现方案

### 1. 布局算法 — 拓扑分层

对 `planEdges` 做 BFS 拓扑排序，计算每个节点的 `layer`（层级）：

```
edges: [{from: "A", to: "B"}, {from: "A", to: "C"}, {from: "B", to: "D"}, {from: "C", to: "D"}]

分层后:
  Layer 0: [A]
  Layer 1: [B, C]
  Layer 2: [D]
```

算法：

```js
function computeLayers(edges) {
  const indeg = {}  // 入度
  const next = {}   // 邻接表

  for (const e of edges) {
    indeg[e.from] ??= 0
    indeg[e.to] = (indeg[e.to] || 0) + 1
    next[e.from] = next[e.from] || []
    next[e.from].push(e.to)
    next[e.to] = next[e.to] || []
  }

  // BFS: 入度为 0 的节点 → Layer 0
  let queue = Object.entries(indeg).filter(([, v]) => v === 0).map(([k]) => k)
  const layers = []
  const depth = {}

  while (queue.length) {
    layers.push(queue)
    const nq = []
    for (const u of queue) {
      depth[u] = layers.length - 1
      for (const v of next[u]) {
        if (--indeg[v] === 0) nq.push(v)
      }
    }
    queue = nq
  }

  return { layers, depth }
}
```

**坐标计算**：

- 节点高度固定 60px，垂直间距 40px → 每层高度 100px
- 节点宽度由内容决定（动态测量，见下文）
- 同层节点水平均分，间距 24px
- 最终每个节点 `{ x: 水平中心, y: 垂直中心, w, h }`

### 2. 渲染结构

```
┌─ 外层容器 (position: relative, width: 100%) ──────────────┐
│                                                            │
│  ┌─ SVG 画布 (position: absolute, left:0, top:0,          │
│  │             100%x100%, pointer-events: none) ──────┐    │
│  │  边线: <path> 贝塞尔曲线连接节点底/顶               │    │
│  │  ├─ running → 蓝色虚线 + dashoffset 动画            │    │
│  │  ├─ done → 绿色实线                                │    │
│  │  └─ pending → 灰色虚线                             │    │
│  └────────────────────────────────────────────────────┘    │
│                                                            │
│  ┌─ 节点卡片层 ───────────────────────────────────────┐    │
│  │  [bookkeeping.query_bills]  ✓ done                  │    │
│  │  [bookkeeping.create_bill]  ⏳ running (pulse动画)   │    │
│  │  [bookkeeping.update_bill]  ○ pending               │    │
│  └──────────────────────────────────────────────────────┘    │
│                                                            │
└────────────────────────────────────────────────────────────┘
```

- 外层容器相对定位
- 节点绝对定位在计算出的 `(x, y)` 坐标上
- SVG 画布绝对定位覆盖整个容器，`pointer-events: none` 不阻挡节点交互
- SVG 和节点都在同一个渲染周期更新，避免不同步

### 3. 边线绘制

使用 SVG `<path>` 画贝塞尔曲线：

```js
function computePath(from, to) {
  const startX = from.x, startY = from.y + from.h / 2  // 节点底部中心
  const endX = to.x, endY = to.y - to.h / 2              // 节点顶部中心
  const cy = (startY + endY) / 2
  return `M ${startX} ${startY} C ${startX} ${cy}, ${endX} ${cy}, ${endX} ${endY}`
}
```

边线动画（running 状态）：

```css
@keyframes dash-move {
  to { stroke-dashoffset: -20; }
}
.edge-running {
  stroke-dasharray: 8 4;
  animation: dash-move 0.6s linear infinite;
}
```

### 4. 动态尺寸测量

关键问题：节点宽高由中文工具名决定，不能预设固定值。节点必须先渲染到 DOM，再读取实际尺寸画边线。

流程：

```
1. plan 事件到达 → 设置 chat.planNodes / chat.planEdges
2. PlanGraph 渲染节点卡片（v-for + ref）
3. nextTick → getBoundingClientRect() 读取每个节点的实际 w/h
4. computeLayers() + 计算坐标（基于实际尺寸）
5. 重排节点位置 + 重绘 SVG 边线
```

实现要点：

```js
const nodeRefs = ref({})  // { [nodeId]: HTMLElement }

onUpdated(async () => {
  await nextTick()
  const rects = {}
  for (const [id, el] of Object.entries(nodeRefs.value)) {
    rects[id] = el.getBoundingClientRect()
  }
  // 基于 rects 计算坐标 → 更新节点位置 + SVG path
})
```

也可以用 `ResizeObserver` 做更稳健的监听，但对这种一次性布局 `nextTick` + `getBoundingClientRect` 足够。

### 5. 状态动画

| 状态 | 颜色 | 效果 |
|------|------|------|
| pending | #d9d9d9（灰色） | 静态，文字灰色 |
| running | #2080f0（蓝色） | CSS pulse 动画（box-shadow 呼吸） |
| done | #18a058（绿色） | 淡入（CSS transition opacity） |
| failed | #d03050（红色） | 抖动动画 |

```css
@keyframes pulse {
  0%, 100% { box-shadow: 0 0 0 0 rgba(32, 128, 240, 0.4); }
  50% { box-shadow: 0 0 0 8px rgba(32, 128, 240, 0); }
}
.node-running {
  animation: pulse 1.5s ease-in-out infinite;
}
```

### 6. 组件接口设计

```vue
<!-- PlanGraph.vue -->
<script setup>
const props = defineProps({
  nodes: Object,   // { [id]: { label, status } }
  edges: Array,    // [{ from, to }]
  collapsed: Boolean
})

const emit = defineEmits(['toggle'])
</script>
```

- 当 `nodes` 为空或单节点时不渲染 DAG（单工具不展示）
- 支持手动收起/展开（`n-collapse` 或自定义 toggle）
- 消息列表滚动到附近时出现（可选，防止视觉干扰）

### 7. 完整渲染流程

```
SSE 事件到达
  │
  ├─ plan_node_start → chat.planNodes[id] = { label, status: 'running' }
  │   └─ PlanGraph 响应式更新节点状态
  │
  ├─ plan_node_done → chat.planNodes[id].status = 'done' | 'failed'
  │   └─ PlanGraph 更新节点 + 边线颜色
  │
  └─ plan → chat.planNodes = {...}, chat.planEdges = [...]
      └─ PlanGraph 全量重排 + 重绘
          ├─ nextTick → 测量节点尺寸
          ├─ computeLayers() → 计算坐标
          ├─ 更新节点绝对定位
          └─ 重绘 SVG 边线
```

## 风险与对策

| 风险 | 对策 |
|------|------|
| 超长工具名撑破节点宽度 | CSS `max-width` + `text-overflow: ellipsis`，hover 时 tooltip 显示全名 |
| 后端不推送 `plan` 事件（单工具） | 不渲染 DAG，仅在 ChatView 中展示工具调用卡片 |
| SVG 边线覆盖节点导致点击失效 | `pointer-events: none` 在 SVG 上，节点在 SVG 上方 |
| 多轮 Agent 迭代导致图表重建抖动 | 节点位置缓存，只在 `plan` 事件时全量重排，`plan_node_*` 仅改状态颜色 |
| 响应式窗口缩放 | 用百分比 + 容器宽度计算坐标，监听 `resize` 事件重新布局 |

## 不使用图库的理由（总结）

- 本项目 DAG 最多 5 节点，dagre minified 50KB+ 是过度引入
- 拓扑分层算法不到 30 行代码
- 无额外依赖，维护成本低
- 动画和交互完全可控
