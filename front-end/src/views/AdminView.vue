<script setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { api } from '@/api/client'
import { NCard, NTag, NSpin, NSelect, NTabs, NTabPane, NEmpty, NAlert, NButton, NCheckbox, NModal, NInput, NInputNumber, useMessage } from 'naive-ui'
import { Chart, registerables } from 'chart.js'
import PageTopBar from '@/components/PageTopBar.vue'
import LLMIOAuditTab from '@/components/LLMIOAuditTab.vue'
import SubAgentTab from '@/components/SubAgentTab.vue'
import { isMacEmbedded } from '@/utils/embed'

Chart.register(...registerables)

const message = useMessage()

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
watch(activeTab, (tab) => {
  if (tab === 'msg-trace' && msgTraces.value.length === 0) fetchMsgTraces()
  if (tab === 'cost' && !costLoaded.value) {
    costLoaded.value = true
    fetchCostCategories(); fetchCostUsers(); fetchCostModels(); fetchCostPrices(); refreshCost()
  }
})
onUnmounted(() => { clearInterval(timer); destroyCharts(); destroyCostCharts() })

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

function makeBarChart(id, labels, datasets, horizontal, trackIn = chartInstances, dualAxis = false) {
  const canvas = document.getElementById(id)
  if (!canvas) return
  Chart.getChart(id)?.destroy()
  const scales = horizontal ? { x: { beginAtZero: true } } : { x: { stacked: true }, y: { stacked: true, beginAtZero: true } }
  if (dualAxis) {
    scales.y1 = { position: 'right', beginAtZero: true, grid: { drawOnChartArea: false } }
  }
  const ch = new Chart(canvas, {
    type: 'bar',
    data: { labels, datasets },
    options: { responsive: true, indexAxis: horizontal ? 'y' : 'x', interaction: { mode: 'index' }, plugins: { legend: { display: labels.length > 1, position: 'top', labels: { boxWidth: 12, font: { size: 11 } } } }, scales }
  })
  trackIn.push(ch)
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

  // ── Request latency (端到端) ──
  const reqLat = h.latency?.request
  if (reqLat?.recent?.length) makeLineChart('request-lat-chart', [{ label: '端到端耗时', data: reqLat.recent }])

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
function fmtKB(v) { if (v == null) return '0 KB'; return (v / 1024).toFixed(0) + ' KB' }
function fmtBytes(v) { if (v == null) return '0 MB'; return (v / 1048576).toFixed(1) + ' MB' }
function uptime(s) { const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60); return h > 0 ? `${h}h ${m}m` : `${m}m` }
function latSummary(s) {
  if (!s || !s.count) return '暂无数据'
  return `avg: ${s.avg_ms}ms | P50: ${s.p50_ms}ms | P99: ${s.p99_ms}ms | 样本: ${s.count}`
}

// ── Token 消耗 Tab ──
const costRangeDays = ref(30)
const costCategory = ref(null)
const costModel = ref([])
const costUsers = ref([])
const costUserOptions = ref([])
const costCategoryOptions = ref([])
const costModelOptions = ref([])
const costSummary = ref(null)
const costSpend = ref(null)
const costLoading = ref(false)
const costLoaded = ref(false)
const costBarStack = ref({ hit: true, miss: true, completion: true })
let costCharts = []

// ── 花销（¥）与定价 ──
const costExpense = ref(null)
const costSelectedDate = ref(null)
const costHourly = ref(null)
const priceRows = ref([])
const priceForm = ref({ model: '', effective_date: '', price_hit: 0, price_miss: 0, price_output: 0, price_hit_off: 0, price_miss_off: 0, price_output_off: 0, weekend_all_off: false, peak_segments: '' })
const priceSaving = ref(false)
const priceModelOptions = ref([])
const priceEditingId = ref(null)

// 饼图钻取：点击扇形 → 弹层展示该分组的 命中/未命中/生成 三色占比
const costDrillOpen = ref(false)
const costDrillDown = ref(null)

// 选中维度求和：勾了哪些 checkbox，饼图就只算哪些维度的 token
function selectedTotal(g) {
  let sum = 0
  if (costBarStack.value.hit) sum += g.prompt_cache_hit || 0
  if (costBarStack.value.miss) sum += g.prompt_cache_miss || 0
  if (costBarStack.value.completion) sum += g.completion || 0
  return sum
}

// 给饼图挂点击钻取：从 group 行读 hit/miss/completion（stats 响应自带，零新 API）
function attachDrillDown(chartId, groups) {
  const ch = Chart.getChart(chartId)
  if (!ch) return
  ch.options.onClick = (_evt, elements) => {
    if (!elements?.length) return
    const idx = elements[0].index
    const g = groups[idx]
    if (!g) return
    costDrillDown.value = {
      title: `${ch.data.labels[idx]} 维度占比`,
      labels: ['命中缓存', '未命中缓存', '生成'],
      data: [g.prompt_cache_hit, g.prompt_cache_miss, g.completion],
    }
    costDrillOpen.value = true
  }
}

