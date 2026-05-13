<script setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { api } from '@/api/client'
import { NCard, NTag, NSpin } from 'naive-ui'
import { Chart, registerables } from 'chart.js'

Chart.register(...registerables)

const health = ref(null)
const error = ref(null)
let timer = null

async function fetchHealth() {
  try {
    const data = await api.get('/api/admin/health')
    health.value = data
    error.value = null
  } catch (e) {
    error.value = e.message || '获取失败'
  }
}

onMounted(() => {
  fetchHealth()
  timer = setInterval(fetchHealth, 10000)
})

onUnmounted(() => {
  clearInterval(timer)
  destroyCharts()
})

// ── 图表 ──

let llmChart = null
let toolChart = null
let pipelineChart = null

function renderLLMChart(el, data) {
  if (llmChart) llmChart.destroy()
  const callers = Object.keys(data)
  const datasets = [
    { label: '成功', data: callers.map(k => data[k].calls - data[k].errors), backgroundColor: '#5b6abf' },
    { label: '失败', data: callers.map(k => data[k].errors), backgroundColor: '#e74c3c' }
  ]
  llmChart = new Chart(el, {
    type: 'bar',
    data: { labels: callers, datasets },
    options: { responsive: true, plugins: { legend: { display: false } }, scales: { x: { stacked: true }, y: { stacked: true, beginAtZero: true } } }
  })
}

function renderToolChart(el, data) {
  if (toolChart) toolChart.destroy()
  const items = []
  for (const [tool, funcs] of Object.entries(data)) {
    for (const [fn, c] of Object.entries(funcs)) {
      items.push({ label: `${tool}.${fn}`, calls: c.calls + c.errors, errors: c.errors })
    }
  }
  items.sort((a, b) => b.calls - a.calls)
  toolChart = new Chart(el, {
    type: 'bar',
    data: {
      labels: items.map(i => i.label),
      datasets: [
        { label: '成功', data: items.map(i => i.calls - i.errors), backgroundColor: '#5b6abf' },
        { label: '失败', data: items.map(i => i.errors), backgroundColor: '#e74c3c' }
      ]
    },
    options: { responsive: true, indexAxis: 'y', plugins: { legend: { display: false } }, scales: { x: { stacked: true, beginAtZero: true }, y: { stacked: true } } }
  })
}

function renderPipelineChart(el, data) {
  if (pipelineChart) pipelineChart.destroy()
  const steps = Object.entries(data)
  steps.sort((a, b) => b[1].avg_ms - a[1].avg_ms)
  pipelineChart = new Chart(el, {
    type: 'bar',
    data: {
      labels: steps.map(s => s[0]),
      datasets: [{ label: 'avg ms', data: steps.map(s => s[1].avg_ms), backgroundColor: '#5b6abf' }]
    },
    options: { responsive: true, indexAxis: 'y', plugins: { legend: { display: false } }, scales: { x: { beginAtZero: true } } }
  })
}

function destroyCharts() {
  if (llmChart) { llmChart.destroy(); llmChart = null }
  if (toolChart) { toolChart.destroy(); toolChart = null }
  if (pipelineChart) { pipelineChart.destroy(); pipelineChart = null }
}

watch(health, (val) => {
  if (!val) return
  setTimeout(() => {
    const llmEl = document.getElementById('llm-chart')
    const toolEl = document.getElementById('tool-chart')
    const pipelineEl = document.getElementById('pipeline-chart')
    if (llmEl && val.counters?.llm_calls) renderLLMChart(llmEl, val.counters.llm_calls)
    if (toolEl && val.counters?.tool_calls) renderToolChart(toolEl, val.counters.tool_calls)
    if (pipelineEl && val.pipeline) renderPipelineChart(pipelineEl, val.pipeline)
  }, 200)
}, { deep: true })

// ── helpers ──
function fmtMB(v) { if (v == null || v === -1) return 'N/A'; return v.toFixed(1) + ' MB' }
function uptime(s) { const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60); return h > 0 ? `${h}h ${m}m` : `${m}m` }
</script>

<template>
  <div style="padding: var(--space-lg); overflow-y: auto; height: 100%;">
    <h2 style="margin-bottom: var(--space-md);">系统监控</h2>
    <NSpin v-if="!health && !error" />
    <div v-if="error" style="color: var(--color-danger); padding: var(--space-md);">{{ error }}</div>

    <template v-if="health">
      <!-- 卡片行 -->
      <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: var(--space-md); margin-bottom: var(--space-lg);">
        <NCard size="small" title="运行时间">
          <div style="font-size: 24px; font-weight: 600;">{{ uptime(health.uptime_seconds) }}</div>
        </NCard>
        <NCard size="small" title="goroutines">
          <div style="font-size: 24px; font-weight: 600;">{{ health.goroutines }}</div>
        </NCard>
        <NCard size="small" title="内存">
          <div style="font-size: 14px;">堆: {{ fmtMB(health.memory?.heap_alloc_mb) }}</div>
          <div style="font-size: 12px; color: var(--color-text-muted);">RSS: {{ fmtMB(health.memory?.os_rss_mb) }}</div>
        </NCard>
        <NCard size="small" title="SSE 连接">
          <div style="font-size: 24px; font-weight: 600;">{{ health.counters?.sse_connections ?? 0 }}</div>
        </NCard>
      </div>

      <!-- 图表 -->
      <div style="display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-md); margin-bottom: var(--space-lg);">
        <NCard size="small" title="LLM 调用">
          <canvas id="llm-chart" />
        </NCard>
        <NCard size="small" title="管线耗时 (avg ms)">
          <canvas id="pipeline-chart" />
        </NCard>
      </div>

      <NCard size="small" title="工具调用" style="margin-bottom: var(--space-lg);">
        <canvas id="tool-chart" />
      </NCard>

      <!-- MCP 状态 -->
      <NCard size="small" title="MCP 服务" style="margin-bottom: var(--space-lg);">
        <div v-if="health.snapshots?.mcp">
          <div v-for="(info, name) in health.snapshots.mcp" :key="name" style="display: flex; align-items: center; gap: var(--space-sm); padding: var(--space-xs) 0;">
            <span style="font-weight: 500;">{{ name }}</span>
            <NTag :type="info.state === 'ready' ? 'success' : info.state === 'error' ? 'error' : 'warning'" size="small">{{ info.state }}</NTag>
            <span style="font-size: 12px; color: var(--color-text-muted);">工具: {{ info.tools }} | 执行中: {{ info.in_flight }}</span>
          </div>
        </div>
        <div v-else style="color: var(--color-text-muted);">无 MCP 服务</div>
      </NCard>

      <!-- 向量存储 -->
      <NCard size="small" title="向量存储" style="margin-bottom: var(--space-lg);">
        <div v-if="health.snapshots?.vector_store">
          <div>条目: {{ health.snapshots.vector_store.total_entries }}</div>
          <div v-for="(count, type) in health.snapshots.vector_store.by_type" :key="type" style="font-size: 13px; color: var(--color-text-muted);">
            {{ type }}: {{ count }}
          </div>
          <div style="font-size: 12px; color: var(--color-text-muted);">估算内存: {{ (health.snapshots.vector_store.estimated_bytes / 1024).toFixed(0) }} KB</div>
        </div>
      </NCard>

      <!-- 活跃会话 -->
      <NCard size="small" title="活跃会话">
        <div style="font-size: 24px; font-weight: 600;">{{ health.snapshots?.active_conversations ?? 0 }}</div>
        <div style="font-size: 12px; color: var(--color-text-muted);">最近 1 小时内</div>
      </NCard>
    </template>
  </div>
</template>
