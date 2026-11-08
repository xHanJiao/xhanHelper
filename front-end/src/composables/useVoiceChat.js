import { ref } from 'vue'
import { api } from '@/api/client'

// 首次点击麦克风时用它解锁 <audio>（浏览器自动播放策略）
const SILENT_WAV = 'data:audio/wav;base64,UklGRiQAAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQAAAAA='
const CHUNK_SAMPLES = 3200 // 200ms @ 16k mono int16 = 6400 bytes
// 一直没人说话就收口：一条连续会话按音频时长计费（静音也计），别白占着豆包并发
const IDLE_STOP_MS = 30_000
// 播报结束后再压 600ms 麦克风门：收掉外放尾音
const GATE_TAIL_MS = 600

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
  // 连续对话：一条连接连着送音频，服务端按判停分句下发 sentence
  url.searchParams.set('mode', 'continuous')
  return url.toString()
}

function log(...args) {
  console.info('[voice]', ...args)
}

/**
 * 连续语音对话：点一下开麦，之后判停一句就提交一轮，回复自动朗读。
 *
 * 难点是回声：浏览器没有 macOS 那样的可靠 AEC，所以播报期间不把真实音频发给 ASR
 * （门控），而是按原节拍发等长静音保活——豆包 8 秒收不到音频包会断会话（45000081）。
 * 代价是播报时听不到你开口，打断只能点按钮（PWA 不做自动打断）。
 */
