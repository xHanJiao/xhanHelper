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
      const conversations = res.conversations || res || []
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

  // 删除会话
  async function remove(id) {
    try {
      await api.delete(`/api/conversations/${id}`)
      list.value = list.value.filter(c => c.id !== id)
      return { success: true }
    } catch (e) {
      console.error('Failed to delete conversation', e)
      return { success: false, message: '删除失败' }
    }
  }

  return {
    list,
    loading,
    total,
    fetchList,
    prepend,
    updateTitle,
    rename,
    remove
  }
})
