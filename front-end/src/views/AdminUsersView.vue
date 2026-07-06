<script setup>
import { ref, computed, onMounted } from 'vue'
import { NButton, NInput, NForm, NFormItem, NDataTable, NPagination, NCard, NSelect, useMessage } from 'naive-ui'
import { api } from '@/api/client'
import PageTopBar from '@/components/PageTopBar.vue'

const message = useMessage()

// 表单
const username = ref('')
const password = ref('')
const collapsed = ref(true)
const creating = ref(false)

// 列表
const users = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const statsDays = ref(3)
const statsDaysOptions = [
  { label: '近1天', value: 1 },
  { label: '近3天', value: 3 },
  { label: '近5天', value: 5 },
  { label: '近10天', value: 10 },
  { label: '近30天', value: 30 }
]

async function handleCreate() {
  if (!username.value.trim() || !password.value.trim()) {
    message.warning('请填写用户名和密码')
    return
  }
  creating.value = true
  try {
    await api.post('/api/admin/users', {
      username: username.value.trim(),
      password: password.value.trim()
    })
    message.success('用户创建成功')
    username.value = ''
    password.value = ''
    page.value = 1
    fetchUsers()
  } catch (e) {
    message.error(e?.message || '创建失败')
  } finally {
    creating.value = false
  }
}

async function fetchUsers() {
  loading.value = true
  try {
    const offset = (page.value - 1) * pageSize
    const res = await api.get(`/api/admin/users?offset=${offset}&limit=${pageSize}&days=${statsDays.value}`)
    users.value = res.users || []
    total.value = res.total_count || 0
  } catch (e) {
    message.error(e?.message || '获取用户列表失败')
  } finally {
    loading.value = false
  }
}

function handlePageChange(p) {
  page.value = p
  fetchUsers()
}

onMounted(() => {
  fetchUsers()
})

const columns = computed(() => [
  { title: '用户名', key: 'username', width: 120 },
  { title: '创建时间', key: 'created_at', width: 160 },
  { title: '上次对话', key: 'last_message_at', width: 160, render: (row) => row.last_message_at || '-' },
  { title: `近${statsDays.value}天消息`, key: 'recent_message_count', width: 100, render: (row) => row.recent_message_count ?? 0 },
  { title: `近${statsDays.value}天笔记`, key: 'recent_note_count', width: 100, render: (row) => row.recent_note_count ?? 0 },
  { title: `近${statsDays.value}天记忆`, key: 'recent_memory_count', width: 100, render: (row) => row.recent_memory_count ?? 0 },
  { title: `近${statsDays.value}天文档`, key: 'recent_doc_count', width: 100, render: (row) => row.recent_doc_count ?? 0 }
])
</script>

<template>
  <div class="users-page">
    <div class="users-topbar">
      <PageTopBar title="用户管理" />
    </div>
    <div class="users-content">
      <NCard style="margin-bottom: var(--space-lg);">
        <template #header>
          <div class="card-header-collapsible" @click="collapsed = !collapsed">
            <span>创建用户</span>
            <span class="collapse-hint">{{ collapsed ? '展开' : '收起' }}</span>
          </div>
        </template>
        <template v-if="!collapsed">
          <NForm>
            <NFormItem label="用户名">
              <NInput
                v-model:value="username"
                placeholder="请输入用户名"
                @keyup.enter="handleCreate"
              />
            </NFormItem>
            <NFormItem label="密码">
              <NInput
                v-model:value="password"
                type="password"
                placeholder="请输入密码"
                @keyup.enter="handleCreate"
              />
            </NFormItem>
            <NButton
              type="primary"
              :loading="creating"
              @click="handleCreate"
              style="margin-top: var(--space-sm);"
            >
              创建用户
            </NButton>
          </NForm>
        </template>
      </NCard>

      <NCard>
        <template #header>
          <div style="display: flex; align-items: center; justify-content: space-between;">
            <span>用户列表</span>
            <NSelect
              v-model:value="statsDays"
              :options="statsDaysOptions"
              size="small"
              style="width: 110px;"
              @update:value="page = 1; fetchUsers()"
            />
          </div>
        </template>
        <NDataTable
          :columns="columns"
          :data="users"
          :loading="loading"
          :bordered="false"
          size="small"
        />
        <div v-if="total > pageSize" style="margin-top: var(--space-md); display: flex; justify-content: center;">
          <NPagination
            :page="page"
            :page-size="pageSize"
            :item-count="total"
            :page-slot="5"
            @update:page="handlePageChange"
          />
        </div>
      </NCard>
    </div>
  </div>
</template>

<style scoped>
.users-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: var(--color-bg-page);
  min-width: 0;
}
.users-topbar {
  flex-shrink: 0;
}
.users-content {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-lg);
  max-width: 1100px;
  width: 100%;
  color: var(--color-text);
}
.card-header-collapsible {
  display: flex;
  justify-content: space-between;
  align-items: center;
  cursor: pointer;
  user-select: none;
}
.collapse-hint {
  font-size: 12px;
  opacity: 0.5;
}
</style>
