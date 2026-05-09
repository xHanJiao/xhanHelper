<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useConversationsStore } from '@/stores/conversations'
import { NButton, NPopconfirm, NModal, NInput, useMessage } from 'naive-ui'

const router = useRouter()
const route = useRoute()
const convs = useConversationsStore()
const message = useMessage()

const renameModalVisible = ref(false)
const renamingId = ref(null)
const renamingTitle = ref('')
const hoveredId = ref(null)

const activeId = computed(() => route.params.id)

onMounted(() => {
  convs.fetchList()
})

function handleNewConversation() {
  router.push('/chat')
}

function handleRename(conv) {
  renamingId.value = conv.id
  renamingTitle.value = conv.title
  renameModalVisible.value = true
}

async function confirmRename() {
  if (!renamingTitle.value.trim()) return
  const res = await convs.rename(renamingId.value, renamingTitle.value.trim())
  if (res.success) {
    message.success('重命名成功')
    renameModalVisible.value = false
  } else {
    message.error(res.message)
  }
}

async function handleDelete(conv) {
  const res = await convs.remove(conv.id)
  if (res.success) {
    message.success('删除成功')
    if (activeId.value === conv.id) {
      router.push('/chat')
    }
  } else {
    message.error(res.message)
  }
}

function handleSelect(conv) {
  router.push(`/chat/${conv.id}`)
}
</script>

<template>
  <div style="width: 280px; background: #fff; border-right: 1px solid #e8e8e8; display: flex; flex-direction: column; height: 100%;">
    <!-- 顶部：用户信息和新对话按钮 -->
    <div style="padding: 20px 16px;">
      <!-- 用户头像 -->
      <div style="display: flex; align-items: center; gap: 12px; margin-bottom: 16px;">
        <div style="width: 48px; height: 48px; border-radius: 50%; background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); display: flex; align-items: center; justify-content: center; color: #fff; font-weight: bold; font-size: 18px;">我</div>
        <div style="font-size: 18px; font-weight: 600;">xhanHelper</div>
      </div>

      <!-- 新对话按钮 -->
      <button @click="handleNewConversation" style="width: 100%; padding: 10px 16px; border: none; border-radius: 16px; background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); color: #fff; font-size: 15px; font-weight: 600; cursor: pointer; display: flex; align-items: center; gap: 10px;">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M12 5v14M5 12h14"/>
        </svg>
        新对话
      </button>
    </div>

    <!-- 历史对话标题 -->
    <div style="padding: 0 20px 10px; color: #999; font-size: 14px; font-weight: 600;">历史对话</div>

    <!-- 对话列表 -->
    <div style="flex: 1; overflow-y: auto; padding: 0 12px;">
      <div
        v-for="conv in convs.list"
        :key="conv.id"
        @click="handleSelect(conv)"
        @mouseenter="hoveredId = conv.id"
        @mouseleave="hoveredId = null"
        :style="{
          padding: '12px 12px',
          borderRadius: '12px',
          marginBottom: '4px',
          cursor: 'pointer',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          background: activeId === conv.id ? '#f5f5f5' : 'transparent',
          fontWeight: activeId === conv.id ? '600' : '400'
        }"
        @mouseover="e => e.currentTarget.style.background = activeId === conv.id ? '#f5f5f5' : '#fafafa'"
        @mouseout="e => e.currentTarget.style.background = activeId === conv.id ? '#f5f5f5' : 'transparent'"
      >
        <div style="display: flex; align-items: center; gap: 10px; min-width: 0; flex: 1;">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#666" stroke-width="2">
            <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
          </svg>
          <div style="overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 15px; color: #333;">{{ conv.title }}</div>
        </div>

        <!-- 操作按钮 - hover 显示 -->
        <div v-if="hoveredId === conv.id" style="display: flex; gap: 4px; flex-shrink: 0;">
          <button @click.stop="handleRename(conv)" style="padding: 6px; border: none; background: none; cursor: pointer; border-radius: 6px; display: flex; align-items: center; justify-content: center;" @mouseover="e => e.currentTarget.style.background = '#eee'" @mouseout="e => e.currentTarget.style.background = 'none'">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#666" stroke-width="2">
              <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
              <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
            </svg>
          </button>
          <NPopconfirm @positive-click="handleDelete(conv)">
            <template #trigger>
              <button @click.stop style="padding: 6px; border: none; background: none; cursor: pointer; border-radius: 6px; display: flex; align-items: center; justify-content: center;" @mouseover="e => e.currentTarget.style.background = '#eee'" @mouseout="e => e.currentTarget.style.background = 'none'">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#e53e3e" stroke-width="2">
                  <path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                  <line x1="10" y1="11" x2="10" y2="17"/>
                  <line x1="14" y1="11" x2="14" y2="17"/>
                </svg>
              </button>
            </template>
            确认删除此会话？
          </NPopconfirm>
        </div>
      </div>
    </div>
  </div>

  <!-- 重命名弹窗 -->
  <NModal v-model:show="renameModalVisible" preset="card" title="重命名会话" style="width: 400px;">
    <NInput v-model:value="renamingTitle" placeholder="请输入新标题" @keyup.enter="confirmRename" />
    <div style="margin-top: 16px; text-align: right;">
      <NButton style="margin-right: 8px;" @click="renameModalVisible = false">取消</NButton>
      <NButton type="primary" @click="confirmRename">确定</NButton>
    </div>
  </NModal>
</template>
