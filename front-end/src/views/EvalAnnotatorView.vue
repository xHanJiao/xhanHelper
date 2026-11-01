<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { api } from '@/api/client'
import { NCard, NTag, NSpin, NSelect, NTabs, NTabPane, NInput, NInputNumber, NButton, NEmpty, NAlert, NRadioGroup, NRadioButton, NPagination, NModal } from 'naive-ui'
import PageTopBar from '@/components/PageTopBar.vue'

const activeTab = ref('overview')

const projects = ref([])
const currentProject = ref(null)
const stats = ref(null)

const queries = ref([])
const queriesTotal = ref(0)
const queriesPage = ref(1)
const queryKeyword = ref('')
const queryStatus = ref('all')
const currentSession = ref('')
const selectedQuery = ref(null)
const queryDetail = ref(null)

const corpus = ref([])
const corpusTotal = ref(0)
const corpusPage = ref(1)
const corpusKeyword = ref('')
const corpusType = ref('all')

const records = ref([])
const recordsTotal = ref(0)
const recordsPage = ref(1)
const recordsKeyword = ref('')
const recordsRelevance = ref(-1)
const recordsSource = ref('all')

const loading = ref(false)
const detailLoading = ref(false)
const saving = ref(false)
const saveMsg = ref('')
const saveErrorQid = ref(null)
const selectedQueryIds = ref(new Set())
const labelLoading = ref(false)
let pollTimer = null
let pollCount = 0

// 评测任务
const runs = ref([])
const showCreateRun = ref(false)
const newRun = ref({ engine: 'vector', name: '', mode: 'all', randomCount: 1 })
const placedQueries = ref([])
const placedTotal = ref(0)
const placedPage = ref(1)
const placedKeyword = ref('')
const selectedRunQueries = ref([])
const showRunDetail = ref(false)
const runDetail = ref(null)

const metricHints = {
  MRR: '平均倒数排名（Mean Reciprocal Rank）：第一个相关结果排在第几位。1=每次都排第1，0=从未找到。越高越好。',
  NDCG5: '归一化折损累计增益@5：前5条结果的综合排序质量，考虑排名位置和5档相关度权重。越高越好。',
  NDCG10: '同上，取前10条。',
  Recall5: '召回率@5：前5条中找到了多少个真正相关的语料。1=全找到，0=全漏了。越高越好。',
  Recall10: '同上，取前10条。',
  Precision5: '精确率@5：前5条中有多少是真正有用的。1=全对，0=全错。越高越好。',
  Precision10: '同上，取前10条。',
}

const showMetricHelp = ref(false)
const helpPos = ref({ x: 0, y: 0 })

function showHelp(e) {
  helpPos.value = { x: e.clientX, y: e.clientY }
  showMetricHelp.value = true
}

const relevanceLabels = { 0: '完全无关', 1: '话题相近', 2: '弱相关', 3: '相关', 4: '完美命中' }
const relevanceColors = { 0: 'default', 1: 'info', 2: 'warning', 3: 'success', 4: 'error' }

async function loadProjects() {
  try {
    const res = await api.get('/api/admin/eval/projects')
    projects.value = res.projects || []
    if (projects.value.length > 0 && !currentProject.value) {
      currentProject.value = projects.value[0].id
    }
  } catch (e) { console.error('[eval] loadProjects:', e) }
}

async function loadStats() {
  if (!currentProject.value) return
  loading.value = true
  try {
    stats.value = await api.get(`/api/admin/eval/projects/${currentProject.value}/stats`)
  } catch (e) { console.error('[eval] loadStats:', e); stats.value = null }
  finally { loading.value = false }
}

async function loadQueries() {
  if (!currentProject.value) return
  loading.value = true
  try {
    const params = `?limit=50&offset=${(queriesPage.value - 1) * 50}&status=${queryStatus.value}`
    const kw = queryKeyword.value ? `&keyword=${encodeURIComponent(queryKeyword.value)}` : ''
    const sid = currentSession.value ? `&session_id=${encodeURIComponent(currentSession.value)}` : ''
    const res = await api.get(`/api/admin/eval/projects/${currentProject.value}/queries${params}${kw}${sid}`)
    queries.value = res.queries || []
    queriesTotal.value = res.total || 0
  } catch (e) { console.error('[eval] loadQueries:', e); queries.value = [] }
  finally { loading.value = false }
}

async function selectQuery(q) {
  selectedQuery.value = q
  detailLoading.value = true
  queryDetail.value = null
  try {
    queryDetail.value = await api.get(`/api/admin/eval/projects/${currentProject.value}/queries/${q.id}`)
  } catch (e) { console.error('[eval] selectQuery:', e); queryDetail.value = null }
  finally { detailLoading.value = false }
}

function updateScore(ann, val) {
  ann._dirty = true
  ann._newRelevance = val
}

async function saveAnnotations() {
  if (!queryDetail.value || !currentProject.value) return
  saving.value = true
  saveMsg.value = ''
  const changed = queryDetail.value.annotations
    .filter(a => a._dirty)
    .map(a => ({ corpus_id: a.corpus_id, relevance: a._newRelevance ?? a.relevance }))
  if (changed.length === 0) { saveMsg.value = '无修改'; saving.value = false; return }
  try {
    await api.patch(`/api/admin/eval/projects/${currentProject.value}/queries/${selectedQuery.value.id}`, {
      annotations: changed,
      review_status: 'reviewed',
      notes: ''
    })
    saveMsg.value = '✓ 已保存'
    selectedQuery.value = null
    queryDetail.value = null
    loadQueries()
  } catch (e) {
    console.error('[eval] saveAnnotations:', e)
    saveMsg.value = '保存失败'
  }
  finally { saving.value = false }
}

async function skipQuery() {
  selectedQuery.value = null
  queryDetail.value = null
}

