<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { useChatStore } from '@/stores/chat'
import {
  NCard,
  NButton,
  NModal,
  NForm,
  NFormItem,
  NInput,
  NSelect,
  NSpace,
  NPopconfirm,
  NGi,
  NGrid,
  NEmpty,
  NSpin,
  useMessage
} from 'naive-ui'
import { useMobile } from '@/composables/useMobile'

const message = useMessage()
const { isMobile, toggleDrawer } = useMobile()
const router = useRouter()
const chat = useChatStore()

const agents = ref([])
const loading = ref(false)
const modalVisible = ref(false)
const editing = ref(null)
const formValue = ref({ name: '', system_prompt: '', model: 'deepseek-chat' })
const submitting = ref(false)

const modelOptions = [
  { label: 'DeepSeek Chat', value: 'deepseek-chat' },
  { label: 'DeepSeek Reasoner', value: 'deepseek-reasoner' },
  { label: '豆包', value: 'doubao' }
]

async function fetchAgents() {
  loading.value = true
  try {
    const res = await api.get('/api/agents')
    agents.value = Array.isArray(res) ? res : (res.agents || [])
  } catch (e) {
    message.error('加载智能体列表失败')
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  formValue.value = { name: '', system_prompt: '', model: 'deepseek-chat' }
  modalVisible.value = true
}

function openEdit(agent) {
  editing.value = agent
  formValue.value = {
    name: agent.name,
    system_prompt: agent.system_prompt,
    model: agent.model
  }
  modalVisible.value = true
}

async function handleSubmit() {
  submitting.value = true
  try {
    if (editing.value) {
      await api.patch(`/api/agents/${editing.value.id}`, formValue.value)
      message.success('更新成功')
    } else {
      await api.post('/api/agents', formValue.value)
      message.success('创建成功')
    }
    modalVisible.value = false
    fetchAgents()
  } catch (e) {
    message.error('操作失败')
  } finally {
    submitting.value = false
  }
}

async function handleDelete(agent) {
  try {
    await api.delete(`/api/agents/${agent.id}`)
    message.success('删除成功')
    fetchAgents()
  } catch (e) {
    message.error('删除失败')
  }
}

async function handleStartChat(agent) {
  chat.agentId = agent.id
  router.push('/chat')
}

onMounted(fetchAgents)
</script>

<template>
  <div style="height: 100vh; display: flex; flex-direction: column; background: var(--color-bg-page);">
    <div style="padding: 16px 24px; border-bottom: 1px solid var(--color-border); background: var(--color-surface); display: flex; align-items: center; justify-content: space-between;">
      <span style="font-size: var(--font-size-lg); font-weight: 500; color: var(--color-text); display: flex; align-items: center; gap: var(--space-sm);">
  <span v-if="isMobile" @click="toggleDrawer()" style="cursor: pointer; font-size: 20px;">☰</span>
  智能体管理
</span>
      <NButton type="primary" size="small" @click="openCreate">创建智能体</NButton>
    </div>
    <div style="flex: 1; overflow-y: auto; padding: 24px;">
      <NSpin :show="loading">
        <NGrid v-if="agents.length > 0" cols="1 s:2 m:3" :x-gap="16" :y-gap="16">
          <NGi v-for="agent in agents" :key="agent.id">
            <NCard :title="agent.name" size="small" hoverable>
              <template #header-extra>
                <NSpace>
                  <NButton size="tiny" quaternary @click="openEdit(agent)">编辑</NButton>
                  <NPopconfirm @positive-click="handleDelete(agent)">
                    <template #trigger>
                      <NButton size="tiny" quaternary type="error">删除</NButton>
                    </template>
                    确认删除此智能体？
                  </NPopconfirm>
                </NSpace>
              </template>
              <div style="font-size: 13px; color: var(--color-text-secondary); line-height: 1.6; max-height: 120px; overflow: hidden; white-space: pre-wrap;">{{ agent.system_prompt }}</div>
              <div style="margin-top: 10px; display: flex; align-items: center; justify-content: space-between;">
                <span style="font-size: 12px; color: var(--color-text-muted);">模型：{{ agent.model }} · {{ new Date(agent.created_at).toLocaleDateString() }}</span>
                <NButton size="tiny" type="primary" quaternary @click.stop="handleStartChat(agent)">开始对话</NButton>
              </div>
            </NCard>
          </NGi>
        </NGrid>
        <NEmpty v-else description="还没有智能体" />
      </NSpin>
    </div>

    <NModal v-model:show="modalVisible" :title="editing ? '编辑智能体' : '创建智能体'" preset="card" style="width: 560px;">
      <NForm label-placement="top">
        <NFormItem label="名称" required>
          <NInput v-model:value="formValue.name" placeholder="智能体名称" maxlength="50" />
        </NFormItem>
        <NFormItem label="System Prompt" required>
          <NInput
            v-model:value="formValue.system_prompt"
            type="textarea"
            placeholder="定义智能体的行为和角色"
            :autosize="{ minRows: 4, maxRows: 10 }"
            maxlength="3000"
          />
        </NFormItem>
        <NFormItem label="模型">
          <NSelect v-model:value="formValue.model" :options="modelOptions" />
        </NFormItem>
      </NForm>
      <div style="margin-top: 16px; text-align: right;">
        <NButton style="margin-right: 8px;" @click="modalVisible = false">取消</NButton>
        <NButton type="primary" :loading="submitting" @click="handleSubmit">确定</NButton>
      </div>
    </NModal>
  </div>
</template>
