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
  useMessage
} from 'naive-ui'
import ListItemCard from '@/components/ListItemCard.vue'
import PageTopBar from '@/components/PageTopBar.vue'
import TopBarBtn from '@/components/TopBarBtn.vue'
import { useExportPDF } from '@/composables/useExportPDF'

const message = useMessage()
const { isMobile } = useMobile()
const { isIOS } = useDevice()
const { exportPDF } = useExportPDF()

const notes = ref([])
const loading = ref(false)
const page = ref(1)
const pageSize = 12
const total = ref(0)
const keyword = ref('')
const contentContainerRef = ref(null)
const { isBarVisible, showBar } = useScrollTopBar(contentContainerRef)

async function fetchNotes() {
  loading.value = true
  try {
    const offset = (page.value - 1) * pageSize
    let url = `/api/notes?limit=${pageSize}&offset=${offset}`
    if (keyword.value) url += `&keyword=${encodeURIComponent(keyword.value)}`
    const res = await api.get(url)
    notes.value = res.notes || []
    total.value = res.total || 0
  } catch (e) {
    message.error('加载笔记列表失败')
  } finally {
    loading.value = false
  }
}

async function handleDelete(note) {
  try {
    await api.delete(`/api/notes/${note.id}`)
    message.success('删除成功')
    fetchNotes()
  } catch (e) {
    message.error('删除失败')
  }
}

function handleSearch() {
  page.value = 1
  fetchNotes()
}

function parseTags(tags) {
  if (!tags) return []
  return tags.split(',').map(t => t.trim()).filter(Boolean)
}

function getCardActions(note) {
  return [
    { key: 'export-pdf', label: '导出PDF', onClick: () => exportPDF(note.content, note.topic || '无标题', 'text') },
    { key: 'delete', label: '删除', type: 'error', confirm: `确认删除笔记「${note.topic || '无标题'}」？`, onClick: () => handleDelete(note) }
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

onMounted(fetchNotes)
</script>

<template>
  <div style="height: 100%; display: flex; flex-direction: column; background: transparent;">
    <PageTopBar title="笔记管理" :visible="!isMobile || isBarVisible">
      <template #right>
        <div style="display: flex; align-items: center; gap: var(--space-md);">
          <NInput
            v-model:value="keyword"
            placeholder="搜索..."
            clearable
            :style="{ width: isMobile ? '140px' : '240px' }"
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
      <NGrid v-if="notes.length > 0" cols="1 s:2 m:3" :x-gap="16" :y-gap="16">
        <NGi v-for="note in notes" :key="note.id">
          <ListItemCard
            :title="note.topic || '无标题'"
            :content="note.content"
            :tags="parseTags(note.tags)"
            :metadata="new Date(note.created_at).toLocaleString()"
            :actions="getCardActions(note)"
            max-content-height="160px"
          />
        </NGi>
      </NGrid>
      <NEmpty v-else-if="!loading" description="还没有笔记" />
      <div v-if="total > pageSize" style="margin-top: var(--space-lg); display: flex; justify-content: center;">
        <NPagination
          v-model:page="page"
          :page-count="Math.ceil(total / pageSize)"
          @update:page="fetchNotes"
        />
      </div>
    </div>
  </div>
</template>