async function saveQueryAnnotations(queryId) {
  if (!currentProject.value) return
  const group = records.value.find(g => g.query_id === queryId)
  if (!group) return
  const changed = group.annotations
    .filter(a => a._dirty)
    .map(a => ({ corpus_id: a.corpus_id, relevance: a._newRelevance ?? a.relevance }))
  if (changed.length === 0) return
  try {
    await api.patch(`/api/admin/eval/projects/${currentProject.value}/queries/${queryId}`, {
      annotations: changed,
      review_status: 'reviewed',
      notes: ''
    })
    saveErrorQid.value = null
    const byID = new Map(changed.map(c => [c.corpus_id, c.relevance]))
    for (const a of group.annotations) {
      if (byID.has(a.corpus_id)) {
        a.relevance = byID.get(a.corpus_id)
        a.label_source = 'corrected'
      }
      delete a._dirty
      delete a._newRelevance
    }
    group.annotations.sort((a, b) => b.relevance - a.relevance)
  } catch (e) {
    console.error('[eval] saveQueryAnnotations:', e)
    saveErrorQid.value = queryId
  }
}

async function labelSelected() {
  const ids = [...selectedQueryIds.value]
  if (ids.length === 0) return
  labelLoading.value = true
  try {
    const now = new Date()
    const name = `batch-${now.getFullYear()}${String(now.getMonth()+1).padStart(2,'0')}${String(now.getDate()).padStart(2,'0')}-${String(now.getHours()).padStart(2,'0')}${String(now.getMinutes()).padStart(2,'0')}${String(now.getSeconds()).padStart(2,'0')}`
    const res = await api.post(`/api/admin/eval/projects/${currentProject.value}/label`, {
      query_ids: ids, candidate_limit: 20, session_name: name
    })
    selectedQueryIds.value = new Set()
    if (res.session_id) startPolling(res.session_id)
  } catch (e) {
    console.error('[eval] labelSelected:', e)
    labelLoading.value = false
  }
}

function startPolling(sessionId) {
  pollCount = 0
  if (pollTimer) clearInterval(pollTimer)
  pollTimer = setInterval(async () => {
    if (++pollCount > 30) { clearInterval(pollTimer); pollTimer = null; labelLoading.value = false; loadQueries(); loadStats(); return }
    try {
      const s = await api.get(`/api/admin/eval/projects/${currentProject.value}/sessions/${sessionId}`)
      if (s.done_count >= s.total_count) {
        clearInterval(pollTimer); pollTimer = null
        labelLoading.value = false
        loadQueries(); loadStats()
      }
    } catch (_) { }
  }, 20000)
}

function toggleSelect(qid) {
  const s = new Set(selectedQueryIds.value)
  if (s.has(qid)) {
    s.delete(qid)
  } else {
    if (s.size >= 10) return
    s.add(qid)
  }
  selectedQueryIds.value = s
}

function gotoSession(sid) {
  activeTab.value = 'annotate'
  currentSession.value = sid
}

async function loadCorpus() {
  if (!currentProject.value) return
  loading.value = true
  try {
    const params = `?limit=50&offset=${(corpusPage.value - 1) * 50}&type=${corpusType.value}`
    const kw = corpusKeyword.value ? `&keyword=${encodeURIComponent(corpusKeyword.value)}` : ''
    const res = await api.get(`/api/admin/eval/projects/${currentProject.value}/corpus${params}${kw}`)
    corpus.value = res.corpus || []
    corpusTotal.value = res.total || 0
  } catch (e) { console.error('[eval] loadCorpus:', e); corpus.value = [] }
  finally { loading.value = false }
}

async function loadRecords() {
  if (!currentProject.value) return
  loading.value = true
  try {
    let params = `?limit=20&offset=${(recordsPage.value - 1) * 20}`
    if (recordsKeyword.value) params += `&keyword=${encodeURIComponent(recordsKeyword.value)}`
    if (recordsRelevance.value >= 0) params += `&relevance=${recordsRelevance.value}`
    if (recordsSource.value !== 'all') params += `&label_source=${recordsSource.value}`
    const res = await api.get(`/api/admin/eval/projects/${currentProject.value}/query-annotations${params}`)
    records.value = res.queries || []
    recordsTotal.value = res.total || 0
    expandedQueries.value = {}
  } catch (e) { console.error('[eval] loadRecords:', e); records.value = [] }
  finally { loading.value = false }
}

async function loadRuns(append) {
  if (!currentProject.value) return
  try {
    const page = append ? Math.ceil(runs.value.length / 20) + 1 : 1
    const res = await api.get(`/api/admin/eval/projects/${currentProject.value}/runs?limit=20&offset=${(page - 1) * 20}`)
    const list = (res.runs || []).map(r => {
      let config = r.config, report = r.report
      try { config = JSON.parse(r.config) } catch (_) { }
      try { report = JSON.parse(r.report) } catch (_) { }
      return { ...r, config, report }
    })
    if (append) {
      runs.value = [...runs.value, ...list]
    } else {
      runs.value = list
    }
  } catch (e) { console.error('[eval] loadRuns:', e) }
}

async function createRun() {
  if (!currentProject.value || !newRun.value.name) return
  try {
    const scope = newRun.value.mode === 'manual'
      ? { mode: 'manual', count: selectedRunQueries.value.length }
      : newRun.value.mode === 'random'
      ? { mode: 'random', count: newRun.value.randomCount }
      : { mode: 'all', count: placedTotal.value }
    const res = await api.post(`/api/admin/eval/projects/${currentProject.value}/runs`, {
      name: newRun.value.name,
      config: { engine: { type: newRun.value.engine, model: 'doubao-embedding-vision-251215' }, scope }
    })
    // 根据模式构建 execute 参数
    const execBody = {}
    if (newRun.value.mode === 'manual') {
      execBody.query_ids = selectedRunQueries.value.map(q => q.id)
    } else if (newRun.value.mode === 'random') {
      execBody.random_count = newRun.value.randomCount
    }
    // all 模式不传参数
    await api.post(`/api/admin/eval/projects/${currentProject.value}/runs/${res.id}/execute`, execBody)
    showCreateRun.value = false
    selectedRunQueries.value = []
    placedQueries.value = []
    await loadRuns(false)
    runPollTimer = null
    pollRun(res.id)
  } catch (e) { console.error('[eval] createRun:', e) }
}

