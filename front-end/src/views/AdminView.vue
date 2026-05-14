<script setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { api } from '@/api/client'
import { NCard, NTag, NSpin, NSelect } from 'naive-ui'
import { Chart, registerables } from 'chart.js'
import PageTopBar from '@/components/PageTopBar.vue'

Chart.register(...registerables)

const health = ref(null)
const error = ref(null)
let timer = null

const refreshOptions = [
  { label: '关闭', value: 0 },
  { label: '5 秒', value: 5000 },
  { label: '10 秒', value: 10000 },
  { label: '30 秒', value: 30000 },
  { label: '60 秒', value: 60000 }
]
const refreshInterval = ref(10000)

function restartTimer() {
  clearInterval(timer)
  if (refreshInterval.value > 0) {
    timer = setInterval(fetchHealth, refreshInterval.value)
  }
}

watch(refreshInterval, restartTimer)

async function fetchHealth() {
  try {
    health.value = await api.get('/api/admin/health')
    error.value = null
  } catch (e) {
    error.value = e.message || '获取失败'
  }
}

onMounted(() => { fetchHealth(); restartTimer() })
onUnmounted(() => { clearInterval(timer); destroyCharts() })

// ── charts ──

let chartInstances = []

function makeLineChart(id, datasets) {
  const canvas = document.getElementById(id)
  if (!canvas) return
  const colors = ['#5b6abf', '#e74c3c', '#27ae60', '#f39c12', '#8e44ad']
  Chart.getChart(id)?.destroy()
  const ds = datasets.map((d, i) => ({
    label: d.label, data: d.data, borderColor: colors[i % colors.length],
    backgroundColor: colors[i % colors.length] + '22', fill: false, pointRadius: 0, tension: 0.3
  }))
  const ch = new Chart(canvas, {
    type: 'line',
    data: { labels: datasets[0]?.data?.map((_, i) => i + 1) || [], datasets: ds },
    options: { responsive: true, plugins: { legend: { position: 'top', labels: { boxWidth: 12, font: { size: 11 } } } }, scales: { x: { display: false }, y: { beginAtZero: true, title: { display: true, text: 'ms' } } } }
  })
  chartInstances.push(ch)
}

function makeBarChart(id, labels, datasets, horizontal) {
  const canvas = document.getElementById(id)
  if (!canvas) return
  Chart.getChart(id)?.destroy()
  const ch = new Chart(canvas, {
    type: 'bar',
    data: { labels, datasets },
    options: { responsive: true, indexAxis: horizontal ? 'y' : 'x', plugins: { legend: { display: labels.length > 1, position: 'top', labels: { boxWidth: 12, font: { size: 11 } } } }, scales: horizontal ? { x: { beginAtZero: true } } : { x: { stacked: true }, y: { stacked: true, beginAtZero: true } } }
  })
  chartInstances.push(ch)
}

function renderAllCharts() {
  destroyCharts()
  const h = health.value; if (!h) return

  // ── LLM latency: one line per caller ──
  const llmLats = h.latency?.llm
  if (llmLats) {
    const datasets = []
    for (const [caller, s] of Object.entries(llmLats)) {
      if (s.recent?.length) datasets.push({ label: caller, data: s.recent })
    }
    if (datasets.length) makeLineChart('llm-lat-chart', datasets)
  }

  // ── Tool latency: one line per tool.fn (top 5) ──
  const toolLats = h.latency?.tool
  if (toolLats) {
    const items = []
    for (const [tool, funcs] of Object.entries(toolLats)) {
      for (const [fn, s] of Object.entries(funcs)) {
        if (s.recent?.length) items.push({ label: tool + '.' + fn, data: s.recent, count: s.count })
      }
    }
    items.sort((a, b) => b.count - a.count)
    if (items.length) makeLineChart('tool-lat-chart', items.slice(0, 5))
  }

  // ── Pipeline: horizontal bar ──
  const pipeLats = h.latency?.pipeline
  if (pipeLats) {
    const steps = Object.entries(pipeLats).filter(([, s]) => s.count > 0)
    if (steps.length) {
      steps.sort((a, b) => b[1].avg_ms - a[1].avg_ms)
      makeBarChart('pipeline-chart', steps.map(s => s[0]), [{ label: 'avg ms', data: steps.map(s => s[1].avg_ms), backgroundColor: '#5b6abf' }], true)
    }
  }

  // ── Embedding latency ──
  const embLat = h.latency?.embedding
  if (embLat?.recent?.length) makeLineChart('embedding-chart', [{ label: 'embedding', data: embLat.recent }])

}

