const KEY = 'voice.speaker'
// 近场电平闸档位：off / loose / standard / near（见 utils/nearFieldGate.js）
const NEAR_FIELD_KEY = 'voice.nearField'

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

// 默认 standard：没设过就走标准档。
// 注意：'off' 也必须落盘——它现在是显式选择，不是「没设过」。
export function readVoiceNearField() {
  try {
    return localStorage.getItem(NEAR_FIELD_KEY) || 'standard'
  } catch (e) {
    console.error('Failed to read voice near-field level', e)
    return 'standard'
  }
}

export function setVoiceNearField(value) {
  try {
    localStorage.setItem(NEAR_FIELD_KEY, value || 'standard')
  } catch (e) {
    console.error('Failed to save voice near-field level', e)
  }
}