async function executeRun(runId) {
  if (!currentProject.value) return
  try {
    await api.post(`/api/admin/eval/projects/${currentProject.value}/runs/${runId}/execute`)
    pollRun(runId)
  } catch (e) { console.error('[eval] executeRun:', e) }
}

let runPollTimer = null
function pollRun(runId) {
  if (runPollTimer) clearInterval(runPollTimer)
  runPollTimer = setInterval(async () => {
    try {
      const res = await api.get(`/api/admin/eval/projects/${currentProject.value}/runs/${runId}`)
      if (res.status === 'done' || res.status === 'error') {
        clearInterval(runPollTimer); runPollTimer = null
        loadRuns()
      }
    } catch (_) { }
  }, 5000)
}

async function deleteRun(runId) {
  if (!currentProject.value) return
  try {
    await api.delete(`/api/admin/eval/projects/${currentProject.value}/runs/${runId}`)
    loadRuns(false)
  } catch (e) { console.error('[eval] deleteRun:', e) }
}

async function viewRun(runId) {
  showRunDetail.value = true
  try {
    runDetail.value = await api.get(`/api/admin/eval/projects/${currentProject.value}/runs/${runId}`)
  } catch (e) { console.error('[eval] viewRun:', e); showRunDetail.value = false }
}

async function loadPlacedQueries(append) {
  if (!currentProject.value) return
  if (!append) placedPage.value = 1
  try {
    const params = `?limit=30&offset=${(placedPage.value - 1) * 30}`
    const kw = placedKeyword.value ? `&keyword=${encodeURIComponent(placedKeyword.value)}` : ''
    const res = await api.get(`/api/admin/eval/projects/${currentProject.value}/queries/placed${params}${kw}`)
    if (append) {
      placedQueries.value = [...placedQueries.value, ...(res.queries || [])]
    } else {
      placedQueries.value = res.queries || []
    }
    placedTotal.value = res.total || 0
  } catch (e) { console.error('[eval] loadPlacedQueries:', e) }
}

function loadMorePlaced() {
  placedPage.value++
  loadPlacedQueries(true)
}

function addQuery(q) {
  if (selectedRunQueries.value.find(x => x.id === q.id)) return
  selectedRunQueries.value = [...selectedRunQueries.value, q]
}

function removeQuery(q) {
  selectedRunQueries.value = selectedRunQueries.value.filter(x => x.id !== q.id)
}

function openCreateRun() {
  newRun.value = { engine: 'vector', name: '', mode: 'all', randomCount: 1 }
  selectedRunQueries.value = []
  placedQueries.value = []
  showCreateRun.value = true
  loadPlacedQueries(false)
}

watch(() => newRun.value.mode, (m) => {
  if (m === 'manual') loadPlacedQueries(false)
})

const projectOptions = computed(() => projects.value.map(p => ({ label: p.name || p.id, value: p.id })))

const sessionOptions = computed(() => {
  const opts = [{ label: '全部批次', value: '' }]
  if (stats.value?.sessions) {
    for (const s of stats.value.sessions) {
      opts.push({ label: s.name || s.id, value: s.id })
    }
  }
  return opts
})

const expandedQueries = ref({})
function toggleQuery(qid) {
  expandedQueries.value[qid] = !expandedQueries.value[qid]
}

const expandedItems = ref({})
function toggleContent(qid, cid) {
  const key = qid + ':' + cid
  expandedItems.value[key] = !expandedItems.value[key]
}

async function quickUpdate(r, v, queryId) {
  if (saveErrorQid.value === queryId) saveErrorQid.value = null
  updateScore(r, v)
  await saveQueryAnnotations(queryId)
}

watch(currentProject, () => { loadStats(); loadQueries(); loadCorpus(); loadRecords(); loadRuns(false) })
watch(activeTab, (t) => {
  if (t === 'overview') { loadStats(); loadRuns(false) }
  if (t === 'annotate') loadQueries()
  if (t === 'corpus') loadCorpus()
  if (t === 'records') loadRecords()
  if (t === 'evaltasks') loadRuns()
})
watch([queriesPage, queryStatus], () => loadQueries())
watch(currentSession, () => { queriesPage.value = 1; loadQueries() })
watch([corpusPage, corpusType], () => loadCorpus())
watch([recordsPage, recordsRelevance, recordsSource], () => loadRecords())

onMounted(() => loadProjects())
</script>

