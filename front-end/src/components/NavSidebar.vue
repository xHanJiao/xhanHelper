<script setup>
import { computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { NMenu } from 'naive-ui'

const router = useRouter()
const route = useRoute()

const menuOptions = [
  { label: '💬 聊天', key: '/chat' },
  { label: '🧠 记忆', key: '/memories' },
  { label: '🤖 智能体', key: '/agents' },
  { label: '📝 笔记', key: '/notes' },
  { label: '📊 账单', key: '/bills' },
  { label: '📋 提示词', key: '/prompts' },
  { label: '🔧 工具', key: '/tools' }
]

const activeKey = computed(() => {
  if (route.path.startsWith('/chat')) return '/chat'
  return route.path
})

function handleUpdate(key) {
  router.push(key)
}
</script>

<template>
  <div style="width: 64px; min-width: 64px; border-right: 1px solid #e8e8e8; background: #fff; display: flex; flex-direction: column;">
    <NMenu
      :value="activeKey"
      :options="menuOptions"
      @update:value="handleUpdate"
      :collapsed="true"
      :collapsed-width="64"
    />
  </div>
</template>
