<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import AuthImage from '@/components/media/AuthImage.vue'
import { prefetchImage } from '@/components/media/imageBlobCache'

const props = defineProps({
  // 单图用法（文档配图等）：不传 sources 时只用 src
  src: { type: String, default: '' },
  // 多图用法（聊天图片）：给了就用它，方向键/左右按钮/滑动切换
  sources: { type: Array, default: () => [] },
  startIndex: { type: Number, default: 0 },
  visible: { type: Boolean, default: false }
})
const emit = defineEmits(['close'])

const list = computed(() => (props.sources.length ? props.sources : [props.src]).filter(Boolean))
const hasMany = computed(() => list.value.length > 1)
const current = ref(0)
// 真正在 <img> 里的地址：切换时先滑出、等图透明了再换，避免原地换 src 的硬切
const shownSrc = ref('')
const frameEl = ref(null)
const switching = ref(false)
let anim = null

const reducedMotion = () => window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false

function clamp(i) {
  if (!list.value.length) return 0
  return Math.min(Math.max(i, 0), list.value.length - 1)
}

function prefetchNeighbors() {
  if (!hasMany.value) return
  prefetchImage(list.value[current.value - 1])
  prefetchImage(list.value[current.value + 1])
}

// 滑出 / 滑入各一小段：中间那一刻（opacity 0）才换图，所以看不到尺寸跳变
function animate(delta, out) {
  const el = frameEl.value
  if (!el || reducedMotion()) return Promise.resolve()
  const shift = out ? -delta * 48 : delta * 48
  const frames = out
    ? [{ transform: 'none', opacity: 1 }, { transform: `translateX(${shift}px)`, opacity: 0 }]
    : [{ transform: `translateX(${shift}px)`, opacity: 0 }, { transform: 'none', opacity: 1 }]
  anim?.cancel() // 上一段（滑出）的 fill 状态必须先撤，否则滑入会从它接着走
  anim = el.animate(frames, { duration: 140, easing: 'ease-out', fill: 'forwards' })
  return anim.finished.catch(() => {}) // 被打断就按已完成处理
}

async function step(delta) {
  if (!hasMany.value || switching.value) return
  const next = clamp(current.value + delta)
  if (next === current.value) return
  switching.value = true
  try {
    await animate(delta, true)
    current.value = next
    shownSrc.value = list.value[next] || ''
    await nextTick() // 让新图先上屏，再做滑入
    prefetchNeighbors()
    await animate(delta, false)
  } finally {
    anim?.cancel() // 回到自然样式（opacity 1、无位移），观感与最后一帧一致
    anim = null
    switching.value = false
  }
}

// 每次打开都从被点的那张开始，顺手把左右两张先拉进缓存
watch([() => props.visible, () => props.startIndex], ([vis]) => {
  if (!vis) return
  current.value = clamp(props.startIndex)
  shownSrc.value = list.value[current.value] || ''
  anim?.cancel()
  anim = null
  prefetchNeighbors()
}, { immediate: true })

// 手机滑动：横向位移够大且不是竖着划，就切图；
// 滑动后短时间内的 click 当手滑处理，免得滑完顺手把浮层关了
let touchStartX = 0
let touchStartY = 0
let swipeEndedAt = 0

function onTouchStart(e) {
  const t = e.changedTouches[0]
  touchStartX = t.clientX
  touchStartY = t.clientY
}

function onTouchEnd(e) {
  const t = e.changedTouches[0]
  const dx = t.clientX - touchStartX
  const dy = t.clientY - touchStartY
  if (Math.abs(dx) > 40 && Math.abs(dx) > Math.abs(dy)) {
    step(dx < 0 ? 1 : -1)
    swipeEndedAt = Date.now()
  }
}

// 点画面任意处关闭（含图本身）；按钮/计数各自 stop
function onTap() {
  if (Date.now() - swipeEndedAt < 350) return
  emit('close')
}

function onKey(e) {
  if (!props.visible) return
  if (e.key === 'Escape') {
    emit('close')
  } else if (e.key === 'ArrowLeft') {
    e.preventDefault()
    step(-1)
  } else if (e.key === 'ArrowRight') {
    e.preventDefault()
    step(1)
  }
}

onMounted(() => document.addEventListener('keydown', onKey))
onBeforeUnmount(() => document.removeEventListener('keydown', onKey))
</script>

<template>
  <Teleport to="body">
    <div
      v-if="visible"
      class="lightbox"
      @click="onTap"
      @touchstart.passive="onTouchStart"
      @touchend="onTouchEnd"
    >
      <div ref="frameEl" class="frame">
        <AuthImage :src="shownSrc" />
      </div>
      <button class="close" type="button" aria-label="关闭" @click.stop="emit('close')">×</button>
      <template v-if="hasMany">
        <button
          class="nav prev"
          type="button"
          aria-label="上一张"
          :disabled="current === 0"
          @click.stop="step(-1)"
        >‹</button>
        <button
          class="nav next"
          type="button"
          aria-label="下一张"
          :disabled="current === list.length - 1"
          @click.stop="step(1)"
        >›</button>
        <div class="counter" @click.stop>{{ current + 1 }} / {{ list.length }}</div>
      </template>
    </div>
  </Teleport>
</template>

<style scoped>
.lightbox {
  position: fixed;
  inset: 0;
  z-index: 3000;
  display: grid;
  place-items: center;
  padding: 24px;
  background: rgba(0, 0, 0, 0.72);
  backdrop-filter: blur(6px);
  -webkit-backdrop-filter: blur(6px);
  /* 滑动切图时别带着背景一起滚/缩 */
  touch-action: none;
}
.frame {
  max-width: min(92vw, 1100px);
  max-height: 88dvh;
}
.frame :deep(img) {
  max-width: min(92vw, 1100px);
  max-height: 88dvh;
  width: auto;
  height: auto;
  object-fit: contain;
  border-radius: 12px;
  box-shadow: 0 18px 50px rgba(0, 0, 0, 0.42);
}
.frame :deep(.ph) { width: 240px; height: 160px; border-radius: 12px; }
/* 关闭键固定在屏幕右上（不被图片尺寸带着走，也不被刘海/状态栏压住） */
.close {
  position: absolute;
  top: calc(env(safe-area-inset-top, 0px) + 12px);
  right: 12px;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  border: 0.5px solid rgba(255, 255, 255, 0.3);
  background: rgba(30, 30, 32, 0.86);
  color: #fff;
  font-size: 18px;
  line-height: 1;
  cursor: pointer;
}
.nav {
  position: absolute;
  top: 50%;
  transform: translateY(-50%);
  width: 44px;
  height: 44px;
  border-radius: 50%;
  border: 0.5px solid rgba(255, 255, 255, 0.3);
  background: rgba(30, 30, 32, 0.86);
  color: #fff;
  font-size: 24px;
  line-height: 1;
  cursor: pointer;
}
.nav.prev { left: 12px; }
.nav.next { right: 12px; }
.nav:disabled { opacity: 0.3; cursor: default; }
.counter {
  position: absolute;
  top: calc(env(safe-area-inset-top, 0px) + 20px);
  left: 50%;
  transform: translateX(-50%);
  padding: 3px 9px;
  border-radius: 999px;
  background: rgba(30, 30, 32, 0.86);
  color: #fff;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
</style>