<template>
  <div class="eval-page">
    <PageTopBar title="检索评测">
      <template #right>
        <NSelect v-model:value="currentProject" :options="projectOptions" size="small" placeholder="选择项目"
          style="width: 240px" />
      </template>
    </PageTopBar>

    <NTabs v-model:value="activeTab" type="line" class="eval-tabs">
      <NTabPane name="overview" tab="标注概览" />
      <NTabPane name="annotate" tab="逐条审核" />
      <NTabPane name="corpus" tab="语料浏览" />
      <NTabPane name="records" tab="标注记录" />
      <NTabPane name="evaltasks" tab="评测任务" />
    </NTabs>

    <div class="eval-content">
      <NSpin :show="loading">

        <!-- Tab: 标注概览 -->
        <div v-if="activeTab === 'overview' && stats" class="tab-overview">
          <div class="stat-cards">
            <NCard size="small"><template #header>query 总数</template>{{ stats.total_queries }}</NCard>
            <NCard size="small"><template #header>LLM 标注</template>{{ stats.labeled_queries }}</NCard>
            <NCard size="small"><template #header>人工审核</template>{{ stats.reviewed_queries }}</NCard>
            <NCard size="small"><template #header>未标注</template>{{ stats.unlabeled_queries }}</NCard>
          </div>
          <NCard title="评测任务统计" size="small" style="margin-top: 12px">
            <div class="stat-cards" v-if="runs.length > 0">
              <NCard size="small"><template #header>任务总数</template>{{ runs.length }}</NCard>
              <NCard size="small"><template #header>已完成</template>{{ runs.filter(r => r.status === 'done').length }}</NCard>
              <NCard size="small"><template #header>运行中</template>{{ runs.filter(r => r.status === 'running').length }}</NCard>
              <NCard size="small"><template #header>失败</template>{{ runs.filter(r => r.status === 'error').length }}</NCard>
            </div>
            <NEmpty v-else description="暂无评测任务" size="small" />
          </NCard>
          <NCard title="标注批次" size="small" style="margin-top: 12px">
            <div v-if="stats.sessions && stats.sessions.length" class="session-table">
              <div class="session-row header">
                <span>批次</span><span>数量</span><span>状态</span><span>时间</span>
              </div>
              <div v-for="s in stats.sessions" :key="s.id" class="session-row clickable" @click="gotoSession(s.id)">
                <span>{{ s.name || s.id }}</span><span>{{ s.query_count }}</span>
                <span><NTag size="tiny" :type="s.status === 'prelabeled' ? 'success' : 'default'">{{ s.status }}</NTag></span>
                <span class="time">{{ s.created_at?.slice(0, 10) }}</span>
              </div>
            </div>
            <NEmpty v-else description="暂无批次" size="small" />
          </NCard>
        </div>

        <!-- Tab: 逐条审核 -->
        <div v-if="activeTab === 'annotate'" class="tab-annotate">
          <div class="annotate-layout">
            <div class="query-list-panel">
              <div class="panel-toolbar">
                <NInput v-model:value="queryKeyword" size="small" placeholder="搜索 query..." clearable @update:value="loadQueries"
                  style="margin-bottom: 8px" />
                <NSelect v-model:value="queryStatus" size="small" :options="[
                  { label: '全部', value: 'all' }, { label: '未选中', value: 'unselected' }, { label: '已选中', value: 'selected' },
                  { label: '预标注', value: 'prelabeled' }, { label: '已审核', value: 'reviewed' }
                ]" style="margin-bottom: 8px" />
                <NSelect v-model:value="currentSession" :options="sessionOptions" size="small" placeholder="选择批次" style="margin-bottom: 8px" />
                <div style="display: flex; gap: 6px; margin-bottom: 8px">
                  <NButton size="tiny" type="primary" @click="labelSelected" :loading="labelLoading"
                    :disabled="selectedQueryIds.size === 0">
                    预标注 ({{ selectedQueryIds.size }}/10)
                  </NButton>
                </div>
              </div>
              <div class="query-items">
                <div v-for="q in queries" :key="q.id" class="query-item"
                  :class="{ active: selectedQuery?.id === q.id, selected: selectedQueryIds.has(q.id) }">
                  <div class="qi-row">
                    <input type="checkbox" :checked="selectedQueryIds.has(q.id)"
                      @click.stop @change="toggleSelect(q.id)" class="qi-check" />
                    <div class="qi-main" @click="selectQuery(q)">
                      <div class="qi-content">{{ q.content?.slice(0, 60) }}{{ q.content?.length > 60 ? '...' : '' }}</div>
                      <div class="qi-meta">
                        <NTag size="tiny" :type="q.reviewed ? 'success' : 'default'">{{ q.reviewed ? '已审核' : '待审核' }}</NTag>
                        <span class="qi-count">标注:{{ q.labeled_count }}</span>
                      </div>
                    </div>
                  </div>
                </div>
                <NEmpty v-if="queries.length === 0" description="暂无 query" size="small" />
              </div>
              <NPagination v-if="queriesTotal > 50" v-model:page="queriesPage"
                :page-count="Math.ceil(queriesTotal / 50)" size="small" style="margin-top: 8px" />
            </div>

            <div class="query-detail-panel">
              <template v-if="!selectedQuery">
                <NEmpty description="← 选择一条 query" />
              </template>
              <NSpin :show="detailLoading" v-else>
                <div class="detail-header">
                  <strong># {{ selectedQuery.content?.slice(0, 80) }}</strong>
                  <div class="detail-meta">
                    <NTag size="small">{{ queryDetail?.review_status || '待标注' }}</NTag>
                    <span class="time" v-if="queryDetail?.query?.created_at">{{ queryDetail.query.created_at?.slice(0, 10) }}</span>
                  </div>
                </div>
                <div class="annotation-list" v-if="queryDetail?.annotations">
                  <div v-for="a in queryDetail.annotations" :key="a.corpus_id" class="annotation-row">
                    <span v-if="!expandedItems[queryDetail.query.id + ':' + a.corpus_id]"
                      class="ann-content clickable" @click="toggleContent(queryDetail.query.id, a.corpus_id)">
                      {{ a.corpus_content?.slice(0, 80) }}{{ a.corpus_content?.length > 80 ? '...' : '' }}
                    </span>
                    <span v-else class="ann-content expanded" @click="toggleContent(queryDetail.query.id, a.corpus_id)">
                      {{ a.corpus_content }}
                    </span>
                    <div class="ann-score">
                      <NRadioGroup :value="a._newRelevance ?? a.relevance"
                        @update:value="v => updateScore(a, v)" size="small">
                        <NRadioButton v-for="s in 5" :key="s" :value="s - 1" :label="String(s - 1)" />
                      </NRadioGroup>
                      <NTag size="tiny" :type="(a._dirty ? 'warning' : relevanceColors[a.relevance])"
                        style="margin-left: 6px; min-width: 32px; text-align: center">
                        {{ a._newRelevance ?? a.relevance }}
                      </NTag>
                    </div>
                  </div>
                </div>
                <div class="detail-actions">
                  <span v-if="saveMsg" class="save-msg">{{ saveMsg }}</span>
                  <NButton size="small" @click="skipQuery">跳过</NButton>
                  <NButton size="small" type="primary" @click="saveAnnotations" :loading="saving">保存修改</NButton>
                </div>
              </NSpin>
            </div>
          </div>
        </div>

        <!-- Tab: 语料浏览 -->
        <div v-if="activeTab === 'corpus'" class="tab-corpus">
          <div class="panel-toolbar" style="display: flex; gap: 8px; margin-bottom: 12px">
            <NInput v-model:value="corpusKeyword" size="small" placeholder="搜索语料..." clearable @update:value="loadCorpus" style="flex: 1" />
            <NSelect v-model:value="corpusType" size="small" :options="[
              { label: '全部', value: 'all' }, { label: '笔记', value: 'note' }, { label: '记忆', value: 'memory' }
            ]" style="width: 110px" />
          </div>
          <div class="corpus-table">
            <div class="cr-row header"><span>类型</span><span>主题</span><span>内容</span><span>标注数</span></div>
            <div v-for="c in corpus" :key="c.id" class="cr-row">
              <span><NTag size="tiny" :type="c.source_type === 'note' ? 'info' : 'warning'">{{ c.source_type === 'note' ? '笔记' : '记忆' }}</NTag></span>
              <span class="topic">{{ c.topic || '-' }}</span>
              <span v-if="!expandedItems['corpus:' + c.id]"
                class="content clickable" @click="toggleContent('corpus', c.id)">
                {{ c.content?.slice(0, 80) }}{{ c.content?.length > 80 ? '...' : '' }}
              </span>
              <span v-else class="content expanded" @click="toggleContent('corpus', c.id)">
                {{ c.content }}
              </span>
              <span>{{ c.annotation_count }}</span>
            </div>
            <NEmpty v-if="corpus.length === 0" description="暂无数据" size="small" />
          </div>
          <NPagination v-if="corpusTotal > 50" v-model:page="corpusPage"
            :page-count="Math.ceil(corpusTotal / 50)" size="small" style="margin-top: 12px" />
        </div>

        <!-- Tab: 标注记录 -->
        <div v-if="activeTab === 'records'" class="tab-records">
          <div class="panel-toolbar" style="display: flex; gap: 8px; margin-bottom: 12px">
            <NInput v-model:value="recordsKeyword" size="small" placeholder="搜索 query..." clearable @update:value="loadRecords" style="flex: 1" />
            <NSelect v-model:value="recordsRelevance" size="small" :options="[
              { label: '全部评分', value: -1 }, { label: '0 无关', value: 0 }, { label: '1 相近', value: 1 },
              { label: '2 弱相关', value: 2 }, { label: '3 相关', value: 3 }, { label: '4 完美', value: 4 }
            ]" style="width: 120px" />
            <NSelect v-model:value="recordsSource" size="small" :options="[
              { label: '全部来源', value: 'all' }, { label: 'LLM', value: 'llm' }, { label: '人工修正', value: 'corrected' }
            ]" style="width: 120px" />
          </div>
          <div class="grouped-records">
            <div v-for="g in records" :key="g.query_id" class="gr-group">
              <div class="gr-query" @click="toggleQuery(g.query_id)">
                <span class="gr-toggle">{{ expandedQueries[g.query_id] ? '▾' : '▸' }}</span>
                <span class="gr-text">{{ g.query_content?.slice(0, 80) }}</span>
                <NTag size="tiny">{{ g.annotations?.length || 0 }} 条</NTag>
                <span v-if="saveErrorQid === g.query_id" class="save-err">修改失败</span>
              </div>
              <div v-if="expandedQueries[g.query_id]" class="gr-items">
                <div class="rr-row header"><span>corpus</span><span>评分</span><span>来源</span><span>时间</span></div>
                <div v-for="r in g.annotations" :key="r.corpus_id" class="rr-row">
                  <span v-if="!expandedItems[g.query_id + ':' + r.corpus_id]"
                    class="content clickable" @click="toggleContent(g.query_id, r.corpus_id)">
                    {{ r.corpus_content?.slice(0, 60) }}{{ r.corpus_content?.length > 60 ? '...' : '' }}
                  </span>
                  <span v-else class="content expanded" @click="toggleContent(g.query_id, r.corpus_id)">
                    {{ r.corpus_content }}
                  </span>
                  <span class="rr-score">
                    <NRadioGroup :value="r._newRelevance ?? r.relevance"
                      @update:value="v => quickUpdate(r, v, g.query_id)" size="tiny">
                      <NRadioButton v-for="s in 5" :key="s" :value="s - 1" :label="String(s - 1)" />
                    </NRadioGroup>
                    <NTag size="tiny" :type="(r._dirty ? 'warning' : relevanceColors[r.relevance])"
                      style="margin-left: 4px; min-width: 28px; text-align: center">
                      {{ r._newRelevance ?? r.relevance }}
                    </NTag>
                  </span>
                  <span><NTag size="tiny">{{ r.label_source === 'llm' ? 'LLM' : '人工' }}</NTag></span>
                  <span class="time">{{ r.labeled_at?.slice(0, 16) }}</span>
                </div>
              </div>
            </div>
            <NEmpty v-if="records.length === 0" description="暂无数据" size="small" />
          </div>
          <NPagination v-if="recordsTotal > 20" v-model:page="recordsPage"
            :page-count="Math.ceil(recordsTotal / 20)" size="small" style="margin-top: 12px" />
        </div>

        <!-- Tab: 评测任务 -->
        <div v-if="activeTab === 'evaltasks'" class="tab-evaltasks">
          <div style="margin-bottom: 12px">
            <NButton size="small" type="primary" @click="openCreateRun">新建评测任务</NButton>
          </div>
          <div class="run-table">
            <div class="run-row header"><span>名称</span><span>引擎</span><span>范围</span><span>状态</span>
              <span>MRR</span><span>NDCG@5</span><span>Recall@10</span>
              <span>操作&nbsp;<span class="help-btn" @mouseenter="showHelp" @mouseleave="showMetricHelp = false">❓</span></span></div>
            <div v-for="r in runs" :key="r.id" class="run-row">
              <span>{{ r.name }}</span>
              <span>{{ r.config?.engine?.type || 'vector' }}</span>
              <span>{{ r.config?.scope ? ({all:'全部',manual:'手动',random:'随机'}[r.config.scope.mode] || '?') + '(' + r.config.scope.count + ')' : '-' }}</span>
              <span><NTag size="tiny" :type="r.status === 'done' ? 'success' : r.status === 'error' ? 'error' : r.status === 'running' ? 'warning' : 'default'">{{ {pending:'待执行',running:'运行中',done:'完成',error:'失败'}[r.status] || r.status }}</NTag></span>
              <span>{{ r.report?.mrr?.toFixed(4) || '-' }}</span>
              <span>{{ r.report?.ndcg_at5?.toFixed(4) || '-' }}</span>
              <span>{{ r.report?.recall_at10?.toFixed(4) || '-' }}</span>
              <span class="run-actions">
                <NButton v-if="r.status === 'done'" size="tiny" @click="viewRun(r.id)">查看</NButton>
                <NButton v-if="r.status === 'pending'" size="tiny" type="primary" @click="executeRun(r.id)">执行</NButton>
                <NButton size="tiny" @click="deleteRun(r.id)">删除</NButton>
              </span>
            </div>
            <NEmpty v-if="runs.length === 0" description="暂无评测任务" size="small" />
            <div v-if="runs.length > 0 && runs.length % 20 === 0" style="text-align: center; margin-top: 8px">
              <NButton size="tiny" @click="loadRuns(true)">加载更多</NButton>
            </div>
          </div>
        </div>

        <!-- 新建评测对话框 -->
        <NModal v-model:show="showCreateRun" title="新建评测任务">
          <NCard size="small" style="width: 640px">
            <div style="margin-bottom: 12px">
              <div style="font-size: 13px; color: var(--color-text-muted); margin-bottom: 4px">引擎类型</div>
              <NSelect v-model:value="newRun.engine" size="small" :options="[{label:'向量检索',value:'vector'}]" />
            </div>
            <div style="margin-bottom: 12px">
              <div style="font-size: 13px; color: var(--color-text-muted); margin-bottom: 4px">任务名称</div>
              <NInput v-model:value="newRun.name" size="small" placeholder="如 vector-baseline" />
            </div>
            <div style="margin-bottom: 12px">
              <div style="font-size: 13px; color: var(--color-text-muted); margin-bottom: 4px">评测范围</div>
              <NRadioGroup v-model:value="newRun.mode" size="small">
                <NRadioButton value="all">全部已标注</NRadioButton>
                <NRadioButton value="random">随机采样</NRadioButton>
                <NRadioButton value="manual">手动指定</NRadioButton>
              </NRadioGroup>
            </div>
            <div v-if="newRun.mode === 'random'" style="margin-bottom: 12px; display: flex; align-items: center; gap: 8px">
              <span style="font-size: 13px; color: var(--color-text-muted)">数量</span>
              <NInputNumber v-model:value="newRun.randomCount" size="small" :min="1" :max="placedTotal" style="width: 80px" />
            </div>
            <div v-if="newRun.mode === 'manual'" style="margin-bottom: 12px">
              <div style="font-size: 13px; color: var(--color-text-muted); margin-bottom: 4px">已选 {{ selectedRunQueries.length }} 条</div>
              <div class="picker-layout">
                <div class="picker-pane">
                  <div class="picker-pane-header">可选 query</div>
                  <div class="picker-pane-list">
                    <div v-for="q in placedQueries" :key="q.id" class="picker-item" @click="addQuery(q)"
                      :class="{ disabled: selectedRunQueries.find(x => x.id === q.id) }">
                      {{ q.content?.slice(0, 50) }}{{ q.content?.length > 50 ? '...' : '' }}
                    </div>
                    <NButton v-if="placedQueries.length < placedTotal" size="small" text @click="loadMorePlaced" style="width: 100%">加载更多...</NButton>
                  </div>
                </div>
                <div class="picker-arrows">
                  <div style="font-size: 18px">→</div>
                  <div style="font-size: 18px; margin-top: 4px">←</div>
                </div>
                <div class="picker-pane">
                  <div class="picker-pane-header">已选择 ({{ selectedRunQueries.length }})</div>
                  <div class="picker-pane-list">
                    <div v-for="q in selectedRunQueries" :key="q.id" class="picker-item" @click="removeQuery(q)">
                      {{ q.content?.slice(0, 50) }}{{ q.content?.length > 50 ? '...' : '' }}
                    </div>
                    <NEmpty v-if="selectedRunQueries.length === 0" description="点击左侧添加" size="small" />
                  </div>
                </div>
              </div>
            </div>
            <div style="display: flex; gap: 8px; justify-content: flex-end">
              <NButton size="small" @click="showCreateRun = false">取消</NButton>
              <NButton size="small" type="primary" @click="createRun">创建并执行</NButton>
            </div>
          </NCard>
        </NModal>

        <!-- 查看评测结果弹窗 -->
        <NModal v-model:show="showRunDetail" style="width: 85vw; max-width: 1100px">
          <template #header>
            <span>{{ runDetail?.name || '评测结果' }}</span>
          </template>
          <NCard size="small" style="max-height: 85vh; overflow-y: auto">
            <template v-if="runDetail">
            <div class="run-report-summary" style="display: flex; gap: 16px; margin-bottom: 16px; flex-wrap: wrap; align-items: center">
              <NTag>MRR: {{ runDetail.report?.mrr?.toFixed(4) || '-' }}</NTag>
              <NTag type="info">NDCG@5: {{ runDetail.report?.ndcg_at5?.toFixed(4) || '-' }}</NTag>
              <NTag type="info">NDCG@10: {{ runDetail.report?.ndcg_at10?.toFixed(4) || '-' }}</NTag>
              <NTag type="success">Recall@5: {{ runDetail.report?.recall_at5?.toFixed(4) || '-' }}</NTag>
              <NTag type="success">Recall@10: {{ runDetail.report?.recall_at10?.toFixed(4) || '-' }}</NTag>
              <NTag type="warning">Precision@5: {{ runDetail.report?.precision_at5?.toFixed(4) || '-' }}</NTag>
              <span class="help-btn" @mouseenter="showHelp" @mouseleave="showMetricHelp = false">❓ 指标说明</span>
            </div>
            <div v-if="runDetail.queries">
              <div v-for="q in runDetail.queries" :key="q.query_id" class="report-query">
                <div class="rq-header" @click="q._open = !q._open">
                  <span class="rq-toggle">{{ q._open ? '▾' : '▸' }}</span>
                  <span v-if="!q._open" class="rq-text">{{ q.query?.slice(0, 60) }}{{ q.query?.length > 60 ? '...' : '' }}</span>
                  <span v-else class="rq-text expanded">{{ q.query }}</span>
                  <NTag size="tiny">MRR: {{ q.summary?.reciprocal_rank?.toFixed(2) }}</NTag>
                  <NTag size="tiny" type="info">NDCG@5: {{ q.summary?.ndcg_at5?.toFixed(2) }}</NTag>
                </div>
                <div v-if="q._open" class="rq-results">
                  <div class="rr-row header"><span>rank</span><span>corpus</span><span>score</span><span>GT</span></div>
                  <div v-for="r in q.results" :key="r.corpus_id" class="rr-row">
                    <span>{{ r.rank }}</span>
                    <span v-if="!expandedItems[q.query_id + ':' + r.corpus_id]"
                      class="content clickable" @click="toggleContent(q.query_id, r.corpus_id)">
                      {{ r.corpus_content?.slice(0, 50) }}{{ r.corpus_content?.length > 50 ? '...' : '' }}
                    </span>
                    <span v-else class="content expanded" @click="toggleContent(q.query_id, r.corpus_id)">
                      {{ r.corpus_content }}
                    </span>
                    <span>{{ r.score?.toFixed(4) }}</span>
                    <span><NTag size="tiny" :type="r.gt >= 3 ? 'success' : r.gt >= 1 ? 'warning' : r.gt === 0 ? 'error' : 'default'">{{ r.gt >= 0 ? r.gt : '-' }}</NTag></span>
                  </div>
                  <div v-if="q.results.length < q.total" class="rq-more">
                    共 {{ q.total }} 条，显示前 {{ q.results.length }} 条
                  </div>
                </div>
              </div>
            </div>
            </template>
          </NCard>
        </NModal>

        <div v-show="showMetricHelp" class="help-tooltip" :style="{ left: helpPos.x + 'px', top: (helpPos.y + 8) + 'px' }" @mouseleave="showMetricHelp = false">
          <div class="help-content">
            <div style="margin-bottom:6px"><strong>MRR</strong><br>{{ metricHints.MRR }}</div>
            <div style="margin-bottom:6px"><strong>NDCG@5</strong><br>{{ metricHints.NDCG5 }}</div>
            <div style="margin-bottom:6px"><strong>NDCG@10</strong><br>{{ metricHints.NDCG10 }}</div>
            <div style="margin-bottom:6px"><strong>Recall@5</strong><br>{{ metricHints.Recall5 }}</div>
            <div style="margin-bottom:6px"><strong>Recall@10</strong><br>{{ metricHints.Recall10 }}</div>
            <div style="margin-bottom:6px"><strong>Precision@5</strong><br>{{ metricHints.Precision5 }}</div>
            <div><strong>Precision@10</strong><br>{{ metricHints.Precision10 }}</div>
          </div>
        </div>

      </NSpin>
    </div>
  </div>
