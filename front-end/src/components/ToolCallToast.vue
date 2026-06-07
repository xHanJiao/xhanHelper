<script setup>
// ============================================================
// ToolCallToast — 工具调用 & 结果检验气泡
// ============================================================
//
// 两套独立的动画系统，共用一个容器：
//
// 1. 工具调用气泡
//    后端发来 tool_call 事件 → 逐个入队 → 每个气泡播一遍 enter/stay/exit 动画
//    同一时间只展示一个，其余排队。
//
// 2. 结果检验气泡（verify）
//    调过工具后，后端对最终回复做质量检验。流程：
//      checking → retry（未通过，模型重新生成）
//      checking → done（通过，接下来推正文）
//    checking 期间气泡持续显示（不自动消失），retry/done 后正文或新工具到达时退场。
//
// 动画生命周期（两个系统共用）：
//   enter(0.3s) → stay(2s / 无限) → exit(0.3s)
//   每个阶段对应一个 CSS class 驱动 transition。
//
// 并发取消（两个系统共用）：
//   不用 setTimeout 嵌套 + clearTimeout，
//   改用 async/await + 版本号（seq）。每次启动新动画时版本号 +1，
//   旧动画在 await 之后发现版本号不匹配就自动退出。
// ============================================================

import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { useChatStore } from '@/stores/chat'
import { buildToolSummary } from '@/composables/useChatSSE'

const chat = useChatStore()

// ---- 工具气泡 ----

const queue = ref([])       // 待展示的工具调用队列
const current = ref(null)   // 当前正在展示的工具调用
const phase = ref('')       // 当前动画阶段：'' | 'enter' | 'stay' | 'exit'
let toolSeq = 0             // 版本号，+1 取消当前动画
let processedCount = 0      // 已入队的工具数，避免重复入队

// ---- 公共常量 ----

const ENTER_MS = 300
const STAY_MS = 2000
const EXIT_MS = 300

const sleep = ms => new Promise(r => setTimeout(r, ms))

// ---- 结果检验气泡 ----

// ---- 状态提示气泡（「正在分析结果...」） ----

const statusPhase = ref('')
const showStatus = ref(false)
let statusSeq = 0

// 后端发来 status 事件 → 显示气泡，正文到了（status 变 null）→ 退场
watch(() => chat.statusMessage, async (msg) => {
  if (!msg) {
    if (showStatus.value) {
      statusSeq++
      statusPhase.value = 'exit'
      await sleep(EXIT_MS)
      showStatus.value = false
      statusPhase.value = ''
    }
    return
  }
  const seq = ++statusSeq
  showStatus.value = true
  statusPhase.value = 'enter'
  await sleep(ENTER_MS)
  if (seq !== statusSeq) return
  statusPhase.value = 'stay'
})


const verifyStatus = computed(() => chat.streamingMessage?.verifyStatus)

// 根据后端 verify 事件的状态码生成展示文字
const verifyText = computed(() => {
  if (!verifyStatus.value) return ''
  switch (verifyStatus.value.status) {
    case 'checking': return `结果检查中...（第${verifyStatus.value.retry}次）`
    case 'done': return '结果检查 ✓'
    case 'retry': return `检查未通过，正在完善...（第${verifyStatus.value.retry}次）`
    default: return ''
  }
})

const verifyPhase = ref('') // 动画阶段：'' | 'enter' | 'stay' | 'exit'
const showVerify = ref(false)
let verifySeq = 0           // 版本号，+1 取消当前动画
let lastVerifyKey = ''      // 上次的 status+retry 组合，用于去重
let verifyDismissed = false // 正文/工具到达后设为 true，阻止后续 verify 事件再弹出气泡

// 启动 verify 气泡动画：enter → stay
// checking/retry 状态下 stay 不自动退出，只有 done 才自动退
async function startVerifyAnimation() {
  const seq = ++verifySeq
  showVerify.value = true
  verifyPhase.value = 'enter'
  await sleep(ENTER_MS)
  if (seq !== verifySeq) return  // 被取消

  verifyPhase.value = 'stay'
  if (verifyStatus.value?.status === 'done') {
    await sleep(STAY_MS)
    if (seq !== verifySeq) return
    await exitVerifyBubble()      // done：展示 2s 后退出
  }
  // checking/retry：stay 无限等待，外部事件触发退出
}

// verify 气泡退场动画
async function exitVerifyBubble() {
  verifyPhase.value = 'exit'
  await sleep(EXIT_MS)
  showVerify.value = false
  verifyPhase.value = ''
}

// 外部强制关闭 verify 气泡（正文到达 / 新工具到达）
// 同时置位 verifyDismissed，阻止后续 done/retry 等事件重新弹出气泡
function stopVerifyAnimation() {
  if (showVerify.value) {
    verifySeq++
    verifyDismissed = true
    exitVerifyBubble()
  }
}

// done 来了 → 取消 checking 的无限 stay → 倒数 STAY_MS → 退出
async function startVerifyExit() {
  verifySeq++
  const seq = ++verifySeq
  await sleep(STAY_MS)
  if (seq !== verifySeq) return
  await exitVerifyBubble()
}