export function useVoiceChat({ onSentence, onError, onNotice, isBusy } = {}) {
  const available = ref(false)
  // 会话开着（连 WS、录音、等回答、朗读都算）；浮层与按钮状态都看它
  const active = ref(false)
  // start 过渡中（按钮暂时禁用）
  const calling = ref(false)
  const level = ref(0)
  // TTS 正在播放（浮层「说」态）
  const speaking = ref(false)

  let socket = null
  let mediaStream = null
  let audioCtx = null
  let worklet = null
  let sourceNode = null
  let audioEl = null
  let objectUrl = null
  let pending = new Int16Array(0)
  let generation = 0
  let speakEpoch = 0
  let sessionSeconds = 600
  let maxTimer = null
  let idleTimer = null
  let endTimer = null
  let gateTailTimer = null
  let finished = true
  // 用户已点结束/服务端到上限：不再提交残句，等连接关掉
  let finishing = false
  // 播报期间压住上行（改发静音保活）
  let gated = false
  let lastActivity = 0
  let sentChunks = 0
  let sentBytes = 0
  let lastUpLog = 0

  async function refresh() {
    try {
      const cfg = await api.get('/api/voice/config')
      const Ctx = window.AudioContext || window.webkitAudioContext
      const hasCapture = !!navigator.mediaDevices?.getUserMedia &&
        !!Ctx && 'audioWorklet' in Ctx.prototype
      available.value = !!(cfg?.ready && hasCapture)
      sessionSeconds = Math.max(30, Number(cfg?.session_seconds) || 600)
    } catch {
      available.value = false
    }
  }

  function ensureAudio() {
    if (!audioEl) {
      audioEl = new Audio()
      // ended 正常收尾，pause 覆盖被 stopSpeaking 打断的情况
      audioEl.addEventListener('ended', onPlaybackEnd)
      audioEl.addEventListener('pause', onPlaybackEnd)
    }
    return audioEl
  }

  function unlockAudio() {
    const el = ensureAudio()
    if (el.dataset.unlocked === '1') return
    el.src = SILENT_WAV
    el.play().catch(() => {})
    el.dataset.unlocked = '1'
  }

  function setGate(value) {
    if (gated === value) return
    gated = value
    log(`mic gate ${value ? 'on' : 'off'}`)
  }

  function sendControl(type) {
    if (socket && socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type }))
    }
  }

  function sendPCM(buffer) {
    if (!socket || socket.readyState !== WebSocket.OPEN) return
    socket.send(buffer)
    sentChunks += 1
    sentBytes += buffer.byteLength
    const now = Date.now()
    if (now - lastUpLog >= 5000) {
      lastUpLog = now
      log(`asr up chunks=${sentChunks} bytes=${sentBytes}`)
    }
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
    // 和 macOS 一致 ×4：原始 RMS 只有 0.02~0.2，不放大浮层几乎看不出动静
    level.value = Math.min(1, Math.sqrt(sum / samples.length) * 4)
    let pcm = resampleToInt16(samples, sampleRate)
    // 门控：真实音频不进 ASR（否则会把自己念的再识别一遍），但上行流不能空闲
    if (gated) pcm = new Int16Array(pcm.length)
    pushSamples(pcm)
  }

  function clearTimers() {
    if (maxTimer) { clearTimeout(maxTimer); maxTimer = null }
    if (endTimer) { clearTimeout(endTimer); endTimer = null }
    if (gateTailTimer) { clearTimeout(gateTailTimer); gateTailTimer = null }
    if (idleTimer) { clearInterval(idleTimer); idleTimer = null }
  }

  function cleanup() {
    // 出错/硬收口时朗读可能还在播：一并停掉，别让 speaking 挂成 true、声音继续出
    stopSpeaking()
    finished = true
    finishing = false
    active.value = false
    calling.value = false
    clearTimers()
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
    gated = false
  }

  function fail(message) {
    log(`fail: ${message || '-'} active=${active.value}`)
    cleanup()
    if (message) {
      console.error('[voice]', message)
      onError?.(message)
    }
  }

  async function start() {
    if (active.value || calling.value) return
    log('start requested (continuous)')
    generation += 1
    const gen = generation
    const isCurrent = () => gen === generation
    calling.value = true
    // 上一轮可能还在收尾（已发 end、等连接关）：直接拆掉，别让它的兜底计时器打到新会话
    clearTimers()
    if (socket) {
      const stale = socket
      socket = null
      try { stale.close() } catch { /* 已关闭 */ }
    }
    finished = false
    finishing = false
    sentChunks = 0
    sentBytes = 0
    lastUpLog = 0
    lastActivity = Date.now()
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
      log('mic ok')
      if (!isCurrent()) { discard(); return }
      ctx = new (window.AudioContext || window.webkitAudioContext)()
      await ctx.resume()
      if (!isCurrent()) { discard(); return }
      await ctx.audioWorklet.addModule('/voice-pcm-worklet.js')
      log(`worklet loaded, ctx=${ctx.sampleRate}Hz`)
      if (!isCurrent()) { discard(); return }
      wk = new AudioWorkletNode(ctx, 'voice-pcm')
      wk.port.onmessage = (event) => handleWorkletMessage(event.data, ctx.sampleRate)
      src = ctx.createMediaStreamSource(stream)

      const ticket = await api.post('/api/voice/asr-ticket', {})
      log('ticket ok')
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
        if (msg.type === 'ready') {
          log('ws ready')
        } else if (msg.type === 'sentence') {
          if (!active.value || finishing) {
            log('sentence dropped (session ending)')
            return
          }
          const text = String(msg.text || '').trim()
          if (!text) return
          log(`sentence chars=${text.length}`)
          lastActivity = Date.now()
          onSentence?.(text)
        } else if (msg.type === 'limit') {
          log('ws limit', msg.message || '')
          onNotice?.(msg.message || '语音会话已到上限')
          // 服务端已 Finish：停采集但不要再发 end，等它吐完最后一句并关连接
          if (active.value) {
            active.value = false
            finishing = true
            detachCapture()
            armEndTimeout()
          }
        } else if (msg.type === 'error') {
          // 已点结束/到上限属收尾阶段：这时补发的错误帧（如 45000081 等包超时）不再弹红
          if (!active.value || finishing) {
            log('ws error ignored (session ending)', msg.error)
            return
          }
          log('ws error', msg.error)
          fail(msg.error || '语音识别失败')
        }
      }
      ws.onerror = () => { if (socket === ws && active.value) fail('语音识别连接失败') }
      ws.onclose = (event) => {
        if (socket !== ws) return
        log(`ws closed code=${event.code} finishing=${finishing}`)
        if (finishing || !active.value) {
          cleanup()
          return
        }
        fail('语音识别连接断开')
      }

      active.value = true
      armSessionLimit()
      startIdleWatch()
      // 服务端要先连豆包（几百毫秒）才会 ready，采集不能等它：点完麦克风立刻开口的
      // 那几百毫秒由 socket 缓冲，服务端连上后照常读，等 ready 反而会吞掉开头。
      ws.onopen = () => { if (socket === ws) startCapture() }
    } catch (err) {
      if (isCurrent()) {
        cleanup()
        throw err
      }
    } finally {
      if (isCurrent()) calling.value = false
    }
  }

  function startCapture() {
    if (finished || !audioCtx || !worklet || !sourceNode) return
    sourceNode.connect(worklet)
    log('capture started')
  }

  /** 停采集，返回不足 200ms 的尾巴（调用方决定发不发）。 */
  function detachCapture() {
    if (maxTimer) { clearTimeout(maxTimer); maxTimer = null }
    if (sourceNode) { sourceNode.disconnect(); sourceNode = null }
    if (mediaStream) {
      mediaStream.getTracks().forEach(t => t.stop())
      mediaStream = null
    }
    if (worklet) {
      worklet.port.onmessage = null
      worklet.disconnect()
      worklet = null
    }
    const tail = pending
    pending = new Int16Array(0)
    return tail
  }

  /** 结束会话（用户点麦克风）：发完尾巴和 end，等服务端收尾后关连接。 */
  function stop() {
    if (!active.value) {
      log('stop ignored: not active')
      return
    }
    // 连接还没开（服务端还在连豆包）：end 发不出去、采集也还没开始，直接拆，别白等 15 秒兜底
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      log('stop before ws open, cleanup directly')
      cleanup()
      return
    }
    log(`stop requested up=${sentBytes}B pending=${pending.length}`)
    active.value = false
    finishing = true
    if (idleTimer) { clearInterval(idleTimer); idleTimer = null }
    stopSpeaking()
    const tail = detachCapture()
    if (tail.length) sendPCM(tail.buffer)
    sendControl('end')
    armEndTimeout()
  }

  /** 硬收口（组件卸载/出错）：不发 end，直接拆连接与麦克风。 */
  function cancel() {
    log(`cancel: active=${active.value} up=${sentBytes}B`)
    generation += 1
    stopSpeaking()
    cleanup()
  }

  function armEndTimeout() {
    if (endTimer) clearTimeout(endTimer)
    endTimer = setTimeout(() => {
      if (finished) return
      log('end timeout, forcing cleanup')
      cleanup()
    }, 15000)
  }

  /** 会话硬上限：按音频时长计费（静音也计时），到点必须收。 */
  function armSessionLimit() {
    if (maxTimer) clearTimeout(maxTimer)
    maxTimer = setTimeout(() => {
      if (!active.value) return
      const limitText = `语音会话已到上限（${Math.round(sessionSeconds / 60)} 分钟），已结束`
      // 正在回答/朗读时别硬切：给一次 15 秒宽限
      if (speaking.value || isBusy?.()) {
        maxTimer = setTimeout(() => {
          if (!active.value) return
          onNotice?.(limitText)
          stop()
        }, 15000)
        return
      }
      onNotice?.(limitText)
      stop()
    }, sessionSeconds * 1000)
  }

  /** 空闲收口：一直没人说话就结束会话，别白占并发与音频时长。 */
  function startIdleWatch() {
    if (idleTimer) clearInterval(idleTimer)
    idleTimer = setInterval(() => {
      if (!active.value) return
      // AI 正在回答/朗读也算会话仍在进行，顺延而不是收口
      if (speaking.value || isBusy?.()) {
        lastActivity = Date.now()
        return
      }
      if (Date.now() - lastActivity < IDLE_STOP_MS) return
      log('idle limit reached, stopping')
      onNotice?.('30 秒没有听到说话，语音会话已结束')
      stop()
    }, 5000)
  }

  function speak(text) {
    if (!String(text || '').trim()) return null
    const cleaned = cleanForSpeech(text)
    const speech = cleaned || '这条回复主要是代码或表格，我放在屏幕上了。'
    stopSpeaking()
    speakEpoch += 1
    const epoch = speakEpoch
    lastActivity = Date.now()
    return (async () => {
      try {
        log(`tts request chars=${speech.length}`)
        const blob = await api.postBlob('/api/voice/tts', { text: speech })
        if (epoch !== speakEpoch) return
        log(`tts ok bytes=${blob.size}`)
        const el = ensureAudio()
        if (objectUrl) URL.revokeObjectURL(objectUrl)
        objectUrl = URL.createObjectURL(blob)
        el.src = objectUrl
        // 播报期间不把麦克风里的声音（含外放回声）发给 ASR，但上行流不能空闲
        setGate(true)
        speaking.value = true
        await el.play()
      } catch (err) {
        if (epoch === speakEpoch) {
          console.error('[voice] tts failed', err)
          speaking.value = false
          setGate(false)
          onError?.('语音播放失败')
        }
      }
    })()
  }

  function onPlaybackEnd() {
    if (!speaking.value) return
    speaking.value = false
    if (gateTailTimer) clearTimeout(gateTailTimer)
    gateTailTimer = setTimeout(() => {
      gateTailTimer = null
      if (!speaking.value) setGate(false)
    }, GATE_TAIL_MS)
  }

  function stopSpeaking() {
    speakEpoch += 1
    speaking.value = false
    if (gateTailTimer) { clearTimeout(gateTailTimer); gateTailTimer = null }
    // 只要门还关着就开：只在 speaking 为真时才开，会卡在「播放结束到门关」这段里
    setGate(false)
    if (!audioEl) return
    audioEl.pause()
    audioEl.currentTime = 0
  }

  return {
    available,
    active,
    calling,
    level,
    speaking,
    refresh,
    start,
    stop,
    cancel,
    speak,
    stopSpeaking
  }
}