</template>

<style scoped>
.eval-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: transparent;
}
.eval-tabs {
  padding: 0 var(--space-lg);
  background: var(--color-surface);
  flex-shrink: 0;
}
.eval-content {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-lg);
  color: var(--color-text);
}
.stat-cards {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
}
.dist-bars { display: flex; flex-direction: column; gap: 6px; }
.dist-row { display: flex; align-items: center; gap: 8px; font-size: 13px; }
.dist-label { width: 100px; flex-shrink: 0; color: var(--color-text-muted); }
.dist-bar { flex: 1; height: 16px; background: var(--color-border); border-radius: 3px; overflow: hidden; }
.dist-fill { height: 100%; background: var(--color-primary); border-radius: 3px; transition: width .3s; }
.dist-count { width: 40px; text-align: right; }
.stat-cards :deep(.n-card__content) { color: var(--color-text); }
.session-table { display: flex; flex-direction: column; gap: 4px; font-size: 13px; }
.session-row { display: flex; gap: 16px; padding: 4px 0; }
.session-row.header { color: var(--color-text-muted); border-bottom: 1px solid var(--color-border); }
.session-row span { flex: 1; }
.session-row.clickable { cursor: pointer; border-radius: 4px; padding: 2px 4px; }
.session-row.clickable:hover { background: var(--color-hover); }
.session-row .time { flex: 0.6; }

