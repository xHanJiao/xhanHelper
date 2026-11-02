import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'

export const useConversationsStore = defineStore('conversations', () => {
  const list = ref([])
  const loading = ref(false)
  const total = ref(0)
  const pageSize = 50

  // 加载会话列表
  async function fetchList(offset = 0) {
    loading.value = true
    try {
      const res = await api.get(`/api/conversations?limit=${pageSize}&offset=${offset}`)
      const conversations = res.conversations || []
      if (offset === 0) {
        list.value = conversations
      } else {
        list.value = [...list.value, ...conversations]
      }
      total.value = res.total || 0
    } catch (e) {
      console.error('Failed to fetch conversations', e)
    } finally {
      loading.value = false
    }
  }

  // 前置新会话（刚创建的）
  function prepend(conv) {
    list.value = [conv, ...list.value.filter(c => c.id !== conv.id)]
  }

  // 更新会话标题
  function updateTitle(id, title) {
    const conv = list.value.find(c => c.id === id)
    if (conv) {
      conv.title = title
    }
  }

  // 更新会话绑定的智能体
  function updateAgent(id, agentId, agentName) {
    const conv = list.value.find(c => c.id === id)
    if (conv) {
      conv.agent_id = agentId || null
      conv.agent_name = agentName || ''
    }
  }

  // 重命名会话
  async function rename(id, title) {
    try {
      await api.patch(`/api/conversations/${id}`, { title })
      updateTitle(id, title)
      return { success: true }
    } catch (e) {
      console.error('Failed to rename conversation', e)
      return { success: false, message: '重命名失败' }
    }
  }

  // 删除会话（归档）
  async function remove(id) {
    try {
      await api.delete(`/api/conversations/${id}`)
      list.value = list.value.filter(c => c.id !== id)
      total.value = Math.max(0, total.value - 1)
      return { success: true }
    } catch (e) {
      console.error('Failed to remove conversation', e)
      return { success: false, message: '归档失败' }
    }
  }

  // 置顶/取消置顶
  async function togglePin(id, pinned) {
    try {
      await api.patch(`/api/conversations/${id}`, { pinned })
      const conv = list.value.find(c => c.id === id)
      if (conv) conv.pinned = pinned
      // 后端列表按 pin 时间倒排；本地也把置顶项挪到前面
      list.value = [...list.value].sort((a, b) =>
        (Number(!!b.pinned) - Number(!!a.pinned)) ||
        (new Date(b.updated_at || 0) - new Date(a.updated_at || 0))
      )
      return { success: true }
    } catch (e) {
      console.error('Failed to pin conversation', e)
      return { success: false, message: '置顶失败' }
    }
  }

  // 已归档会话（设置页用）
  async function fetchArchived(offset = 0) {
    loading.value = true
    try {
      const res = await api.get(`/api/conversations?archived=true&limit=${pageSize}&offset=${offset}`)
      return { list: res.conversations || [], total: res.total || 0 }
    } finally {
      loading.value = false
    }
  }

  return {
    list,
    loading,
    total,
    fetchList,
    prepend,
    updateTitle,
    updateAgent,
    rename,
    remove,
    togglePin,
    fetchArchived
  }
})