// 钻取弹层内的小饼图（NModal after-enter 后 canvas 才在 DOM）
function renderDrillChart() {
  const d = costDrillDown.value
  if (!d) return
  const canvas = document.getElementById('cost-drill-chart')
  if (!canvas) return
  Chart.getChart('cost-drill-chart')?.destroy()
  const colors = ['#5b6abf', '#e74c3c', '#27ae60']
  const ch = new Chart(canvas, {
    type: 'pie',
    data: { labels: d.labels, datasets: [{ data: d.data, backgroundColor: colors }] },
    options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { position: 'right', labels: { boxWidth: 12, font: { size: 11 } } } } }
  })
  costCharts.push(ch)
}

// 堆叠维度切换：至少保留一个维度，全取消时拦截并提示
function toggleBarStack(key) {
  const next = { ...costBarStack.value, [key]: !costBarStack.value[key] }
  if (!next.hit && !next.miss && !next.completion) {
    message.warning('至少保留一个展示维度')
    return
  }
  costBarStack.value = next
}

const costRanges = [
  { label: '1 天', value: 1 },
  { label: '3 天', value: 3 },
  { label: '7 天', value: 7 },
  { label: '15 天', value: 15 },
  { label: '30 天', value: 30 }
]

// 本地日期格式化（toISOString 是 UTC，凌晨会差一天）
function localDay(d) {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

function costDateRange() {
  const end = new Date()
  const start = new Date()
  start.setDate(start.getDate() - (costRangeDays.value - 1))
  return { start: localDay(start), end: localDay(end) }
}

async function fetchCostCategories() {
  try {
    const list = await api.get('/api/admin/cost/categories') || []
    const labelMap = { main_chat: '主聊天', post_chat: '对话后处理', reflection: '反思', tool: '工具', admin: '管理与评估' }
    costCategoryOptions.value = list.map(c => ({ label: labelMap[c] || c, value: c }))
  } catch (_) {}
}

async function fetchCostUsers() {
  try {
    const users = await api.get('/api/admin/cost/users') || []
    costUserOptions.value = users.map(u => ({ label: u.user_id.slice(0, 8) + '...', value: u.user_id }))
  } catch (_) {}
}

async function fetchCostModels() {
  try {
    const models = await api.get('/api/admin/cost/models') || []
    costModelOptions.value = models.map(m => ({ label: m, value: m }))
  } catch (_) {}
}

async function fetchCostSummary() {
  try {
    const ids = costUsers.value
    const params = []
    if (ids.length) params.push(...ids.map(id => `user_id=${id}`))
    if (costCategory.value) params.push(`category=${costCategory.value}`)
    if (costModel.value.length) params.push(...costModel.value.map(m => `model=${m}`))
    const qs = params.length ? '?' + params.join('&') : ''
    costSummary.value = await api.get('/api/admin/cost/summary' + qs)
  } catch (_) {}
}

// 消费卡片：固定取近 7 天花销，前端按日累加出今日/近3日/近7日
async function fetchCostSpend() {
  const end = new Date()
  const start = new Date()
  start.setDate(start.getDate() - 6)
  const params = [`start=${localDay(start)}`, `end=${localDay(end)}`]
  if (costCategory.value) params.push(`category=${costCategory.value}`)
  if (costModel.value.length) params.push(...costModel.value.map(m => `model=${m}`))
  if (costUsers.value.length) params.push(...costUsers.value.map(id => `user_id=${id}`))
  try {
    costSpend.value = await api.get('/api/admin/cost/expense?' + params.join('&'))
  } catch (_) {}
}

// 近 n 日消费合计（含今日）
function spendWindow(n) {
  const start = new Date()
  start.setDate(start.getDate() - (n - 1))
  const s = localDay(start)
  return (costSpend.value?.days || [])
    .filter(d => d.date >= s)
    .reduce((sum, d) => sum + (d.cost || 0), 0)
}

async function fetchCostStats(groupBy) {
  const { start, end } = effectiveRange()
  const params = [`start=${start}`, `end=${end}`, `group_by=${groupBy}`]
  if (costCategory.value) params.push(`category=${costCategory.value}`)
  if (costModel.value.length) params.push(...costModel.value.map(m => `model=${m}`))
  if (costUsers.value.length) params.push(...costUsers.value.map(id => `user_id=${id}`))
  const res = await api.get('/api/admin/cost/stats?' + params.join('&'))
  return res || { total: {}, groups: [] }
}

// 统计范围：分时模式下收窄到选中日，否则按时间范围
function effectiveRange() {
  if (costSelectedDate.value) return { start: costSelectedDate.value, end: costSelectedDate.value }
  return costDateRange()
}

function destroyCostCharts() {
  costCharts.forEach(c => c.destroy?.())
  costCharts = []
}

function makePieChart(id, labels, data) {
  const canvas = document.getElementById(id)
  if (!canvas) return
  Chart.getChart(id)?.destroy()
  const colors = ['#5b6abf', '#e74c3c', '#27ae60', '#f39c12', '#8e44ad', '#3498db', '#1abc9c', '#e67e22']
  const ch = new Chart(canvas, {
    type: 'pie',
    data: { labels, datasets: [{ data, backgroundColor: labels.map((_, i) => colors[i % colors.length]) }] },
    options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { position: 'right', labels: { boxWidth: 12, font: { size: 11 } } } } }
  })
  costCharts.push(ch)
}

