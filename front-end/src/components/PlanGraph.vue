<script setup>
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { NCollapse, NCollapseItem } from 'naive-ui'

const props = defineProps({
  nodes: { type: Object, default: () => ({}) },
  edges: { type: Array, default: () => [] },
  visible: { type: Boolean, default: false }
})

const emit = defineEmits(['toggle'])

const containerRef = ref(null)
const nodeRefs = ref({})
const nodePositions = ref({})
const edgePaths = ref([])
const containerWidth = ref(800)
const expandedNames = ref(['plan'])

const H_SPACING = 24
const NODE_H = 60
const V_SPACING = 40
const LAYER_H = NODE_H + V_SPACING

const nodeIds = computed(() => Object.keys(props.nodes))

const showGraph = computed(() => props.visible && nodeIds.value.length >= 2)

function computeLayers() {
  const ids = nodeIds.value
  if (ids.length === 0) return { layers: [], depth: {} }

  const indeg = {}
  const next = {}
  for (const id of ids) {
    indeg[id] = 0
    next[id] = []
  }
  for (const e of props.edges) {
    if (indeg[e.to] !== undefined) indeg[e.to]++
    next[e.from]?.push(e.to)
  }

  let queue = Object.entries(indeg).filter(([, v]) => v === 0).map(([k]) => k)
  if (queue.length === 0) queue = [ids[0]]

  const layers = []
  const depth = {}
  while (queue.length) {
    layers.push(queue)
    const nq = []
    for (const u of queue) {
      depth[u] = layers.length - 1
      for (const v of next[u] || []) {
        if (--indeg[v] === 0) nq.push(v)
      }
    }
    queue = nq
  }

  return { layers, depth }
}

function layoutNodes() {
  const ids = nodeIds.value
  if (ids.length === 0) return

  const { layers } = computeLayers()
  const positions = {}

  layers.forEach((layer, li) => {
    const count = layer.length
    const y = li * LAYER_H + NODE_H / 2

    layer.forEach((id, ci) => {
      let w = 200
      const el = nodeRefs.value[id]
      if (el) {
        w = el.getBoundingClientRect().width
      }
      const totalW = count * (200 + H_SPACING) - H_SPACING
      const startX = (containerWidth.value - totalW) / 2
      const x = startX + ci * (200 + H_SPACING) + 100

      positions[id] = { x, y, w, h: NODE_H }
    })
  })

  nodePositions.value = positions
}

function computeEdgePaths() {
  const positions = nodePositions.value
  const paths = []
  for (const e of props.edges) {
    const from = positions[e.from]
    const to = positions[e.to]
    if (!from || !to) continue

    const startX = from.x
    const startY = from.y + from.h / 2
    const endX = to.x
    const endY = to.y - to.h / 2
    const cy = (startY + endY) / 2

    const d = `M ${startX} ${startY} C ${startX} ${cy}, ${endX} ${cy}, ${endX} ${endY}`

    let status = 'pending'
    if (props.nodes[e.from] && props.nodes[e.to]) {
      if (props.nodes[e.from].status === 'done' && props.nodes[e.to].status === 'done') {
        status = 'done'
      } else if (props.nodes[e.from].status === 'running' || props.nodes[e.to].status === 'running') {
        status = 'running'
      }
    }

    paths.push({ d, status, from: e.from, to: e.to })
  }
  edgePaths.value = paths
}

function updateLayout() {
  if (containerRef.value) {
    containerWidth.value = containerRef.value.getBoundingClientRect().width || 800
  }
  nextTick(() => {
    layoutNodes()
    nextTick(() => {
      computeEdgePaths()
    })
  })
}

let resizeObserver = null

onMounted(() => {
  resizeObserver = new ResizeObserver(() => {
    if (showGraph.value) updateLayout()
  })
  if (containerRef.value) {
    resizeObserver.observe(containerRef.value)
  }
  updateLayout()
})

onUnmounted(() => {
  resizeObserver?.disconnect()
})

