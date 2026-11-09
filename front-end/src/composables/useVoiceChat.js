import { ref } from 'vue'
import { api } from '@/api/client'
import { cutSentences } from '@/utils/speech'
import { readVoiceSpeaker, readVoiceNearField } from '@/utils/voicePref'
import { NEAR_FIELD_WINDOW_MS, nearFieldThreshold, shouldAccept } from '@/utils/nearFieldGate'

// 首次点击麦克风时用它解锁 <audio>（浏览器自动播放策略）
const SILENT_WAV = 'data:audio/wav;base64,UklGRiQAAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQAAAAA='
const CHUNK_SAMPLES = 3200 // 200ms @ 16k mono int16 = 6400 bytes
// 一直没人说话就收口：一条连续会话按音频时长计费（静音也计），别白占着豆包并发
const IDLE_STOP_MS = 30_000
// 播报结束后再压 600ms 麦克风门：收掉外放尾音
const GATE_TAIL_MS = 600
// 松手后等最后半句 definite：1.5s 静默提交，3s 硬上限（豆包判停 800ms + 二遍识别）
const HOLD_TAIL_MS = 1500
const HOLD_MAX_MS = 3000

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
  // TTS 正在播放（「说」态）
  const speaking = ref(false)
  // 回复已生成完、TTS 还在合成音频（还没开播）：「想」态要把它算进去，
  // 否则这段里会先闪回「听」（听→想→听→说）
  const preparing = ref(false)
  // 句级朗读进度：sentences = 已切开的句子 + 还没切开的流式尾巴；index = 正在播的句子
  const speechSentences = ref([])
  const speechIndex = ref(0)
  // 按住说：按住期间判停分句只暂存、不提交；松手后整段作为一轮
  const holding = ref(false)

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
  // 近场闸：最近 2 秒的电平帧（带门控标记，播报期的 TTS 残留不能算用户说话）
  let levelFrames = []
  let lastNearFieldNoticeAt = 0

  // hold 暂存：holding = 按住中；awaitingTail = 已松手、还在等最后半句
  let heldSentences = []
  let heldAwaitingTail = false
  let heldTailTimer = null
  let heldCapTimer = null

  // 句级 TTS 流水线状态（代次用 speakEpoch）
  let speechFrozen = []      // 已切开的句子，每个对应一次 TTS 请求
  let speechTail = ''        // 还没切开的流式尾巴（只显示、不合成）
  let speechConsumed = 0     // 已从累积文本里消费掉的字符数
  let speechTasks = new Map() // index -> Promise<Blob|null>
  let speechEnded = true     // 本轮回复是否已结束（endReplyStream）
  let wakeResolve = null     // 泵等待「新句子/结束」的单槽信号

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

  /** 近场闸：远处的声音不提交；关闭时恒放行（与加闸之前完全一致）。 */
  function acceptNearField() {
    const threshold = nearFieldThreshold(readVoiceNearField())
    if (threshold == null) return true
    if (shouldAccept(levelFrames, threshold)) return true
    log(`sentence dropped: near-field gate frames=${levelFrames.length}`)
    // 误拒比误收难受：被吞掉时必须让人看见，限频 10 秒
    const now = Date.now()
    if (now - lastNearFieldNoticeAt >= 10_000) {
      lastNearFieldNoticeAt = now
      onNotice?.('已忽略：声音来自远处')
    }
    return false
  }

  function handleWorkletMessage(samples, sampleRate) {
    if (!samples || !samples.length) return
    let sum = 0
    for (let i = 0; i < samples.length; i++) sum += samples[i] * samples[i]
    // 和 macOS 一致 ×4：原始 RMS 只有 0.02~0.2，不放大浮层几乎看不出动静
    level.value = Math.min(1, Math.sqrt(sum / samples.length) * 4)
    const now = Date.now()
    levelFrames.push({ t: now, level: level.value, gated })
    const cutoff = now - (NEAR_FIELD_WINDOW_MS + 500)
    while (levelFrames.length && levelFrames[0].t < cutoff) levelFrames.shift()
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
    resetHeld()
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
    levelFrames = []
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
    resetHeld()
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
    levelFrames = []
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
      // iOS Safari：AudioContext 必须在用户手势的同一个任务里创建/恢复。await 过
      // getUserMedia（权限弹窗不算手势）之后再 new/resume，context 会一直 suspended，
      // worklet 的 process 永远不会跑 —— 表现就是「麦克风开着但一句也识别不到」。
      ctx = new (window.AudioContext || window.webkitAudioContext)()
      ctx.resume().catch(() => {})
      stream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1 } })
      log('mic ok')
      if (!isCurrent()) { discard(); return }
      await ctx.audioWorklet.addModule('/voice-pcm-worklet.js')
      log(`worklet loaded, ctx=${ctx.sampleRate}Hz state=${ctx.state}`)
      if (!isCurrent()) { discard(); return }
      wk = new AudioWorkletNode(ctx, 'voice-pcm')
      wk.port.onmessage = (event) => handleWorkletMessage(event.data, ctx.sampleRate)
      // Safari 只从 destination 反向拉取音频图：输出不接任何东西的 worklet 不会执行
      // process（Chrome 会）。接一条零增益到 destination 保活，不影响录音。
      const sink = ctx.createGain()
      sink.gain.value = 0
      wk.connect(sink)
      sink.connect(ctx.destination)
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
          if (!acceptNearField()) return
          log(`sentence chars=${text.length}`)
          lastActivity = Date.now()
          // 按住 / 松手等尾巴：只暂存，不提交、不打断
          if (holding.value || heldAwaitingTail) {
            heldSentences.push(text)
            if (heldAwaitingTail) armHeldTail()
            return
          }
          onSentence?.(text)
        } else if (msg.type === 'voice') {
          // 服务端听到非空识别结果（含未判停的 partial）：空闲收口据此顺延
          lastActivity = Date.now()
          // 松手后 partial 还在变，说明最后半句没吐完：重置 1.5s 尾巴窗口
          if (heldAwaitingTail) armHeldTail()
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
        // ctx 在 getUserMedia 之前就建好了：失败路径也要关掉，否则留一个空跑的音频上下文
        discard()
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
  function stop({ flush = true } = {}) {
    if (!active.value) {
      log('stop ignored: not active')
      return
    }
    // 用户主动退出时先把暂存的话提交出去，不丢话；上限/空闲收口则不补发
    if (flush) flushHeld(true)
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
          stop({ flush: false })
        }, 15000)
        return
      }
      onNotice?.(limitText)
      stop({ flush: false })
    }, sessionSeconds * 1000)
  }

  /** 空闲收口：一直没人说话就结束会话，别白占并发与音频时长。 */
  function startIdleWatch() {
    if (idleTimer) clearInterval(idleTimer)
    idleTimer = setInterval(() => {
      if (!active.value) return
      // AI 正在回答/朗读、或用户按住说（可能正在想）也算会话仍在进行，顺延而不是收口
      if (speaking.value || isBusy?.() || holding.value) {
        lastActivity = Date.now()
        return
      }
      if (Date.now() - lastActivity < IDLE_STOP_MS) return
      log('idle limit reached, stopping')
      onNotice?.('30 秒没有听到说话，语音会话已结束')
      stop({ flush: false })
    }, 5000)
  }

  // MARK: 按住说（hold 暂存）

  /** 是否有还没提交的暂存（退出语音时决定是否通知服务端停旧轮）。 */
  function hasHeld() {
    return heldSentences.length > 0
  }

  function clearHeldTimers() {
    if (heldTailTimer) { clearTimeout(heldTailTimer); heldTailTimer = null }
    if (heldCapTimer) { clearTimeout(heldCapTimer); heldCapTimer = null }
  }

  function resetHeld() {
    clearHeldTimers()
    holding.value = false
    heldAwaitingTail = false
    heldSentences = []
  }

  function startHold() {
    if (!active.value || finishing) return
    // 松手后的等尾巴窗口内再次按住：上一批暂存还没提交，先提交，别丢话
    if (heldSentences.length) flushHeld(true)
    clearHeldTimers()
    heldSentences = []
    heldAwaitingTail = false
    holding.value = true
    lastActivity = Date.now()
    stopSpeaking()
    log('hold start')
  }

  function endHold() {
    if (!holding.value) return
    holding.value = false
    heldAwaitingTail = true
    log('hold end')
    // 3s 硬上限从松手算起，不受后续 sentence/voice 重置影响
    if (heldCapTimer) clearTimeout(heldCapTimer)
    heldCapTimer = setTimeout(() => {
      heldCapTimer = null
      flushHeld()
    }, HOLD_MAX_MS)
    armHeldTail()
  }

  /** 尾巴窗口：每来一条 sentence/voice 就重置 1.5s 静默计时。 */
  function armHeldTail() {
    if (!heldAwaitingTail) return
    if (heldTailTimer) clearTimeout(heldTailTimer)
    heldTailTimer = setTimeout(() => {
      heldTailTimer = null
      flushHeld()
    }, HOLD_TAIL_MS)
  }

  function flushHeld(force = false) {
    if (holding.value) {
      if (!force) return
      holding.value = false
    }
    // 会话已结束/正在收尾（上限、断线、用户已退出）：清空暂存，不补发
    if (!active.value || finishing) {
      resetHeld()
      return
    }
    heldAwaitingTail = false
    clearHeldTimers()
    const text = heldSentences.join('').trim()
    heldSentences = []
    if (!text) return
    log(`hold flush chars=${text.length}`)
    lastActivity = Date.now()
    onSentence?.(text)
  }

  // MARK: 句级朗读流水线

  function wake() {
    const resolve = wakeResolve
    wakeResolve = null
    if (resolve) resolve()
  }

  function waitWake() {
    return new Promise((resolve) => { wakeResolve = resolve })
  }

  function freezeSentence(sentence) {
    const index = speechFrozen.length
    speechFrozen.push(sentence)
    const speech = cleanForSpeech(sentence)
    const speaker = readVoiceSpeaker()
    speechTasks.set(index, speech
      ? api.postBlob('/api/voice/tts', speaker ? { text: speech, speaker } : { text: speech }).catch((err) => {
          console.error('[voice] tts failed', err)
          return null
        })
      : Promise.resolve(null))
  }

  /** 新一轮语音回复：先收掉上一轮（含还在念的）。 */
  function beginReplyStream() {
    stopSpeaking()
    speakEpoch += 1
    speechFrozen = []
    speechTail = ''
    speechConsumed = 0
    speechTasks = new Map()
    speechSentences.value = []
    speechIndex.value = 0
    speechEnded = false
    preparing.value = true
    lastActivity = Date.now()
    runSpeechPump(speakEpoch)
  }

  /** 流式正文（累积全文）：切出的句子立刻排队合成，尾巴只显示不合成。 */
  function appendReplyDelta(text) {
    if (speechEnded) beginReplyStream()
    const full = String(text || '')
    if (full.length <= speechConsumed) return
    const cut = cutSentences(full, speechConsumed, speechFrozen.length === 0)
    cut.sentences.forEach(freezeSentence)
    speechConsumed = cut.consumed
    speechTail = full.slice(speechConsumed)
    speechSentences.value = speechTail ? [...speechFrozen, speechTail] : speechFrozen.slice()
    lastActivity = Date.now()
    if (cut.sentences.length) wake()
  }

  /** 本轮回复结束：尾巴整句合成，播完即收。 */
  function endReplyStream(text) {
    if (speechEnded) {
      // 极端情况：一条 delta 回调都没走（正常流式不会），收尾时整段补上
      if (!String(text || '').trim()) return
      beginReplyStream()
    }
    if (text) appendReplyDelta(text)
    if (speechTail) {
      freezeSentence(speechTail)
      speechTail = ''
      speechSentences.value = speechFrozen.slice()
    }
    speechEnded = true
    wake()
  }

  function playBlob(el, blob) {
    return new Promise((resolve, reject) => {
      let settled = false
      const finish = (err) => {
        if (settled) return
        settled = true
        el.removeEventListener('ended', onEnded)
        el.removeEventListener('pause', onPause)
        el.removeEventListener('error', onError)
        if (err) reject(err)
        else resolve()
      }
      const onEnded = () => finish()
      const onPause = () => finish() // stopSpeaking 打断：直接收尾，泵看代次退出
      const onError = () => finish(new Error('audio element error'))
      el.addEventListener('ended', onEnded)
      el.addEventListener('pause', onPause)
      el.addEventListener('error', onError)
      if (objectUrl) URL.revokeObjectURL(objectUrl)
      objectUrl = URL.createObjectURL(blob)
      el.src = objectUrl
      el.play().catch(finish)
    })
  }

  async function runSpeechPump(epoch) {
    const el = ensureAudio()
    let index = 0
    let playing = false
    try {
      for (;;) {
        if (epoch !== speakEpoch) return
        if (index < speechFrozen.length) {
          const blob = await (speechTasks.get(index) || Promise.resolve(null))
          if (epoch !== speakEpoch) return
          speechIndex.value = index
          if (blob) {
            if (!playing) { setGate(true); playing = true }
            preparing.value = false
            speaking.value = true
            await playBlob(el, blob)
            if (epoch !== speakEpoch) return
          }
          index += 1
          continue
        }
        if (speechEnded) break
        await waitWake()
      }
      speaking.value = false
      preparing.value = false
      speechSentences.value = []
      speechIndex.value = 0
      if (playing) {
        if (gateTailTimer) clearTimeout(gateTailTimer)
        gateTailTimer = setTimeout(() => {
          gateTailTimer = null
          if (epoch === speakEpoch && !speaking.value) setGate(false)
        }, GATE_TAIL_MS)
      }
    } catch (err) {
      if (epoch !== speakEpoch) return
      console.error('[voice] playback failed', err)
      speaking.value = false
      preparing.value = false
      setGate(false)
      onError?.('语音播放失败')
    }
  }

  function stopSpeaking() {
    speakEpoch += 1
    speechEnded = true
    speechFrozen = []
    speechTail = ''
    speechConsumed = 0
    speechTasks = new Map()
    speechSentences.value = []
    speechIndex.value = 0
    preparing.value = false
    speaking.value = false
    wake()
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
    preparing,
    speechSentences,
    speechIndex,
    holding,
    hasHeld,
    refresh,
    start,
    startHold,
    endHold,
    stop,
    cancel,
    appendReplyDelta,
    endReplyStream,
    stopSpeaking
  }
}