async function renderCostCharts() {
  destroyCostCharts()
  try {
    // 分时模式下 token 柱状图由 fetchCostHourly 拿到数据后统一渲染（避免用上一次的陈旧数据），这里只画饼图
    if (!costSelectedDate.value) {
      // 每日柱状图（堆叠维度由勾选决定）
      const daily = await fetchCostStats('date')
      const labels = daily.groups.map(g => g.key)
      const datasets = []
      if (costBarStack.value.hit) datasets.push({ label: '缓存命中', data: daily.groups.map(g => g.prompt_cache_hit), backgroundColor: '#5b6abf' })
      if (costBarStack.value.miss) datasets.push({ label: '缓存未命中', data: daily.groups.map(g => g.prompt_cache_miss), backgroundColor: '#e74c3c' })
      if (costBarStack.value.completion) datasets.push({ label: '生成', data: daily.groups.map(g => g.completion), backgroundColor: '#27ae60' })
      datasets.push({ label: '平均 token/次', type: 'line', yAxisID: 'y1', data: daily.groups.map(g => Math.round(selectedTotal(g) / (g.call_count || 1))), borderColor: '#8e44ad', backgroundColor: '#8e44ad22', fill: false, pointRadius: 2, tension: 0.3 })
      if (datasets.length) {
        makeBarChart('cost-daily-chart', labels, datasets, false, costCharts, true)
        // 点击柱子 → 进入该日分时视图
        const ch = Chart.getChart('cost-daily-chart')
        if (ch) ch.options.onClick = (_evt, el) => { if (el?.length) selectDate(labels[el[0].index]) }
      }
    }

    // 三个饼图（分时模式下范围=选中日，自动跟随）
    const cat = await fetchCostStats('category')
    makePieChart('cost-cat-chart', cat.groups.map(g => g.key || '未分类'), cat.groups.map(g => selectedTotal(g)))
    attachDrillDown('cost-cat-chart', cat.groups)
    const model = await fetchCostStats('model')
    makePieChart('cost-model-chart', model.groups.map(g => g.key || '未分类'), model.groups.map(g => selectedTotal(g)))
    attachDrillDown('cost-model-chart', model.groups)
    const sub = await fetchCostStats('sub_category')
    makePieChart('cost-sub-chart', sub.groups.map(g => g.key || '未分类'), sub.groups.map(g => selectedTotal(g)))
    attachDrillDown('cost-sub-chart', sub.groups)
  } catch (_) {}
}

// 分时 token 柱状图（00-23 时，堆叠 hit/miss/output，跟随勾选）
function renderHourlyTokenChart() {
  const h = costHourly.value
  const canvas = document.getElementById('cost-daily-chart')
  if (!canvas || !h) return
  Chart.getChart('cost-daily-chart')?.destroy()
  const datasets = []
  if (costBarStack.value.hit) datasets.push({ label: '缓存命中', data: h.hours.map(x => x.hit), backgroundColor: '#5b6abf' })
  if (costBarStack.value.miss) datasets.push({ label: '缓存未命中', data: h.hours.map(x => x.miss), backgroundColor: '#e74c3c' })
  if (costBarStack.value.completion) datasets.push({ label: '生成', data: h.hours.map(x => x.output), backgroundColor: '#27ae60' })
  const ch = new Chart(canvas, {
    type: 'bar',
    data: { labels: h.hours.map(x => x.hour + ':00'), datasets },
    options: { responsive: true, interaction: { mode: 'index' }, plugins: { legend: { position: 'top', labels: { boxWidth: 12, font: { size: 11 } } } }, scales: { x: { stacked: true }, y: { stacked: true, beginAtZero: true } } }
  })
  costCharts.push(ch)
}

async function refreshCost() {
  costLoading.value = true
  try {
    await Promise.all([
      fetchCostSummary(),
      fetchCostSpend(),
      renderCostCharts(),
      costSelectedDate.value ? fetchCostHourly() : fetchCostExpense()
    ])
  } finally {
    costLoading.value = false
  }
}

function selectDate(date) {
  if (costSelectedDate.value === date) { costSelectedDate.value = null; refreshCost(); return }
  costSelectedDate.value = date
  refreshCost()
}

function clearDate() {
  costSelectedDate.value = null
  refreshCost()
}

function fmtMoney(n) {
  if (n == null) return '¥0.00'
  return '¥' + Number(n).toFixed(2)
}

