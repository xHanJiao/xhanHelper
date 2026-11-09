<script setup>
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

// 输入框里的语音波形带：以中线镜像填充的一根刺状波形，原地呼吸（只变振幅）。
// 与 macOS 的 VoiceWaveLine 同一套数学与参数，见 docs/design/voice-chat/mockup.html。
const props = defineProps({
  phase: { type: String, required: true }, // listening | thinking | speaking
  level: { type: Number, default: 0 }
})
const emit = defineEmits(['interrupt'])

// 定稿参数：听 / 说 = 幅度 1.00 / 响应 2.00 / 波包 1.15 / 静息 1.00 / 横向 0.05；
// 想 = 同一组参数、幅度 ×0.8。
const AMP = 1.0
const RESPONSE = 2.0
const PACKET = 1.15
const WOBBLE = 1.0
const DRIFT = 0.05
const THINK = 0.8

const canvasEl = ref(null)

let raf = 0
let ctx = null
let cssW = 0
let cssH = 0
let accent = '#2F7D63'
let secondary = '#41C9E2'
let reduce = false
let amp = 2
let lastT = 0
const HALVES = new Float64Array(241) // N = 240，逐帧复用，不新建数组
let phaseAt = -999
let prevPhase = props.phase
let observer = null
let themeObserver = null

function readColors() {
  const cs = getComputedStyle(document.documentElement)
  accent = cs.getPropertyValue('--app-accent').trim() || accent
  secondary = cs.getPropertyValue('--accent-secondary').trim() || secondary
}

function resize() {
  const el = canvasEl.value
  if (!el) return
  const dpr = window.devicePixelRatio || 1
  const rect = el.getBoundingClientRect()
  cssW = Math.max(1, rect.width)
  cssH = Math.max(1, rect.height)
  el.width = Math.round(cssW * dpr)
  el.height = Math.round(cssH * dpr)
  ctx = el.getContext('2d')
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
}

// 值噪声：平滑插值的随机序列，做出真实录音那种稀疏尖峰
function hash1(n, seed) {
  const v = Math.sin(n * 127.1 + seed * 311.7) * 43758.5453
  return v - Math.floor(v)
}
function vnoise(v, seed) {
  const i = Math.floor(v)
  const f = v - i
  const u = f * f * (3 - 2 * f)
  return hash1(i, seed) * (1 - u) + hash1(i + 1, seed) * u
}
function noise(x, t, seed) {
  const a = vnoise(x * 70 + t, seed)
  const b = vnoise(x * 190 - t * 0.9, seed + 7.3)
  const n = Math.pow(0.7 * a + 0.3 * b, 2.0)
  const swell = 0.55 + 0.45 * vnoise(x * 6 + t * 0.55, seed + 3)
  return n * swell
}
function cadence(t) {
  return (0.5 + 0.5 * Math.sin(t * 3.6)) * (0.3 + 0.7 * Math.abs(Math.sin(t * 1.7 + 0.7)))
}

// 半高（中线到边缘），两端用 sin^0.55 收口
function halfFor(p, x, t) {
  const env = Math.pow(Math.sin(Math.PI * x), 0.55)
  if (p === 'thinking') {
    const period = 2.4 / PACKET
    const pos = reduce ? 0.5 : -0.14 + ((t % period) / period) * 1.28
    const g = Math.exp(-Math.pow((x - pos) / 0.16, 2))
    const n = noise(x, reduce ? 0 : t * 3.6 * RESPONSE * DRIFT, 3.1)
    return 7 * AMP * THINK * g * (0.3 + 0.7 * n)
  }
  const a = Math.sqrt(Math.min(1, Math.max(0, props.level / 0.22)))
  const sp = p === 'listening' ? 1.4 + 3.2 * a : 2 + 3.5 * cadence(t)
  const n = noise(x, reduce ? 0 : t * sp * RESPONSE * DRIFT, p === 'speaking' ? 5.7 : 2.3)
  return amp * env * (0.08 + 0.92 * n)
}

function clampHalf(h) {
  return Math.min(cssH / 2 - 1, Math.max(0.55, h)) // 0.55 保证静音时也有一条细线
}

