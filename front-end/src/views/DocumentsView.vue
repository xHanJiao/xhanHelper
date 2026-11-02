<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { useMobile } from '@/composables/useMobile'
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
import PageTopBar from '@/components/PageTopBar.vue'
import PageHeader from '@/components/PageHeader.vue'
import { useExportPDF } from '@/composables/useExportPDF'
import { ensureDocumentShareUrl, writeClipboard, downloadMarkdown } from '@/utils/document'

const message = useMessage()
const router = useRouter()
const { isMobile } = useMobile()
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

async function fetchDocuments() {
  loading.value = true
  try {
    const offset = (page.value - 1) * pageSize
    let url = `/api/documents?limit=${pageSize}&offset=${offset}`
    if (keyword.value) url += `&keyword=${encodeURIComponent(keyword.value)}`
    if (selectedTag.value) url += `&tag=${encodeURIComponent(selectedTag.value)}`
    const res = await api.get(url)
    documents.value = res.documents || []
    total.value = res.total || 0
  } catch (e) {
    message.error('加载文档列表失败')
  } finally {
    loading.value = false
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

onMounted(() => {
  fetchDocuments()
  fetchDocTags()
})
</script>

<template>
  <div style="height: 100%; display: flex; flex-direction: column; background: transparent;">
    <PageTopBar v-if="!isMobile" />
    <div class="page-scroll doc-scroll">
      <PageHeader
        title="文档"
        searchable
        :filterable="docTags.length > 0"
        :filter-count="selectedTag ? 1 : 0"
        v-model:keyword="keyword"
        search-placeholder="搜索文档"
        @search="handleSearch"
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

      <NGrid v-if="documents.length > 0" cols="1 s:2 m:3" :x-gap="16" :y-gap="16">
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
          @update:page="fetchDocuments"
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
.doc-scroll { width: 100%; }
.empty { display: grid; justify-items: center; gap: 6px; padding: 12px 0; }
</style>