// 存储的峰段是 compact JSON [[start,end],...]，转回可读文本；空 = 全天谷
function fmtPeakSegments(s) {
  if (!s) return '全天谷'
  try {
    const segs = JSON.parse(s)
    if (!segs.length) return '全天谷'
    return segs.map(x => `${x[0]}-${x[1]}`).join(', ')
  } catch {
    return s
  }
}

async function fetchCostExpense() {
  const { start, end } = effectiveRange()
  const params = [`start=${start}`, `end=${end}`]
  if (costCategory.value) params.push(`category=${costCategory.value}`)
  if (costModel.value.length) params.push(...costModel.value.map(m => `model=${m}`))
  if (costUsers.value.length) params.push(...costUsers.value.map(id => `user_id=${id}`))
  const res = await api.get('/api/admin/cost/expense?' + params.join('&'))
  const cur = effectiveRange()
  // 响应返回期间筛选/分时已变化 → 丢弃陈旧数据，避免旧图覆盖新图
  if (costSelectedDate.value || cur.start !== start || cur.end !== end) return
  costExpense.value = res || { total: 0, days: [], categories: [], models: [], unpriced_models: [] }
  renderExpenseChart()
}

async function fetchCostHourly() {
  const date = costSelectedDate.value
  if (!date) return
  const params = [`date=${date}`]
  if (costCategory.value) params.push(`category=${costCategory.value}`)
  if (costModel.value.length) params.push(...costModel.value.map(m => `model=${m}`))
  if (costUsers.value.length) params.push(...costUsers.value.map(id => `user_id=${id}`))
  const res = await api.get('/api/admin/cost/hourly?' + params.join('&'))
  if (costSelectedDate.value !== date) return // 已切到别的日期/退出分时，丢弃陈旧响应
  costHourly.value = res || { date, hours: [], totals: {}, unpriced_models: [] }
  // 同一次响应渲染两个分时图，时间轴必然一致
  renderHourlyTokenChart()
  renderExpenseChart()
}

// 堆叠柱状图柱顶显示当日总值（¥）
const barTotalPlugin = {
  id: 'barTotal',
  afterDatasetsDraw(chart) {
    const { ctx } = chart
    const xScale = chart.scales.x
    const yScale = chart.scales.y
    if (!xScale || !yScale || !chart.data?.labels?.length) return
    const ds = chart.data.datasets.filter((_, i) => chart.isDatasetVisible(i))
    if (!ds.length) return
    chart.data.labels.forEach((_, i) => {
      const total = ds.reduce((s, d) => s + (Number(d.data[i]) || 0), 0)
      if (total <= 0) return
      const x = xScale.getPixelForValue(i)
      const y = yScale.getPixelForValue(total)
      ctx.save()
      ctx.font = '10px sans-serif'
      ctx.fillStyle = '#8a8f98'
      ctx.textAlign = 'center'
      ctx.fillText('¥' + total.toFixed(2), x, y - 4)
      ctx.restore()
    })
  }
}

function renderExpenseChart() {
  const e = costSelectedDate.value ? costHourly.value : costExpense.value
  const canvas = document.getElementById('cost-expense-chart')
  if (!canvas || !e) return
  Chart.getChart('cost-expense-chart')?.destroy()
  const datasets = []
  const labels = costSelectedDate.value ? e.hours.map(x => x.hour + ':00') : e.days.map(d => d.date)
  const data = costSelectedDate.value ? e.hours : e.days
  if (costBarStack.value.hit) datasets.push({ label: '缓存命中', data: data.map(d => d.cost_hit), backgroundColor: '#5b6abf' })
  if (costBarStack.value.miss) datasets.push({ label: '缓存未命中', data: data.map(d => d.cost_miss), backgroundColor: '#e74c3c' })
  if (costBarStack.value.completion) datasets.push({ label: '生成', data: data.map(d => d.cost_output), backgroundColor: '#27ae60' })
  const ch = new Chart(canvas, {
    type: 'bar',
    data: { labels, datasets },
    options: { responsive: true, interaction: { mode: 'index' }, plugins: { legend: { position: 'top', labels: { boxWidth: 12, font: { size: 11 } } } }, scales: { x: { stacked: true }, y: { stacked: true, beginAtZero: true, title: { display: true, text: '¥' } } } },
    plugins: [barTotalPlugin]
  })
  if (!costSelectedDate.value) {
    // 点击柱子 → 进入该日分时视图
    ch.options.onClick = (_evt, el) => { if (el?.length) selectDate(labels[el[0].index]) }
  }
  costCharts.push(ch)
}

async function fetchCostPrices() {
  try { priceRows.value = await api.get('/api/admin/cost/prices') || [] } catch (_) {}
  try {
    const models = await api.get('/api/admin/cost/models') || []
    priceModelOptions.value = models.map(m => ({ label: m, value: m }))
  } catch (_) {}
}