function targetAmp(t) {
  if (props.phase === 'thinking') return 0
  if (props.phase === 'speaking') {
    // 两条不可通约的正弦相乘：像说话的重音，而不是节拍器
    return (8 + 12 * cadence(t)) * AMP
  }
  const a = Math.sqrt(Math.min(1, Math.max(0, props.level / 0.22)))
  const idle = (2.6 + 0.8 * WOBBLE * Math.sin(t * 0.7)) * AMP
  const active = (2 + 12 * a) * AMP
  const w = Math.min(1, a / 0.2) // 静息呼吸与说话电平平滑衔接
  return idle * (1 - w) + active * w
}

function draw(t) {
  if (!ctx) return
  ctx.clearRect(0, 0, cssW, cssH)
  const mid = cssH / 2
  const m = Math.min(1, Math.max(0, (t - phaseAt) / 0.22))
  const ease = m < 0.5 ? 2 * m * m : 1 - Math.pow(-2 * m + 2, 2) / 2
  const N = 240
  const px = (x) => 6 + x * (cssW - 12)
  // 稳态下 prevPhase == phase，只算一套；交叉淡化才两套都算
  const mixing = prevPhase !== props.phase && ease < 1
  for (let i = 0; i <= N; i++) {
    const x = i / N
    const current = halfFor(props.phase, x, t)
    const h = mixing ? halfFor(prevPhase, x, t) * (1 - ease) + current * ease : current
    HALVES[i] = clampHalf(h)
  }

  ctx.beginPath()
  for (let i = 0; i <= N; i++) {
    if (i === 0) ctx.moveTo(px(i / N), mid - HALVES[i])
    else ctx.lineTo(px(i / N), mid - HALVES[i])
  }
  // 下半边镜像回去，闭合填充成一条有厚度的波形带
  for (let i = N; i >= 0; i--) {
    ctx.lineTo(px(i / N), mid + HALVES[i])
  }
  ctx.closePath()

  ctx.save()
  if (props.phase === 'speaking' && !reduce) {
    ctx.shadowBlur = 6
    ctx.shadowColor = 'rgba(65,201,226,0.4)'
  }
  ctx.globalAlpha = props.phase === 'thinking' ? 0.55 : 1
  ctx.fillStyle = props.phase === 'speaking' ? secondary : accent
  ctx.fill()
  ctx.restore()
}

function interrupt() {
  if (props.phase === 'speaking') emit('interrupt')
}

function frame(now) {
  raf = requestAnimationFrame(frame)
  const dt = Math.min(0.05, Math.max(0.001, lastT ? (now - lastT) / 1000 : 0.016))
  lastT = now
  const t = reduce ? 0 : now / 1000
  const target = targetAmp(t)
  // 振幅快起慢落：响应 2.0 → 起 15ms / 落 60ms
  const tau = (target > amp ? 0.03 : 0.12) / RESPONSE
  amp += (target - amp) * (1 - Math.exp(-dt / tau))
  draw(t)
}

watch(() => props.phase, (next, old) => {
  prevPhase = old || next
  phaseAt = reduce ? -999 : lastT / 1000
})

onMounted(() => {
  reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
  readColors()
  resize()
  observer = new ResizeObserver(resize)
  if (canvasEl.value) observer.observe(canvasEl.value)
  themeObserver = new MutationObserver(readColors)
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class', 'data-theme'] })
  raf = requestAnimationFrame(frame)
})

onBeforeUnmount(() => {
  cancelAnimationFrame(raf)
  observer?.disconnect()
  themeObserver?.disconnect()
})
</script>

<template>
  <div
    class="voice-wave"
    :class="`is-${phase}`"
    :role="phase === 'speaking' ? 'button' : undefined"
    :tabindex="phase === 'speaking' ? 0 : -1"
    :aria-label="phase === 'speaking' ? '语音：正在朗读，点一下打断' : '语音会话中'"
    @click="interrupt"
    @keydown.enter.prevent="interrupt"
    @keydown.space.prevent="interrupt"
  >
    <canvas ref="canvasEl" class="voice-wave-canvas" />
  </div>
</template>

<style scoped>
.voice-wave {
  height: 36px;
  width: 100%;
  -webkit-tap-highlight-color: transparent;
  user-select: none;
}

.voice-wave.is-speaking {
  cursor: pointer;
}

.voice-wave:focus-visible {
  outline: 2px solid var(--app-accent);
  outline-offset: 4px;
  border-radius: 4px;
}

.voice-wave-canvas {
  display: block;
  width: 100%;
  height: 36px;
}
</style>
