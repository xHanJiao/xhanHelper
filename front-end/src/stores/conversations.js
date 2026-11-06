import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'

export const useConversationsStore = defineStore('conversations', () => {
  const list = ref([])
  const loading = ref(false)
  const total = ref(0)
  // 服务端已加载条数 = 下一页的 offset。不能用 list.length：本地 prepend 会把条目
  // 加到列表里（新建会话、补回当前会话），那样分页会跳过一条。
  const loadedCount = ref(0)
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
        // 按 id 去重：本地补进来的条目可能又出现在下一页里
        const seen = new Set(list.value.map(c => c.id))
        list.value = [...list.value, ...conversations.filter(c => !seen.has(c.id))]
      }
      loadedCount.value = offset + conversations.length
      total.value = res.total || 0
    } catch (e) {
      console.error('Failed to fetch conversations', e)
    } finally {
      loading.value = false
    }
  }

  // 前置会话（刚创建的 / 顶到最前）：保留已有条目的字段，避免丢掉 mode/ephemeral 等
  function prepend(conv) {
    const prev = list.value.find(c => c.id === conv.id)
    list.value = [{ ...prev, ...conv }, ...list.value.filter(c => c.id !== conv.id)]
  }

  // 新建会话：服务端立刻建行（不再等首条消息懒创建），id 在发消息前就确定——
  // 流式中切 Tab/锁屏导致断流时，这条会话也不会失去归属（列表可见、能回到原会话）。
  async function create(agentId) {
    const res = await api.post('/api/conversations', agentId ? { agent_id: agentId } : {})
    prepend({
      id: res.id,
      // 服务端靠 DB 默认值叫「新对话」，MySQL 下 Create 后实体里可能还是空串
      title: res.title || '新对话',
      agent_id: res.agent_id,
      agent_name: res.agent_name,
      mode: res.mode || 'work',
      ephemeral: !!res.ephemeral
    })
    return res
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

  // 更新会话模式 / 无痕标记（与 PATCH 成功后的服务端状态保持一致）
  function updateMode(id, mode) {
    const conv = list.value.find(c => c.id === id)
    if (conv) conv.mode = mode
  }

  function updateEphemeral(id, ephemeral) {
    const conv = list.value.find(c => c.id === id)
    if (conv) conv.ephemeral = ephemeral
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
    loadedCount,
    fetchList,
    prepend,
    create,
    updateTitle,
    updateAgent,
    updateMode,
    updateEphemeral,
    rename,
    remove,
    togglePin,
    fetchArchived
  }
})
