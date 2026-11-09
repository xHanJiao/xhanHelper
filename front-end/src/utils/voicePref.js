const KEY = 'voice.speaker'

// 空串 = 没选过，请求不带 speaker，服务端用默认音色。
export function readVoiceSpeaker() {
  try {
    return localStorage.getItem(KEY) || ''
  } catch (e) {
    console.error('Failed to read voice speaker', e)
    return ''
  }
}

export function setVoiceSpeaker(id) {
  try {
    if (id) localStorage.setItem(KEY, id)
    else localStorage.removeItem(KEY)
  } catch (e) {
    console.error('Failed to save voice speaker', e)
  }
}
