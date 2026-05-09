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
  NSpace,
  useMessage
} from 'naive-ui'

const message = useMessage()

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
    notes.value = Array.isArray(res) ? res : (res.notes || [])
    total.value = res.total || notes.value.length
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
  <div style="height: 100vh; display: flex; flex-direction: column; background: #f7f8fa;">
    <div style="padding: 16px 24px; border-bottom: 1px solid #e8e8e8; background: #fff; display: flex; align-items: center; justify-content: space-between;">
      <span style="font-size: 16px; font-weight: 500;">笔记管理</span>
      <div style="display: flex; align-items: center; gap: 12px;">
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
    <div style="flex: 1; overflow-y: auto; padding: 24px;">
      <NGrid v-if="notes.length > 0" cols="1 s:2 m:3" :x-gap="16" :y-gap="16">
        <NGi v-for="note in notes" :key="note.id">
          <NCard :title="note.topic || '无标题'" size="small" hoverable>
            <template #header-extra>
              <NButton size="tiny" quaternary type="error" @click="handleDelete(note)">删除</NButton>
            </template>
            <div style="font-size: 14px; color: #666; line-height: 1.7; white-space: pre-wrap; max-height: 160px; overflow: hidden;">
              {{ truncate(note.content, 300) }}
            </div>
            <div v-if="note.tags" style="margin-top: 8px; display: flex; flex-wrap: wrap; gap: 4px;">
              <NTag v-for="tag in parseTags(note.tags)" :key="tag" size="small">{{ tag }}</NTag>
            </div>
            <div style="margin-top: 8px; font-size: 12px; color: #999;">
              {{ new Date(note.created_at).toLocaleString() }}
            </div>
          </NCard>
        </NGi>
      </NGrid>
      <NEmpty v-else-if="!loading" description="还没有笔记" />
      <div v-if="total > pageSize" style="margin-top: 24px; display: flex; justify-content: center;">
        <NPagination
          v-model:page="page"
          :page-count="Math.ceil(total / pageSize)"
          @update:page="fetchNotes"
        />
      </div>
    </div>
  </div>
</template>
