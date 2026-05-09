import { useAuthStore } from '@/stores/auth'

const BASE = import.meta.env.VITE_API_BASE || ''

class AuthError extends Error {
  constructor(message) {
    super(message)
    this.name = 'AuthError'
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
    auth.logout()
    throw new AuthError('未登录或 token 已过期')
  }

  return res
}

export const api = {
  get: (path) => request(path).then(r => r.json()),
  post: (path, body) => request(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  }).then(r => r.json()),
  postRaw: (path, body, contentType = 'text/plain') => request(path, {
    method: 'POST',
    headers: { 'Content-Type': contentType },
    body
  }).then(r => r.json()),
  patch: (path, body) => request(path, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  }).then(r => r.json()),
  delete: (path) => request(path, { method: 'DELETE' }).then(r => r.json()),
  stream: (path, body, signal) => request(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal
  })
}
