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
  const headers = { ...options.headers }

  if (auth.token) {
    headers['Authorization'] = `Bearer ${auth.token}`
  }

  const res = await fetch(BASE + path, { ...options, headers })

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
  })
}