async function savePrice() {
  const f = priceForm.value
  if (!f.model || !f.effective_date) {
    message.warning('请填写模型名和生效日期')
    return
  }
  priceSaving.value = true
  try {
    await api.post('/api/admin/cost/prices', f)
    message.success(priceEditingId.value ? '已更新' : '已保存')
    priceForm.value = { model: '', effective_date: '', price_hit: 0, price_miss: 0, price_output: 0, price_hit_off: 0, price_miss_off: 0, price_output_off: 0, weekend_all_off: false, peak_segments: '' }
    priceEditingId.value = null
    await Promise.all([fetchCostPrices(), fetchCostExpense()])
  } catch (e) {
    message.error(e.message || '保存失败')
  } finally {
    priceSaving.value = false
  }
}

// 编辑：把记录填回表单（峰段 JSON 转回文本），保存走 Upsert 覆盖
function editPrice(p) {
  priceEditingId.value = p.id
  priceForm.value = {
    model: p.model,
    effective_date: p.effective_date,
    price_hit: p.price_hit,
    price_miss: p.price_miss,
    price_output: p.price_output,
    price_hit_off: p.price_hit_off,
    price_miss_off: p.price_miss_off,
    price_output_off: p.price_output_off,
    weekend_all_off: !!p.weekend_all_off,
    peak_segments: fmtPeakSegments(p.peak_segments) === '全天谷' ? '' : fmtPeakSegments(p.peak_segments),
  }
  message.info('已填入表单，修改后点保存（同模型+日期覆盖原记录）')
}

function cancelEditPrice() {
  priceEditingId.value = null
  priceForm.value = { model: '', effective_date: '', price_hit: 0, price_miss: 0, price_output: 0, price_hit_off: 0, price_miss_off: 0, price_output_off: 0, weekend_all_off: false, peak_segments: '' }
}

async function deletePrice(id) {
  try {
    await api.delete(`/api/admin/cost/prices/${id}`)
    message.success('已删除')
    await Promise.all([fetchCostPrices(), fetchCostExpense()])
  } catch (e) {
    message.error(e.message || '删除失败')
  }
}

function fmtTokens(n) {
  if (n == null) return '0'
  return n.toLocaleString()
}

function hitRate(t) {
  if (!t) return '0'
  const denom = (t.prompt_cache_hit || 0) + (t.prompt_cache_miss || 0)
  return denom ? ((t.prompt_cache_hit / denom) * 100).toFixed(1) : '0'
}

// 筛选变化时刷新图表。时间范围变化时退出分时（选中日可能不在新范围）；分类/用户/维度变化保留分时。
// 必须放在所有 cost 声明之后（const 不提升，提前引用会 TDZ 报错）
watch([costRangeDays, costCategory, costModel, costUsers, costBarStack], ([newR], [oldR]) => {
  if (newR !== oldR && costSelectedDate.value) costSelectedDate.value = null
  if (activeTab.value === 'cost') refreshCost()
}, { deep: true })

// 窗口拖拽 resize 时 Chart.js 有 ~100ms 内建 debounce，停止拖拽后强制校准一次，
// 避免 canvas 内联尺寸滞后导致柱状图/饼图右沿不对齐。
// cost 柱状图实例也挂在 costCharts 上（makeBarChart 的 trackIn 参数），统一校准。
let costResizeTimer = null
function onCostWindowResize() {
  clearTimeout(costResizeTimer)
  costResizeTimer = setTimeout(() => {
    // 先尝试原地 resize（轻量）
    costCharts.forEach(c => c.resize?.())
    chartInstances.forEach(c => c.resize?.())
    // 拖拽后仍不对齐（canvas 内联尺寸与容器脱节）时，重建一次图表兜底
    setTimeout(() => {
      if (activeTab.value === 'cost') {
        const stale = costCharts.some(c => {
          const parent = c.canvas?.parentElement
          return parent && Math.abs(c.canvas.clientWidth - parent.clientWidth) > 2
        })
        if (stale) renderCostCharts()
      }
    }, 200)
  }, 150)
}
window.addEventListener('resize', onCostWindowResize)
onUnmounted(() => {
  clearTimeout(costResizeTimer)
  window.removeEventListener('resize', onCostWindowResize)
})

</script>

