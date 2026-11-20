<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NPopconfirm, NSpin, useMessage } from 'naive-ui'
import { api } from '@/api/client'
import PageHeader from '@/components/PageHeader.vue'
import { useVisibleRefresh } from '@/composables/useVisibleRefresh'

// 灵阅列表：单列大卡（手机）、桌面自动 2–3 列。卡片只显示标题/摘要/标签/日期，
// 正文点进去再取（列表接口不带 html）。
const router = useRouter()
const message = useMessage()

const items = ref([])
const total = ref(0)
const loading = ref(false)
const keyword = ref('')
const pageSize = 20

// 标签 chip 多选 = 交集（后端 ?tags=a,b 就是交集口径）
const tags = ref([])
const selectedTags = ref([])
// 面板里的草稿：点「查看结果」才提交，关掉面板不弄脏当前筛选
const draftTags = ref([])
const MAX_TAGS = 10

// 多选删除：上限与后端 maxBatchDelete 一致
const MAX_SELECT = 100
const selecting = ref(false)
const selectedIds = ref([])
const deleting = ref(false)
const selectedCount = computed(() => selectedIds.value.length)

// 请求序号：快速切标签 / 加载更多在飞时切筛选，只让最后一次响应落地
let listSeq = 0

async function fetchList(reset = true, { silent = false } = {}) {
  const my = ++listSeq
  loading.value = true
  try {
    const offset = reset ? 0 : items.value.length
    let url = `/api/artifacts?limit=${pageSize}&offset=${offset}`
    if (keyword.value.trim()) url += `&keyword=${encodeURIComponent(keyword.value.trim())}`
    if (selectedTags.value.length) {
      url += `&tags=${selectedTags.value.map((t) => encodeURIComponent(t)).join(',')}`
    }
    const res = await api.get(url)
    if (my !== listSeq) return false
    const list = res.items || []
    items.value = reset ? list : [...items.value, ...list]
    total.value = res.total || 0
    return true
  } catch (e) {
    if (my !== listSeq) return false
    console.error('Failed to load artifacts', e)
    // 后台自动刷新失败（回前台短时无网）不弹 toast，留日志就够
    if (!silent) message.error('加载灵阅列表失败')
    return false
  } finally {
    if (my === listSeq) loading.value = false
  }
}

// 标签失败不影响列表：只是没有 chip 可用
async function fetchTags() {
  try {
    const res = await api.get('/api/artifacts/tags')
    tags.value = (Array.isArray(res?.tags) ? res.tags : [])
      .filter((t) => t?.name)
      .slice(0, 20)
  } catch (e) {
    console.error('Failed to load artifact tags', e)
  }
}

function openFilter() {
  draftTags.value = [...selectedTags.value]
}

function toggleDraftTag(name) {
  const i = draftTags.value.indexOf(name)
  if (i >= 0) {
    draftTags.value.splice(i, 1)
  } else {
    if (draftTags.value.length >= MAX_TAGS) {
      message.warning(`最多同时筛选 ${MAX_TAGS} 个标签`)
      return
    }
    draftTags.value.push(name)
  }
}

function applyFilters() {
  selectedTags.value = [...draftTags.value]
  fetchList()
}

function removeTag(name) {
  selectedTags.value = selectedTags.value.filter((t) => t !== name)
  fetchList()
}

function resetFilters() {
  keyword.value = ''
  selectedTags.value = []
  fetchList()
}

function clearFilters() {
  draftTags.value = []
}

// 胶囊行上的「清除」只清标签，搜索词有自己的 ×
function resetTagFilter() {
  selectedTags.value = []
  fetchList()
}

// 选择态里改搜索会重拉列表，先把选择退掉，别留下已不在列表里的 id
function runSearch() {
  if (selecting.value) exitSelect()
  fetchList()
}

function enterSelect() {
  selecting.value = true
  selectedIds.value = []
}

function exitSelect() {
  selecting.value = false
  selectedIds.value = []
}

function isSelected(id) {
  return selectedIds.value.includes(id)
}

function toggleSelect(a) {
  if (isSelected(a.id)) {
    selectedIds.value = selectedIds.value.filter((id) => id !== a.id)
    return
  }
  if (selectedIds.value.length >= MAX_SELECT) {
    message.warning(`一次最多选 ${MAX_SELECT} 条`)
    return
  }
  selectedIds.value.push(a.id)
}

