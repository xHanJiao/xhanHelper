import { api } from '@/api/client'
import { ApiError } from '@/api/response'

// 文档分享 / 下载的共用逻辑（列表页与阅读页共用）。

// 已分享就复用，没分享过（404）就创建。其它错误（401/500/网络）照常抛出。
export async function ensureDocumentShareUrl(id) {
  try {
    const res = await api.get(`/api/documents/${id}/share`)
    if (res?.url) return window.location.origin + res.url
  } catch (e) {
    if (!(e instanceof ApiError) || e.code !== 404) throw e
  }
  const res = await api.post(`/api/documents/${id}/share`, {})
  if (!res?.url) throw new Error('分享链接创建失败')
  return window.location.origin + res.url
}

// LAN http / 权限被拒时 navigator.clipboard 可能不存在或直接 reject，两种情况都退回 execCommand。
export async function writeClipboard(text) {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return
    } catch (e) {
      console.warn('clipboard API rejected, falling back to execCommand', e)
    }
  }
  const el = document.createElement('textarea')
  el.value = text
  el.style.position = 'fixed'
  el.style.opacity = '0'
  document.body.appendChild(el)
  el.select()
  const ok = document.execCommand('copy')
  el.remove()
  if (!ok) throw new Error('copy failed')
}

function sanitizeFilename(name) {
  return (name || 'document').replace(/[/\\:*?"<>|]/g, '_').slice(0, 100)
}

export function downloadMarkdown(content, title) {
  const blob = new Blob([content || ''], { type: 'text/markdown' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = sanitizeFilename(title) + '.md'
  document.body.appendChild(a)
  a.click()
  a.remove()
  // Safari 立刻 revoke 会取消下载
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
