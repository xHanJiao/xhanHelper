import { ref } from 'vue'
import { api } from '@/api/client'

// 首次点击麦克风时用它解锁 <audio>（浏览器自动播放策略）
const SILENT_WAV = 'data:audio/wav;base64,UklGRiQAAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQAAAAA='
const CHUNK_SAMPLES = 3200 // 200ms @ 16k mono int16 = 6400 bytes

function resampleToInt16(input, inputRate) {
  if (inputRate === 16000) {
    const out = new Int16Array(input.length)
    for (let i = 0; i < input.length; i++) {
      const s = Math.max(-1, Math.min(1, input[i]))
      out[i] = s < 0 ? s * 0x8000 : s * 0x7fff
    }
    return out
  }
  const ratio = inputRate / 16000
  const length = Math.floor(input.length / ratio)
  const out = new Int16Array(length)
  for (let i = 0; i < length; i++) {
    const pos = i * ratio
    const i0 = Math.floor(pos)
    const frac = pos - i0
    const s = (input[i0] || 0) * (1 - frac) + (input[i0 + 1] || 0) * frac
    const clamped = Math.max(-1, Math.min(1, s))
    out[i] = clamped < 0 ? clamped * 0x8000 : clamped * 0x7fff
  }
  return out
}

function cleanForSpeech(text) {
  let s = String(text || '')
  s = s.replace(/```[\s\S]*?```/g, ' ')
  s = s.replace(/`[^`]*`/g, ' ')
  s = s.replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
  s = s.replace(/^\s{0,3}#{1,6}\s*/gm, '')
  s = s.replace(/^\s*[-*+]\s+/gm, '')
  s = s.replace(/^\s*\d+\.\s+/gm, '')
  s = s.replace(/[*_>#|]/g, '')
  s = s.replace(/\s+/g, ' ').trim()
  if (s.length < 2) return ''
  return s.slice(0, 2000)
}

function websocketURL(ticket) {
  const base = import.meta.env.VITE_API_BASE || window.location.origin
  const url = new URL('/api/voice/asr', base)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  url.searchParams.set('ticket', ticket)
  return url.toString()
}

export function useVoiceChat({ onFinalTranscript } = {}) {
  const available = ref(false)
  const recording = ref(false)
  const busy = ref(false)
  const transcript = ref('')
  const level = ref(0)

  let socket = null
  let mediaStream = null
  let audioCtx = null
  let worklet = null
  let sourceNode = null
  let audioEl = null
  let objectUrl = null
  let pending = new Int16Array(0)
  let stopTimer = null
  let finished = false

  async function refresh() {
    try {
      const cfg = await api.get('/api/voice/config')
      available.value = !!(cfg?.enabled && cfg?.ready)
    } catch {
      available.value = false
    }
  }

  function ensureAudio() {
    if (!audioEl) audioEl = new Audio()
    return audioEl
  }

  function unlockAudio() {
    const el = ensureAudio()
    if (el.dataset.unlocked === '1') return
    el.src = SILENT_WAV
    el.play().catch(() => {})
    el.dataset.unlocked = '1'
  }

  function sendPCM(buffer) {
    if (!socket || socket.readyState !== WebSocket.OPEN) return
    socket.send(buffer)
  }

  function pushSamples(int16) {
    if (!int16.length) return
    const merged = new Int16Array(pending.length + int16.length)
    merged.set(pending)
    merged.set(int16, pending.length)
    pending = merged
    while (pending.length >= CHUNK_SAMPLES) {
      const chunk = pending.slice(0, CHUNK_SAMPLES)
      pending = pending.slice(CHUNK_SAMPLES)
      sendPCM(chunk.buffer)
    }
  }

  function handleWorkletMessage(samples, sampleRate) {
    if (!samples || !samples.length) return
    let sum = 0
    for (let i = 0; i < samples.length; i++) sum += samples[i] * samples[i]
    level.value = Math.sqrt(sum / samples.length)
    pushSamples(resampleToInt16(samples, sampleRate))
  }

  function cleanup() {
    finished = true
    recording.value = false
    busy.value = false
    if (stopTimer) {
      clearTimeout(stopTimer)
      stopTimer = null
    }
    if (worklet) {
      worklet.port.onmessage = null
      worklet.disconnect()
      worklet = null
    }
    if (sourceNode) {
      sourceNode.disconnect()
      sourceNode = null
    }
    if (mediaStream) {
      mediaStream.getTracks().forEach(t => t.stop())
      mediaStream = null
    }
    if (audioCtx) {
      audioCtx.close().catch(() => {})
      audioCtx = null
    }
    if (socket) {
      const ws = socket
      socket = null
      try { ws.close() } catch { /* 已关闭 */ }
    }
    pending = new Int16Array(0)
    level.value = 0
  }

  function fail(message) {
    cleanup()
    if (message) console.error('[voice]', message)
    return message || '语音识别失败'
  }

  function finish() {
    if (finished) return
    const text = transcript.value.trim()
    cleanup()
    if (!text) return
    onFinalTranscript?.(text)
  }

  async function start() {
    if (recording.value || busy.value) return
    busy.value = true
    finished = false
    transcript.value = ''
    stopSpeaking()
    unlockAudio()
    try {
      mediaStream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1 } })
      audioCtx = new (window.AudioContext || window.webkitAudioContext)()
      await audioCtx.resume()
      await audioCtx.audioWorklet.addModule('/voice-pcm-worklet.js')
      worklet = new AudioWorkletNode(audioCtx, 'voice-pcm')
      worklet.port.onmessage = (event) => handleWorkletMessage(event.data, audioCtx.sampleRate)
      sourceNode = audioCtx.createMediaStreamSource(mediaStream)

      const ticket = await api.post('/api/voice/asr-ticket', {})
      socket = new WebSocket(websocketURL(ticket.ticket))
      socket.binaryType = 'arraybuffer'
      socket.onmessage = (event) => {
        let msg
        try {
          msg = JSON.parse(event.data)
        } catch {
          return
        }
        if (msg.type === 'result') {
          if (msg.text) transcript.value = msg.text
          if (msg.final) finish()
        } else if (msg.type === 'error') {
          fail(msg.error)
        }
      }
      socket.onerror = () => fail('语音识别连接失败')
      socket.onclose = () => {
        if (!finished) fail('语音识别连接断开')
      }
      socket.onopen = () => {
        if (finished || !audioCtx || !sourceNode || !worklet) return
        sourceNode.connect(worklet)
        recording.value = true
        busy.value = false
      }
    } catch (err) {
      busy.value = false
      cleanup()
      throw err
    }
  }

  function stop() {
    if (!recording.value) return
    busy.value = true
    recording.value = false
    if (sourceNode) {
      sourceNode.disconnect()
      sourceNode = null
    }
    if (mediaStream) {
      mediaStream.getTracks().forEach(t => t.stop())
      mediaStream = null
    }
    if (worklet) {
      worklet.port.onmessage = null
      worklet.disconnect()
      worklet = null
    }
    if (pending.length) {
      sendPCM(pending.buffer)
      pending = new Int16Array(0)
    }
    if (socket && socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: 'end' }))
    }
    stopTimer = setTimeout(() => fail('语音识别超时'), 15000)
  }

  function cancel() {
    if (socket && socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: 'cancel' }))
    }
    cleanup()
  }

  async function speak(text) {
    const cleaned = cleanForSpeech(text)
    if (!cleaned) return
    try {
      const blob = await api.postBlob('/api/voice/tts', { text: cleaned })
      const el = ensureAudio()
      if (objectUrl) URL.revokeObjectURL(objectUrl)
      objectUrl = URL.createObjectURL(blob)
      el.src = objectUrl
      await el.play()
    } catch (err) {
      console.error('[voice] tts failed', err)
    }
  }

  function stopSpeaking() {
    if (!audioEl) return
    audioEl.pause()
    audioEl.currentTime = 0
  }

  return {
    available,
    recording,
    busy,
    transcript,
    level,
    refresh,
    start,
    stop,
    cancel,
    speak,
    stopSpeaking,
    unlockAudio
  }
}
