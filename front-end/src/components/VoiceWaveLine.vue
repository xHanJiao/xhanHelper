<script setup>
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

// 输入框里的语音音波线：一根正弦曲线，原地呼吸（相位锁定，只有振幅在变）。
// 与 macOS 的 VoiceWaveLine 同一套数学与参数，见 docs/design/voice-chat/mockup.html。
const props = defineProps({
  phase: { type: String, required: true }, // listening | thinking | speaking
  level: { type: Number, default: 0 }
})
const emit = defineEmits(['interrupt'])

// 定稿参数：幅度 0.80 / 响应 2.00 / 波包 1.15
const AMP = 0.8
const RESPONSE = 2.0
const PACKET = 1.15
const WAVES = 2.2
const STROKE = 1.6

const WORD = { listening: '听', thinking: '想', speaking: '说' }

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

// 单条正弦：以横轴中点做中心对称（f(0.5+d) = -f(0.5-d)），两端收口
function yAt(phase, x, t) {
  const k = Math.PI * 2 * WAVES
  if (phase === 'thinking') {
    const period = 3.4 / PACKET
    const pos = reduce ? 0.5 : -0.16 + ((t % period) / period) * 1.32
    const g = Math.exp(-Math.pow((x - pos) / 0.19, 2))
    const wave = Math.sin((x - pos) * k - (reduce ? 0 : t * 1.4 * PACKET))
    return 12.5 * AMP * g * wave
  }
  const env = Math.pow(Math.sin(Math.PI * x), 0.55)
  return amp * env * Math.sin(k * (x - 0.5))
}

function targetAmp(t) {
  if (props.phase === 'thinking') return 0
  if (props.phase === 'speaking') {
    // 两条不可通约的正弦相乘：像说话的重音，而不是节拍器
    const cadence = (0.5 + 0.5 * Math.sin(t * 3.6)) * (0.3 + 0.7 * Math.abs(Math.sin(t * 1.7 + 0.7)))
    return (5 + 9 * cadence) * AMP
  }
  const a = Math.sqrt(Math.min(1, Math.max(0, props.level / 0.22)))
  return (2 + 12 * a) * AMP + 0.5 * Math.sin(t * 0.7)
}

function draw(t) {
  if (!ctx) return
  ctx.clearRect(0, 0, cssW, cssH)
  const mid = cssH / 2
  const m = Math.min(1, Math.max(0, (t - phaseAt) / 0.22))
  const ease = m < 0.5 ? 2 * m * m : 1 - Math.pow(-2 * m + 2, 2) / 2
  const N = 200
  ctx.beginPath()
  for (let i = 0; i <= N; i++) {
    const x = i / N
    const y = yAt(prevPhase, x, t) * (1 - ease) + yAt(props.phase, x, t) * ease
    const px = 6 + x * (cssW - 12)
    const py = mid + Math.min(mid - 1, Math.max(-(mid - 1), y))
    if (i === 0) ctx.moveTo(px, py)
    else ctx.lineTo(px, py)
  }
  ctx.save()
  if (props.phase === 'speaking' && !reduce) {
    ctx.shadowBlur = 6
    ctx.shadowColor = 'rgba(65,201,226,0.4)'
  }
  ctx.globalAlpha = props.phase === 'thinking' ? 0.55 : 1
  ctx.strokeStyle = props.phase === 'speaking' ? secondary : accent
  ctx.lineWidth = STROKE
  ctx.lineJoin = 'round'
  ctx.lineCap = 'round'
  ctx.stroke()
  ctx.restore()
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
    @click="phase === 'speaking' && emit('interrupt')"
  >
    <span class="voice-wave-word">{{ WORD[phase] }}</span>
    <canvas ref="canvasEl" class="voice-wave-canvas" />
    <span v-if="phase === 'speaking'" class="voice-wave-hint">点一下打断</span>
  </div>
</template>

<style scoped>
.voice-wave {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 36px;
  width: 100%;
}

.voice-wave.is-speaking {
  cursor: pointer;
}

.voice-wave-word {
  flex: 0 0 22px;
  font-size: 12px;
  color: var(--color-text-secondary);
}

.voice-wave-canvas {
  flex: 1 1 auto;
  height: 36px;
  display: block;
}

.voice-wave-hint {
  flex: 0 0 auto;
  font-size: 11px;
  color: var(--color-text-secondary);
  white-space: nowrap;
}
</style>
