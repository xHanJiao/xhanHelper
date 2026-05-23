<script setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { api } from '@/api/client'
import { NCard, NTag, NSpin, NSelect, NTabs, NTabPane, NEmpty, NAlert, NButton } from 'naive-ui'
import { Chart, registerables } from 'chart.js'
import PageTopBar from '@/components/PageTopBar.vue'

Chart.register(...registerables)

const activeTab = ref('overview')
const health = ref(null)
const error = ref(null)
const hooks = ref(null)
let timer = null

// ── 反思 Tab ──
const traces = ref([])
const tracesTotal = ref(0)
const tracesLoading = ref(false)
const selectedTrace = ref(null)
const traceDetail = ref(null)

async function fetchTraces() {
  tracesLoading.value = true
  try {
    const res = await api.get('/api/admin/reflection/traces?limit=20')
    traces.value = res.items || []
    tracesTotal.value = res.total || 0
  } catch (_) {
  } finally {
    tracesLoading.value = false
  }
}

async function fetchTraceDetail(trace) {
  selectedTrace.value = trace
  try {
    traceDetail.value = await api.get(`/api/admin/reflection/traces/${trace.id}`)
  } catch (_) {
    traceDetail.value = null
  }
}

function traceStatusColor(s) {
  const map = { completed: 'success', empty: 'warning', error: 'error', running: 'info' }
  return map[s] || 'default'
}

function traceStatusLabel(s) {
  const map = { completed: '已完成', empty: '无发现', error: '异常', running: '运行中' }
  return map[s] || s
}

// ── 主题发现 Tab ──
const findings = ref([])
const findingsTotal = ref(0)
const findingsLoading = ref(false)
const findingsPage = ref(0)
const findingsStatus = ref('')

async function fetchFindings() {
  findingsLoading.value = true
  try {
    const pageSize = 20
    const offset = findingsPage.value * pageSize
    const res = await api.get(`/api/admin/pending-findings?limit=${pageSize}&offset=${offset}&status=${findingsStatus.value}`)
    findings.value = res.items || []
    findingsTotal.value = res.total || 0
  } catch (_) {
  } finally {
    findingsLoading.value = false
  }
}

function findingsPageLabel() {
  const pageSize = 20
  const start = findingsPage.value * pageSize + 1
  const end = Math.min((findingsPage.value + 1) * pageSize, findingsTotal.value)
  return `${start}-${end} / ${findingsTotal.value}`
}

function statusColor(s) {
  const map = { pending: 'warning', delivered: 'success', failed: 'error' }
  return map[s] || 'default'
}

function statusLabel(s) {
  const map = { pending: '待投递', delivered: '已投递', failed: '失败' }
  return map[s] || s
}

// ── 消息 Trace Tab ──
const msgTraces = ref([])
const msgTracesTotal = ref(0)
const msgTracesLoading = ref(false)
const msgTracesPage = ref(0)
const expandedTrace = ref(null)

async function fetchMsgTraces() {
  msgTracesLoading.value = true
  try {
    const pageSize = 20
    const offset = msgTracesPage.value * pageSize
    const res = await api.get(`/api/admin/message-traces?limit=${pageSize}&offset=${offset}`)
    msgTraces.value = res.items || []
    msgTracesTotal.value = res.total || 0
  } catch (_) {
  } finally {
    msgTracesLoading.value = false
  }
}

function tracePageLabel() {
  const pageSize = 20
  const start = msgTracesPage.value * pageSize + 1
  const end = Math.min((msgTracesPage.value + 1) * pageSize, msgTracesTotal.value)
  return `${start}-${end} / ${msgTracesTotal.value}`
}

function toggleTrace(msg) {
  expandedTrace.value = expandedTrace.value === msg.id ? null : msg.id
}

function parseTraceExtra(extra) {
  if (!extra) return null
  try { return JSON.parse(extra) } catch { return null }
}

