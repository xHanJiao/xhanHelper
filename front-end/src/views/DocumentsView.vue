<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import {
  NButton,
  NInput,
  NPagination,
  NGrid,
  NGi,
  NEmpty,
  NModal,
  useMessage
} from 'naive-ui'
import ListItemCard from '@/components/ListItemCard.vue'
import PageHeader from '@/components/PageHeader.vue'
import { useExportPDF } from '@/composables/useExportPDF'
import { useVisibleRefresh } from '@/composables/useVisibleRefresh'
import { ensureDocumentShareUrl, writeClipboard, downloadMarkdown } from '@/utils/document'

const message = useMessage()
const router = useRouter()
const { exportPDF } = useExportPDF()

const documents = ref([])
const loading = ref(false)
const page = ref(1)
const pageSize = 12
const total = ref(0)
const keyword = ref('')
const selectedTag = ref(null)
const docTags = ref([])
const draftTag = ref(null)

const showShareModal = ref(false)
const shareUrl = ref('')

async function handleShare(doc) {
  try {
    shareUrl.value = await ensureDocumentShareUrl(doc.id)
    showShareModal.value = true
  } catch (e) {
    console.error('Failed to share document', e)
    message.error('创建分享链接失败')
  }
}

async function copyShareUrl() {
  try {
    await writeClipboard(shareUrl.value)
    message.success('已复制到剪贴板')
  } catch (e) {
    console.error('Failed to copy share url', e)
    message.error('复制失败，请手动选择链接')
  }
}

// 请求序号：快速改搜索词/翻页时，慢的旧响应不许覆盖新结果
let listSeq = 0

async function fetchDocuments({ silent = false } = {}) {
  const my = ++listSeq
  loading.value = true
  try {
    const offset = (page.value - 1) * pageSize
    let url = `/api/documents?limit=${pageSize}&offset=${offset}`
    if (keyword.value) url += `&keyword=${encodeURIComponent(keyword.value)}`
    if (selectedTag.value) url += `&tag=${encodeURIComponent(selectedTag.value)}`
    const res = await api.get(url)
    if (my !== listSeq) return false
    documents.value = res.documents || []
    total.value = res.total || 0
    return true
  } catch (e) {
    if (my !== listSeq) return false
    console.error('Failed to load documents', e)
    // 后台自动刷新失败（回前台短时无网）不弹 toast，留日志就够
    if (!silent) message.error('加载文档列表失败')
    return false
  } finally {
    if (my === listSeq) loading.value = false
  }
}

function handleDownload(doc) {
  downloadMarkdown(doc.content, doc.title)
}

async function handleDelete(doc) {
  try {
    await api.delete(`/api/documents/${doc.id}`)
    message.success('删除成功')
    fetchDocuments()
    fetchDocTags()
  } catch (e) {
    message.error('删除失败')
  }
}

function handleSearch() {
  page.value = 1
  fetchDocuments()
}

// 标签从接口拿全量（当前页文档里凑出来的标签不全）
async function fetchDocTags() {
  try {
    const res = await api.get('/api/documents/tags')
    docTags.value = (Array.isArray(res?.tags) ? res.tags : []).filter((t) => t?.name).slice(0, 20)
  } catch (e) {
    console.error('Failed to load document tags', e)
  }
}

function openFilter() {
  draftTag.value = selectedTag.value
}

function applyTag() {
  selectedTag.value = draftTag.value
  page.value = 1
  fetchDocuments()
}

function removeTag() {
  draftTag.value = null
  selectedTag.value = null
  page.value = 1
  fetchDocuments()
}

function clearDraftTag() {
  draftTag.value = null
}

function resetFilters() {
  keyword.value = ''
  removeTag()
}

function parseTags(tags) {
  if (!tags) return []
  return tags.split(',').map(t => t.trim()).filter(Boolean)
}

function getCardActions(doc) {
  return [
    { key: 'open', label: '阅读', onClick: () => router.push(`/documents/${doc.id}`) },
    { key: 'export-pdf', label: '导出PDF', onClick: () => exportPDF(doc.content, doc.title, 'markdown') },
    { key: 'share', label: '分享', onClick: () => handleShare(doc) },
    { key: 'download', label: '下载', onClick: () => handleDownload(doc) },
    { key: 'delete', label: '删除', type: 'error', confirm: `确认删除文档「${doc.title}」？`, onClick: () => handleDelete(doc) }
  ]
}