// ---- verify 状态变化监听 ----
watch(verifyStatus, (vs) => {
  if (!vs) {
    lastVerifyKey = ''
    return
  }
  // 正文/工具到达后已强制退场，后续 verify 事件不再弹出气泡
  if (verifyDismissed) return

  // 同一状态不重复触发
  const key = vs.status + '|' + vs.retry
  if (key === lastVerifyKey) return
  lastVerifyKey = key

  // 气泡已在展示：不重启动画，verifyText 计算属性会自动更新文字
  if (showVerify.value) {
    if (vs.status === 'done' && verifyPhase.value === 'stay') {
      startVerifyExit()  // checking → done：开始退出倒计时
    }
    return  // retry：文字自动更新，气泡留着等正文/工具到达再关
  }

  // 工具气泡正在展示，先让工具播完
  if (phase.value !== '' || current.value) return

  startVerifyAnimation()
})

// verify 气泡退场后 → 处理积压的工具调用
watch(showVerify, (showing) => {
  if (!showing) {
    const tcs = chat.streamingMessage?.toolCalls
    if (tcs && processedCount < tcs.length) {
      for (let i = processedCount; i < tcs.length; i++) {
        enqueue(tcs[i])
      }
      processedCount = tcs.length
    }
  }
})

// 正文开始推送 → 所有气泡退场（工具、verify、status），内容才是主角
watch(() => chat.streamingMessage?.content, (newVal) => {
  if (!newVal) return
  // 关 verify
  if (showVerify.value && verifyStatus.value?.status !== 'checking') {
    stopVerifyAnimation()
  }
  // 清工具队列 + 关当前工具气泡
  if (current.value || queue.value.length > 0) {
    queue.value = []
    toolSeq++
    if (current.value) {
      current.value = null
      phase.value = ''
    }
  }
})

// 新工具调用到达 → 同上，非 checking 则退场
watch(() => chat.streamingMessage?.toolCalls?.length, (newVal) => {
  if (newVal && showVerify.value && verifyStatus.value?.status !== 'checking') {
    stopVerifyAnimation()
  }
})

// ---- 工具气泡动画 ----

// 当前展示的工具气泡摘要
const summary = computed(() => {
  if (!current.value) return ''
  return buildToolSummary(current.value, null)
})

// 工具调用入队
function enqueue(tc) {
  queue.value.push(tc)
  if (phase.value === '' && !current.value) {
    startNext()  // 当前无动画，立刻开播
  }
}

// 播放下一个工具气泡：enter → stay → 自动 exit
async function startNext() {
  if (queue.value.length === 0) {
    // 工具队列清空，检查是否有 verify 待展示
    if (lastVerifyKey && !showVerify.value && !current.value && phase.value === '') {
      startVerifyAnimation()
    }
    return
  }
  const tc = queue.value.shift()
  current.value = tc

  const seq = ++toolSeq
  phase.value = 'enter'
  await sleep(ENTER_MS)
  if (seq !== toolSeq) return

  phase.value = 'stay'
  await sleep(STAY_MS)
  if (seq !== toolSeq) return

  await beginExit()
}

// 工具气泡退场，退完后自动播下一个
async function beginExit() {
  phase.value = 'exit'
  await sleep(EXIT_MS)
  current.value = null
  phase.value = ''
  startNext()
}

// 流结束时清空队列 + 关 verify
function flush() {
  queue.value = []
  toolSeq++
  stopVerifyAnimation()
  if (phase.value && phase.value !== 'exit') {
    beginExit()
  }
}

// 新对话开始时重置所有状态
function reset() {
  queue.value = []
  processedCount = 0
  toolSeq++
  verifySeq++
  current.value = null
  phase.value = ''
  showVerify.value = false
  verifyPhase.value = ''
  lastVerifyKey = ''
  verifyDismissed = false
  showStatus.value = false
  statusPhase.value = ''
  statusSeq++
}

// ---- 外部数据源监听 ----

// 后端流式推送 tool_call 事件 → 入队
watch(() => chat.streamingMessage?.toolCalls?.length, (newLen) => {
  if (!chat.streamingMessage || !newLen) return
  const tcs = chat.streamingMessage.toolCalls
  for (let i = processedCount; i < tcs.length; i++) {
    enqueue(tcs[i])
  }
  processedCount = tcs.length
})

// 对话流状态切换 → 重置或刷出
watch(() => chat.isStreaming, (val) => {
  if (val) {
    reset()
  } else {
    processedCount = 0
    flush()
  }
})

onBeforeUnmount(() => {
  toolSeq++
  verifySeq++
})
</script>

<template>
  <div v-if="current || showVerify || showStatus" class="tool-toast-anchor">
    <div v-if="showVerify" class="tool-toast-bubble" :class="'phase-' + verifyPhase">
      <span class="tool-toast-icon verify-icon">&#128269;</span>
      <span class="tool-toast-text">{{ verifyText }}</span>
    </div>
    <div v-if="showStatus" class="tool-toast-bubble" :class="'phase-' + statusPhase">
      <span class="tool-toast-icon">&#9203;</span>
      <span class="tool-toast-text">{{ chat.statusMessage }}</span>
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
