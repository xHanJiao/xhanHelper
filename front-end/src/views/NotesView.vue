<script setup>
import { ref, onMounted } from 'vue'
import { api } from '@/api/client'
import { useMobile } from '@/composables/useMobile'
import {
  NCard,
  NButton,
  NInput,
  NPagination,
  NGrid,
  NGi,
  NEmpty,
  NTag,
  NSpace,
  useMessage
} from 'naive-ui'

const message = useMessage()
const { isMobile, toggleDrawer } = useMobile()

const notes = ref([])
const loading = ref(false)
const page = ref(1)
const pageSize = 12
const total = ref(0)
const keyword = ref('')

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
  if (!window.confirm(`确认删除笔记「${note.topic}」？`)) return
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

function truncate(text, max) {
  if (!text) return ''
  return text.length > max ? text.substring(0, max) + '...' : text
}

function parseTags(tags) {
  if (!tags) return []
  return tags.split(',').map(t => t.trim()).filter(Boolean)
}

onMounted(fetchNotes)
</script>

<template>
  <div style="height: 100vh; display: flex; flex-direction: column; background: var(--color-bg-page);">
    <div style="padding: var(--space-md) var(--space-lg); border-bottom: 1px solid var(--color-border); background: var(--color-surface); display: flex; align-items: center; justify-content: space-between;">
      <span style="font-size: var(--font-size-lg); font-weight: 500; color: var(--color-text); display: flex; align-items: center; gap: var(--space-sm);">
  <span v-if="isMobile" @click="toggleDrawer()" style="cursor: pointer; font-size: 20px;">☰</span>
  笔记管理
</span>
      <div style="display: flex; align-items: center; gap: var(--space-md);">
        <NInput
          v-model:value="keyword"
          placeholder="搜索笔记..."
          clearable
          style="width: 240px;"
          @keyup.enter="handleSearch"
          @clear="handleSearch"
        />
        <NButton size="small" @click="handleSearch">搜索</NButton>
      </div>
    </div>
    <div style="flex: 1; overflow-y: auto; padding: var(--space-lg);">
      <NGrid v-if="notes.length > 0" cols="1 s:2 m:3" :x-gap="16" :y-gap="16">
        <NGi v-for="note in notes" :key="note.id">
          <NCard :title="note.topic || '无标题'" size="small" hoverable>
            <template #header-extra>
              <NButton size="tiny" quaternary type="error" @click="handleDelete(note)">删除</NButton>
            </template>
            <div style="font-size: 14px; color: var(--color-text-secondary); line-height: 1.7; white-space: pre-wrap; max-height: 160px; overflow: hidden;">
              {{ truncate(note.content, 300) }}
            </div>
            <div v-if="note.tags" style="margin-top: var(--space-sm); display: flex; flex-wrap: wrap; gap: var(--space-xs);">
              <NTag v-for="tag in parseTags(note.tags)" :key="tag" size="small">{{ tag }}</NTag>
            </div>
            <div style="margin-top: var(--space-sm); font-size: 12px; color: var(--color-text-muted);">
              {{ new Date(note.created_at).toLocaleString() }}
            </div>
          </NCard>
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
