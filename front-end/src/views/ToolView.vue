<script setup>
import { ref, onMounted } from 'vue'
import { api } from '@/api/client'
import { useMobile } from '@/composables/useMobile'
import PageHeader from '@/components/PageHeader.vue'
import {
  NCard,
  NButton,
  NModal,
  NForm,
  NFormItem,
  NInput,
  NTag,
  NGi,
  NGrid,
  NEmpty,
  useMessage
} from 'naive-ui'
import PageTopBar from '@/components/PageTopBar.vue'

const message = useMessage()
const { isMobile } = useMobile()

const tools = ref([])
const loading = ref(false)
const modalVisible = ref(false)
const editing = ref(null)
const formValue = ref({ name: '', description: '', schema_json: '' })
const submitting = ref(false)

const statusLabel = { draft: '草稿', active: '启用', inactive: '停用' }
const statusType = { draft: 'default', active: 'success', inactive: 'warning' }

async function fetchTools() {
  loading.value = true
  try {
    const res = await api.get('/api/tools')
    tools.value = res.tools || []
  } catch (e) {
    message.error('加载工具列表失败')
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  formValue.value = { name: '', description: '', schema_json: '{}' }
  modalVisible.value = true
}

function openEdit(tool) {
  editing.value = tool
  formValue.value = {
    name: tool.name,
    description: tool.description,
    schema_json: typeof tool.schema_json === 'string' ? tool.schema_json : JSON.stringify(tool.schema_json, null, 2)
  }
  modalVisible.value = true
}

async function handleSubmit() {
  submitting.value = true
  try {
    if (editing.value) {
      await api.patch(`/api/tools/${editing.value.id}`, formValue.value)
      message.success('更新成功')
    } else {
      await api.post('/api/tools', formValue.value)
      message.success('创建成功')
    }
    modalVisible.value = false
    fetchTools()
  } catch (e) {
    message.error('操作失败')
  } finally {
    submitting.value = false
  }
}

async function handleDelete(tool) {
  if (!window.confirm(`确认删除工具「${tool.name}」？`)) return
  try {
    await api.delete(`/api/tools/${tool.id}`)
    message.success('删除成功')
    fetchTools()
  } catch (e) {
    message.error('删除失败')
  }
}

function schemaPreview(schema) {
  if (!schema) return ''
  try {
    const obj = typeof schema === 'string' ? JSON.parse(schema) : schema
    const fn = obj.functions ? obj.functions.map(f => f.name).join(', ') : ''
    return fn || JSON.stringify(obj).substring(0, 100)
  } catch {
    return (typeof schema === 'string' ? schema : '').substring(0, 100)
  }
}

onMounted(fetchTools)
</script>

<template>
  <div style="height: 100%; display: flex; flex-direction: column; background: transparent;">
    <PageTopBar v-if="!isMobile" />
    <div class="page-scroll">
      <PageHeader title="动态工具">
        <template #actions>
          <button class="page-action" type="button" @click="openCreate">创建</button>
        </template>
      </PageHeader>
      <NGrid v-if="tools.length > 0" cols="1 s:2 m:3" :x-gap="16" :y-gap="16">
        <NGi v-for="tool in tools" :key="tool.id">
          <NCard size="small" hoverable>
            <template #header>
              <div style="display: flex; align-items: center; gap: var(--space-sm);">
                <span style="font-weight: 500;">{{ tool.name }}</span>
                <NTag :type="statusType[tool.status]" size="small">{{ statusLabel[tool.status] || tool.status }}</NTag>
              </div>
            </template>
            <template #header-extra>
              <div style="display: flex; align-items: center; gap: var(--space-xs);">
                <NButton size="tiny" quaternary @click="openEdit(tool)">编辑</NButton>
                <NButton size="tiny" quaternary type="error" @click="handleDelete(tool)">删除</NButton>
              </div>
            </template>
            <div style="font-size: var(--font-size-base); color: var(--color-text-secondary); line-height: 1.7; max-height: 80px; overflow: hidden;">
              {{ tool.description || '无描述' }}
            </div>
            <div v-if="tool.schema_json" style="margin-top: var(--space-sm); font-size: var(--font-size-sm); color: var(--color-text-muted); font-family: monospace;">
              函数: {{ schemaPreview(tool.schema_json) }}
            </div>
            <div style="margin-top: var(--space-sm); font-size: var(--font-size-sm); color: var(--color-text-muted);">
              {{ new Date(tool.created_at).toLocaleString() }}
            </div>
          </NCard>
        </NGi>
      </NGrid>
      <NEmpty v-else-if="!loading" description="还没有动态工具" />
    </div>

    <NModal v-model:show="modalVisible" :title="editing ? '编辑工具' : '创建工具'" preset="card" style="width: 640px;">
      <NForm label-placement="top">
        <NFormItem label="名称" required>
          <NInput v-model:value="formValue.name" placeholder="工具名称" />
        </NFormItem>
        <NFormItem label="描述">
          <NInput v-model:value="formValue.description" type="textarea" placeholder="描述此工具的功能" :autosize="{ minRows: 2, maxRows: 4 }" />
        </NFormItem>
        <NFormItem label="Schema JSON" required>
          <NInput
            v-model:value="formValue.schema_json"
            type="textarea"
            placeholder='{"functions": [...]}'
            :autosize="{ minRows: 6, maxRows: 16 }"
            style="font-family: monospace;"
          />
        </NFormItem>
      </NForm>
      <div style="margin-top: var(--space-lg); text-align: right;">
        <NButton style="margin-right: var(--space-sm);" @click="modalVisible = false">取消</NButton>
        <NButton type="primary" :loading="submitting" @click="handleSubmit">确定</NButton>
      </div>
    </NModal>
  </div>
</template>