function destroyCharts() {
  chartInstances.forEach(c => c.destroy?.())
  chartInstances = []
}

watch(health, () => { if (health.value) setTimeout(renderAllCharts, 100) }, { deep: true })

// ── helpers ──
function fmtMB(v) { if (v == null || v === -1) return 'N/A'; return (v * 1).toFixed(1) + ' MB' }
function uptime(s) { const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60); return h > 0 ? `${h}h ${m}m` : `${m}m` }
function latSummary(s) {
  if (!s || !s.count) return '暂无数据'
  return `avg: ${s.avg_ms}ms | P50: ${s.p50_ms}ms | P99: ${s.p99_ms}ms | 样本: ${s.count}`
}
</script>

<template>
  <div style="display: flex; flex-direction: column; height: 100vh; background: var(--color-bg-page); min-width: 0;">
    <PageTopBar title="系统监控" :visible="true">
      <template #right>
        <NSelect v-model:value="refreshInterval" :options="refreshOptions" size="small" style="width: 100px;" />
      </template>
    </PageTopBar>
    <div style="flex: 1; overflow-y: auto; padding: var(--space-lg);">
    <NSpin v-if="!health && !error" />
    <div v-if="error" style="color: var(--color-danger); padding: var(--space-md);">{{ error }}</div>

    <template v-if="health">
      <!-- 卡片行 -->
      <div class="stats-grid">
        <NCard size="small" title="运行时间"><div style="font-size: 22px; font-weight: 600;">{{ uptime(health.uptime_seconds) }}</div></NCard>
        <NCard size="small" title="goroutines"><div style="font-size: 22px; font-weight: 600;">{{ health.goroutines }}</div></NCard>
        <NCard size="small" title="内存"><div>堆: {{ fmtMB(health.memory?.heap_alloc_mb) }}</div><div style="font-size: 12px; color: var(--color-text-muted);">RSS: {{ fmtMB(health.memory?.os_rss_mb) }}</div></NCard>
        <NCard size="small" title="SSE 连接"><div style="font-size: 22px; font-weight: 600;">{{ health.counters?.sse_connections ?? 0 }}</div></NCard>
        <NCard size="small" title="活跃会话"><div style="font-size: 22px; font-weight: 600;">{{ health.snapshots?.active_conversations ?? 0 }}</div><div style="font-size: 12px; color: var(--color-text-muted);">最近 1h</div></NCard>
        <NCard v-if="health.snapshots?.markdown_files" size="small" title="Markdown"><div style="font-size: 22px; font-weight: 600;">{{ health.snapshots.markdown_files.count }}</div><div style="font-size: 12px; color: var(--color-text-muted);">个文件</div></NCard>
      </div>

      <!-- LLM 调用次数 + 向量存储（同行） -->
      <div style="display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-md); margin-bottom: var(--space-lg);">
        <NCard v-if="health.counters?.llm_calls" size="small" title="LLM 调用次数 + Token 消耗">
          <div style="display: grid; gap: var(--space-sm);">
            <div v-for="(s, caller) in health.counters.llm_calls" :key="caller" style="background: var(--color-bg); border-radius: var(--radius-md); padding: var(--space-sm) var(--space-md);">
              <div style="font-weight: 600; font-size: 14px; margin-bottom: 4px;">{{ caller }}</div>
              <div style="font-size: 24px; font-weight: 700; color: var(--color-primary);">{{ s.calls }} <span style="font-size: 12px; font-weight: 400; color: var(--color-text-muted);">次</span></div>
              <div style="font-size: 12px; color: var(--color-text-muted); margin-top: 2px;">
                失败 <span :style="{ color: s.errors > 0 ? 'var(--color-danger)' : 'var(--color-text-muted)' }">{{ s.errors }}</span> · 成功率 {{ (s.success_rate * 100).toFixed(1) }}%
              </div>
              <div v-if="health.counters?.llm_tokens?.[caller]" style="font-size: 12px; color: var(--color-text-muted); margin-top: 4px;">
                token — prompt: {{ health.counters.llm_tokens[caller].prompt_tokens.toLocaleString() }} · completion: {{ health.counters.llm_tokens[caller].completion_tokens.toLocaleString() }}
              </div>
            </div>
          </div>
        </NCard>

        <NCard v-if="health.snapshots?.vector_store" size="small" title="向量存储">
          <div><span style="font-weight: 600; font-size: 22px;">{{ health.snapshots.vector_store.total_entries }}</span><span style="font-size: 12px; color: var(--color-text-muted);"> 条目</span></div>
          <div v-for="(c, t) in health.snapshots.vector_store.by_type" :key="t" style="font-size: 12px; color: var(--color-text-muted); margin-top: 2px;">{{ t }}: {{ c }}</div>
          <div style="font-size: 11px; color: var(--color-text-muted); margin-top: 2px;">内存: {{ (health.snapshots.vector_store.estimated_bytes / 1024).toFixed(0) }} KB</div>
        </NCard>
      </div>

      <!-- 四级耗时：田字格 -->
      <div class="latency-grid">
        <NCard v-if="health.latency?.llm" size="small" title="LLM 调用耗时">
          <div v-for="(s, caller) in health.latency.llm" :key="caller" style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">{{ caller }} — {{ latSummary(s) }}</div>
          <canvas id="llm-lat-chart" />
        </NCard>

        <NCard v-if="health.latency?.pipeline" size="small" title="管线耗时 (avg ms)">
          <div v-for="(s, step) in health.latency.pipeline" :key="step" style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 2px;">{{ step }} — avg: {{ s.avg_ms }}ms | P99: {{ s.p99_ms }}ms | {{ s.count }} 次</div>
          <canvas id="pipeline-chart" style="margin-top: var(--space-sm);" />
        </NCard>

        <NCard v-if="health.latency?.embedding?.count" size="small" title="Embedding 耗时">
          <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">{{ latSummary(health.latency.embedding) }}</div>
          <canvas id="embedding-chart" />
        </NCard>

        <NCard v-if="health.latency?.tool" size="small" title="工具调用耗时" class="latency-full-row">
          <div v-for="(funcs, tool) in health.latency.tool" :key="tool" style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 2px;">
            <span v-for="(s, fn) in funcs" :key="fn">{{ tool }}.{{ fn }} — {{ latSummary(s) }}<br v-if="Object.keys(funcs).length > 1" /></span>
          </div>
          <canvas id="tool-lat-chart" style="margin-top: var(--space-sm);" />
        </NCard>
      </div>

      <!-- MCP -->
      <NCard size="small" title="MCP 服务" style="margin-bottom: var(--space-lg);">
        <div v-if="Object.keys(health.snapshots?.mcp || {}).length">
          <div v-for="(info, name) in health.snapshots.mcp" :key="name" style="display: flex; align-items: center; gap: var(--space-sm); padding: var(--space-xs) 0;">
            <span style="font-weight: 500;">{{ name }}</span>
            <NTag :type="info.state === 'ready' ? 'success' : 'warning'" size="small">{{ info.state }}</NTag>
            <span style="font-size: 12px; color: var(--color-text-muted);">工具: {{ info.tools }} | 执行中: {{ info.in_flight }}</span>
          </div>
        </div>
        <div v-else style="color: var(--color-text-muted);">无</div>
      </NCard>
    </template>
  </div>
  </div>
</template>

<style scoped>
.stats-grid {
  display: grid;
  grid-template-columns: repeat(6, 1fr);
  gap: var(--space-md);
  margin-bottom: var(--space-lg);
}
@media (max-width: 768px) {
  .stats-grid {
    grid-template-columns: repeat(2, 1fr);
  }
}

.latency-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--space-md);
  margin-bottom: var(--space-lg);
}
.latency-grid .n-card {
  margin-bottom: 0;
}
.latency-full-row {
  grid-column: 1 / -1;
}
@media (max-width: 768px) {
  .latency-grid {
    grid-template-columns: 1fr;
  }
}
.latency-grid canvas {
  max-height: 200px;
}
</style>

