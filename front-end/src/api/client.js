import { useAuthStore } from '@/stores/auth'
import { unwrap } from '@/api/response'

const BASE = import.meta.env.VITE_API_BASE || ''

class AuthError extends Error {
  constructor(message, status = 401) {
    super(message)
    this.name = 'AuthError'
    this.status = status
  }
}

async function request(path, options = {}) {
  const auth = useAuthStore()
  const { headers: _h, ...rest } = options
  const headers = { ..._h }

  if (auth.token) {
    headers['Authorization'] = `Bearer ${auth.token}`
  }

  const res = await fetch(BASE + path, { ...rest, headers })

  if (res.status === 401) {
    if (auth.token) {
      auth.logout()
    }
    throw new AuthError('未登录或 token 已过期', res.status)
  }

  return res
}

export const api = {
  get: (path) => request(path).then(r => r.json()).then(unwrap),
  post: (path, body) => request(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  }).then(r => r.json()).then(unwrap),
  postRaw: (path, body, contentType = 'text/plain') => request(path, {
    method: 'POST',
    headers: { 'Content-Type': contentType },
    body
  }).then(r => r.json()).then(unwrap),
  // multipart 上传：不设 Content-Type，边界交给浏览器
  upload: (path, file) => {
    const form = new FormData()
    form.append('file', file)
    return request(path, { method: 'POST', body: form }).then(r => r.json()).then(unwrap)
  },
  // 取二进制（图床私有读要 Bearer，不能直接给 <img src>）
  blob: (path) => request(path).then(r => r.blob()),
  // POST 取二进制（语音 TTS）：失败时把服务端错误 JSON 变成可读错误
  postBlob: (path, body) => request(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  }).then(async (r) => {
    if (!r.ok) {
      let msg = `HTTP ${r.status}`
      try {
        const data = await r.json()
        msg = data?.error || data?.message || msg
      } catch {
        // 非 JSON 错误体：保留 HTTP 状态
      }
      throw new Error(msg)
    }
    return r.blob()
  }),
  patch: (path, body) => request(path, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  }).then(r => r.json()).then(unwrap),
  delete: (path) => request(path, { method: 'DELETE' }).then(r => r.json()).then(unwrap),
  stream: (path, body, signal) => request(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal
  }),
  // 续传：GET 也走 Bearer（用 fetch 而不是 EventSource，才能带 header）
  streamGet: (path, signal) => request(path, {
    headers: { Accept: 'text/event-stream' },
    signal
  }),
  publicGet: (path) => fetch(BASE + path).then(r => r.json()).then(unwrap)
}
