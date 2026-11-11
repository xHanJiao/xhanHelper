// 会话失效时暂存未发出的消息：登录后恢复成暂停的排队消息。
// 用 sessionStorage：同一标签页内跨登录页保留，关掉标签页即丢（不长期占空间）。
const KEY = 'xhanhelper_unsent_draft'

export function saveUnsentDraft(conversationId, items) {
  const valid = (items || []).filter(it => it && (it.content?.trim() || it.imageIds?.length))
  if (!valid.length) return
  try {
    sessionStorage.setItem(KEY, JSON.stringify({
      conversationId: conversationId || null,
      items: valid
    }))
  } catch (e) {
    console.error('Failed to save unsent draft', e)
  }
}

export function peekUnsentDraft() {
  try {
    const raw = sessionStorage.getItem(KEY)
    return raw ? JSON.parse(raw) : null
  } catch (e) {
    console.error('Failed to read unsent draft', e)
    return null
  }
}

// 恢复时消费掉，避免二次登录重复入队；conversationId 对不上就不动（等回到原会话再恢复）。
export function takeUnsentDraft(conversationId) {
  const draft = peekUnsentDraft()
  if (!draft) return null
  if (draft.conversationId && draft.conversationId !== (conversationId || null)) return null
  try {
    sessionStorage.removeItem(KEY)
  } catch (e) {
    console.error('Failed to clear unsent draft', e)
  }
  return draft
}
