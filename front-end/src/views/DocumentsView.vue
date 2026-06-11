<script setup>
import { ref, onMounted } from 'vue'
import { api } from '@/api/client'
import { useMobile } from '@/composables/useMobile'
import { useScrollTopBar } from '@/composables/useScrollTopBar'
import { useDevice } from '@/composables/useDevice'
import {
  NButton,
  NInput,
  NPagination,
  NGrid,
  NGi,
  NEmpty,
  NSelect,
  NModal,
  useMessage
} from 'naive-ui'
import ListItemCard from '@/components/ListItemCard.vue'
import PageTopBar from '@/components/PageTopBar.vue'
import TopBarBtn from '@/components/TopBarBtn.vue'

const message = useMessage()
const { isMobile } = useMobile()
const { isIOS } = useDevice()

const documents = ref([])
const loading = ref(false)
const page = ref(1)
const pageSize = 12
const total = ref(0)
const keyword = ref('')
const selectedTag = ref(null)
const tagOptions = ref([])
const contentContainerRef = ref(null)
const { isBarVisible, showBar } = useScrollTopBar(contentContainerRef)

const showShareModal = ref(false)
const shareUrl = ref('')
const shareExpires = ref('')

async function handleShare(doc) {
  try {
    let shareRes
    try {
      shareRes = await api.get(`/api/documents/${doc.id}/share`)
    } catch (e) {
      shareRes = null
    }

    if (!shareRes) {
      shareRes = await api.post(`/api/documents/${doc.id}/share`, {})
    }

    shareUrl.value = window.location.origin + shareRes.url
    shareExpires.value = new Date(shareRes.expires_at).toLocaleString()
    showShareModal.value = true
  } catch (e) {
    message.error('创建分享链接失败')
  }
}

function copyShareUrl() {
  navigator.clipboard.writeText(shareUrl.value).then(() => {
    message.success('已复制到剪贴板')
  }).catch(() => {
    message.error('复制失败')
  })
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

    const allTags = new Set()
    documents.value.forEach(d => {
      if (d.tags) {
        d.tags.split(',').map(t => t.trim()).filter(Boolean).forEach(t => allTags.add(t))
      }
    })
    tagOptions.value = [...allTags].map(t => ({ label: t, value: t }))
  } catch (e) {
    message.error('加载文档列表失败')
  } finally {
    loading.value = false
  }
}

function sanitizeFilename(title) {
  return (title || 'document')
    .replace(/[/\\:*?"<>|]/g, '_')
    .slice(0, 100)
}

function handleDownload(doc) {
  const filename = sanitizeFilename(doc.title) + '.md'
  const blob = new Blob([doc.content], { type: 'text/markdown' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

async function handleDelete(doc) {
  try {
    await api.delete(`/api/documents/${doc.id}`)
    message.success('删除成功')
    fetchDocuments()
  } catch (e) {
    message.error('删除失败')
  }
}

function handleSearch() {
  page.value = 1
  fetchDocuments()
}

function handleTagFilter(tag) {
  selectedTag.value = tag
  page.value = 1
  fetchDocuments()
}

function parseTags(tags) {
  if (!tags) return []
  return tags.split(',').map(t => t.trim()).filter(Boolean)
}

function getCardActions(doc) {
  return [
    { key: 'share', label: '分享', onClick: () => handleShare(doc) },
    { key: 'download', label: '下载', onClick: () => handleDownload(doc) },
    { key: 'delete', label: '删除', type: 'error', confirm: `确认删除文档「${doc.title}」？`, onClick: () => handleDelete(doc) }
  ]
}

function handleContainerClick(e) {
  if (!isIOS) return
  const container = contentContainerRef.value
  if (!container) return
  const rect = container.getBoundingClientRect()
  const clickY = e.clientY - rect.top
  const topThird = rect.height / 3
  if (clickY <= topThird) {
    showBar()
  }
}

onMounted(fetchDocuments)
</script>

<template>
  <div style="height: 100vh; display: flex; flex-direction: column; background: var(--color-bg-page);">
    <PageTopBar title="文档" :visible="!isMobile || isBarVisible">
      <template #right>
        <div style="display: flex; align-items: center; gap: var(--space-md);">
          <NSelect
            v-model:value="selectedTag"
            :options="tagOptions"
            placeholder="标签筛选"
            clearable
            :style="{ width: isMobile ? '120px' : '160px' }"
            size="small"
            @update:value="handleTagFilter"
          />
          <NInput
            v-model:value="keyword"
            placeholder="搜索..."
            clearable
            :style="{ width: isMobile ? '120px' : '200px' }"
            size="small"
            @keyup.enter="handleSearch"
            @clear="handleSearch"
          />
          <TopBarBtn v-if="!isMobile" label="搜索" @click="handleSearch" />
        </div>
      </template>
    </PageTopBar>
    <div
      ref="contentContainerRef"
      @touchstart="isMobile ? showBar() : null"
      @click="handleContainerClick"
      style="flex: 1; overflow-y: auto; padding: var(--space-lg);"
    >
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
      <NEmpty v-else-if="!loading" description="还没有文档" />
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
        有效期至：{{ shareExpires }}
      </div>
    </NModal>
  </div>
</template>
