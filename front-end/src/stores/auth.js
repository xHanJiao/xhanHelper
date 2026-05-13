import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { api } from '@/api/client'

const STORAGE_KEY = 'xhanhelper_auth'

export const useAuthStore = defineStore('auth', () => {
  const token = ref(null)
  const user = ref(null)
  const loading = ref(false)

  // 从 localStorage 恢复
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

  // 保存到 localStorage
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

  // 清空 localStorage
  function clearStorage() {
    try {
      localStorage.removeItem(STORAGE_KEY)
    } catch (e) {
      console.error('Failed to clear auth storage', e)
    }
  }

  // 监听 storage 事件（多标签页同步）
  function setupStorageListener() {
    window.addEventListener('storage', (e) => {
      if (e.key === STORAGE_KEY) {
        if (!e.newValue) {
          // 其他标签页登出了
          token.value = null
          user.value = null
        } else {
          loadFromStorage()
        }
      }
    })
  }

  // 登录
  async function login(username, password) {
    loading.value = true
    try {
      const res = await api.post('/api/auth/login', { username, password })
      token.value = res.token
      user.value = { id: res.user_id, username, isAdmin: !!res.is_admin }
      saveToStorage()
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

  // 注册
  async function register(username, password) {
    loading.value = true
    try {
      const res = await api.post('/api/auth/register', { username, password })
      token.value = res.token
      user.value = { id: res.user_id, username, isAdmin: !!res.is_admin }
      saveToStorage()
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

  // 登出
  async function logout() {
    if (isLoggingOut) return
    isLoggingOut = true
    try {
      if (token.value) {
        await api.post('/api/auth/logout', {})
      }
    } catch (e) {
      // 忽略登出 API 错误
    }
    token.value = null
    user.value = null
    clearStorage()
    isLoggingOut = false
  }

  // 从 localStorage 重新恢复（供路由守卫等需要时调用）
  function restore() {
    loadFromStorage()
  }

  // 初始化
  loadFromStorage()
  setupStorageListener()

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