function formatTime(ts) {
  if (!ts) return '-'
  const d = new Date(ts)
  return d.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

const refreshOptions = [
  { label: '关闭', value: 0 },
  { label: '5 秒', value: 5000 },
  { label: '10 秒', value: 10000 },
  { label: '30 秒', value: 30000 },
  { label: '60 秒', value: 60000 }
]
const refreshInterval = ref(60000)

function restartTimer() {
  clearInterval(timer)
  if (refreshInterval.value > 0) {
    timer = setInterval(() => { fetchHealth(); fetchHooks() }, refreshInterval.value)
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

async function fetchHooks() {
  try { hooks.value = await api.get('/api/admin/hooks') } catch (_) {}
}

onMounted(() => { fetchHealth(); fetchHooks(); restartTimer(); fetchTraces() })
watch(activeTab, (tab) => { if (tab === 'msg-trace' && msgTraces.value.length === 0) fetchMsgTraces(); if (tab === 'pending-findings' && findings.value.length === 0) fetchFindings() })
onUnmounted(() => { clearInterval(timer); destroyCharts() })

function formatJSON(raw) {
  if (!raw) return ''
  // 后端已做 compactJSON，直接展示；兜底做一次 parse+stringify
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

async function copyJSON(raw) {
  if (!raw) return
  try {
    await navigator.clipboard.writeText(raw)
  } catch {
    // fallback silently
  }
}

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
        <NSelect v-if="activeTab === 'overview'" v-model:value="refreshInterval" :options="refreshOptions" size="small" style="width: 100px;" />
        <span v-else style="font-size: 12px; color: var(--color-text-muted);">共 {{ activeTab === 'pending-findings' ? findingsTotal : tracesTotal }} 条记录</span>
      </template>
    </PageTopBar>
    <NTabs v-model:value="activeTab" type="line" style="padding: 0 var(--space-lg); background: var(--color-surface); flex-shrink: 0;">
      <NTabPane name="overview" tab="概览" />
      <NTabPane name="reflection" tab="反思" />
      <NTabPane name="msg-trace" tab="消息Trace" />
      <NTabPane name="pending-findings" tab="主题发现" />
    </NTabs>
    <div style="flex: 1; overflow-y: auto; padding: var(--space-lg);">

    <!-- ========== 概览 Tab ========== -->
    <div v-if="activeTab === 'overview'">
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
      </div>

      <div v-if="health.latency?.tool || hooks" style="display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-md); margin-bottom: var(--space-lg);">
        <NCard v-if="health.latency?.tool" size="small" title="工具调用耗时">
          <div v-for="(funcs, tool) in health.latency.tool" :key="tool" style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 2px;">
            <span v-for="(s, fn) in funcs" :key="fn">{{ tool }}.{{ fn }} — {{ latSummary(s) }}<br v-if="Object.keys(funcs).length > 1" /></span>
          </div>
          <canvas id="tool-lat-chart" style="margin-top: var(--space-sm);" />
        </NCard>
        <NCard v-if="hooks" size="small" title="钩子状态">
          <div style="display: grid; gap: 4px; font-size: 13px;">
            <div>待处理延迟任务: <strong :style="{ color: hooks.pending_delayed_tasks > 0 ? 'var(--color-warning)' : 'var(--color-text-muted)' }">{{ hooks.pending_delayed_tasks }}</strong></div>
            <div v-if="hooks.next_delayed_task_at">最近到期: <strong>{{ hooks.next_delayed_task_at }}</strong></div>
            <div>提醒定时器: <strong>{{ hooks.commitment_timers }}</strong></div>
            <div>投递窗口剩余: <strong>{{ hooks.deliver_remaining }}</strong></div>
            <div>主题发现剩余: <strong>{{ hooks.discover_remaining }}</strong></div>
          </div>
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

    <!-- ========== 反思 Tab ========== -->
    <div v-if="activeTab === 'reflection'" style="padding-top: var(--space-sm);">
      <div v-if="!selectedTrace">
        <NSpin v-if="tracesLoading && traces.length === 0" />
        <NEmpty v-else-if="traces.length === 0" description="暂无反思记录" style="margin-top: 80px;" />
        <div v-else style="display: flex; flex-direction: column; gap: var(--space-sm);">
          <NCard v-for="t in traces" :key="t.id" size="small" style="cursor: pointer;" @click="fetchTraceDetail(t)" :hoverable="true">
            <div style="display: flex; justify-content: space-between; align-items: center;">
              <div>
                <span style="font-weight: 500;">{{ formatTime(t.started_at) }}</span>
                <span style="font-size: 12px; color: var(--color-text-muted); margin-left: var(--space-sm);">{{ t.trigger_type === 'cron' ? '⏰ 定时' : '💬 对话后' }}</span>
              </div>
              <div style="display: flex; align-items: center; gap: var(--space-sm);">
                <span style="font-size: 13px; color: var(--color-text-secondary);">{{ t.total_findings }} 发现 · {{ t.delivered_count }} 投递</span>
                <NTag :type="traceStatusColor(t.status)" size="small">{{ traceStatusLabel(t.status) }}</NTag>
              </div>
            </div>
          </NCard>
        </div>
      </div>
      <div v-else>
        <div style="display: flex; align-items: center; gap: var(--space-md); margin-bottom: var(--space-md);">
          <NButton size="small" @click="selectedTrace = null; traceDetail = null;">← 返回</NButton>
          <span style="font-weight: 500;">Trace {{ selectedTrace.id.slice(0, 8) }}</span>
          <NTag :type="traceStatusColor(selectedTrace.status)" size="small">{{ traceStatusLabel(selectedTrace.status) }}</NTag>
        </div>
        <NSpin v-if="!traceDetail" />
        <template v-else>
          <NAlert v-if="traceDetail.trace?.status === 'empty'" type="info" style="margin-bottom: var(--space-md);">
            此投递窗口内无待处理发现，未触发 LLM 调用与决策记录。
          </NAlert>
          <NCard size="small" title="LLM 调用记录" style="margin-bottom: var(--space-md);">
            <div v-if="traceDetail.llm_calls?.length" style="display: flex; flex-direction: column; gap: var(--space-sm);">
              <NCard v-for="call in traceDetail.llm_calls" :key="call.id" size="small" :bordered="false" style="background: var(--color-bg);">
                <div style="font-size: 13px; margin-bottom: var(--space-xs);">
                  <NTag size="tiny">{{ call.caller }}</NTag>
                  <span v-if="call.caller_detail" style="margin-left: var(--space-xs); color: var(--color-text-muted);">{{ call.caller_detail }}</span>
                  <span style="margin-left: var(--space-sm); font-size: 11px; color: var(--color-text-muted);">{{ call.latency_ms }}ms · prompt: {{ call.prompt_tokens }} · completion: {{ call.completion_tokens }}</span>
                </div>
                <details v-if="call.response_json" style="font-size: 11px; color: var(--color-text-muted);">
                  <summary>查看 LLM 输出</summary>
                  <div style="position: relative;">
                    <button @click="copyJSON(call.response_json)" title="复制" style="position: absolute; top: 4px; right: 4px; padding: 2px 6px; border: none; background: var(--color-surface-hover); border-radius: var(--radius-sm); cursor: pointer; font-size: 12px; color: var(--color-text-muted); line-height: 1;">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
                    </button>
                    <pre style="max-height: 200px; overflow: auto; white-space: pre-wrap; margin-top: var(--space-xs); background: var(--color-surface); padding: var(--space-sm); border-radius: var(--radius-sm);">{{ formatJSON(call.response_json) }}</pre>
                  </div>
                </details>
              </NCard>
            </div>
            <div v-else style="color: var(--color-text-muted);">无记录</div>
          </NCard>
          <NCard size="small" title="决策记录">
            <div v-if="traceDetail.decisions?.length" style="display: flex; flex-direction: column; gap: var(--space-sm);">
              <div v-for="d in traceDetail.decisions" :key="d.id" style="font-size: 13px; padding: var(--space-xs) 0; border-bottom: 1px solid var(--color-border);">
                <NTag :type="d.verdict === 'topn' || d.verdict === 'selected' || d.verdict === 'nominated' || d.verdict === 'success' ? 'success' : d.verdict === 'dropped' || d.verdict === 'rejected' || d.verdict === 'error' ? 'error' : 'warning'" size="tiny">{{ d.verdict }}</NTag>
                <span style="margin-left: var(--space-sm);">{{ d.stage }} · {{ d.subject }}</span>
                <div style="font-size: 12px; color: var(--color-text-muted); margin-top: 2px;">{{ d.reason }}</div>
                <details v-if="d.detail_json" style="font-size: 11px; color: var(--color-text-muted); margin-top: var(--space-xs);">
                  <summary>查看详情 ({{ d.detail_json.length.toLocaleString() }} 字符)</summary>
                  <div style="position: relative;">
                    <button @click="copyJSON(d.detail_json)" title="复制" style="position: absolute; top: 4px; right: 4px; padding: 2px 6px; border: none; background: var(--color-surface-hover); border-radius: var(--radius-sm); cursor: pointer; font-size: 12px; color: var(--color-text-muted); line-height: 1;">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
                    </button>
                    <pre style="max-height: 200px; overflow: auto; white-space: pre-wrap; margin-top: var(--space-xs); background: var(--color-surface); padding: var(--space-sm); border-radius: var(--radius-sm);">{{ formatJSON(d.detail_json) }}</pre>
                  </div>
                </details>
              </div>
            </div>
            <div v-else style="color: var(--color-text-muted);">无记录</div>
          </NCard>
        </template>
      </div>
      <NButton size="small" style="margin-top: var(--space-md);" @click="fetchTraces()" :loading="tracesLoading">刷新列表</NButton>
    </div>

    <!-- ========== 消息 Trace Tab ========== -->
    <div v-if="activeTab === 'msg-trace'" style="padding-top: var(--space-sm);">
      <NSpin v-if="msgTracesLoading && msgTraces.length === 0" />
      <NEmpty v-else-if="msgTraces.length === 0" description="暂无 trace 记录" style="margin-top: 80px;" />

      <div v-else style="display: flex; flex-direction: column; gap: var(--space-sm);">
        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: var(--space-sm);">
          <NButton size="small" @click="fetchMsgTraces()" :loading="msgTracesLoading">刷新列表</NButton>
          <div style="display: flex; gap: var(--space-sm); align-items: center;">
            <span style="font-size: 12px; color: var(--color-text-muted);">{{ tracePageLabel() }}</span>
            <NButton size="tiny" :disabled="msgTracesPage === 0" @click="msgTracesPage--; fetchMsgTraces()">上一页</NButton>
            <NButton size="tiny" :disabled="(msgTracesPage + 1) * 20 >= msgTracesTotal" @click="msgTracesPage++; fetchMsgTraces()">下一页</NButton>
          </div>
        </div>

        <NCard v-for="msg in msgTraces" :key="msg.id" size="small" style="cursor: pointer;" @click="toggleTrace(msg)" :hoverable="true">
          <div style="display: flex; justify-content: space-between; align-items: center;">
            <div>
              <span style="font-weight: 500;">{{ formatTime(msg.created_at) }}</span>
              <span style="font-size: 12px; color: var(--color-text-muted); margin-left: var(--space-sm);">{{ msg.conversation_id?.substring(0, 8) }}...</span>
            </div>
            <NTag size="tiny" :type="expandedTrace === msg.id ? 'success' : 'default'">{{ expandedTrace === msg.id ? '展开' : '折叠' }}</NTag>
          </div>

          <div v-if="expandedTrace === msg.id" style="margin-top: var(--space-md);">
            <div v-if="parseTraceExtra(msg.extra)?.providers">
              <div v-for="(p, pi) in parseTraceExtra(msg.extra).providers" :key="pi"
                   style="margin-bottom: var(--space-sm); padding: var(--space-sm); background: var(--color-bg); border-radius: var(--radius-md);">
                <div style="display: flex; gap: var(--space-xs); align-items: center; margin-bottom: 4px;">
                  <NTag size="tiny" :type="p.type === 'static' ? 'info' : 'warning'">{{ p.name }}</NTag>
                  <span style="font-size: 11px; color: var(--color-text-muted);">{{ p.type }}</span>
                </div>
                <div v-if="p.input" style="font-size: 11px; color: var(--color-text-muted); margin-bottom: 4px;">
                  query: {{ p.input.query }}
                </div>
                <div v-if="p.items?.length">
                  <div v-for="(item, ii) in p.items" :key="ii" style="font-size: 12px; padding: 2px 0; border-top: 1px solid var(--color-border);">
                    <span>{{ item.text }}</span>
                    <span style="margin-left: var(--space-sm); color: var(--color-primary);">score={{ item.score?.toFixed(4) }}</span>
                    <span v-if="item.vector_score" style="margin-left: var(--space-xs); color: var(--color-text-muted);">vec={{ item.vector_score?.toFixed(4) }}</span>
                    <span v-if="item.keyword_score" style="margin-left: var(--space-xs); color: var(--color-text-muted);">kw={{ item.keyword_score?.toFixed(4) }}</span>
                  </div>
                </div>
                <div v-else style="font-size: 11px; color: var(--color-text-muted);">无匹配结果</div>
              </div>
            </div>
            <div v-else style="font-size: 12px; color: var(--color-text-muted);">
              <details>
                <summary>原始 Extra ({{ msg.extra?.length || 0 }} 字符)</summary>
                <pre style="max-height: 200px; overflow: auto; white-space: pre-wrap; margin-top: var(--space-xs); font-size: 11px;">{{ formatJSON(msg.extra) }}</pre>
              </details>
            </div>
          </div>
        </NCard>
      </div>
    </div>

    <!-- ========== 主题发现 Tab ========== -->
    <div v-if="activeTab === 'pending-findings'" style="padding-top: var(--space-sm);">
      <NSpin v-if="findingsLoading && findings.length === 0" />
      <NEmpty v-else-if="findings.length === 0" description="暂无主题发现记录" style="margin-top: 80px;" />

      <div v-else style="display: flex; flex-direction: column; gap: var(--space-sm);">
        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: var(--space-sm);">
          <div style="display: flex; gap: var(--space-sm); align-items: center;">
            <NButton size="small" @click="fetchFindings()" :loading="findingsLoading">刷新</NButton>
            <NSelect v-model:value="findingsStatus" :options="[{ label:'待投递', value:'pending' },{ label:'已投递', value:'delivered' },{ label:'失败', value:'failed' },{ label:'全部', value:'' }]" size="small" style="width: 110px;" @update:value="findingsPage = 0; fetchFindings()" />
          </div>
          <div style="display: flex; gap: var(--space-sm); align-items: center;">
            <span style="font-size: 12px; color: var(--color-text-muted);">{{ findingsPageLabel() }}</span>
            <NButton size="tiny" :disabled="findingsPage === 0" @click="findingsPage--; fetchFindings()">上一页</NButton>
            <NButton size="tiny" :disabled="(findingsPage + 1) * 20 >= findingsTotal" @click="findingsPage++; fetchFindings()">下一页</NButton>
          </div>
        </div>

        <NCard v-for="f in findings" :key="f.id" size="small">
          <div style="display: flex; justify-content: space-between; align-items: flex-start; gap: var(--space-md);">
            <div style="flex: 1;">
              <div style="display: flex; gap: var(--space-xs); align-items: center; margin-bottom: 4px;">
                <span style="font-weight: 600; font-size: 14px;">{{ f.title }}</span>
                <NTag size="tiny" :type="statusColor(f.status)">{{ statusLabel(f.status) }}</NTag>
                <NTag v-if="f.urgency" size="tiny" :type="f.urgency === 'high' ? 'error' : f.urgency === 'medium' ? 'warning' : 'default'">{{ f.urgency }}</NTag>
              </div>
              <div v-if="f.theme_name" style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">
                主题: {{ f.theme_name }}
                <span v-if="f.theme_id" style="margin-left: var(--space-sm); font-size: 11px;">({{ f.theme_id.substring(0, 8) }}...)</span>
              </div>
              <div style="font-size: 13px; color: var(--color-text); white-space: pre-wrap; margin-bottom: 4px;">{{ f.content }}</div>
              <div v-if="f.source_conv_id" style="font-size: 11px; color: var(--color-text-muted);">
                来源: {{ f.source_conv_id.substring(0, 8) }}...
                <span style="margin-left: var(--space-sm);">重试: {{ f.retry_count }}</span>
              </div>
            </div>
            <span style="font-size: 11px; color: var(--color-text-muted); flex-shrink: 0;">{{ formatTime(f.created_at) }}</span>
          </div>
        </NCard>
      </div>
    </div>
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
@media (max-width: 768px) {
  .latency-grid {
    grid-template-columns: 1fr;
  }
}
.latency-grid canvas {
  max-height: 200px;
}
</style>