<template>
  <div style="display: flex; flex-direction: column; height: 100%; background: transparent; min-width: 0;">
    <!-- App 内嵌 WebView 时隐藏顶部导航（macOS 客户端已有自己的面板 chrome） -->
    <PageTopBar v-if="!isMacEmbedded()" title="系统监控" :visible="true">
      <template #right>
        <NSelect v-if="activeTab === 'overview'" v-model:value="refreshInterval" :options="refreshOptions" size="small" style="width: 100px;" />
        <span v-else style="font-size: 12px; color: var(--color-text-muted);">共 {{ tracesTotal }} 条记录</span>
      </template>
    </PageTopBar>
    <NTabs v-model:value="activeTab" type="line" style="padding: 0 var(--space-lg); background: var(--color-surface); flex-shrink: 0;">
      <NTabPane name="overview" tab="概览" />
      <!-- <NTabPane name="reflection" tab="反思" /> -->
      <!-- <NTabPane name="msg-trace" tab="消息Trace" /> -->
      <NTabPane name="cost" tab="成本中心" />
      <NTabPane name="llm-io" tab="后处理审计" />
      <NTabPane name="subagent" tab="子任务" />
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
        <NCard v-if="health.snapshots?.debug_json_queue" size="small" title="Debug JSON 队列">
          <div><span style="font-size: 22px; font-weight: 600;">{{ health.snapshots.debug_json_queue.pending ?? 0 }}</span><span style="font-size: 12px; color: var(--color-text-muted);"> 排队</span><span style="font-size: 12px; color: var(--color-text-muted);"> · {{ fmtKB(health.snapshots.debug_json_queue.pending_bytes ?? 0) }}</span></div>
          <div style="font-size: 12px; color: var(--color-text-muted); margin-top: 2px;">
            已刷 {{ (health.snapshots.debug_json_queue.processed ?? 0).toLocaleString() }} 条 / {{ fmtBytes(health.snapshots.debug_json_queue.processed_bytes ?? 0) }}
          </div>
          <div style="font-size: 12px; color: var(--color-text-muted); margin-top: 2px;">
            丢弃 <span :style="{ color: ((health.snapshots.debug_json_queue.dropped_full ?? 0) + (health.snapshots.debug_json_queue.dropped_oversize ?? 0)) > 0 ? 'var(--color-warning)' : 'var(--color-text-muted)' }">{{ (health.snapshots.debug_json_queue.dropped_full ?? 0) + (health.snapshots.debug_json_queue.dropped_oversize ?? 0) }}</span>
            · 失败批次 <span :style="{ color: (health.snapshots.debug_json_queue.process_errors ?? 0) > 0 ? 'var(--color-danger)' : 'var(--color-text-muted)' }">{{ health.snapshots.debug_json_queue.process_errors ?? 0 }}</span>
          </div>
        </NCard>
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

      <!-- 请求端到端耗时 -->
      <NCard v-if="health.latency?.request?.count" size="small" title="请求耗时 (端到端)" style="margin-bottom: var(--space-lg);">
        <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">{{ latSummary(health.latency.request) }}</div>
        <canvas id="request-lat-chart" />
      </NCard>

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
            <div v-for="q in (hooks.task_queues || [])" :key="q.task_type" style="padding-left: 8px; color: var(--color-text-muted);">
              · {{ q.task_type }}: <strong style="color: var(--color-text);">{{ q.pending }}</strong>
            </div>
            <div>主题发现剩余: <strong>{{ hooks.discover_remaining }}</strong></div>
            <div>会话归档剩余: <strong>{{ hooks.archive_remaining }}</strong></div>
          </div>
        </NCard>
      </div>

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
                <NTag :type="d.verdict === 'topn' || d.verdict === 'selected' || d.verdict === 'nominated' || d.verdict === 'success' || d.verdict === 'verified' || d.verdict === 'corrected' ? 'success' : d.verdict === 'dropped' || d.verdict === 'rejected' || d.verdict === 'error' ? 'error' : 'warning'" size="tiny">{{ d.verdict }}</NTag>
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

    <!-- ========== LLM 审计 Tab ========== -->
    <div v-if="activeTab === 'llm-io'" style="padding-top: var(--space-sm);">
      <LLMIOAuditTab />
    </div>
    <!-- ========== 子任务 Tab ========== -->
    <div v-if="activeTab === 'subagent'" style="padding-top: var(--space-sm);">
      <SubAgentTab />
    </div>
    <!-- ========== Token 消耗 Tab ========== -->
    <div v-if="activeTab === 'cost'" style="padding-top: var(--space-sm);">
      <!-- 筛选栏 -->
      <div style="display: flex; gap: var(--space-md); align-items: center; margin-bottom: var(--space-md); flex-wrap: wrap;">
        <NSelect v-model:value="costRangeDays" :options="costRanges" size="small" style="width: 110px;" />
        <NSelect v-model:value="costUsers" :options="costUserOptions" size="small" multiple clearable style="min-width: 200px;" placeholder="全部用户" />
        <NSelect v-model:value="costCategory" :options="costCategoryOptions" size="small" clearable style="min-width: 130px;" placeholder="全部分类" />
        <NSelect v-model:value="costModel" :options="costModelOptions" size="small" multiple clearable style="min-width: 200px;" placeholder="全部模型" />
        <div style="display: flex; gap: var(--space-sm); align-items: center; margin-left: auto;">
          <span style="font-size: 12px; color: var(--color-text-muted);">堆叠:</span>
          <NCheckbox :checked="costBarStack.hit" @update:checked="toggleBarStack('hit')">命中</NCheckbox>
          <NCheckbox :checked="costBarStack.miss" @update:checked="toggleBarStack('miss')">未命中</NCheckbox>
          <NCheckbox :checked="costBarStack.completion" @update:checked="toggleBarStack('completion')">生成</NCheckbox>
          <NButton size="small" @click="refreshCost()" :loading="costLoading">刷新</NButton>
        </div>
      </div>

      <!-- 概览卡片 -->
      <div class="stats-grid" style="margin-bottom: var(--space-lg);">
        <NCard size="small" title="今日 Token">
          <div style="font-size: 22px; font-weight: 600;">{{ fmtTokens(costSummary?.today?.total_tokens) }}</div>
          <div style="font-size: 12px; color: var(--color-text-muted);">{{ costSummary?.today?.call_count ?? 0 }} 次调用 · 命中率 {{ hitRate(costSummary?.today) }}%</div>
        </NCard>
        <NCard size="small" title="昨日 Token">
          <div style="font-size: 22px; font-weight: 600;">{{ fmtTokens(costSummary?.yesterday?.total_tokens) }}</div>
          <div style="font-size: 12px; color: var(--color-text-muted);">{{ costSummary?.yesterday?.call_count ?? 0 }} 次调用 · 命中率 {{ hitRate(costSummary?.yesterday) }}%</div>
        </NCard>
        <NCard size="small" title="本月 Token">
          <div style="font-size: 22px; font-weight: 600;">{{ fmtTokens(costSummary?.this_month?.total_tokens) }}</div>
          <div style="font-size: 12px; color: var(--color-text-muted);">{{ costSummary?.this_month?.call_count ?? 0 }} 次调用 · 命中率 {{ hitRate(costSummary?.this_month) }}%</div>
        </NCard>
        <NCard size="small" title="本月缓存">
          <div style="font-size: 22px; font-weight: 600;">命中 {{ hitRate(costSummary?.this_month) }}%</div>
          <div style="font-size: 12px; color: var(--color-text-muted);">命中 {{ fmtTokens(costSummary?.this_month?.prompt_cache_hit) }} · 未命中 {{ fmtTokens(costSummary?.this_month?.prompt_cache_miss) }}</div>
        </NCard>
        <NCard size="small" title="今日消费">
          <div style="font-size: 22px; font-weight: 600;">{{ fmtMoney(spendWindow(1)) }}</div>
          <div style="font-size: 12px; color: var(--color-text-muted);">近3日 {{ fmtMoney(spendWindow(3)) }} · 近7日 {{ fmtMoney(spendWindow(7)) }}</div>
        </NCard>
      </div>

      <!-- 图表区：柱状图 + 饼图共享同一弹性容器，保证左右边界对齐 -->
      <div class="cost-charts">
        <div v-if="costSelectedDate" style="display: flex; align-items: center; gap: var(--space-sm); margin-bottom: var(--space-sm);">
          <NButton size="small" @click="clearDate">← 返回按日</NButton>
          <span style="font-size: 13px; font-weight: 600;">{{ costSelectedDate }} 分时详情（24 小时，北京时间）</span>
        </div>
        <NCard size="small">
          <template #header>
            <span>{{ costSelectedDate ? '分时 Token 消耗' : '每日 Token 消耗' }}</span>
          </template>
          <canvas id="cost-daily-chart" />
        </NCard>

        <NCard size="small">
          <template #header>
            <div style="display: flex; justify-content: space-between; align-items: center;">
              <span>{{ costSelectedDate ? '分时花销' : '每日花销' }}</span>
              <span style="font-size: 13px; color: var(--color-primary); font-weight: 600;">
                {{ costSelectedDate ? fmtMoney(costHourly?.totals?.cost) : fmtMoney(costExpense?.total) }}
                <span style="color: var(--color-text-muted); font-weight: 400;">
                  （峰 {{ costSelectedDate ? fmtMoney(costHourly?.totals?.cost_peak) : fmtMoney(costExpense?.total_peak) }} /
                  谷 {{ costSelectedDate ? fmtMoney(costHourly?.totals?.cost_off) : fmtMoney(costExpense?.total_off) }}）
                </span>
              </span>
            </div>
          </template>
          <NAlert v-if="(costSelectedDate ? costHourly?.unpriced_models : costExpense?.unpriced_models)?.length" type="warning" style="margin-bottom: var(--space-md);">
            以下模型无有效价格，按 0 计：{{ (costSelectedDate ? costHourly.unpriced_models : costExpense.unpriced_models).join(', ') }}
          </NAlert>
          <canvas id="cost-expense-chart" />
        </NCard>

        <div class="cost-pie-grid">
          <NCard size="small" title="分类分布">
            <div class="pie-box"><canvas id="cost-cat-chart" /></div>
          </NCard>
          <NCard size="small" title="模型分布">
            <div class="pie-box"><canvas id="cost-model-chart" /></div>
          </NCard>
          <NCard size="small" title="子类分布">
            <div class="pie-box"><canvas id="cost-sub-chart" /></div>
          </NCard>
        </div>

        <!-- 模型定价管理 -->
        <NCard size="small" title="模型定价 (元/1M tokens，北京时间)">
          <div style="display: flex; gap: var(--space-sm); align-items: flex-end; flex-wrap: wrap; margin-bottom: var(--space-md);">
            <div>
              <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">模型名</div>
              <NSelect v-model:value="priceForm.model" :options="priceModelOptions" filterable clearable placeholder="选择模型" size="small" style="width: 200px;" />
            </div>
            <div>
              <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">生效日期</div>
              <NInput v-model:value="priceForm.effective_date" type="date" placeholder="" size="small" style="width: 150px;" />
            </div>
            <div>
              <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">峰·命中</div>
              <NInputNumber v-model:value="priceForm.price_hit" :min="0" :precision="4" size="small" style="width: 100px;" />
            </div>
            <div>
              <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">峰·未命中</div>
              <NInputNumber v-model:value="priceForm.price_miss" :min="0" :precision="4" size="small" style="width: 100px;" />
            </div>
            <div>
              <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">峰·生成</div>
              <NInputNumber v-model:value="priceForm.price_output" :min="0" :precision="4" size="small" style="width: 100px;" />
            </div>
            <div>
              <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">谷·命中</div>
              <NInputNumber v-model:value="priceForm.price_hit_off" :min="0" :precision="4" size="small" style="width: 100px;" />
            </div>
            <div>
              <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">谷·未命中</div>
              <NInputNumber v-model:value="priceForm.price_miss_off" :min="0" :precision="4" size="small" style="width: 100px;" />
            </div>
            <div>
              <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">谷·生成</div>
              <NInputNumber v-model:value="priceForm.price_output_off" :min="0" :precision="4" size="small" style="width: 100px;" />
            </div>
            <div>
              <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">峰时段</div>
              <NInput v-model:value="priceForm.peak_segments" placeholder="08:30-12:00,14:00-17:00 留空=全天谷" size="small" style="width: 230px;" />
            </div>
            <div>
              <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">周末</div>
              <NCheckbox v-model:checked="priceForm.weekend_all_off" size="small">周末全天谷</NCheckbox>
            </div>
            <NButton size="small" type="primary" :loading="priceSaving" @click="savePrice">{{ priceEditingId ? '更新' : '保存' }}</NButton>
            <NButton v-if="priceEditingId" size="small" @click="cancelEditPrice">取消</NButton>
          </div>
          <div style="font-size: 11px; color: var(--color-text-muted); margin-bottom: var(--space-sm);">
            峰时段格式：HH:MM-HH:MM，多段用英文逗号分隔；峰段内用峰价，其余用谷价（段开始含、结束不含）；全部按北京时间判定。勾选「周末全天谷」后周六/周日恒按谷价，峰时段仅作用于周一~周五。
          </div>
          <div v-if="priceRows.length" style="display: flex; flex-direction: column; gap: 4px;">
            <div v-for="p in priceRows" :key="p.id" style="display: flex; align-items: center; gap: var(--space-sm); font-size: 13px; padding: 4px 0; border-top: 1px solid var(--color-border);">
              <span style="font-weight: 500; min-width: 150px;">{{ p.model }}</span>
              <NTag size="tiny">{{ p.effective_date }}</NTag>
              <span style="color: var(--color-text-secondary);">峰 {{ p.price_hit }}/{{ p.price_miss }}/{{ p.price_output }}</span>
              <span style="color: var(--color-text-secondary);">谷 {{ p.price_hit_off }}/{{ p.price_miss_off }}/{{ p.price_output_off }}</span>
              <span style="color: var(--color-text-secondary);">{{ fmtPeakSegments(p.peak_segments) }}</span>
              <NTag v-if="p.weekend_all_off" size="tiny" type="success">周末全天谷</NTag>
              <NButton size="tiny" quaternary style="margin-left: auto;" @click="editPrice(p)">编辑</NButton>
              <NButton size="tiny" quaternary type="error" @click="deletePrice(p.id)">删除</NButton>
            </div>
          </div>
          <NEmpty v-else description="暂无定价记录，请先添加" />
        </NCard>
      </div>

      <!-- 钻取弹层：点击饼图扇形查看该分组三维度占比 -->
      <NModal v-model:show="costDrillOpen" preset="card" :title="costDrillDown?.title" style="width: 420px;" @after-enter="renderDrillChart">
        <div class="pie-box"><canvas id="cost-drill-chart" /></div>
        <div style="font-size: 11px; color: var(--color-text-muted); margin-top: var(--space-sm);">
          命中 {{ fmtTokens(costDrillDown?.data?.[0]) }} · 未命中 {{ fmtTokens(costDrillDown?.data?.[1]) }} · 生成 {{ fmtTokens(costDrillDown?.data?.[2]) }}
        </div>
      </NModal>
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

/* Token 消耗 Tab：图表区统一弹性容器，柱状图与饼图左右对齐 */
.cost-charts {
  display: flex;
  flex-direction: column;
  gap: var(--space-lg);
}

/* Token 消耗 Tab：三个饼图一行排布 */
.cost-pie-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: var(--space-md);
}
/* 饼图容器定高：Chart.js maintainAspectRatio:false 需要容器有确定高度 */
.pie-box {
  height: 300px;
}
@media (max-width: 900px) {
  .cost-pie-grid {
    grid-template-columns: 1fr;
  }
}
</style>

