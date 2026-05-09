<script setup>
import { computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { NLayoutSider, NMenu } from 'naive-ui'

const router = useRouter()
const route = useRoute()

const menuOptions = [
  { label: '💬 聊天', key: '/chat' },
  { label: '🧠 记忆', key: '/memories' },
  { label: '🤖 智能体', key: '/agents' },
  { label: '📝 笔记', key: '/notes' },
  { label: '📊 账单', key: '/bills' }
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
  <NLayoutSider bordered nativeScrollbar width="64" collapsed-width="64">
    <NMenu
      :value="activeKey"
      :options="menuOptions"
      @update:value="handleUpdate"
      :collapsed="true"
      :collapsed-width="64"
    />
  </NLayoutSider>
</template>
