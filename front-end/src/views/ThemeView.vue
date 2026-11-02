<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '@/api/client'
import { useMobile } from '@/composables/useMobile'
import { NModal, NForm, NFormItem, NInput, NButton, NSpin, NGrid, NGi, NEmpty, NTag, useMessage } from 'naive-ui'
import PageTopBar from '@/components/PageTopBar.vue'
import PageHeader from '@/components/PageHeader.vue'
import ListItemCard from '@/components/ListItemCard.vue'

const { isMobile } = useMobile()

const message = useMessage()
const items = ref([])
const loading = ref(false)
const modalVisible = ref(false)
const editing = ref(null)
const formValue = ref({ name: '', description: '' })
const submitting = ref(false)

async function fetchList() {
  loading.value = true
  try {
    const res = await api.get('/api/themes')
    items.value = res.items || []
  } catch (_) {
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  formValue.value = { name: '', description: '' }
  modalVisible.value = true
}

function openEdit(item) {
  editing.value = item
  formValue.value = { name: item.name, description: item.description }
  modalVisible.value = true
}

async function handleSubmit() {
  if (!formValue.value.name.trim()) {
    message.warning('请输入主题名称')
    return
  }
  submitting.value = true
  try {
    if (editing.value) {
      await api.patch(`/api/themes/${editing.value.id}`, { description: formValue.value.description })
      message.success('已更新')
    } else {
      await api.post('/api/themes', formValue.value)
      message.success('创建成功')
    }
    modalVisible.value = false
    await fetchList()
  } catch (e) {
    message.error((editing.value ? '更新' : '创建') + '失败: ' + (e.message || ''))
  } finally {
    submitting.value = false
  }
}

async function handleStatusChange(item, status) {
  try {
    await api.patch(`/api/themes/${item.id}`, { status })
    item.status = status
    message.success(status === 'active' ? '已激活' : status === 'closed' ? '已关闭' : '已归档')
  } catch (e) {
    message.error('操作失败: ' + (e.message || ''))
  }
}

function formatTime(ts) {
  if (!ts) return ''
  return new Date(ts).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

function statusColor(s) {
  return s === 'active' ? 'success' : s === 'archived' ? 'warning' : 'default'
}
function statusLabel(s) {
  return s === 'active' ? '活跃' : s === 'archived' ? '已归档' : '已关闭'
}

function activeActions(item) {
  return [
    { key: 'edit', label: '编辑', onClick: () => openEdit(item) },
    { key: 'close', label: '关闭', confirm: '确定关闭该主题？', onClick: () => handleStatusChange(item, 'closed') },
    { key: 'archive', label: '归档', confirm: '确定归档该主题？', onClick: () => handleStatusChange(item, 'archived') },
  ]
}

function closedActions(item) {
  return [
    { key: 'reactivate', label: '重新激活', onClick: () => handleStatusChange(item, 'active') },
  ]
}

function buildTags(item) {
  return [{ text: statusLabel(item.status), type: statusColor(item.status) }]
}

function buildContent(item) {
  let text = item.description
  if (item.state_text) text += '\n\n' + item.state_text
  return text
}

function buildActions(item) {
  if (item.status === 'active') return activeActions(item)
  if (item.status === 'closed') return closedActions(item)
  return []
}

function buildMetadata(item) {
  let parts = []
  if (item.last_run_at) parts.push('上次分析 ' + formatTime(item.last_run_at))
  parts.push('更新于 ' + formatTime(item.updated_at))
  return parts.join(' · ')
}

onMounted(fetchList)
</script>

<template>
  <div style="display: flex; flex-direction: column; height: 100%; background: transparent; min-width: 0;">
    <PageTopBar v-if="!isMobile" />
    <div class="page-scroll">
      <PageHeader title="主题">
        <template #actions>
          <button class="page-action" type="button" @click="openCreate">新建</button>
        </template>
      </PageHeader>
      <NSpin v-if="loading && items.length === 0" />
      <NEmpty v-else-if="items.length === 0" description="暂无主题，点击右上角新建" style="margin-top: 80px;" />
      <NGrid v-else :cols="isMobile ? 1 : 3" :x-gap="16" :y-gap="16">
        <NGi v-for="item in items" :key="item.id">
          <ListItemCard
            :title="item.name"
            :content="buildContent(item)"
            :tags="buildTags(item)"
            :metadata="buildMetadata(item)"
            :max-content-height="'150px'"
            :actions="buildActions(item)"
          />
        </NGi>
      </NGrid>
    </div>

    <NModal v-model:show="modalVisible" preset="card" title="主题" style="width: 480px;">
      <NForm label-placement="top">
        <NFormItem label="名称" required>
          <NInput v-model:value="formValue.name" placeholder="如：睡眠管理、职业发展" :disabled="!!editing" />
        </NFormItem>
        <NFormItem label="描述">
          <NInput v-model:value="formValue.description" type="textarea" :autosize="{ minRows: 2, maxRows: 4 }" placeholder="这个主题追踪什么？" />
        </NFormItem>
        <div style="display: flex; justify-content: flex-end; gap: var(--space-sm); margin-top: var(--space-md);">
          <NButton @click="modalVisible = false">取消</NButton>
          <NButton type="primary" :loading="submitting" @click="handleSubmit">{{ editing ? '保存' : '创建' }}</NButton>
        </div>
      </NForm>
    </NModal>
  </div>
</template>
