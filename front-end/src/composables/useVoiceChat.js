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

// 纯表格（每行都是 |...|）逐格念出来没意义，当占位处理
function isTableOnly(text) {
  const lines = String(text || '').split('\n').map(l => l.trim()).filter(Boolean)
  if (lines.length < 2) return false
  return lines.every(l => l.startsWith('|') && l.endsWith('|'))
}

function cleanForSpeech(text) {
  if (isTableOnly(text)) return ''
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

export function useVoiceChat({ onFinalTranscript, onError } = {}) {
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
  let maxTimer = null
  let finished = false
  let generation = 0
  let speakEpoch = 0
  let maxSeconds = 60

  async function refresh() {
    try {
      const cfg = await api.get('/api/voice/config')
      const Ctx = window.AudioContext || window.webkitAudioContext
      const hasCapture = !!navigator.mediaDevices?.getUserMedia &&
        !!Ctx && 'audioWorklet' in Ctx.prototype
      available.value = !!(cfg?.ready && hasCapture)
      maxSeconds = Math.max(5, Number(cfg?.max_seconds) || 60)
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
    if (maxTimer) {
      clearTimeout(maxTimer)
      maxTimer = null
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
    if (message) {
      console.error('[voice]', message)
      onError?.(message)
    }
    return message || '语音识别失败'
  }

  function finish() {
    if (finished) return
    const text = transcript.value.trim()
    cleanup()
    if (!text) {
      onError?.('没有识别到内容')
      return
    }
    onFinalTranscript?.(text)
  }

  function startCapture() {
    if (finished || !audioCtx || !worklet || !sourceNode) return
    sourceNode.connect(worklet)
    recording.value = true
    busy.value = false
    if (maxTimer) clearTimeout(maxTimer)
    maxTimer = setTimeout(() => {
      if (recording.value) stop()
    }, Math.max(1, maxSeconds - 2) * 1000)
  }

  async function start() {
    if (recording.value || busy.value) return
    generation += 1
    const gen = generation
    const isCurrent = () => gen === generation
    busy.value = true
    finished = false
    transcript.value = ''
    stopSpeaking()
    unlockAudio()

    // 登记到共享状态之前，资源只归本代次所有：连点/快速重启时用共享 cleanup()
    // 会把后来那一轮的东西一起拆掉。
    let stream = null
    let ctx = null
    let wk = null
    let src = null
    const discard = () => {
      if (wk) {
        wk.port.onmessage = null
        wk.disconnect()
      }
      if (src) src.disconnect()
      if (stream) stream.getTracks().forEach(t => t.stop())
      if (ctx) ctx.close().catch(() => {})
    }

    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1 } })
      if (!isCurrent()) { discard(); return }
      ctx = new (window.AudioContext || window.webkitAudioContext)()
      await ctx.resume()
      if (!isCurrent()) { discard(); return }
      await ctx.audioWorklet.addModule('/voice-pcm-worklet.js')
      if (!isCurrent()) { discard(); return }
      wk = new AudioWorkletNode(ctx, 'voice-pcm')
      wk.port.onmessage = (event) => handleWorkletMessage(event.data, ctx.sampleRate)
      src = ctx.createMediaStreamSource(stream)

      const ticket = await api.post('/api/voice/asr-ticket', {})
      if (!isCurrent()) { discard(); return }

      // 到这里才交给共享状态，cleanup() 从此负责回收
      mediaStream = stream
      audioCtx = ctx
      worklet = wk
      sourceNode = src

      const ws = new WebSocket(websocketURL(ticket.ticket))
      ws.binaryType = 'arraybuffer'
      socket = ws
      ws.onmessage = (event) => {
        if (socket !== ws) return
        let msg
        try {
          msg = JSON.parse(event.data)
        } catch {
          return
        }
        if (msg.type === 'result') {
          if (msg.text) transcript.value = msg.text
          if (msg.final) finish()
        } else if (msg.type === 'limit') {
          // 服务端到上限后会 Finish；停止采集但不要发 end，等 final
          recording.value = false
          busy.value = true
          if (maxTimer) {
            clearTimeout(maxTimer)
            maxTimer = null
          }
          if (sourceNode) { sourceNode.disconnect(); sourceNode = null }
          if (mediaStream) {
            mediaStream.getTracks().forEach(t => t.stop())
            mediaStream = null
          }
          onError?.(msg.message || '录音达到上限，已按当前内容识别')
          stopTimer = setTimeout(() => fail('语音识别超时'), 15000)
        } else if (msg.type === 'error') {
          fail(msg.error)
        }
      }
      ws.onerror = () => { if (socket === ws) fail('语音识别连接失败') }
      ws.onclose = () => {
        if (socket === ws && !finished) fail('语音识别连接断开')
      }
      // 服务端要先连豆包（几百毫秒）才会 ready，采集不能等它：点完麦克风立刻开口的
      // 那几百毫秒由 socket 缓冲，服务端连上后照常读，等 ready 反而会吞掉开头。
      ws.onopen = () => { if (socket === ws) startCapture() }
    } catch (err) {
      if (isCurrent()) {
        busy.value = false
        cleanup()
        throw err
      }
    }
  }

  function stop() {
    if (!recording.value) return
    busy.value = true
    recording.value = false
    if (maxTimer) {
      clearTimeout(maxTimer)
      maxTimer = null
    }
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
    generation += 1
    if (socket && socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: 'cancel' }))
    }
    cleanup()
  }

  async function speak(text) {
    if (!String(text || '').trim()) return
    const cleaned = cleanForSpeech(text)
    const speech = cleaned || '这条回复主要是代码或表格，我放在屏幕上了。'
    stopSpeaking()
    speakEpoch += 1
    const epoch = speakEpoch
    try {
      const blob = await api.postBlob('/api/voice/tts', { text: speech })
      if (epoch !== speakEpoch) return
      const el = ensureAudio()
      if (objectUrl) URL.revokeObjectURL(objectUrl)
      objectUrl = URL.createObjectURL(blob)
      el.src = objectUrl
      await el.play()
    } catch (err) {
      console.error('[voice] tts failed', err)
      onError?.('语音播放失败')
    }
  }

  function stopSpeaking() {
    speakEpoch += 1
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
