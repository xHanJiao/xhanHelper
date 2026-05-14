<script setup>
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
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
const route = useRoute()

const memories = ref([])
const loading = ref(false)
const page = ref(1)
const pageSize = 12
const total = ref(0)
const keyword = ref('')
const contentContainerRef = ref(null)
const { isBarVisible, showBar } = useScrollTopBar(contentContainerRef)

const extracting = ref(false)
const extractStatus = ref('')
const extractProgress = ref('')
const extractCount = ref(0)

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

async function startExtract(convId) {
  extracting.value = true
  extractStatus.value = '正在加载消息...'
  extractCount.value = 0

  try {
    const token = localStorage.getItem('xhanhelper_auth')
    let authHeader = {}
    if (token) {
      try {
        authHeader = { 'Authorization': 'Bearer ' + JSON.parse(token).token }
      } catch {}
    }
    const res = await fetch(`/api/conversations/${convId}/extract-memories/stream`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...authHeader }
    })

    if (!res.ok) {
      extracting.value = false
      extractStatus.value = '提取失败'
      return
    }

    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''

    while (true) {
      const { done, value } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop()

      for (const line of lines) {
        if (!line.startsWith('event: ') && !line.startsWith('data: ')) continue

        if (line.startsWith('event: ')) {
          // store event type for next data line
          continue
        }

        if (line.startsWith('data: ')) {
          const raw = line.slice(6)
          try {
            const evt = JSON.parse(raw)
            handleExtractEvent(evt)
          } catch {
            // skip malformed
          }
        }
      }
    }
  } catch (e) {
    extracting.value = false
    extractStatus.value = '提取失败: ' + e.message
  }
}

function handleExtractEvent(evt) {
  switch (evt.Type) {
    case 'status':
      extractStatus.value = evt.Message || evt.Phase
      break
    case 'memory':
      if (evt.Memory) {
        memories.value = [evt.Memory, ...memories.value]
        total.value++
        extractCount.value++
      }
      break
    case 'duplicate':
      // could show a toast but keeping it minimal
      break
    case 'done':
      extracting.value = false
      extractStatus.value = ''
      extractProgress.value = evt.Created > 0
        ? `提取完成，新增 ${evt.Created} 条记忆`
        : ''
      if (evt.Duplicates > 0) {
        extractProgress.value += `，跳过 ${evt.Duplicates} 条重复`
      }
      break
    case 'error':
      extracting.value = false
      extractStatus.value = '提取失败: ' + (evt.Message || '')
      break
  }
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

onMounted(() => {
  const convId = route.query.conv_id
  const shouldExtract = route.query.extracting === 'true'
  if (shouldExtract && convId) {
    startExtract(convId)
  }
  fetchMemories()
})
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
      <!-- 提取状态 -->
      <div v-if="extracting || extractProgress" :style="{ marginBottom: 'var(--space-md)', padding: 'var(--space-md)', background: 'var(--color-surface)', borderRadius: 'var(--radius-lg)', border: '1px solid var(--color-border)' }">
        <div v-if="extracting" style="display: flex; align-items: center; gap: 8px; color: var(--color-text-muted);">
          <div style="width: 16px; height: 16px; border: 2px solid var(--color-border); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 0.8s linear infinite;"></div>
          <span>{{ extractStatus }}</span>
        </div>
        <div v-if="extractProgress && !extracting" style="color: var(--color-success); font-size: 14px;">
          {{ extractProgress }}
        </div>
      </div>

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
      <NEmpty v-else-if="!loading && !extracting" description="还没有记忆" />
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
@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
