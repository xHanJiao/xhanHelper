<script setup>
import { ref, onMounted } from 'vue'
import { api } from '@/api/client'
import { useMobile } from '@/composables/useMobile'
import {
  NModal,
  NSpin,
  NTag,
  NEmpty,
  NGrid,
  NGi,
  NPagination,
  useMessage
} from 'naive-ui'
import ListItemCard from '@/components/ListItemCard.vue'
import PageHeader from '@/components/PageHeader.vue'

const message = useMessage()
const { isMobile } = useMobile()

const items = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 12
const loading = ref(false)

const detailVisible = ref(false)
const detail = ref(null)
const detailLoading = ref(false)
let detailSeq = 0

// 请求序号：快速翻页时，慢的旧响应不许覆盖新结果
let listSeq = 0

async function fetchEvents() {
  const my = ++listSeq
  loading.value = true
  try {
    const offset = (page.value - 1) * pageSize
    const res = await api.get(`/api/event-threads?limit=${pageSize}&offset=${offset}`)
    if (my !== listSeq) return
    items.value = res.items || []
    total.value = res.total || 0
  } catch (e) {
    if (my !== listSeq) return
    message.error('加载事件列表失败')
  } finally {
    if (my === listSeq) loading.value = false
  }
}

async function openDetail(item) {
  const seq = ++detailSeq
  detailVisible.value = true
  detail.value = null
  detailLoading.value = true
  try {
    const res = await api.get(`/api/event-threads/${item.id}`)
    if (seq !== detailSeq) return
    detail.value = res
  } catch (e) {
    if (seq !== detailSeq) return
    message.error('加载事件详情失败')
    detailVisible.value = false
  } finally {
    if (seq === detailSeq) detailLoading.value = false
  }
}

function formatDate(ts) {
  if (!ts) return ''
  return new Date(ts).toLocaleDateString('zh-CN')
}

function formatDateTime(ts) {
  if (!ts) return ''
  return new Date(ts).toLocaleString('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit'
  })
}

function buildTags(item) {
  return [{ text: `${item.memory_count} 条记忆`, type: 'default' }]
}

function buildMetadata(item) {
  return item.last_memory_at ? `最近记忆 ${formatDate(item.last_memory_at)}` : '暂无记忆'
}

function buildActions(item) {
  return [
    { key: 'detail', label: '查看记忆', type: 'primary', onClick: () => openDetail(item) }
  ]
}

onMounted(fetchEvents)
</script>

<template>
  <div style="height: 100%; display: flex; flex-direction: column; background: transparent;">
    <div class="page-scroll">
      <PageHeader title="事件" />

      <NSpin v-if="loading && items.length === 0" />
      <NEmpty
        v-else-if="items.length === 0"
        description="还没有事件，助手会在对话结束后把相关记忆聚成事件"
        style="margin-top: 80px;"
      />
      <NGrid v-else :cols="isMobile ? 1 : 3" :x-gap="16" :y-gap="16">
        <NGi v-for="item in items" :key="item.id">
          <ListItemCard
            :title="item.name"
            :content="item.summary || '暂无摘要'"
            :tags="buildTags(item)"
            :metadata="buildMetadata(item)"
            :primary-actions="buildActions(item)"
            max-content-height="150px"
          />
        </NGi>
      </NGrid>

      <div v-if="total > pageSize" style="margin-top: var(--space-lg); display: flex; justify-content: center;">
        <NPagination
          v-model:page="page"
          :page-count="Math.ceil(total / pageSize)"
          @update:page="fetchEvents"
        />
      </div>
    </div>

    <NModal
      v-model:show="detailVisible"
      preset="card"
      :title="detail?.name || '事件详情'"
      style="width: 720px; max-width: 92vw;"
    >
      <NSpin v-if="detailLoading" />
      <div v-else-if="detail" class="event-detail">
        <div v-if="detail.summary" class="event-summary">{{ detail.summary }}</div>
        <div class="event-meta">
          共 {{ detail.memory_count }} 条记忆 · 更新于 {{ formatDate(detail.updated_at) }}
        </div>

        <NEmpty v-if="!detail.memories || detail.memories.length === 0" description="该事件下还没有记忆" />
        <div v-else class="event-memories">
          <div v-for="m in detail.memories" :key="m.id" class="event-memory">
            <div class="event-memory-head">
              <NTag size="small" :bordered="false">{{ m.topic || '记忆' }}</NTag>
              <span class="event-memory-date">{{ formatDateTime(m.updated_at) }}</span>
            </div>
            <div class="event-memory-content">{{ m.content }}</div>
            <ul v-if="m.facts && m.facts.length" class="event-memory-facts">
              <li v-for="(fact, i) in m.facts" :key="i">「{{ fact }}」</li>
            </ul>
          </div>
        </div>
      </div>
    </NModal>
  </div>
</template>

<style scoped>
.event-detail {
  max-height: 68vh;
  overflow-y: auto;
  padding-right: 4px;
}

.event-summary {
  font-size: 14px;
  line-height: 1.7;
  color: var(--color-text);
  white-space: pre-wrap;
}

.event-meta {
  margin: var(--space-sm) 0 var(--space-md);
  font-size: 12px;
  color: var(--color-text-secondary);
}

.event-memories {
  display: flex;
  flex-direction: column;
  gap: var(--space-sm);
}

.event-memory {
  padding: var(--space-sm) var(--space-md);
  border-radius: var(--radius-md);
  background: var(--color-surface);
}

.event-memory-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-sm);
  margin-bottom: 6px;
}

.event-memory-date {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.event-memory-content {
  font-size: 14px;
  line-height: 1.7;
  color: var(--color-text-secondary);
  white-space: pre-wrap;
}

.event-memory-facts {
  margin: 6px 0 0;
  padding-left: 18px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--color-text-muted);
}
</style>
