<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { useChatStore } from '@/stores/chat'
import { useScrollTopBar } from '@/composables/useScrollTopBar'
import { useDevice } from '@/composables/useDevice'
import {
  NButton,
  NModal,
  NForm,
  NFormItem,
  NInput,
  NSwitch,
  NSelect,
  NGi,
  NGrid,
  NEmpty,
  NSpin,
  useMessage
} from 'naive-ui'
import { useMobile } from '@/composables/useMobile'
import ListItemCard from '@/components/ListItemCard.vue'
import PageTopBar from '@/components/PageTopBar.vue'
import TopBarBtn from '@/components/TopBarBtn.vue'

const message = useMessage()
const { isMobile } = useMobile()
const { isIOS } = useDevice()
const router = useRouter()
const chat = useChatStore()

const agents = ref([])
const loading = ref(false)
const modalVisible = ref(false)
const editing = ref(null)
const formValue = ref({ name: '', system_prompt: '', model: 'deepseek-chat', temperature: null, max_tokens: null, thinking: true })
const submitting = ref(false)
const contentContainerRef = ref(null)
const { isBarVisible, showBar } = useScrollTopBar(contentContainerRef)

const modelOptions = [
  { label: 'DeepSeek Chat', value: 'deepseek-chat' },
  { label: 'DeepSeek Reasoner', value: 'deepseek-reasoner' },
  { label: '豆包', value: 'doubao' }
]

async function fetchAgents() {
  loading.value = true
  try {
    const res = await api.get('/api/agents')
    agents.value = res.agents || []
  } catch (e) {
    message.error('加载智能体列表失败')
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  formValue.value = { name: '', system_prompt: '', model: 'deepseek-chat', temperature: null, max_tokens: null, thinking: true }
  modalVisible.value = true
}

function openEdit(agent) {
  editing.value = agent
  let temperature = null, maxTokens = null, thinking = true
  if (agent.params_json) {
    try {
      const p = JSON.parse(agent.params_json)
      temperature = p.temperature ?? null
      maxTokens = p.max_tokens ?? null
      thinking = p.thinking ?? true
    } catch {}
  }
  formValue.value = {
    name: agent.name,
    system_prompt: agent.system_prompt,
    model: agent.model,
    temperature,
    max_tokens: maxTokens,
    thinking
  }
  modalVisible.value = true
}

function buildAgentPayload(fv) {
  const p = { name: fv.name, system_prompt: fv.system_prompt, model: fv.model }
  const params = {}
  if (fv.temperature !== null && fv.temperature !== undefined && fv.temperature !== '') {
    params.temperature = Number(fv.temperature)
  }
  if (fv.max_tokens !== null && fv.max_tokens !== undefined && fv.max_tokens !== '') {
    params.max_tokens = Number(fv.max_tokens)
  }
  if (fv.thinking !== null && fv.thinking !== undefined) {
    params.thinking = fv.thinking
  }
  if (Object.keys(params).length > 0) {
    p.params_json = JSON.stringify(params)
  }
  return p
}

async function handleSubmit() {
  submitting.value = true
  try {
    const payload = buildAgentPayload(formValue.value)
    if (editing.value) {
      await api.patch(`/api/agents/${editing.value.id}`, payload)
      message.success('更新成功')
    } else {
      await api.post('/api/agents', payload)
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

function getCardActions(agent) {
  return [
    { key: 'edit', label: '编辑', onClick: () => openEdit(agent) },
    { key: 'delete', label: '删除', type: 'error', confirm: '确认删除此智能体？', onClick: () => handleDelete(agent) }
  ]
}

function getCardPrimaryActions(agent) {
  return [
    { key: 'chat', label: '开始对话', type: 'tertiary', onClick: () => handleStartChat(agent) }
  ]
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

onMounted(fetchAgents)
</script>

<template>
  <div style="height: 100vh; display: flex; flex-direction: column; background: var(--color-bg-page);">
    <PageTopBar title="智能体管理" :visible="!isMobile || isBarVisible">
      <template #right>
        <TopBarBtn label="创建智能体" @click="openCreate" />
      </template>
    </PageTopBar>
    <div
      ref="contentContainerRef"
      @touchstart="isMobile ? showBar() : null"
      @click="handleContainerClick"
      style="flex: 1; overflow-y: auto; padding: var(--space-lg);"
    >
      <NSpin :show="loading">
        <NGrid v-if="agents.length > 0" cols="1 s:2 m:3" :x-gap="16" :y-gap="16">
          <NGi v-for="agent in agents" :key="agent.id">
            <ListItemCard
              :title="agent.name"
              :content="agent.system_prompt"
              :metadata="`模型：${agent.model} · ${new Date(agent.created_at).toLocaleDateString()}`"
              :actions="getCardActions(agent)"
              :primary-actions="getCardPrimaryActions(agent)"
              max-content-height="120px"
            />
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
        <NFormItem label="思考模式">
          <NSwitch v-model:value="formValue.thinking" />
        </NFormItem>
        <NFormItem label="Temperature (0~2)">
          <NInput v-model:value="formValue.temperature" placeholder="默认 1.0" />
        </NFormItem>
        <NFormItem label="Max Tokens">
          <NInput v-model:value="formValue.max_tokens" placeholder="默认 4096" />
        </NFormItem>
      </NForm>
      <div style="margin-top: var(--space-md); text-align: right;">
        <NButton style="margin-right: 8px;" @click="modalVisible = false">取消</NButton>
        <NButton type="primary" :loading="submitting" @click="handleSubmit">确定</NButton>
      </div>
    </NModal>
  </div>
</template>
