<script setup>
import { ref, onMounted } from 'vue'
import { api } from '@/api/client'
import { useMobile } from '@/composables/useMobile'
import {
  NButton,
  NModal,
  NForm,
  NFormItem,
  NSelect,
  NInput,
  NGi,
  NGrid,
  NEmpty,
  useMessage
} from 'naive-ui'
import ListItemCard from '@/components/ListItemCard.vue'
import PageTopBar from '@/components/PageTopBar.vue'
import PageHeader from '@/components/PageHeader.vue'

const message = useMessage()
const { isMobile } = useMobile()

const prompts = ref([])
const loading = ref(false)
const modalVisible = ref(false)
const formValue = ref({ prompt_type: 'flash', system_prompt: '' })
const submitting = ref(false)

const typeOptions = [
  { label: 'Flash（日常对话）', value: 'flash' },
  { label: 'Pro（深度思考）', value: 'pro' }
]

const typeLabel = { flash: 'Flash', pro: 'Pro' }

async function fetchPrompts() {
  loading.value = true
  try {
    const res = await api.get('/api/prompts')
    prompts.value = res.prompts || []
  } catch (e) {
    message.error('加载 Prompt 列表失败')
  } finally {
    loading.value = false
  }
}

function openCreate() {
  formValue.value = { prompt_type: 'flash', system_prompt: '' }
  modalVisible.value = true
}

async function handleSubmit() {
  submitting.value = true
  try {
    await api.post('/api/prompts', formValue.value)
    message.success('创建成功（已自动设为 active）')
    modalVisible.value = false
    fetchPrompts()
  } catch (e) {
    message.error('创建失败')
  } finally {
    submitting.value = false
  }
}

async function handleRollback(prompt) {
  try {
    await api.post(`/api/prompts/${prompt.id}/rollback`, {})
    message.success('已回滚到此版本')
    fetchPrompts()
  } catch (e) {
    message.error('回滚失败')
  }
}

async function handleDelete(prompt) {
  try {
    await api.delete(`/api/prompts/${prompt.id}`)
    message.success('删除成功')
    fetchPrompts()
  } catch (e) {
    message.error('删除失败')
  }
}

function getCardTags(p) {
  const tags = [typeLabel[p.prompt_type]]
  if (p.status === 'active') {
    tags.push('生效中')
  }
  return tags
}

function getCardActions(p) {
  const actions = []
  if (p.status !== 'active') {
    actions.push({ key: 'rollback', label: '回滚到此', onClick: () => handleRollback(p) })
  }
  actions.push({ key: 'delete', label: '删除', type: 'error', confirm: '确认删除此 Prompt？', onClick: () => handleDelete(p) })
  return actions
}

onMounted(fetchPrompts)
</script>

<template>
  <div style="height: 100%; display: flex; flex-direction: column; background: transparent;">
    <PageTopBar v-if="!isMobile" />
    <div class="page-scroll">
      <PageHeader title="System Prompt">
        <template #actions>
          <button class="page-action" type="button" @click="openCreate">创建</button>
        </template>
      </PageHeader>
      <NGrid v-if="prompts.length > 0" cols="1 s:2 m:3" :x-gap="16" :y-gap="16">
        <NGi v-for="p in prompts" :key="p.id">
          <ListItemCard
            :title="`Prompt #${p.id}`"
            :content="p.system_prompt"
            :tags="getCardTags(p)"
            :metadata="new Date(p.created_at).toLocaleString()"
            :actions="getCardActions(p)"
            max-content-height="160px"
          />
        </NGi>
      </NGrid>
      <NEmpty v-else-if="!loading" description="还没有 System Prompt" />
    </div>

    <NModal v-model:show="modalVisible" title="创建 System Prompt" preset="card" style="width: 560px;">
      <NForm label-placement="top">
        <NFormItem label="类型" required>
          <NSelect v-model:value="formValue.prompt_type" :options="typeOptions" />
        </NFormItem>
        <NFormItem label="System Prompt" required>
          <NInput
            v-model:value="formValue.system_prompt"
            type="textarea"
            placeholder="输入 System Prompt..."
            :autosize="{ minRows: 4, maxRows: 10 }"
          />
        </NFormItem>
      </NForm>
      <div style="margin-top: var(--space-lg); text-align: right;">
        <NButton style="margin-right: var(--space-sm);" @click="modalVisible = false">取消</NButton>
        <NButton type="primary" :loading="submitting" @click="handleSubmit">创建</NButton>
      </div>
    </NModal>
  </div>
</template>