function openCard(a) {
  if (selecting.value) toggleSelect(a)
  else router.push(`/artifacts/${a.id}`)
}

async function deleteSelected() {
  if (!selectedIds.value.length || deleting.value) return
  deleting.value = true
  try {
    const res = await api.post('/api/artifacts/batch-delete', { ids: selectedIds.value })
    const removed = Number(res?.deleted) || 0
    const gone = new Set(selectedIds.value)
    items.value = items.value.filter((a) => !gone.has(a.id))
    total.value = Math.max(0, total.value - removed)
    exitSelect()
    fetchTags()
    // 当前页删空但库里还有：补一页，别让「还没有灵阅」盖住事实
    if (!items.value.length && total.value > 0) fetchList()
    message.success(removed ? `已删除 ${removed} 条` : '没有可删除的条目')
  } catch (e) {
    console.error('Failed to delete artifacts', e)
    message.error('删除失败，请重试')
  } finally {
    deleting.value = false
  }
}

function parseTags(tags) {
  return (tags || '').split(',').map(t => t.trim()).filter(Boolean)
}

function formatDate(value) {
  if (!value) return ''
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' })
}

// 页头刷新：失败时 fetchList 自己弹过错误提示，这里不再弹成功
const refreshing = ref(false)

async function manualRefresh() {
  if (refreshing.value) return
  refreshing.value = true
  try {
    const ok = await fetchList()
    fetchTags()
    if (ok) message.success('已刷新')
  } finally {
    refreshing.value = false
  }
}

// PWA 挂起再回前台不会重新 mount，列表会停在旧快照；多选态里不刷，别把勾选洗掉
useVisibleRefresh(() => {
  fetchList(true, { silent: true })
  fetchTags()
}, { enabled: () => !selecting.value })

onMounted(() => {
  fetchList()
  fetchTags()
})
</script>

<template>
  <div class="page">
    <div class="page-scroll">
      <PageHeader
        :title="selecting ? `已选 ${selectedCount}` : '灵阅'"
        :searchable="!selecting"
        :filterable="!selecting && tags.length > 0"
        :filter-count="selecting ? 0 : selectedTags.length"
        :refreshable="!selecting"
        :refreshing="refreshing"
        v-model:keyword="keyword"
        search-placeholder="搜索标题/摘要"
        @search="runSearch"
        @refresh="manualRefresh"
        @open-filter="openFilter"
        @apply="applyFilters"
        @clear="clearFilters"
        @reset="resetTagFilter"
      >
        <template #actions>
          <template v-if="selecting">
            <button class="icon-btn" type="button" aria-label="取消选择" title="取消选择" @click="exitSelect">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>
            </button>
            <NPopconfirm
              :positive-text="`删除 ${selectedCount} 条`"
              negative-text="取消"
              @positive-click="deleteSelected"
            >
              <template #trigger>
                <button class="icon-btn danger" type="button" aria-label="删除所选" title="删除所选" :disabled="!selectedCount || deleting">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h16"/><path d="M9.5 7V5.5A1.5 1.5 0 0 1 11 4h2a1.5 1.5 0 0 1 1.5 1.5V7"/><path d="M6.5 7l.8 12a2 2 0 0 0 2 1.9h5.4a2 2 0 0 0 2-1.9L17.5 7"/></svg>
                </button>
              </template>
              删除 {{ selectedCount }} 条灵阅？不可恢复，已分享的链接会立即失效。
            </NPopconfirm>
          </template>
          <button v-else-if="items.length" class="icon-btn" type="button" aria-label="选择" title="选择" @click="enterSelect">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="3.5"/><path d="M8.5 12.2l2.6 2.6 4.6-5.2"/></svg>
          </button>
        </template>

        <template #pills>
          <span v-for="t in (selecting ? [] : selectedTags)" :key="t" class="filter-pill">
            {{ t }}
            <button type="button" :aria-label="`移除标签 ${t}`" @click="removeTag(t)">×</button>
          </span>
        </template>

        <template #filters>
          <div class="filter-label">按标签筛选<span class="filter-hint">可多选，取交集</span></div>
          <div class="filter-wrap">
            <button
              v-for="t in tags"
              :key="t.name"
              type="button"
              class="filter-chip"
              :class="{ on: draftTags.includes(t.name) }"
              @click="toggleDraftTag(t.name)"
            >
              <span class="tick">✓</span>{{ t.name }}<span class="n">{{ t.count }}</span>
            </button>
          </div>
        </template>
      </PageHeader>

      <div v-if="loading && !items.length" class="hint"><NSpin size="small" /></div>
      <div v-else-if="!items.length" class="empty">
        <template v-if="keyword.trim() || selectedTags.length">
          <div class="empty-ico">✦</div>
          <p>没有匹配的灵阅。</p>
          <button type="button" class="btn" @click="resetFilters">清除筛选</button>
        </template>
        <template v-else>
          <div class="empty-ico">✦</div>
          <p>还没有灵阅。在对话里让模型做一页，就会出现在这里。</p>
          <router-link to="/chat" class="btn">去对话</router-link>
        </template>
      </div>

      <div v-else class="grid">
        <button
          v-for="a in items"
          :key="a.id"
          type="button"
          class="card"
          :class="{ picked: selecting && isSelected(a.id), selecting }"
          :aria-pressed="selecting ? isSelected(a.id) : undefined"
          @click="openCard(a)"
        >
          <span v-if="selecting" class="check" :class="{ on: isSelected(a.id) }">
            <span v-if="isSelected(a.id)">✓</span>
          </span>
          <span class="title">{{ a.title || '未命名页面' }}</span>
          <span v-if="a.summary" class="summary">{{ a.summary }}</span>
          <span class="foot">
            <span class="tags">
              <span v-for="t in parseTags(a.tags)" :key="t" class="tag">{{ t }}</span>
            </span>
            <span class="date">{{ formatDate(a.created_at) }}</span>
          </span>
        </button>
      </div>

      <div v-if="!selecting && items.length < total" class="more">
        <NButton size="small" :loading="loading" @click="fetchList(false)">加载更多</NButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page { height: 100%; display: flex; flex-direction: column; min-height: 0; }

