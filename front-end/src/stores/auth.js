import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'

const STORAGE_KEY = 'xhanhelper_auth'

// 心跳间隔：每 30 分钟续期一次（token TTL=2880min，不需要频繁刷新）
const HEARTBEAT_INTERVAL = 30 * 60 * 1000

export const useAuthStore = defineStore('auth', () => {
  const token = ref(null)
  const user = ref(null)
  const loading = ref(false)

  // ---- localStorage ----

  function loadFromStorage() {
    try {
      const stored = localStorage.getItem(STORAGE_KEY)
      if (stored) {
        const data = JSON.parse(stored)
        token.value = data.token
        user.value = data.user
      }
    } catch (e) {
      console.error('Failed to load auth from storage', e)
    }
  }

  function saveToStorage() {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({
        token: token.value,
        user: user.value
      }))
    } catch (e) {
      console.error('Failed to save auth to storage', e)
    }
  }

  function clearStorage() {
    try {
      localStorage.removeItem(STORAGE_KEY)
    } catch (e) {
      console.error('Failed to clear auth storage', e)
    }
  }

  function setupStorageListener() {
    window.addEventListener('storage', (e) => {
      if (e.key === STORAGE_KEY) {
        if (!e.newValue) {
          token.value = null
          user.value = null
          stopHeartbeat()
        } else {
          loadFromStorage()
          startHeartbeat()
        }
      }
    })
  }

  // ---- 心跳续期 ----

  let heartbeatTimer = null

  async function doHeartbeat() {
    if (!token.value) return
    try {
      await api.post('/api/auth/refresh', {})
    } catch (err) {
      // 仅 token 真正过期（401）时登出；网络抖动忽略，下次心跳重试
      if (err.status === 401) {
        await logout()
      }
    }
  }

  function startHeartbeat() {
    stopHeartbeat()
    if (!token.value) return
    // 不在 store 初始化期间立即调用 api，等第一次 interval 触发
    // token TTL 60 分钟，心跳每 3 分钟，缓冲区充裕
    heartbeatTimer = setInterval(doHeartbeat, HEARTBEAT_INTERVAL)
  }

  function stopHeartbeat() {
    if (heartbeatTimer) {
      clearInterval(heartbeatTimer)
      heartbeatTimer = null
    }
  }

  // ---- API ----

  async function login(username, password) {
    loading.value = true
    try {
      const res = await api.post('/api/auth/login', { username, password })
      token.value = res.token
      user.value = { id: res.user_id, username, isAdmin: !!res.is_admin }
      saveToStorage()
      startHeartbeat()
      return { success: true }
    } catch (err) {
      let message = '登录失败'
      if (err.status === 401) {
        message = '用户名或密码错误'
      }
      return { success: false, message }
    } finally {
      loading.value = false
    }
  }

  async function register(username, password) {
    loading.value = true
    try {
      const res = await api.post('/api/auth/register', { username, password })
      token.value = res.token
      user.value = { id: res.user_id, username, isAdmin: !!res.is_admin }
      saveToStorage()
      startHeartbeat()
      return { success: true }
    } catch (err) {
      let message = '注册失败'
      if (err.status === 403) {
        message = '注册已禁用或用户名不在允许列表'
      } else if (err.status === 409) {
        message = '用户名已存在'
      }
      return { success: false, message }
    } finally {
      loading.value = false
    }
  }

  let isLoggingOut = false

  async function logout() {
    if (isLoggingOut) return
    isLoggingOut = true
    stopHeartbeat()
    try {
      if (token.value) {
        await api.post('/api/auth/logout', {})
      }
    } catch {
      // 忽略登出 API 错误
    }
    token.value = null
    user.value = null
    clearStorage()
    isLoggingOut = false
  }

  function restore() {
    loadFromStorage()
    if (token.value) {
      startHeartbeat()
    }
  }

  // ---- 页面卸载时停止心跳 ----
  if (typeof window !== 'undefined') {
    window.addEventListener('beforeunload', stopHeartbeat)
  }

  // 初始化
  loadFromStorage()
  setupStorageListener()
  if (token.value) {
    startHeartbeat()
  }

  return {
    token,
    user,
    loading,
    login,
    register,
    logout,
    restore
  }
})
