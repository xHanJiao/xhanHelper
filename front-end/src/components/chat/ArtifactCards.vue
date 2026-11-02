<script setup>
import { computed, onBeforeUnmount, ref } from 'vue'
import { useRouter } from 'vue-router'

// 一轮多卡：收起时只露第一张 + 窄条「＋N」，点开横滑全部；展开后 8 秒自动收回，
// 鼠标/焦点在条子里时暂停计时（与已确认的 UI 形态一致）。
const props = defineProps({
  items: { type: Array, default: () => [] }
})

const router = useRouter()
const expanded = ref(false)
const AUTO_COLLAPSE_MS = 8000
let timer = null

const visible = computed(() => (expanded.value ? props.items : props.items.slice(0, 1)))

function clearTimer() {
  if (timer) {
    clearTimeout(timer)
    timer = null
  }
}

function startTimer() {
  clearTimer()
  timer = setTimeout(() => { expanded.value = false }, AUTO_COLLAPSE_MS)
}

function toggle() {
  expanded.value = !expanded.value
  if (expanded.value) startTimer()
  else clearTimer()
}

function pause() {
  if (expanded.value) clearTimer()
}

function resume() {
  if (expanded.value && !timer) startTimer()
}

function open(id) {
  router.push(`/artifacts/${id}`)
}

onBeforeUnmount(clearTimer)
</script>

<template>
  <div
    v-if="items.length"
    class="cards"
    :class="{ expanded }"
    @mouseenter="pause"
    @mouseleave="resume"
    @focusin="pause"
    @focusout="resume"
  >
    <button
      v-for="a in visible"
      :key="a.id"
      type="button"
      class="card"
      @click="open(a.id)"
    >
      <span class="card-title">{{ a.title || '未命名页面' }}</span>
      <span v-if="a.summary" class="card-summary">{{ a.summary }}</span>
      <span class="card-meta">灵阅 · HTML</span>
    </button>

    <button v-if="items.length > 1" type="button" class="more" @click="toggle">
      {{ expanded ? '收起' : `＋${items.length - 1}` }}
    </button>
  </div>
</template>

<style scoped>
.cards {
  display: flex;
  align-items: stretch;
  gap: 8px;
  margin-top: 8px;
  max-width: 100%;
  overflow-x: auto;
  scrollbar-width: none;
}
.cards::-webkit-scrollbar { display: none; }
.card {
  flex: 0 0 auto;
  width: 220px;
  height: 96px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 12px;
  border: 0.5px solid var(--color-border);
  border-radius: 11px;
  background: var(--color-input);
  text-align: left;
  cursor: pointer;
  transition: background var(--transition-fast), border-color var(--transition-fast);
}
.card:hover {
  background: var(--color-primary-soft);
  border-color: var(--color-border-strong);
}
.card-title {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.card-summary {
  font-size: 11.5px;
  line-height: 1.5;
  color: var(--color-text-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.card-meta {
  margin-top: auto;
  font-size: 10px;
  color: var(--color-text-muted);
}
.more {
  flex: 0 0 auto;
  width: 46px;
  height: 96px;
  border: 0.5px dashed var(--color-border-strong);
  border-radius: 11px;
  background: transparent;
  color: var(--color-text-muted);
  font-size: 12px;
  cursor: pointer;
}
.more:hover { background: var(--color-primary-soft); color: var(--color-primary); }
@media (max-width: 1099px) {
  .card { width: 200px; }
}
</style>
