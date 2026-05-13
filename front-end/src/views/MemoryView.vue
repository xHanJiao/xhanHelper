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
  NTag,
  useMessage
} from 'naive-ui'
import ListItemCard from '@/components/ListItemCard.vue'
import PageTopBar from '@/components/PageTopBar.vue'
import TopBarBtn from '@/components/TopBarBtn.vue'

const message = useMessage()
const { isMobile } = useMobile()
const { isIOS } = useDevice()

const memories = ref([])
const loading = ref(false)
const page = ref(1)
const pageSize = 12
const total = ref(0)
const keyword = ref('')
const contentContainerRef = ref(null)
const { isBarVisible, showBar } = useScrollTopBar(contentContainerRef)

const sourceMap = { conversation: '对话提取', csv_import: 'CSV导入' }

async function fetchMemories() {
  loading.value = true
  try {
    const offset = (page.value - 1) * pageSize
    let url = `/api/memories?limit=${pageSize}&offset=${offset}`
    if (keyword.value) url += `&keyword=${encodeURIComponent(keyword.value)}`
    const res = await api.get(url)
    memories.value = res.memories || []
    total.value = res.total || 0
  } catch (e) {
    message.error('加载记忆列表失败')
  } finally {
    loading.value = false
  }
}

async function handleDelete(mem) {
  try {
    await api.delete(`/api/memories/${mem.id}`)
    message.success('删除成功')
    fetchMemories()
  } catch (e) {
    message.error('删除失败')
  }
}

function handleSearch() {
  page.value = 1
  fetchMemories()
}

function getCardTags(mem) {
  return [sourceMap[mem.source] || mem.source]
}

function getCardActions(mem) {
  return [
    { key: 'delete', label: '删除', type: 'error', confirm: '确认删除此记忆？', onClick: () => handleDelete(mem) }
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

onMounted(fetchMemories)
</script>

<template>
  <div style="height: 100vh; display: flex; flex-direction: column; background: var(--color-bg-page);">
    <PageTopBar title="记忆管理" :visible="!isMobile || isBarVisible">
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
      <NGrid v-if="memories.length > 0" cols="1 s:2 m:3" :x-gap="16" :y-gap="16">
        <NGi v-for="mem in memories" :key="mem.id">
          <ListItemCard
            :title="mem.memory_key || '记忆'"
            :content="mem.memory_value"
            :tags="getCardTags(mem)"
            :metadata="new Date(mem.created_at).toLocaleString()"
            :actions="getCardActions(mem)"
            max-content-height="160px"
          />
        </NGi>
      </NGrid>
      <NEmpty v-else-if="!loading" description="还没有记忆" />
      <div v-if="total > pageSize" style="margin-top: var(--space-lg); display: flex; justify-content: center;">
        <NPagination
          v-model:page="page"
          :page-count="Math.ceil(total / pageSize)"
          @update:page="fetchMemories"
        />
      </div>
    </div>
  </div>
</template>
