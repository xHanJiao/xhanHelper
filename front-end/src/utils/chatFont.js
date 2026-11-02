const KEY = 'chat.fontScale'
const SIZES = { small: '13.5px', normal: '15px', large: '16.5px' }

export const CHAT_FONT_OPTIONS = [
  { value: 'small', label: '小' },
  { value: 'normal', label: '标准' },
  { value: 'large', label: '大' }
]

export function readChatFont() {
  try {
    const v = localStorage.getItem(KEY)
    return SIZES[v] ? v : 'normal'
  } catch (e) {
    console.error('Failed to read chat font scale', e)
    return 'normal'
  }
}

export function applyChatFont(name) {
  document.documentElement.style.setProperty('--chat-font-size', SIZES[name] || SIZES.normal)
}

export function setChatFont(name) {
  applyChatFont(name)
  try { localStorage.setItem(KEY, name) } catch (e) {
    console.error('Failed to save chat font scale', e)
  }
}
