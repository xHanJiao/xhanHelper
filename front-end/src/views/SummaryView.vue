<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { useMobile } from '@/composables/useMobile'
import {
  NPagination,
  NGrid,
  NGi,
  NEmpty,
  useMessage
} from 'naive-ui'
import ListItemCard from '@/components/ListItemCard.vue'
import PageTopBar from '@/components/PageTopBar.vue'
import PageHeader from '@/components/PageHeader.vue'

const message = useMessage()
const router = useRouter()
const { isMobile } = useMobile()

const summaries = ref([])
const loading = ref(false)
const page = ref(1)
const pageSize = 12
const total = ref(0)
const keyword = ref('')

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

onMounted(fetchSummaries)
</script>

<template>
  <div style="height: 100%; display: flex; flex-direction: column; background: transparent;">
    <PageTopBar v-if="!isMobile" />
    <div class="page-scroll">
      <PageHeader
        title="摘要"
        searchable
        v-model:keyword="keyword"
        search-placeholder="搜索摘要"
        @search="handleSearch"
      />
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
