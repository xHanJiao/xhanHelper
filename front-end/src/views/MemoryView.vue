<script setup>
import { ref, onMounted } from 'vue'
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
import PageHeader from '@/components/PageHeader.vue'

const message = useMessage()
const { isMobile } = useMobile()

const memories = ref([])
const loading = ref(false)
const page = ref(1)
const pageSize = 12
const total = ref(0)
const keyword = ref('')


// 请求序号：快速改搜索词/翻页时，慢的旧响应不许覆盖新结果
let listSeq = 0

async function fetchMemories() {
  const my = ++listSeq
  loading.value = true
  try {
    const offset = (page.value - 1) * pageSize
    let url = `/api/memories?limit=${pageSize}&offset=${offset}`
    if (keyword.value) url += `&keyword=${encodeURIComponent(keyword.value)}`
    const res = await api.get(url)
    if (my !== listSeq) return
    memories.value = res.memories || []
    total.value = res.total || 0
  } catch (e) {
    if (my !== listSeq) return
    message.error('加载记忆列表失败')
  } finally {
    if (my === listSeq) loading.value = false
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
  if (!mem.tags) return []
  return mem.tags.split(',').map(t => t.trim()).filter(Boolean).map(t => ({ text: t }))
}

function getCardActions(mem) {
  return [
    { key: 'delete', label: '删除', type: 'error', confirm: '确认删除此记忆？', onClick: () => handleDelete(mem) }
  ]
}

onMounted(() => {
  fetchMemories()
})
</script>

<template>
  <div style="height: 100%; display: flex; flex-direction: column; background: transparent;">
    <div class="page-scroll">
      <PageHeader
        title="记忆"
        searchable
        v-model:keyword="keyword"
        search-placeholder="搜索记忆"
        @search="handleSearch"
      />
      <!-- 提取状态 -->
      <NGrid v-if="memories.length > 0" :cols="isMobile ? 1 : 3" :x-gap="16" :y-gap="16">
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

<style scoped>
</style>