// 刷新不动 page：翻到第 3 页回前台，还是第 3 页
const refreshing = ref(false)

async function manualRefresh() {
  if (refreshing.value) return
  refreshing.value = true
  try {
    const ok = await fetchDocuments()
    fetchDocTags()
    if (ok) message.success('已刷新')
  } finally {
    refreshing.value = false
  }
}

// PWA 挂起再回前台不会重新 mount，列表会停在旧快照
useVisibleRefresh(() => {
  fetchDocuments({ silent: true })
  fetchDocTags()
})

onMounted(() => {
  fetchDocuments()
  fetchDocTags()
})
</script>

<template>
  <div style="height: 100%; display: flex; flex-direction: column; background: transparent;">
    <div class="page-scroll">
      <PageHeader
        title="文档"
        searchable
        :filterable="docTags.length > 0"
        :filter-count="selectedTag ? 1 : 0"
        refreshable
        :refreshing="refreshing"
        v-model:keyword="keyword"
        search-placeholder="搜索文档"
        @search="handleSearch"
        @refresh="manualRefresh"
        @open-filter="openFilter"
        @apply="applyTag"
        @clear="clearDraftTag"
        @reset="removeTag"
      >
        <template #pills>
          <span v-if="selectedTag" class="filter-pill">
            {{ selectedTag }}
            <button type="button" aria-label="移除标签筛选" @click="removeTag">×</button>
          </span>
        </template>
        <template #filters>
          <div class="filter-label">按标签筛选<span class="filter-hint">单选</span></div>
          <div class="filter-wrap">
            <button
              v-for="t in docTags"
              :key="t.name"
              type="button"
              class="filter-chip"
              :class="{ on: draftTag === t.name }"
              @click="draftTag = draftTag === t.name ? null : t.name"
            >
              <span class="tick">✓</span>{{ t.name }}<span class="n">{{ t.count }}</span>
            </button>
          </div>
        </template>
      </PageHeader>

      <!-- 文档卡带正文预览，多列太挤，恒单列 -->
      <NGrid v-if="documents.length > 0" :cols="1" :x-gap="16" :y-gap="16">
        <NGi v-for="doc in documents" :key="doc.id">
          <ListItemCard
            :title="doc.title"
            :content="doc.content"
            :tags="parseTags(doc.tags)"
            :metadata="'更新于 ' + new Date(doc.updated_at).toLocaleString()"
            :actions="getCardActions(doc)"
            content-type="markdown"
            max-content-height="200px"
          />
        </NGi>
      </NGrid>
      <div v-else-if="!loading" class="empty">
        <NEmpty :description="(selectedTag || keyword) ? '没有匹配的文档' : '还没有文档'" />
        <button v-if="selectedTag || keyword" type="button" class="page-action" @click="resetFilters">清除筛选</button>
      </div>
      <div v-if="total > pageSize" style="margin-top: var(--space-lg); display: flex; justify-content: center;">
        <NPagination
          v-model:page="page"
          :page-count="Math.ceil(total / pageSize)"
          @update:page="fetchDocuments()"
        />
      </div>
    </div>
    <NModal v-model:show="showShareModal" preset="card" title="分享文档" style="width: 480px;">
      <div style="margin-bottom: var(--space-md);">
        <label style="display: block; font-size: 0.875rem; color: var(--color-text-secondary); margin-bottom: var(--space-xs);">分享链接</label>
        <div style="display: flex; gap: var(--space-xs);">
          <NInput :value="shareUrl" readonly style="flex: 1;" />
          <NButton @click="copyShareUrl" type="primary" size="small">复制</NButton>
        </div>
      </div>
      <div style="color: var(--color-text-tertiary); font-size: 0.8rem;">
        链接长期有效
      </div>
    </NModal>
  </div>
</template>

<style scoped>
.empty { display: grid; justify-items: center; gap: 6px; padding: 12px 0; }
</style>
