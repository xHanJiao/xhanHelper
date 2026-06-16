<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { api } from '@/api/client'
import { NCard, NTag, NSpin, NSelect, NTabs, NTabPane, NInput, NButton, NEmpty, NAlert, NRadioGroup, NRadioButton, NPagination, NPopover, NText } from 'naive-ui'
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

watch(currentProject, () => { loadStats(); loadQueries(); loadCorpus(); loadRecords() })
watch(activeTab, (t) => {
  if (t === 'overview') loadStats()
  if (t === 'annotate') loadQueries()
  if (t === 'corpus') loadCorpus()
  if (t === 'records') loadRecords()
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
            <NCard size="small"><template #header>标注条目</template>{{ stats.relevance_distribution ? Object.values(stats.relevance_distribution).reduce((a, b) => a + b, 0) : 0 }}</NCard>
          </div>
          <NCard title="相关性分布" size="small" style="margin-top: 12px">
            <div v-if="stats.relevance_distribution" class="dist-bars">
              <div v-for="label in 5" :key="label" class="dist-row">
                <span class="dist-label">{{ label - 1 }} {{ relevanceLabels[label - 1] }}</span>
                <div class="dist-bar">
                  <div class="dist-fill" :style="{ width: (stats.relevance_distribution[label - 1] || 0) / Math.max(...Object.values(stats.relevance_distribution), 1) * 100 + '%' }" />
                </div>
                <span class="dist-count">{{ stats.relevance_distribution[label - 1] || 0 }}</span>
              </div>
            </div>
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
                    <div class="ann-content" :title="a.corpus_content">{{ a.corpus_content?.slice(0, 80) }}</div>
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
              <span class="content" :title="c.content">{{ c.content?.slice(0, 80) }}</span>
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

      </NSpin>
    </div>
  </div>
</template>

<style scoped>
.eval-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
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
}
.stat-cards {
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  gap: 12px;
}
.dist-bars { display: flex; flex-direction: column; gap: 6px; }
.dist-row { display: flex; align-items: center; gap: 8px; font-size: 13px; }
.dist-label { width: 100px; flex-shrink: 0; color: var(--color-text-muted); }
.dist-bar { flex: 1; height: 16px; background: var(--color-border); border-radius: 3px; overflow: hidden; }
.dist-fill { height: 100%; background: var(--color-primary); border-radius: 3px; transition: width .3s; }
.dist-count { width: 40px; text-align: right; }
.session-table { display: flex; flex-direction: column; gap: 4px; font-size: 13px; }
.session-row { display: flex; gap: 16px; padding: 4px 0; }
.session-row.header { color: var(--color-text-muted); border-bottom: 1px solid var(--color-border); }
.session-row span { flex: 1; }
.session-row.clickable { cursor: pointer; border-radius: 4px; padding: 2px 4px; }
.session-row.clickable:hover { background: var(--color-hover); }
.session-row .time { flex: 0.6; }

.annotate-layout { display: flex; gap: 0; height: calc(100vh - 180px); }
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
</style>