watch(() => props.nodes, () => {
  if (showGraph.value) updateLayout()
}, { deep: true })

watch(() => containerRef.value, () => {
  if (containerRef.value && resizeObserver) {
    resizeObserver.observe(containerRef.value)
  }
})

function setNodeRef(id, el) {
  if (el) nodeRefs.value[id] = el
}
</script>

<template>
  <div v-if="showGraph" class="plan-graph-wrapper">
    <NCollapse v-model:expanded-names="expandedNames">
      <NCollapseItem title="本轮执行计划" name="plan">
        <div ref="containerRef" class="plan-graph-container">
          <svg class="plan-graph-edges" :viewBox="`0 0 ${containerWidth} ${nodeIds.length > 0 ? (Object.keys(nodePositions).length > 0 ? Math.max(...Object.values(nodePositions).map(p => p.y)) + NODE_H : 100) : 100}`">
            <path
              v-for="(ep, idx) in edgePaths"
              :key="idx"
              :d="ep.d"
              :class="['edge', `edge-${ep.status}`]"
              fill="none"
              stroke-width="2"
            />
          </svg>
          <div
            v-for="id in nodeIds"
            :key="id"
            :ref="(el) => setNodeRef(id, el)"
            class="plan-node"
            :class="`node-${nodes[id]?.status || 'pending'}`"
            :style="nodePositions[id] ? {
              position: 'absolute',
              left: (nodePositions[id].x - 100) + 'px',
              top: (nodePositions[id].y - NODE_H / 2) + 'px',
              width: '200px'
            } : {}"
          >
            <div class="node-label">{{ nodes[id]?.label || id }}</div>
            <div class="node-status">
              <span v-if="nodes[id]?.status === 'running'" class="status-badge running">执行中</span>
              <span v-else-if="nodes[id]?.status === 'done'" class="status-badge done">完成</span>
              <span v-else-if="nodes[id]?.status === 'failed'" class="status-badge failed">失败</span>
              <span v-else class="status-badge pending">等待</span>
            </div>
          </div>
        </div>
      </NCollapseItem>
    </NCollapse>
  </div>
</template>

<style scoped>
.plan-graph-wrapper {
  margin: var(--space-md) 0;
}
.plan-graph-container {
  position: relative;
  min-height: 100px;
  overflow: visible;
}
.plan-graph-edges {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  overflow: visible;
  pointer-events: none;
}
.edge {
  stroke-dasharray: 6 3;
}
.edge-pending {
  stroke: #d9d9d9;
}
.edge-running {
  stroke: #2080f0;
  stroke-dasharray: 8 4;
  animation: dash-move 0.6s linear infinite;
}
.edge-done {
  stroke: #18a058;
}
.edge-failed {
  stroke: #d03050;
}

.plan-node {
  background: var(--color-surface);
  border: 2px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: var(--space-md) 14px;
  text-align: center;
  transition: border-color 0.3s, box-shadow 0.3s;
}
.node-pending {
  border-color: #d9d9d9;
}
.node-running {
  border-color: #2080f0;
  animation: node-pulse 1.5s ease-in-out infinite;
}
.node-done {
  border-color: #18a058;
}
.node-failed {
  border-color: #d03050;
}
.node-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--color-text);
  margin-bottom: var(--space-xs);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.status-badge {
  font-size: 12px;
  padding: 1px var(--space-sm);
  border-radius: var(--radius-md);
}
.status-badge.pending {
  color: var(--color-text-muted);
  background: var(--color-bg);
}
.status-badge.running {
  color: #2080f0;
  background: #e8f0fe;
}
.status-badge.done {
  color: #18a058;
  background: #e8f5e9;
}
.status-badge.failed {
  color: #d03050;
  background: #fff0f0;
}

@keyframes dash-move {
  to { stroke-dashoffset: -24; }
}
@keyframes node-pulse {
  0%, 100% { box-shadow: 0 0 0 0 rgba(32, 128, 240, 0.3); }
  50% { box-shadow: 0 0 0 8px rgba(32, 128, 240, 0.1); }
}
</style>