.hint { display: grid; place-items: center; padding: 40px 0; }
.empty { text-align: center; padding: 60px 12px; color: var(--color-text-secondary); }
.empty-ico {
  width: 44px; height: 44px; margin: 0 auto 12px; border-radius: 14px;
  display: grid; place-items: center; background: var(--color-primary-soft);
  color: var(--color-primary); font-size: 20px;
}
.empty p { font-size: 12.5px; line-height: 1.6; margin: 0 0 14px; }
.btn {
  display: inline-block; border: 0; border-radius: 999px; background: var(--color-primary);
  color: #fff; padding: 7px 16px; font-size: 12.5px; text-decoration: none; cursor: pointer;
}
[data-theme="dark"] .btn { color: #10221B; }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 10px; }
.card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 14px;
  text-align: left;
  cursor: pointer;
  border: 0.5px solid var(--color-border);
  border-radius: 12px;
  background: var(--color-surface);
  box-shadow: var(--shadow-sm);
  transition: background var(--transition-fast), transform var(--transition-fast);
}
.card:hover { background: var(--color-primary-soft); transform: translateY(-1px); }
.card.picked { border-color: var(--color-primary); background: var(--color-primary-soft); transform: none; }
.check {
  position: absolute;
  top: 10px;
  right: 10px;
  width: 20px;
  height: 20px;
  border: 1.5px solid var(--color-border);
  border-radius: 50%;
  display: grid;
  place-items: center;
  font-size: 12px;
  line-height: 1;
  color: #fff;
  background: var(--color-surface);
}
.check.on { background: var(--color-primary); border-color: var(--color-primary); }
[data-theme="dark"] .check.on { color: #10221B; }
.card.selecting .title { padding-right: 24px; }
.title { font-size: 15px; font-weight: 600; }
.summary {
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--color-text-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.foot { display: flex; align-items: center; gap: 8px; margin-top: 2px; }
.tags { display: flex; flex-wrap: wrap; gap: 4px; flex: 1; min-width: 0; }
.tag {
  font-size: 10px;
  padding: 2px 7px;
  border-radius: 99px;
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
.date { font-size: 10px; color: var(--color-text-muted); flex: 0 0 auto; }
.more { display: flex; justify-content: center; padding: 18px 0 4px; }
@media (max-width: 1099px) {
  .grid { grid-template-columns: 1fr; }
}
</style>
