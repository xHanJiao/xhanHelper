<script setup>
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { useChatStore } from '@/stores/chat'
import { buildToolSummary } from '@/composables/useChatSSE'

const chat = useChatStore()

const queue = ref([])
const current = ref(null)
const phase = ref('') // '' | 'enter' | 'stay' | 'exit'
let phaseTimer = null
let processedCount = 0

const ENTER_MS = 300
const STAY_MS = 1000
const EXIT_MS = 300

const summary = computed(() => {
  if (!current.value) return ''
  return buildToolSummary(current.value, null)
})

function clearTimer() {
  if (phaseTimer) {
    clearTimeout(phaseTimer)
    phaseTimer = null
  }
}

function enqueue(tc) {
  queue.value.push(tc)
  if (phase.value === '' && !current.value) {
    startNext()
  }
}

function startNext() {
  if (queue.value.length === 0) return
  const tc = queue.value.shift()
  current.value = tc
  phase.value = 'enter'

  clearTimer()
  phaseTimer = setTimeout(() => {
    if (phase.value !== 'enter') return
    phase.value = 'stay'

    clearTimer()
    phaseTimer = setTimeout(() => {
      if (phase.value !== 'stay') return
      beginExit()
    }, STAY_MS)
  }, ENTER_MS)
}

function beginExit() {
  phase.value = 'exit'
  clearTimer()
  phaseTimer = setTimeout(() => {
    current.value = null
    phase.value = ''
    startNext()
  }, EXIT_MS)
}

function flush() {
  queue.value = []
  clearTimer()
  if (phase.value && phase.value !== 'exit') {
    beginExit()
  }
}

function reset() {
  queue.value = []
  processedCount = 0
  clearTimer()
  current.value = null
  phase.value = ''
}

watch(() => chat.streamingMessage?.toolCalls?.length, (newLen) => {
  if (!chat.streamingMessage || !newLen) return
  const tcs = chat.streamingMessage.toolCalls
  for (let i = processedCount; i < tcs.length; i++) {
    enqueue(tcs[i])
  }
  processedCount = tcs.length
})

watch(() => chat.isStreaming, (val) => {
  if (val) {
    reset()
  } else {
    processedCount = 0
    flush()
  }
})

onBeforeUnmount(() => {
  clearTimer()
})
</script>

<template>
  <div v-if="current" class="tool-toast-anchor">
    <div class="tool-toast-bubble" :class="'phase-' + phase">
      <span class="tool-toast-icon">&#9881;</span>
      <span class="tool-toast-text">{{ summary }}</span>
    </div>
  </div>
</template>

<style scoped>
.tool-toast-anchor {
  position: absolute;
  top: 100%;
  left: 0;
  right: 0;
  display: flex;
  pointer-events: none;
  padding: 0 var(--space-lg);
}

.tool-toast-bubble {
  display: flex;
  align-items: baseline;
  gap: 6px;
}

.phase-enter {
  animation: toast-slide-in 0.3s ease-out forwards;
}

.phase-stay {
  transform: translateX(0);
  opacity: 1;
}

.phase-exit {
  animation: toast-slide-out 0.3s ease-in forwards;
}

@keyframes toast-slide-in {
  from { transform: translateX(40px); opacity: 0; }
  to   { transform: translateX(0);    opacity: 1; }
}

@keyframes toast-slide-out {
  from { transform: translateX(0);    opacity: 1; }
  to   { transform: translateX(-40px); opacity: 0; }
}

.tool-toast-icon {
  font-size: 11px;
  flex-shrink: 0;
}

.tool-toast-text {
  font-size: 12px;
  color: var(--color-text-muted);
  line-height: 1.5;
}
</style>
