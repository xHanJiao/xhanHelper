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

const verifyStatus = computed(() => chat.streamingMessage?.verifyStatus)

const verifyText = computed(() => {
  if (!verifyStatus.value) return ''
  switch (verifyStatus.value.status) {
    case 'checking': return `结果检查中...（第${verifyStatus.value.retry}次）`
    case 'done': return '结果检查 ✓'
    case 'retry': return `检查未通过，正在完善...（第${verifyStatus.value.retry}次）`
    default: return ''
  }
})

// verify 动画状态（独立于工具调用队列，与 verifyStatus 状态对应）
const verifyPhase = ref('')   // '' | 'enter' | 'stay' | 'exit'
const showVerify = ref(false) // 是否正在展示 verify 气泡
let verifyTimer = null
let lastVerifyKey = ''        // status+retry 组合，用于检测状态变化

function clearVerifyTimer() {
  if (verifyTimer) {
    clearTimeout(verifyTimer)
    verifyTimer = null
  }
}

function startVerifyAnimation() {
  clearVerifyTimer()
  showVerify.value = true
  verifyPhase.value = 'enter'

  verifyTimer = setTimeout(() => {
    verifyPhase.value = 'stay'
    verifyTimer = setTimeout(() => {
      verifyPhase.value = 'exit'
      verifyTimer = setTimeout(() => {
        showVerify.value = false
        verifyPhase.value = ''
      }, EXIT_MS)
    }, STAY_MS)
  }, ENTER_MS)
}

function stopVerifyAnimation() {
  clearVerifyTimer()
  if (showVerify.value && verifyPhase.value !== 'exit') {
    verifyPhase.value = 'exit'
    verifyTimer = setTimeout(() => {
      showVerify.value = false
      verifyPhase.value = ''
    }, EXIT_MS)
  }
}

watch(verifyStatus, (vs) => {
  if (!vs) {
    lastVerifyKey = ''
    return
  }
  const key = vs.status + '|' + vs.retry
  if (key === lastVerifyKey) return
  lastVerifyKey = key

  // 等工具气泡退场后再展示 verify，否则一起飘会很乱
  if (phase.value !== '' || current.value) {
    return
  }

  startVerifyAnimation()
})

watch(showVerify, (showing) => {
  if (!showing && lastVerifyKey) {
    // verify 动画播完，检查是否有积压的工具调用待处理
    const tcs = chat.streamingMessage?.toolCalls
    if (tcs && processedCount < tcs.length) {
      for (let i = processedCount; i < tcs.length; i++) {
        enqueue(tcs[i])
      }
      processedCount = tcs.length
    }
  }
})

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
  if (queue.value.length === 0) {
    // 工具队列清空，检查是否有积压的 verify 待展示
    if (lastVerifyKey && !showVerify.value && !current.value && phase.value === '') {
      startVerifyAnimation()
    }
    return
  }
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
  stopVerifyAnimation()
  if (phase.value && phase.value !== 'exit') {
    beginExit()
  }
}

function reset() {
  queue.value = []
  processedCount = 0
  clearTimer()
  clearVerifyTimer()
  current.value = null
  phase.value = ''
  showVerify.value = false
  verifyPhase.value = ''
  lastVerifyKey = ''
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
  clearVerifyTimer()
})
</script>

<template>
  <div v-if="current || showVerify" class="tool-toast-anchor">
    <div v-if="showVerify" class="tool-toast-bubble" :class="'phase-' + verifyPhase">
      <span class="tool-toast-icon verify-icon">&#128269;</span>
      <span class="tool-toast-text">{{ verifyText }}</span>
    </div>
    <div v-if="current" class="tool-toast-bubble" :class="'phase-' + phase">
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
