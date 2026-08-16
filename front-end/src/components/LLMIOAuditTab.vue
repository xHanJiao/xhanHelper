<script setup>
import { ref, h, onMounted } from 'vue'
import { NDataTable, NTag, NButton, NSelect, NInput, NSpace, NCollapse, NCollapseItem, NDrawer, NDrawerContent, NSkeleton, useMessage } from 'naive-ui'
import { api } from '@/api/client'

const message = useMessage()

const categoryOptions = [
  { label: '全部（含未标注）', value: '' },
  { label: '后处理', value: 'post_chat' },
  { label: '管理', value: 'admin' },
  { label: '工具', value: 'tool' },
]

const categoryTagColor = { post_chat: 'info', admin: 'purple', tool: 'warning', '': 'default' }
const categoryLabel = { post_chat: '后处理', admin: '管理', tool: '工具', '': '未标注' }

const rows = ref([])
const total = ref(0)
const loading = ref(false)
const page = ref(1)
const pageSize = 20
const category = ref('')
const convId = ref('')
const detail = ref(null)
const detailLoading = ref(false)

async function fetchList() {
  loading.value = true
  try {
    const offset = (page.value - 1) * pageSize
    const res = await api.get(`/api/admin/llm-io?limit=${pageSize}&offset=${offset}&category=${category.value}&conv_id=${convId.value}`)
    rows.value = res.data || []
    total.value = res.total || 0
  } catch (e) {
    message.error(`加载失败: ${e?.message || e}`)
  } finally {
    loading.value = false
  }
}

function fmtTokens(r) {
  return ((r.prompt_tokens || 0) + (r.completion_tokens || 0)).toLocaleString()
}

function fmtMs(ms) {
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)}s` : `${ms}ms`
}

function fmtTime(ts) {
  const d = new Date(ts)
  const p = n => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

async function openDetail(row) {
  detailLoading.value = true
  detail.value = null
  try {
    detail.value = await api.get(`/api/admin/llm-io/${row.id}`)
  } catch (e) {
    message.error(`加载详情失败: ${e?.message || e}`)
  } finally {
    detailLoading.value = false
  }
}

function fmtJSON(s) {
  if (!s) return ''
  try {
    return JSON.stringify(JSON.parse(s), null, 2)
  } catch {
    return s
  }
}

function copyText(text) {
  navigator.clipboard?.writeText(text || '')
  message.success('已复制')
}

const columns = [
  { title: '时间', key: 'created_at', width: 110, render: r => fmtTime(r.created_at) },
  {
    title: '类别', key: 'category', width: 80,
    render: r => h(NTag, { size: 'small', type: categoryTagColor[r.category] || 'default' }, { default: () => categoryLabel[r.category] || r.category }),
  },
  { title: '子类', key: 'sub_category', width: 150 },
  { title: '模型', key: 'model', ellipsis: true, render: r => r.model?.replace('deepseek-', 'ds-') || '-' },
  { title: 'Tokens', key: 'tokens', width: 90, render: fmtTokens },
  { title: '耗时', key: 'latency', width: 80, render: r => fmtMs(r.latency_ms) },
  {
    title: '状态', key: 'status', width: 70,
    render: r => r.error_msg
      ? h(NTag, { size: 'small', type: 'error' }, { default: () => '失败' })
      : h(NTag, { size: 'small', type: 'success' }, { default: () => '✓' }),
  },
]

onMounted(fetchList)
</script>

<template>
  <div>
    <NSpace style="margin-bottom: 12px;" align="center">
      <NSelect v-model:value="category" :options="categoryOptions" size="small" style="width: 130px;" @update:value="page = 1; fetchList()" />
      <NInput v-model:value="convId" size="small" placeholder="会话ID筛选" clearable style="width: 200px;" @keyup.enter="page = 1; fetchList()" />
      <NButton size="small" @click="page = 1; fetchList()">刷新</NButton>
      <span style="font-size: 12px; color: var(--color-text-muted);">共 {{ total }} 条 · 保留 7 天</span>
    </NSpace>

    <NDataTable
      :columns="columns"
      :data="rows"
      :loading="loading"
      :row-key="r => r.id"
      :pagination="{ page, pageSize, itemCount: total, onChange: p => { page = p; fetchList() } }"
      size="small"
      @row-click="(_, row) => openDetail(row)"
      style="cursor: pointer;"
    />

    <NDrawer :show="!!detail" :width="720" placement="right" @update:show="v => { if (!v) detail = null }">
      <NDrawerContent v-if="detail" :title="`${detail.sub_category} · ${detail.model}`" closable>
        <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 12px;">
          {{ fmtTime(detail.created_at) }}
          <template v-if="detail.conv_id"> · 会话 {{ detail.conv_id }}</template>
          · {{ ((detail.prompt_tokens || 0) + (detail.completion_tokens || 0)).toLocaleString() }} tokens
          · {{ fmtMs(detail.latency_ms) }}
        </div>
        <NCollapse :default-expanded-names="['response']">
          <NCollapseItem title="System Prompt" name="sys">
            <pre class="io-pre">{{ detail.system_prompt }}</pre>
            <NButton size="tiny" style="margin-top: 4px;" @click="copyText(detail.system_prompt)">复制</NButton>
          </NCollapseItem>
          <NCollapseItem title="Messages" name="msgs">
            <pre class="io-pre">{{ fmtJSON(detail.messages_json) }}</pre>
            <NButton size="tiny" style="margin-top: 4px;" @click="copyText(fmtJSON(detail.messages_json))">复制</NButton>
          </NCollapseItem>
          <NCollapseItem title="Response" name="response">
            <pre class="io-pre">{{ fmtJSON(detail.response) }}</pre>
            <NButton size="tiny" style="margin-top: 4px;" @click="copyText(detail.response)">复制</NButton>
          </NCollapseItem>
          <NCollapseItem v-if="detail.error_msg" title="Error" name="error">
            <pre class="io-pre" style="color: var(--color-danger);">{{ detail.error_msg }}</pre>
          </NCollapseItem>
        </NCollapse>
      </NDrawerContent>
      <NDrawerContent v-else>
        <NSkeleton text style="width: 100%;" />
      </NDrawerContent>
    </NDrawer>
  </div>
</template>

<style scoped>
.io-pre {
  max-height: 320px;
  overflow: auto;
  background: var(--color-bg-code, #0f1419);
  color: var(--color-text-secondary, #d0d7de);
  padding: 10px;
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