.annotate-layout { display: flex; gap: 0; height: calc(100dvh - 180px); }
.query-list-panel { width: 300px; flex-shrink: 0; border-right: 1px solid var(--color-border); display: flex; flex-direction: column; }
.panel-toolbar { padding: 8px; }
.query-items { flex: 1; overflow-y: auto; }
.query-item { padding: 8px 12px; cursor: pointer; border-bottom: 1px solid var(--color-border); }
.query-item:hover { background: var(--color-hover); }
.query-item.active { background: var(--color-primary-light, rgba(24,160,88,.08)); }
.qi-row { display: flex; align-items: flex-start; gap: 6px; }
.query-item.selected { background: var(--color-primary-light, rgba(24,160,88,.12)); }
.qi-check { margin-top: 2px; flex-shrink: 0; cursor: pointer; }
.qi-main { flex: 1; min-width: 0; cursor: pointer; }
.qi-content { font-size: 13px; line-height: 1.4; margin-bottom: 4px; }
.qi-meta { display: flex; justify-content: space-between; align-items: center; font-size: 12px; color: var(--color-text-muted); }
.qi-count { font-size: 11px; }

.query-detail-panel { flex: 1; padding: 12px 16px; overflow-y: auto; }
.detail-header { margin-bottom: 12px; }
.detail-header strong { font-size: 15px; display: block; margin-bottom: 6px; }
.detail-meta { display: flex; gap: 8px; align-items: center; font-size: 12px; color: var(--color-text-muted); }
.annotation-list { display: flex; flex-direction: column; gap: 8px; margin-bottom: 16px; }
.annotation-row { display: flex; align-items: center; gap: 8px; padding: 6px 8px; border: 1px solid var(--color-border); border-radius: 6px; font-size: 13px; }
.ann-content { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ann-content.clickable { cursor: pointer; }
.ann-content.clickable:hover { background: var(--color-hover); border-radius: 3px; }
.ann-content.expanded { white-space: normal; overflow: visible; word-break: break-all; cursor: pointer; }
.ann-score { display: flex; align-items: center; flex-shrink: 0; }
.detail-actions { display: flex; justify-content: flex-end; gap: 8px; align-items: center; margin-top: 16px; padding-top: 12px; border-top: 1px solid var(--color-border); }
.save-msg { font-size: 12px; color: var(--color-primary); margin-right: auto; }

.corpus-table, .records-table { font-size: 13px; }
.cr-row, .rr-row { display: flex; gap: 12px; padding: 6px 8px; border-bottom: 1px solid var(--color-border); align-items: center; }
.cr-row.header, .rr-row.header { color: var(--color-text-muted); font-weight: 500; background: var(--color-bg-alt); }
.cr-row .topic { width: 80px; flex-shrink: 0; color: var(--color-text-muted); font-size: 12px; }
.cr-row .content, .rr-row .content { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rr-row span { min-width: 0; }

.grouped-records { display: flex; flex-direction: column; gap: 4px; }
.gr-group { border: 1px solid var(--color-border); border-radius: 6px; overflow: hidden; }
.gr-query {
  display: flex; align-items: center; gap: 8px; padding: 8px 12px;
  cursor: pointer; font-size: 13px; background: var(--color-bg-alt);
  user-select: none;
}
.gr-query:hover { background: var(--color-hover); }
.gr-toggle { width: 14px; flex-shrink: 0; color: var(--color-text-muted); }
.gr-text { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.save-err { font-size: 11px; color: var(--color-error, #d03050); flex-shrink: 0; }
.gr-items { padding: 0 12px 8px; }
.gr-items .rr-row { padding: 4px 8px; }
.gr-items .rr-row .time { font-size: 11px; color: var(--color-text-muted); }
.rr-score { display: flex; align-items: center; flex-shrink: 0; }

.content.clickable { cursor: pointer; }
.content.clickable:hover { background: var(--color-hover); border-radius: 3px; }
.content.expanded { cursor: pointer; white-space: normal; word-break: break-all; }

.run-table { font-size: 13px; }
.run-row { display: flex; gap: 12px; padding: 6px 8px; border-bottom: 1px solid var(--color-border); align-items: center; }
.run-row.header { color: var(--color-text-muted); font-weight: 500; background: var(--color-bg-alt); }
.run-row span { flex: 1; min-width: 0; }
.run-row .run-actions { display: flex; gap: 4px; flex-shrink: 0; }

.picker-layout { display: flex; gap: 6px; height: 260px; }
.picker-pane { flex: 1; border: 1px solid var(--color-border); border-radius: 6px; display: flex; flex-direction: column; overflow: hidden; }
.picker-pane-header { font-size: 12px; color: var(--color-text-muted); padding: 6px 8px; border-bottom: 1px solid var(--color-border); background: var(--color-bg-alt); flex-shrink: 0; }
.picker-pane-list { flex: 1; overflow-y: auto; padding: 4px; }
.picker-item { font-size: 12px; padding: 4px 6px; cursor: pointer; border-radius: 3px; border-bottom: 1px solid var(--color-border); }
.picker-item:last-child { border-bottom: none; }
.picker-item:hover { background: var(--color-hover); }
.picker-item.disabled { opacity: .4; cursor: not-allowed; }
.picker-arrows { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 4px; color: var(--color-text-muted); flex-shrink: 0; width: 24px; }

.report-query { border: 1px solid var(--color-border); border-radius: 6px; margin-bottom: 6px; overflow: hidden; }
.rq-header { display: flex; align-items: center; gap: 8px; padding: 6px 10px; cursor: pointer; font-size: 13px; background: var(--color-bg-alt); user-select: none; }
.rq-header:hover { background: var(--color-hover); }
.rq-toggle { width: 14px; flex-shrink: 0; color: var(--color-text-muted); }
.rq-text { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rq-text.expanded { white-space: normal; overflow: visible; }
.rq-results { padding: 0 10px 8px; }
.rq-more { font-size: 12px; color: var(--color-text-muted); text-align: center; padding: 4px; }

.help-btn { cursor: pointer; font-size: 13px; color: var(--color-text-muted); user-select: none; }
.help-btn:hover { color: var(--color-primary); }
.help-content { font-size: 12px; line-height: 1.5; }
.help-content strong { display: inline-block; min-width: 80px; }

.help-tooltip {
  position: fixed;
  z-index: 99999;
  max-width: 360px;
  background: var(--color-bg);
  border: 1px solid var(--color-border);
  border-radius: 8px;
  padding: 12px 16px;
  box-shadow: 0 4px 16px rgba(0,0,0,.2);
}
</style>
