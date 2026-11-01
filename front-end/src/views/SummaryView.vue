<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { useMobile } from '@/composables/useMobile'
import { useScrollTopBar } from '@/composables/useScrollTopBar'
import { useDevice } from '@/composables/useDevice'
import {
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

const message = useMessage()
const router = useRouter()
const { isMobile } = useMobile()
const { isIOS } = useDevice()

const summaries = ref([])
const loading = ref(false)
const page = ref(1)
const pageSize = 12
const total = ref(0)
const keyword = ref('')
const contentContainerRef = ref(null)
const { isBarVisible, showBar } = useScrollTopBar(contentContainerRef)

async function fetchSummaries() {
  loading.value = true
  try {
    const offset = (page.value - 1) * pageSize
    let url = `/api/summaries?limit=${pageSize}&offset=${offset}`
    if (keyword.value) url += `&keyword=${encodeURIComponent(keyword.value)}`
    const res = await api.get(url)
    summaries.value = res.summaries || []
    total.value = res.total || 0
  } catch (e) {
    message.error('加载摘要列表失败')
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  page.value = 1
  fetchSummaries()
}

function parseTopics(topics) {
  if (!topics) return []
  return topics.split(',').map(t => t.trim()).filter(Boolean)
}

function buildTags(s) {
  const tags = parseTopics(s.topics).map(t => ({ text: t }))
  if (s.status !== 'active') {
    tags.unshift({ text: '已替代', type: 'warning' })
  }
  return tags
}

function viewMessages(s) {
  router.push(`/summaries/${s.id}/messages`)
}

function getCardActions(s) {
  return [
    { key: 'view', label: '查看消息', onClick: () => viewMessages(s) }
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

onMounted(fetchSummaries)
</script>

<template>
  <div style="height: 100%; display: flex; flex-direction: column; background: transparent;">
    <PageTopBar title="摘要管理" :visible="!isMobile || isBarVisible">
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
      <NGrid v-if="summaries.length > 0" cols="1 s:2 m:3" :x-gap="16" :y-gap="16">
        <NGi v-for="s in summaries" :key="s.id">
          <ListItemCard
            :title="s.conversation_id ? `会话 ${s.conversation_id.slice(0, 8)}...` : '未知会话'"
            :content="s.summary_text"
            :tags="buildTags(s)"
            :metadata="new Date(s.created_at).toLocaleString()"
            :actions="getCardActions(s)"
            max-content-height="200px"
          />
        </NGi>
      </NGrid>
      <NEmpty v-else-if="!loading" description="还没有摘要" />
      <div v-if="total > pageSize" style="margin-top: var(--space-lg); display: flex; justify-content: center;">
        <NPagination
          v-model:page="page"
          :page-count="Math.ceil(total / pageSize)"
          @update:page="fetchSummaries"
        />
      </div>
    </div>
  </div>
</template>
