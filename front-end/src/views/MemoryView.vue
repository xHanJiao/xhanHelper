<script setup>
import { ref, onMounted } from 'vue'
import { api } from '@/api/client'
import {
  NCard,
  NButton,
  NInput,
  NPagination,
  NGrid,
  NGi,
  NEmpty,
  NTag,
  useMessage
} from 'naive-ui'

const message = useMessage()

const memories = ref([])
const loading = ref(false)
const page = ref(1)
const pageSize = 12
const keyword = ref('')

const sourceMap = { conversation: '对话提取', csv_import: 'CSV导入' }

async function fetchMemories() {
  loading.value = true
  try {
    const offset = (page.value - 1) * pageSize
    let url = `/api/memories?limit=${pageSize}&offset=${offset}`
    if (keyword.value) url += `&keyword=${encodeURIComponent(keyword.value)}`
    const res = await api.get(url)
    memories.value = Array.isArray(res) ? res : (res.data || res.memories || [])
  } catch (e) {
    message.error('加载记忆列表失败')
  } finally {
    loading.value = false
  }
}

async function handleDelete(mem) {
  if (!window.confirm('确认删除此记忆？')) return
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

onMounted(fetchMemories)
</script>

<template>
  <div style="height: 100vh; display: flex; flex-direction: column; background: var(--color-bg-page);">
    <div style="padding: 16px 24px; border-bottom: 1px solid var(--color-border); background: var(--color-surface); display: flex; align-items: center; justify-content: space-between;">
      <span style="font-size: 16px; font-weight: 500;">记忆管理</span>
      <div style="display: flex; align-items: center; gap: 12px;">
        <NInput
          v-model:value="keyword"
          placeholder="搜索记忆..."
          clearable
          style="width: 240px;"
          @keyup.enter="handleSearch"
          @clear="handleSearch"
        />
        <NButton size="small" @click="handleSearch">搜索</NButton>
      </div>
    </div>
    <div style="flex: 1; overflow-y: auto; padding: 24px;">
      <NGrid v-if="memories.length > 0" cols="1 s:2 m:3" :x-gap="16" :y-gap="16">
        <NGi v-for="mem in memories" :key="mem.id">
          <NCard size="small" hoverable>
            <template #header-extra>
              <NButton size="tiny" quaternary type="error" @click="handleDelete(mem)">删除</NButton>
            </template>
            <div style="font-size: 14px; color: var(--color-text); line-height: 1.7; white-space: pre-wrap; max-height: 160px; overflow: hidden;">
              {{ mem.memory_value || mem.memory_key || '无内容' }}
            </div>
            <div style="margin-top: 10px; display: flex; align-items: center; justify-content: space-between;">
              <NTag size="small">{{ sourceMap[mem.source] || mem.source }}</NTag>
              <span style="font-size: 12px; color: var(--color-text-muted);">{{ new Date(mem.created_at).toLocaleString() }}</span>
            </div>
          </NCard>
        </NGi>
      </NGrid>
      <NEmpty v-else-if="!loading" description="还没有记忆" />
      <div v-if="memories.length === pageSize" style="margin-top: 24px; display: flex; justify-content: center;">
        <NPagination
          v-model:page="page"
          :page-count="99"
          @update:page="fetchMemories"
        />
      </div>
    </div>
  </div>
</template>
