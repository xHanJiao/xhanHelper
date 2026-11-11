<script>
// 模块级（跨组件实例、整页加载只初始化一次）：
// 冷启动第一次落到 /chat 时定位最新会话；之后点顶栏「新对话」仍算空白新对话。
let coldStartPending = true
</script>

<script setup>
/**
 * ============================================================================
 * ChatView 异步生命周期总览
 * ============================================================================
 *
 * 本页面有多个异步行为，分布在文件各处。以下按触发时机分类汇总：
 *
 * ── 一、页面首次挂载 ───────────────────────────────────────────────────────
 *   onMounted (L38):
 *     → fetchLlmConfig()          GET /api/llm-config          填充 LLM 参数面板
 *     → fetchAgents()             GET /api/agents              填充智能体切换列表
 *
 * ── 二、路由变化（核心入口，immediate: true） ─────────────────────────────────
 *   watch(route.params.id) (L339):
 *     进入分支前统一 cancel() 旧 SSE 流（判据：目标会话 != 当前会话）
 *     【分支 A】切换到新会话 (newId !== convId)
 *       → loadHistory(newId)          GET messages-v2        拉取最近 7 条消息
 *       → fetchAssociatedMemories()   POST /memories/batch   批量获取关联记忆详情
 *       → stickToBottom = true + nextTick + scrollToBottom   滚动到底部（跟随意图重置为贴底）
 *       → setTimeout 200ms            激活上滑加载监听（防误触发）
 *       → fetchLlmConfig()            根据智能体重拉 LLM 参数
 *     【分支 B】同会话重载 (needsRefetch=true)
 *       → 流程同分支 A，完成后 chat.needsRefetch = false
 *     【分支 C】无会话 ID
 *       → 冷启动首次：fetchList() 后 router.replace 到列表第一条（最新会话）
 *       → 其余情况：chat.reset()（空白欢迎页，会话由首条消息懒创建；顶栏＋不走这里，它先建会话）
 *
 * ── 三、用户发送消息 ─────────────────────────────────────────────────────────
 *   handleSend() (L935):
 *     → 流式中 / 队列非空：enqueuePendingSend()  只进 pendingSends，等本轮结束再发
 *     → 空闲且队列空：dispatchOutgoing()         本地添加用户消息 + 调 SSE
 *     → 当前轮 done 后 drainQueue()              按序出队（失败则暂停）
 *
 * ── 四、用户上滑加载更多历史 ─────────────────────────────────────────────────
 *   handleScroll() (L497):
 *     → 距顶部 ≤ 15% 可视高度 + hasMoreBefore + 不在加载中
 *     → loadHistory(convId, 'before', oldestCursor)   分页加载更早消息
 *     → prependMessages + 恢复滚动位置
 *
 * ── 五、智能体切换 ───────────────────────────────────────────────────────────
 *   handleSelectAgent() (L130):
 *     → PATCH /api/conversations/:id         更新会话绑定的智能体
 *     → convs.updateAgent()                  更新会话列表 store
 *     → chat.agentId / chat.agentName         更新聊天 store
 *     → fetchLlmConfig()                     刷新 LLM 配置
 *
 * ── 六、流式响应期间（被动触发） ──────────────────────────────────────────────
 *   watch(chat.streamingMessage?.content)   (L953)  → scrollToBottom()（贴底才跟随）
 *   watch(chat.streamingMessage?.reasoning) (L954)  → scrollToBottom()（贴底才跟随）
 *   ResizeObserver(chat-column)             (L319)  → 内容异步长高（图片/markdown）时贴底
 *   watch(chat.llmParams)                   (L107)  → 同步更新 llmSettings
 *
 * ── 七、输入框收起 ──────────────────────────────────────────────────────────
 *   toggleInput()          → 手机上 ⌄ 按钮收起/展开整张输入卡（收起要同时 blur 退键盘）
 *   watch(inputVisible)    → nextTick + 触发 textarea input 事件（自适应高度）
 *   语音连续对话：麦克风按钮开关会话，判停一句自动提交一轮；浮层「打断」只停朗读
 *
 * ── 八、页面卸载 ─────────────────────────────────────────────────────────────
 *   onBeforeUnmount (L446): cancel() 中断正在进行的 SSE 流
 * ============================================================================
 */

import { ref, reactive, computed, watch, nextTick, onBeforeUnmount, onMounted, provide } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useChatStore } from '@/stores/chat'
import { useConversationsStore } from '@/stores/conversations'
import { useChatSSE } from '@/composables/useChatSSE'
import { useVoiceChat } from '@/composables/useVoiceChat'
import { useScrollTopBar } from '@/composables/useScrollTopBar'
import { useDevice } from '@/composables/useDevice'
import { api } from '@/api/client'
import {
  NInput,
  NButton,
  NTooltip,
  useMessage
} from 'naive-ui'
import MessageBubble from '@/components/MessageBubble.vue'
import VoiceWaveLine from '@/components/VoiceWaveLine.vue'
import ToolCallToast from '@/components/ToolCallToast.vue'
import SelectionActionBar from '@/components/SelectionActionBar.vue'
import AttachmentTray from '@/components/chat/AttachmentTray.vue'
import { prepareImage } from '@/utils/image'
import { waitForUploads } from '@/utils/voiceImages'
import { shouldDrainQueue } from '@/utils/promptQueue'
import { peekUnsentDraft, saveUnsentDraft, takeUnsentDraft } from '@/utils/unsentDraft'
import { useAuthStore } from '@/stores/auth'

import PageTopBar from '@/components/PageTopBar.vue'
import { useMobile } from '@/composables/useMobile'

const route = useRoute()
const router = useRouter()
const chat = useChatStore()
const convs = useConversationsStore()
const auth = useAuthStore()
const { sendMessage, cancel, resumeStream, setSuspended, clearNetworkError, finishStream } = useChatSSE()
const nMessage = useMessage()
const { isMobile } = useMobile()
const { isIOS } = useDevice()
// 语音：连续对话 / 录音发送（移动端长按说话、桌面端点击说话）两档。
// 连续档点麦克风开一条连续会话，判停一句就提交一轮、回复自动朗读；
// 录音档只在本地录音、松手/再点一下整段识别出字，不朗读。浮层「打断」只停朗读（会话保留），「结束对话」才收会话。
const {
  available: voiceAvailable,
  active: voiceActive,
  calling: voiceCalling,
  level: voiceLevel,
  speaking: voiceSpeaking,
  preparing: voicePreparing,
  refresh: refreshVoice,
  enter: enterVoice,
  switchMode: switchVoiceMode,
  stop: stopVoice,
  cancel: cancelVoice,
  speechSentences: voiceSpeechSentences,
  speechIndex: voiceSpeechIndex,
  holding: voiceHolding,
  mode: voiceMode,
  holdRecording: voiceHoldRecording,
  holdTranscribing: voiceHoldTranscribing,
  switching: voiceSwitching,
  hasHeld: voiceHasHeld,
  startHold: startVoiceHold,
  endHold: endVoiceHold,
  startHoldTalk: startVoiceHoldTalk,
  stopHoldTalk: stopVoiceHoldTalk,
  appendReplyDelta: appendVoiceReply,
  endReplyStream: endVoiceReply,
  stopSpeaking
} = useVoiceChat({
  onSentence: submitVoiceSentence,
  onHoldSentence: submitHoldSentence,
  onError: (message) => nMessage.error(message),
  onNotice: (message) => nMessage.info(message),
  isBusy: () => chat.isStreaming
})

// 语音条是否"占着"输入区。切档过渡期（旧链路已拆、新档还在建连）active 会是 false，
// 这时不能把波形条换回输入框，否则切一下闪一下。
const voiceBarOpen = computed(() => voiceActive.value || voiceSwitching.value)

// 音波条状态：说 > 想 > 听。「想」包含 TTS 合成期（preparing）与长按松手后的识别期，
// 否则回复流刚结束、音频还没到时会先闪回「听」。
const voicePhase = computed(() => {
  if (voiceSpeaking.value) return 'speaking'
  if (chat.isStreaming || voicePreparing.value || voiceHoldTranscribing.value) return 'thinking'
  return 'listening'
})

// 连续会话里「打断」只停朗读/停生成，麦克风不断（PWA 不做自动打断，靠点音波条）
function voiceInterrupt() {
  // 打断的主场景是本轮已流完、TTS 正在念：这时发 stop 会误杀下一轮，只停朗读就够
  stopSpeaking()
  if (chat.isStreaming) handleStop()
}

// 结束语音会话 = 停采集 + 停朗读 + 停生成：只停采集的话，正在跑的回复会在会话结束后
// 才朗读出来（onAssistantReply 还挂在那条流上）。
// 退出语音时 flush 出来的新一轮按普通消息提交：不朗读、也不随后被停掉
let voiceExitFlush = false

// 退出语音 = 停采集 + 停朗读 + 停生成。先停旧轮，再 flush 暂存（否则会把刚提交的新轮一起停掉）；
// flush 期间用 voiceExitFlush 标记，让提交走非语音路径（不挂 TTS 回调）。
function endVoiceSession() {
  if (chat.isStreaming) {
    // 有暂存要 flush 时不发 /api/chat/stop：迟到的 stop 会误杀刚提交的新轮，
    // 交给服务端 Register 顶掉旧 loop（与 submitVoiceSentence 同一策略）
    if (voiceHasHeld()) stopLocalStream()
    else handleStop()
  }
  voiceExitFlush = true
  stopVoice()
  voiceExitFlush = false
}

// 空格接管：连续档按住/松开暂存；录音档按一下开始、再按一下发送（桌面端）。
// 空格只在「不是在跟界面控件打交道」时接管：输入框、可编辑区、按钮、链接都算
function isTextEntryTarget(target) {
  if (!(target instanceof Element)) return false
  if (target.isContentEditable) return true
  return !!target.closest('input, textarea, button, a, [role="button"], [contenteditable="true"]')
}

function onVoiceKeyDown(e) {
  if (!voiceActive.value) return
  // Esc：录音中丢弃本次（移动端有上滑，桌面端靠它）
  if (e.key === 'Escape') {
    if (voiceHoldRecording.value) {
      e.preventDefault()
      holdCancelArmed.value = false
      stopVoiceHoldTalk({ cancelled: true })
    }
    return
  }
  if (e.code !== 'Space' || e.isComposing) return
  if (e.repeat) {
    // 按住期间的空格自动重复也要拦下，别漏给页面滚动
    if (voiceHolding.value || voiceHoldRecording.value) e.preventDefault()
    return
  }
  if (isTextEntryTarget(e.target)) return
  e.preventDefault()
  if (voiceMode.value === 'hold') {
    // 桌面端是两态按钮：空格也按「一下开始 / 一下发送」
    if (voiceHoldRecording.value) stopVoiceHoldTalk({ cancelled: false })
    else beginHoldTalk()
    return
  }
  if (!voiceHolding.value) startVoiceHold()
}

function onVoiceKeyUp(e) {
  if (!voiceActive.value || e.code !== 'Space') return
  // 连续档：先收本监听器开出的 hold，再看焦点：按住中途焦点移进输入框时，松开也必须收尾
  if (voiceHolding.value) {
    e.preventDefault()
    endVoiceHold()
    return
  }
  if (voiceMode.value === 'hold') return
  if (isTextEntryTarget(e.target)) return
  e.preventDefault()
  endVoiceHold()
}

function onVoiceWindowBlur() {
  if (voiceHolding.value) endVoiceHold()
  // 移动端按住录音时切走 = 手指不在了，丢弃；桌面端是两态录音，切窗口不能把录了一半的话丢掉
  if (isMobile.value) onHoldTalkCancel()
}

// 录音发送档：移动端按住录音、松手整段识别，上滑 60px 进取消区（松手丢弃）；
// 桌面端是两态按钮，点一下开始、再点一下发送，录音中左侧出现取消钮（Esc 同取消）。
const holdPointerStartY = ref(0)
const holdCancelArmed = ref(false)
// 松手后可能还在等图上传（最长 15 秒）：这期间再按一次会并发提交，后一条被
// sendMessage 的 isStreaming 静默丢掉（见 docs/bugs/061），所以先不接新的按下
const holdSubmitting = ref(false)

function beginHoldTalk() {
  if (holdSubmitting.value) return
  startVoiceHoldTalk()
}

function onHoldTalkPointerDown(e) {
  if (!isMobile.value || voiceMode.value !== 'hold') return
  // 只在移动端拦默认行为：桌面端靠 click 切两态，别把 click 也一并拦掉
  e.preventDefault()
  holdPointerStartY.value = e.clientY
  holdCancelArmed.value = false
  // 没有指针捕获的话，手指移出按钮后 pointerup 会丢（iOS PWA 尤其明显）
  e.currentTarget?.setPointerCapture?.(e.pointerId)
  beginHoldTalk()
}

function onHoldTalkPointerMove(e) {
  if (!isMobile.value || !voiceHoldRecording.value) return
  holdCancelArmed.value = holdPointerStartY.value - e.clientY > 60
}

function onHoldTalkPointerUp() {
  if (!isMobile.value || !voiceHoldRecording.value) return
  const cancelled = holdCancelArmed.value
  holdCancelArmed.value = false
  stopVoiceHoldTalk({ cancelled })
}

function onHoldTalkPointerCancel() {
  if (!isMobile.value || !voiceHoldRecording.value) return
  holdCancelArmed.value = false
  stopVoiceHoldTalk({ cancelled: true })
}

// 桌面端两态按钮：点一下开始录音，再点一下停止并发送
function onHoldTalkToggle() {
  if (isMobile.value || voiceMode.value !== 'hold') return
  if (voiceHoldRecording.value) stopVoiceHoldTalk({ cancelled: false })
  else beginHoldTalk()
}

function onHoldTalkCancel() {
  if (!voiceHoldRecording.value) return
  holdCancelArmed.value = false
  stopVoiceHoldTalk({ cancelled: true })
}

const holdTalkLabel = computed(() => {
  if (voiceHoldRecording.value) {
    if (!isMobile.value) return '点击发送'
    return holdCancelArmed.value ? '松开手指取消发送' : '松开发送'
  }
  return isMobile.value ? '长按说话' : '点击开始录音'
})

const holdTalkTooltip = computed(() => {
  if (voiceHoldRecording.value) {
    if (!isMobile.value) return '点击发送（Esc 取消）'
    return holdCancelArmed.value ? '松开手指，取消发送' : '松开发送'
  }
  return isMobile.value ? '长按说话：按住录音，松手出字（上滑取消）' : '点击开始录音（再点发送，Esc 取消）'
})

onMounted(() => {
  fetchAgents()
  refreshVoice()
  document.addEventListener('visibilitychange', onVisibilityChange)
  // 输入区高度变化（收起/展开/挂图）时重算「↓」的位置
  if (mobileInputRef.value) {
    composerObserver = new ResizeObserver(onLayoutResize)
    composerObserver.observe(mobileInputRef.value)
  }
  // 消息内容长高（图片加载完、markdown 异步渲染）也要贴底：这类变化没有任何 watcher 会触发
  if (messagesColumnRef.value) {
    contentObserver = new ResizeObserver(() => {
      if (stickToBottom) pinToBottom()
    })
    contentObserver.observe(messagesColumnRef.value)
  }
  window.addEventListener('resize', onLayoutResize)
  window.addEventListener('keydown', onVoiceKeyDown)
  window.addEventListener('keyup', onVoiceKeyUp)
  window.addEventListener('blur', onVoiceWindowBlur)
  nextTick(onLayoutResize)
  // 复用已存在会话（从其它 tab 返回，route id 与 store 中 conversationId 相同，
  // 导致 watcher 三分支全跳过）时，主动同步一次：拉取最新消息 + 滚到底，
  // 避免看到陈旧列表且停在旧滚动位置。
  // 注意：仅当 route id === store conversationId 时才补，避免与 watcher 分支 A（切换会话）重复请求。
  if (chat.conversationId && !chat.isStreaming && route.params.id === chat.conversationId) {
    syncCurrentConversation()
  }
})

const inputValue = ref('')
const messagesEndRef = ref(null)
const messagesContainerRef = ref(null)
const messagesColumnRef = ref(null)
const isLoadingHistory = ref(false)
const isFirstLoad = ref(true)
const mobileInputRef = ref(null)
const isInitialScrollDone = ref(false)
const isAwayFromBottom = ref(false)
const skipInputTransition = ref(false)

// --- Selection mode state ---
const selectionMode = ref(false)
const selectedUserMsgIds = ref(new Set())

function enterSelectionMode(roundKey) {
  selectionMode.value = true
  inputVisible.value = false
  showCooldownActive = false
  clearTimeout(hideCooldownTimer)
  if (roundKey) {
    selectedUserMsgIds.value = new Set([roundKey])
  }
}

function exitSelectionMode() {
  selectionMode.value = false
  selectedUserMsgIds.value = new Set()
  if (!isMobile.value) {
    inputVisible.value = true
  }
}

function toggleRound(roundKey) {
  if (!roundKey) return
  const s = new Set(selectedUserMsgIds.value)
  if (s.has(roundKey)) {
    s.delete(roundKey)
  } else {
    if (s.size >= 10) return
    s.add(roundKey)
  }
  selectedUserMsgIds.value = s
}

// Compute roundKey for each message: each user msg starts a round,
// subsequent non-user msgs inherit that roundKey until next user msg.
// 语音朗读进度：只给「正在念的那条」——流式气泡或最后一条 assistant
const voiceSpeech = computed(() => {
  if (!voiceSpeaking.value && !voicePreparing.value) return null
  const sentences = voiceSpeechSentences.value
  if (!sentences.length) return null
  return { sentences, index: voiceSpeechIndex.value }
})

const lastAssistantId = computed(() => {
  const last = chat.messages[chat.messages.length - 1]
  return last && last.role === 'assistant' ? last.id : ''
})

const messagesWithRoundKeys = computed(() => {
  let currentRoundKey = ''
  return chat.messages.map(msg => {
    if (msg.role === 'user') {
      currentRoundKey = msg.id
    }
    return {
      ...msg,
      roundKey: currentRoundKey || '',
    }
  })
})

// Provide selection context to child components
provide('selectionMode', selectionMode)
provide('selectedUserMsgIds', selectedUserMsgIds)
provide('toggleRound', toggleRound)
provide('enterSelectionMode', enterSelectionMode)
provide('exitSelectionMode', exitSelectionMode)
// --- End selection mode state ---

const { isBarVisible, showBar } = useScrollTopBar(messagesContainerRef)

const inputVisible = ref(true)
// 「↓」按钮贴在输入卡上沿：输入卡高度会变（收起/加图），写死 bottom 会压在卡上。
// 按钮绝对定位在 .chat-card 内，基准取卡片底边，键盘弹出缩壳时也不会错位。
const scrollBtnBottom = ref(120)
let composerObserver = null
let contentObserver = null
function updateScrollBtnBottom() {
  const el = mobileInputRef.value
  if (!el) return
  const card = el.closest('.chat-card')
  if (!card) return
  scrollBtnBottom.value = Math.round(card.getBoundingClientRect().bottom - el.getBoundingClientRect().top) + 12
}
// 消息容器高度变了（收起/展开/挂附件）也要重算「↓」该不该露，别等下一次 scroll
function updateAwayFromBottom() {
  const c = messagesContainerRef.value
  if (!c) return
  isAwayFromBottom.value = c.scrollHeight - c.scrollTop - c.clientHeight > 200
}
function onLayoutResize() {
  updateScrollBtnBottom()
  updateAwayFromBottom()
  // 视口/输入卡变矮（挂图、键盘、拖窗口）会让最新内容落到可视区下方：贴底时跟上
  if (stickToBottom) pinToBottom()
}
let hideCooldownTimer = null
let showCooldownActive = false
let scrollRafPending = false

// 「要不要跟着底部走」只由用户意图决定：scrollTop 变小 = 用户把视图往上挪了（在读历史）。
// 不能用「距底距离」判断——内容变高也会让距离变大，把自己撑高的那一帧误判成上滑，
// 结果整轮流式都不再跟随（见 docs/bugs/063）。
let stickToBottom = true
let lastScrollTop = 0
const STICK_SLACK = 80

function pinToBottom() {
  const el = messagesContainerRef.value
  if (!el) return
  el.scrollTop = el.scrollHeight
  lastScrollTop = el.scrollTop
  stickToBottom = true
}

watch(inputVisible, async (val) => {
  if (val) {
    await nextTick()
    const textarea = document.querySelector('.chat-input textarea')
    if (textarea) {
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    }
  }
})

function toggleInput() {
  inputVisible.value = !inputVisible.value
  if (inputVisible.value) {
    nextTick(() => {
      document.querySelector('.chat-input textarea')?.focus()
    })
  } else {
    // 收起要连键盘一起收：只把高度收 0，iOS 上焦点还在，键盘不会退
    document.querySelector('.chat-input textarea')?.blur()
  }
}

// 智能体列表（用于切换/解绑）
const agentsList = ref([])

async function fetchAgents() {
  try {
    const res = await api.get('/api/agents')
    agentsList.value = (res.agents || []).map(a => ({ id: a.id, name: a.name }))
  } catch (e) {
    console.error('Failed to fetch agents', e)
  }
}

async function handleSelectAgent(agentId) {
  const convId = chat.conversationId
  if (!convId) {
    // 空白新会话（直接访问 /chat、还没懒创建）没有会话行可 PATCH：
    // 先记在本地，首条消息随 agent_id 一起落库（服务端建会话时带上）。
    const agent = agentsList.value.find(a => a.id === agentId)
    chat.agentId = agentId || null
    chat.agentName = agent?.name || ''
    return
  }

  try {
    const body = { agent_id: agentId }
    const res = await api.patch(`/api/conversations/${convId}`, body)
    const newAgentId = res.agent_id || null
    const newAgentName = res.agent_name || ''

    // 更新 conversations store（列表和当前会话）
    convs.updateAgent(convId, newAgentId, newAgentName)

    // 更新 chat store
    chat.agentId = newAgentId
    chat.agentName = newAgentName

  } catch (e) {
    console.error('Failed to update agent binding', e)
  }
}

const currentAgentName = computed(() => {
  const conv = convs.list.find(c => c.id === chat.conversationId)
  return conv?.agent_name || chat.agentName || ''
})

const currentAgentId = computed(() => {
  const conv = convs.list.find(c => c.id === chat.conversationId)
  return conv?.agent_id || chat.agentId || null
})

function scrollToBottom() {
  nextTick(() => {
    if (scrollRafPending) return
    scrollRafPending = true
    requestAnimationFrame(() => {
      scrollRafPending = false
      // 一视同仁地看跟随意图：用户自己在读历史就不拽回去（流式结束、内容长高都不例外）
      if (!stickToBottom) return
      pinToBottom()
    })
  })
}

function handleScrollToBottomBtn() {
  stickToBottom = true
  nextTick(() => {
    requestAnimationFrame(() => pinToBottom())
  })
}

// 历史请求序号：快速切会话时，晚到的旧响应不许再写 store（慢网下 A 的响应会盖掉 B）。
// 与 DocumentReadView 的 renderSeq 是同一套做法。
let loadSeq = 0

// 序号过期，或请求对应的会话已经不是当前会话（「新对话」会 reset 成 null，但不自增序号）。
function isStale(seq, convId) {
  return seq !== loadSeq || convId !== chat.conversationId
}

async function loadHistory(convId, direction = 'before', cursor = null) {
  if (!convId) return false
  const seq = ++loadSeq
  isLoadingHistory.value = true
  try {
    let url = `/api/conversations/${convId}/messages-v2?limit=7`
    if (cursor) {
      url += `&cursor=${cursor}&direction=${direction}`
    }
    const res = await api.get(url)
    // convId 也要校验：离开会话时 conversationId 可能变 null（归档当前会话、落到 /chat 的
    // chat.reset()），那种情况不会自增 loadSeq，只靠 seq 拦不住在途请求写回旧会话消息。
    if (isStale(seq, convId)) return false
    if (res) {
      const msgs = (res.messages || []).flatMap(normalizeMessage)
      if (direction === 'before' && cursor) {
        const scrollContainer = messagesContainerRef.value
        const oldScrollHeight = scrollContainer.scrollHeight
        const oldScrollTop = scrollContainer.scrollTop

        chat.prependMessages(msgs)

        await nextTick()
        await new Promise(resolve => {
          requestAnimationFrame(() => {
            // 等 DOM 期间切了会话就别再动滚动条了，容器里已经是另一个会话的消息
            if (isStale(seq, convId)) {
              resolve()
              return
            }
            const newScrollHeight = scrollContainer.scrollHeight
            scrollContainer.scrollTop = oldScrollTop + (newScrollHeight - oldScrollHeight)
            // 程序化滚动要同步基线，否则下次 scroll 事件会拿旧值比较
            lastScrollTop = scrollContainer.scrollTop
            resolve()
          })
        })
      } else {
        chat.setMessages(msgs)
      }
      // 上面可能有 await（前置历史要等 DOM 重排），期间又切了会话就到此为止
      if (isStale(seq, convId)) return false
      chat.oldestCursor = res.oldest_cursor
      chat.hasMoreBefore = res.has_more_before
      // 批量获取关联记忆详情
      await fetchAssociatedMemories()
      return true
    }
    return false
  } catch (e) {
    if (isStale(seq, convId)) return false
    console.error('Failed to load history', e)
    nMessage.error('加载历史消息失败')
    return false
  } finally {
    // 这里只认序号：新对话分支会把 conversationId 置 null，若也按会话判过期，
    // 旧请求的 finally 会被跳过，isLoadingHistory 永远停 true（spinner 卡死）。
    if (seq === loadSeq) isLoadingHistory.value = false
  }
}

// 路由序号：切会话后，旧 watcher / 旧刷新流程的后续 UI 动作（滚动、同步智能体）不再执行。
let routeSeq = 0

// 多端同步兜底：tab 从后台切回前台时，全量刷新当前会话最新消息，
// 补齐其他端（客户端/其它 tab）在此期间产生的新消息。
let lastSyncAt = 0
async function syncCurrentConversation({ force = false } = {}) {
  if (!chat.conversationId) return
  const now = Date.now()
  if (!force && now - lastSyncAt < 3000) return
  lastSyncAt = now
  const seq = routeSeq
  const ok = await loadHistory(chat.conversationId)
  if (seq !== routeSeq) return
  // 拉历史成功 = 连接没问题：把「网络层」断流错误收掉（服务端 error 保留，用户需要知道）
  if (ok) clearNetworkError()
  await nextTick()
  requestAnimationFrame(() => pinToBottom())
}

// 回前台：在流式中就续传（补发断点后的事件并继续跟流），否则拉一次历史。
// 续传失败/后端不支持（老后端 404）一律降级成拉历史，绝不留在灰态。
let resuming = false
async function resumeOrSync() {
  if (resuming) return
  resuming = true
  try {
    if (!chat.isStreaming) {
      await syncCurrentConversation()
      return
    }
    const why = await resumeStream()
    // done：续上了；failed：已把半截收尾并亮错；suspended：又切后台了，等下次回来
    if (why === 'done' || why === 'failed' || why === 'suspended') return
    // idle/reset/unsupported/closed：本轮已结束或续不上，收尾后以服务端为准
    if (why === 'reset') nMessage.warning('连接中断，已重新加载')
    finishStream()
    await syncCurrentConversation({ force: true })
  } finally {
    resuming = false
  }
}

function onVisibilityChange() {
  if (document.visibilityState === 'hidden') {
    // 冻结期间浏览器会攒着定时器、恢复时立刻补触发：先暂停看门狗，别误报超时
    setSuspended(true)
    // 移动端按住录音时切后台/锁屏（iOS PWA 不一定触发 window blur）：丢掉这段并放掉麦克风
    if (isMobile.value) onHoldTalkCancel()
    return
  }
  setSuspended(false)
  resumeOrSync()
}

async function fetchAssociatedMemories() {
  // 先捕获数组引用：setMessages / prependMessages 都是整体替换数组，
  // 这样 await 期间切了会话，结果只会写回旧数组，不会污染新会话的消息。
  const messages = chat.messages
  const allMemIds = new Set()
  for (const msg of messages) {
    if (msg.memoryIds?.length) {
      for (const id of msg.memoryIds) allMemIds.add(id)
    }
  }
  if (allMemIds.size === 0) return
  try {
    const memories = await api.post('/api/memories/batch', { ids: [...allMemIds] })
    const memMap = {}
    if (Array.isArray(memories)) {
      for (const mem of memories) {
        memMap[mem.id] = mem
      }
    }
    for (const msg of messages) {
      if (msg.memoryIds?.length) {
        msg.associatedMemories = msg.memoryIds.map(id => memMap[id]).filter(Boolean)
      }
    }
  } catch (e) {
    console.error('Failed to fetch associated memories', e)
  }
}

function normalizeMessage(msg) {
  const normalized = { ...msg }
  // 解析 memory_ids JSON 字符串
  if (msg.memory_ids) {
    try {
      normalized.memoryIds = typeof msg.memory_ids === 'string'
        ? JSON.parse(msg.memory_ids)
        : msg.memory_ids
    } catch {
      normalized.memoryIds = []
    }
  }
  if (msg.reasoning_content && !msg.reasoning) {
    normalized.reasoning = msg.reasoning_content
    normalized.reasoningDone = true
  }
  // 解析附件图片 id（JSON 数组字符串）
  if (msg.image_ids) {
    try {
      normalized.imageIds = typeof msg.image_ids === 'string'
        ? JSON.parse(msg.image_ids)
        : msg.image_ids
    } catch {
      normalized.imageIds = []
      console.error('Failed to parse image_ids', msg.id, msg.image_ids)
    }
  }

  let toolCalls = []
  if (msg.tool_calls_json) {
    try {
      const rawTCs = typeof msg.tool_calls_json === 'string'
        ? JSON.parse(msg.tool_calls_json)
        : msg.tool_calls_json
      toolCalls = (Array.isArray(rawTCs) ? rawTCs : []).map(tc => {
        // 兼容后端新旧两种序列化格式
        // 新格式（flat）: {tool:"note", function:"record_note", args:{...}}
        if (typeof tc.tool === 'string' && typeof tc.function === 'string') {
          return {
            tool: tc.tool,
            function: tc.function,
            args: tc.args || {},
            status: 'done',
            result: tc.result || null
          }
        }
        // 旧格式（nested）: {function:{name:"note.record_note", arguments:"{...}"}}
        const fullName = tc.function?.name || ''
        let tool = fullName
        let func = fullName
        const doubleIdx = fullName.lastIndexOf('__')
        if (doubleIdx > 0) {
          tool = fullName.substring(0, doubleIdx)
          func = fullName.substring(doubleIdx + 2)
        } else {
          const dotIdx = fullName.indexOf('.')
          if (dotIdx > 0) {
            tool = fullName.substring(0, dotIdx)
            func = fullName.substring(dotIdx + 1)
          }
        }
        let args = {}
        if (tc.function?.arguments) {
          try {
            args = JSON.parse(tc.function.arguments)
          } catch {
            args = tc.function.arguments
          }
        }
        return {
          tool,
          function: func,
          args,
          status: 'done',
          result: tc.result || null
        }
      })
    } catch {
      toolCalls = []
    }
  }

  normalized.toolCalls = toolCalls
  return [normalized]
}

// 会话失效后登录回来：把未发消息恢复成暂停的排队区（不自动发送）。
function restoreUnsentDraft(newId) {
  const draft = takeUnsentDraft(newId ? String(newId) : null)
  if (!draft) return
  for (const item of draft.items) {
    chat.enqueuePendingSend({ content: item.content || '', imageIds: item.imageIds || [] })
  }
  chat.queuePaused = true
  nMessage.info('已恢复未发送的消息')
}

// 监听路由中的会话 ID 变化，这是 ChatView 的核心入口逻辑。
// 只要路由 /chat/:id 中的 id 发生变化，就会触发此 watcher。
// 通过 { immediate: true } 确保页面首次加载时也会立即执行。
//
// 触发场景有三类：
// 1. 用户点击侧边栏切换到另一个会话 → newId 改变，且不等于当前 chat.conversationId
// 2. 外部事件要求重新加载当前会话（如信箱跳转）→ newId 不变，但 chat.needsRefetch 被设为 true
// 3. 用户直接访问 /chat 无 id 或 id 被清空 → newId 为 undefined/null
watch(() => route.params.id, async (newId) => {
  // 整页加载后的第一次路由解析才算「冷启动」，无论落在 /chat 还是 /chat/:id。
  // 放在这里消费而不是在分支 C 里，避免「深链打开 /chat/A → 点新对话」又弹回最新会话。
  const coldStart = coldStartPending
  coldStartPending = false
  const seq = ++routeSeq

  exitSelectionMode()
  // --- 离开旧会话时，取消正在进行的 SSE 流 ---
  // 避免旧会话的流式响应继续污染新会话的消息列表。
  // 判据是「目标会话 != 当前会话」，不能看 isStreaming：
  // 归档当前会话 / 落到 /chat 都会 chat.reset()，reset 把 isStreaming 置 false，
  // 那时旧流还在跑却已经没人拦它了。cancel() 对已结束的流是空操作，无条件调即可。
  // 懒创建场景不受影响：done 先写 chat.conversationId 再 router.replace，那时 newId === 当前会话。
  if (newId !== chat.conversationId) {
    clearPendingWithNotice()
    cancel()
  }

  // ====================================================================
  // 分支 A：切换到另一个会话（newId 存在且不等于当前会话）
  // 这是最常见的场景：用户点击侧边栏列表中的某个会话
  // ====================================================================
  if (newId && newId !== chat.conversationId) {
    // 重置 UI 状态：确保输入框可见、标记首次加载中
    // 切换会话时跳过输入框 CSS 过渡，避免过渡期间 scrollHeight 变化导致定位不准
    skipInputTransition.value = true
    inputVisible.value = true
    isFirstLoad.value = true
    // 延迟 200ms 才允许「上滑加载更多历史」触发，防止初入页面时的滚动事件误触发分页
    isInitialScrollDone.value = false

    // ① 切换 chat store 中的当前会话 ID，并拉取最近 20 条消息
    chat.setConversation(newId)
    // 上面可能刚 cancel 过旧流（cancel 会置 needsRefetch），本分支已经重新拉过历史，别留到下次
    chat.needsRefetch = false
    await loadHistory(newId)
    if (seq !== routeSeq) return

    // ② 等 DOM 渲染完新消息后，把滚动条直接定位到底部（最新消息处）
    stickToBottom = true
    lastScrollTop = 0
    await nextTick()
    requestAnimationFrame(() => pinToBottom())
    // 恢复输入框 CSS 过渡
    await nextTick()
    skipInputTransition.value = false

    // ③ 解除首次加载标记，并在 200ms 后激活滚动监听（给滚动条一个稳定期）
    isFirstLoad.value = false
    setTimeout(() => { if (seq === routeSeq) isInitialScrollDone.value = true }, 200)

    // ④ 从 conversations store 中读取该会话绑定的智能体，同步到 chat store
    // 这样后续发送消息时会携带正确的 agent_id
    const currentConv = convs.list.find(c => c.id === newId)
    if (currentConv?.agent_id) {
      chat.agentId = currentConv.agent_id
      chat.agentName = currentConv.agent_name || ''
    } else {
      chat.agentId = null
      chat.agentName = null
    }

  // ====================================================================
  // 分支 B：当前会话不变但需要重新拉取消息
  // 例如：外部页面（信箱）触发跳转到某个已有会话时，newId 可能与 chat.conversationId 一致
  // 此时 conversationId 没变，setConversation 不会重新拉取历史，
  // 所以通过 needsRefetch 标志来强制重新加载消息列表
  // ====================================================================
  } else if (newId && chat.needsRefetch) {
    // 与分支 A 相同的 UI 重置 + 历史加载 + 滚动定位流程
    skipInputTransition.value = true
    inputVisible.value = true
    isFirstLoad.value = true
    isInitialScrollDone.value = false
    await loadHistory(newId)
    if (seq !== routeSeq) return
    stickToBottom = true
    lastScrollTop = 0
    await nextTick()
    requestAnimationFrame(() => pinToBottom())
    await nextTick()
    skipInputTransition.value = false

    // 清除重载标志，避免下次路由不变时重复加载
    chat.needsRefetch = false

    isFirstLoad.value = false
    setTimeout(() => { if (seq === routeSeq) isInitialScrollDone.value = true }, 200)

    const currentConv2 = convs.list.find(c => c.id === newId)
    if (currentConv2?.agent_id) {
      chat.agentId = currentConv2.agent_id
      chat.agentName = currentConv2.agent_name || ''
    } else {
      chat.agentId = null
      chat.agentName = null
    }

  // ====================================================================
  // 分支 C：路由里没有会话 ID（用户访问 /chat 或被重定向到无 id 状态）
  // 冷启动首次进来定位到会话列表第一条（置顶优先，其次更新时间倒序），
  // 之后跳转会重进分支 A 拉最近消息并滚到底；
  // 其余情况（直接访问 /chat、客户端跳转）直接显示空白页，会话由首条消息懒创建。
  // 顶栏「新对话」不走这里：它先建会话再跳 /chat/:id（id 先确定，断流也不丢归属）。
  // ====================================================================
  } else if (!newId) {
    inputVisible.value = true
    isInitialScrollDone.value = false
    // 上一条分支可能停在 skip=true 就被新路由顶掉了，这里兜底恢复，别让输入框动画一直关着
    skipInputTransition.value = false

    if (coldStart) {
      // 顺带把列表换成最新的，顶栏会话切换器直接可用
      await convs.fetchList()
      if (seq !== routeSeq) return
      const latest = convs.list[0]
      if (latest?.id) {
        router.replace(`/chat/${latest.id}`)
        return
      }
    }

    chat.reset()
    // 空白新对话：没有历史可读，从「贴底」开始
    stickToBottom = true
    lastScrollTop = 0
    // 新对话没有在途的历史请求，旧请求的 spinner 不该继续顶着（它的 finally 还要等网络）
    isLoadingHistory.value = false
  }

  restoreUnsentDraft(newId)
}, { immediate: true })

// 登出前保存输入框 + 排队项（非 SSE 401 路径）。同会话已有草稿则跳过（SSE 路径已存过）。
function saveUnsentBeforeUnmount() {
  const existing = peekUnsentDraft()
  if (existing && (existing.conversationId || null) === (chat.conversationId || null)) return
  const content = inputValue.value.trim()
  const imageIds = draftImages.value
    .filter(d => d.status === 'done' && d.imageId)
    .map(d => d.imageId)
  const items = []
  if (content || imageIds.length) items.push({ content, imageIds })
  for (const p of chat.pendingSends) {
    items.push({ content: p.content || '', imageIds: p.imageIds || [] })
  }
  saveUnsentDraft(chat.conversationId, items)
}

onBeforeUnmount(() => {
  // 会话失效（回前台校验/心跳/上传等非 SSE 路径）也要保留未发内容：
  // SSE 401 分支已经存过就不覆盖，避免空输入冲掉刚存的那条。
  if (auth.sessionExpired) saveUnsentBeforeUnmount()
  clearTimeout(hideCooldownTimer)
  composerObserver?.disconnect()
  contentObserver?.disconnect()
  window.removeEventListener('resize', onLayoutResize)
  window.removeEventListener('keydown', onVoiceKeyDown)
  window.removeEventListener('keyup', onVoiceKeyUp)
  window.removeEventListener('blur', onVoiceWindowBlur)
  document.removeEventListener('visibilitychange', onVisibilityChange)
  cancelVoice()
  stopSpeaking()
  chat.clearPendingSends()
  if (chat.isStreaming) cancel()
})

watch(() => chat.streamingMessage?.content, () => scrollToBottom())
watch(() => chat.streamingMessage?.reasoning, () => scrollToBottom())

// 收集当前草稿；校验不过返回 null（提示沿用原发送路径）
function collectOutgoing() {
  const content = inputValue.value.trim()
  const ready = draftImages.value.filter(d => d.status === 'done')
  const failed = draftImages.value.some(d => d.status === 'error')
  const uploading = draftImages.value.some(d => d.status === 'uploading')

  if (!content && !ready.length) {
    if (failed) nMessage.warning('有图片上传失败，请重试或移除')
    return null
  }
  if (uploading) {
    nMessage.info('图片还在上传，稍等')
    return null
  }
  if (failed) {
    nMessage.warning('有图片上传失败，请重试或移除')
    return null
  }
  return {
    content,
    imageIds: ready.map(d => d.imageId)
  }
}

function clearComposer() {
  inputValue.value = ''
  // 预览 URL 在这里回收：消息里的图改走鉴权接口读，不依赖本地 objectURL
  clearDrafts()
}

// 入队：只进 pendingSends，不碰 messages（done 会用最后一条 user 回填真实 id）
function enqueueOutgoing(outgoing) {
  chat.enqueuePendingSend(outgoing)
  clearComposer()
  stickToBottom = true      // 刚发过消息，排队块出现也必须贴底
  scrollToBottom()
}

// 直发 / 出队共用：写正式消息 + 调 SSE
async function dispatchOutgoing(outgoing) {
  chat.addMessage({
    id: Date.now().toString(),
    role: 'user',
    content: outgoing.content,
    imageIds: outgoing.imageIds,
    created_at: new Date().toISOString()
  })

  // 把当前会话移到列表最前面
  const conv = convs.list.find(c => c.id === chat.conversationId)
  if (chat.conversationId && conv?.title) {
    convs.prepend({ id: chat.conversationId, title: conv.title })
  }
  stickToBottom = true
  scrollToBottom()

  // 打字发送不朗读（语音轮才带 voice_mode + onAssistantReply）
  await sendMessage(outgoing.content, {
    conversationId: chat.conversationId,
    agentId: chat.agentId,
    attachments: outgoing.imageIds,
    // 仅新会话需要：已有会话的模式/无痕走 PATCH 单独落库
    initMode: chat.conversationId ? undefined : chat.mode,
    ephemeral: chat.conversationId ? false : chat.ephemeral
  })
}

// 语音/手动发送要立刻接一轮时，先挂起自动出队：stopLocalStream() 会把 isStreaming 置 false，
// watcher 若抢先 drainQueue 就会把排队消息发出去，刚提交的语音轮反被 sendMessage 的
// isStreaming 闸门静默丢弃（docs/bugs/061 同类问题）。
let suppressQueueDrain = false
function holdQueueForManualSend() {
  if (chat.pendingSends.length) suppressQueueDrain = true
}
function releaseQueueAfterManualSend() {
  if (!suppressQueueDrain) return
  suppressQueueDrain = false
  if (!chat.error) drainQueue()
}

// 按序出队。sendMessage 会等到整轮结束，循环天然按轮推进。
let drainingQueue = false
async function drainQueue() {
  if (drainingQueue || suppressQueueDrain) return
  drainingQueue = true
  try {
    while (shouldDrainQueue({
      streaming: chat.isStreaming,
      paused: chat.queuePaused,
      held: suppressQueueDrain,
      count: chat.pendingSends.length
    })) {
      const next = chat.takeNextPendingSend()
      if (!next) break
      await dispatchOutgoing(next)
    }
  } catch (err) {
    // 发送层自己会处理网络/服务错误；这里兜的是入队后搬运消息这类意外，暂停避免连发
    console.error('Queue dispatch failed', err)
    chat.queuePaused = true
  } finally {
    drainingQueue = false
  }
}

function clearPendingWithNotice() {
  const n = chat.clearPendingSends()
  if (n) nMessage.info(`已清空 ${n} 条排队消息`)
}

function resumePending() {
  if (chat.isStreaming) return
  chat.queuePaused = false
  drainQueue()
}

// 当前轮结束自动出队；失败则暂停，保留队列等用户继续/手动发送
watch(() => chat.isStreaming, (streaming) => {
  if (streaming) return
  if (chat.error) {
    if (chat.pendingSends.length) chat.queuePaused = true
    return
  }
  if (shouldDrainQueue({
    streaming: false,
    paused: chat.queuePaused,
    held: suppressQueueDrain,
    count: chat.pendingSends.length
  })) {
    drainQueue()
  }
})

async function handleSend() {
  const outgoing = collectOutgoing()
  if (!outgoing) return
  exitSelectionMode()

  // 流式中、队列非空或暂停中：都按先来后到追加，避免插队/丢消息
  if (chat.isStreaming || chat.pendingSends.length || chat.queuePaused) {
    enqueueOutgoing(outgoing)
    if (!chat.isStreaming) {
      chat.queuePaused = false
      drainQueue()
    }
    return
  }

  // 直发才清输入框；队列出队不能清，用户可能正在打下一句
  clearComposer()
  await dispatchOutgoing(outgoing)
}

// 麦克风按钮 = 进入语音条（按上次的档位）。语音条里再点是「结束」：
// 连续档发 end 让最后一句识别完；cancel 会把已经识别出来的文本整段丢掉（见 docs/bugs/049）。
async function toggleVoice() {
  if (voiceActive.value) {
    endVoiceSession()
    return
  }
  if (voiceCalling.value) return
  if (voiceSpeaking.value) stopSpeaking()
  try {
    await enterVoice()
  } catch (err) {
    nMessage.error(`无法开始语音对话：${err?.message || '麦克风不可用'}`)
  }
}

// 两段式胶囊只切档，不录音。切档时如果语音条开着，composable 会拆旧链路按新档位重开。
function onSwitchVoiceMode(next) {
  switchVoiceMode(next)
}

/// 停掉本地 SSE 流，保留已经生成的部分。
/// 不发 /api/chat/stop：下一轮 sendMessage 会顶掉服务端的旧 loop，迟到的 stop 会误杀新轮。
function stopLocalStream() {
  const partial = chat.streamingMessage
  cancel()
  if (partial && (partial.content || partial.reasoning)) {
    chat.addMessage({ ...partial, id: `local-${Date.now()}`, toolCalls: [] })
  }
}

/// 取走托盘里已上传完成的图片，返回 imageId 列表（见 docs/bugs/061）。
/// 只清走随消息发出去的，上传中/失败的留在托盘里等用户处理。
/// `wait` 只给长按档用：松手后允许等图传完再发（此时不会有第二条提交）；
/// 连续档每条分句都是一轮，等待会拖慢对话，所以只取已完成的，没传完的留给下一句。
async function takeVoiceImages({ wait = false } = {}) {
  if (wait && draftImages.value.some(d => d.status === 'uploading')) {
    nMessage.info('图片上传中，完成后自动发送')
    if (await waitForUploads(() => draftImages.value)) {
      nMessage.warning('图片上传超时，未完成的图片已跳过')
    }
  }
  // 失败提示每条草稿只弹一次：草稿不会被自动清掉，连续档每句都弹会很吵
  const failed = draftImages.value.filter(d => d.status === 'error' && !d.notified)
  if (failed.length) {
    failed.forEach(d => { d.notified = true })
    nMessage.warning('部分图片上传失败，已跳过')
  }
  const ready = draftImages.value.filter(d => d.status === 'done')
  ready.forEach(removeDraft)
  return ready.map(d => d.imageId)
}

// 连续对话：一条判停分句 = 一轮。上一轮还在跑就先停本地流，避免两个 loop 交叉写库。
async function submitVoiceSentence(text) {
  const content = String(text || '').trim()
  if (!content) return
  // 语音轮要立刻接一轮：先挂起自动出队，别让排队消息抢在它前面（见 drainQueue 的说明）
  holdQueueForManualSend()
  // 无条件停朗读：上一轮已流完但还在念时，门还关着，不停就会把这句一起吞掉
  stopSpeaking()
  if (chat.isStreaming) stopLocalStream()
  const imageIds = await takeVoiceImages()
  chat.addMessage({
    id: `voice-${Date.now()}`,
    role: 'user',
    content,
    imageIds,
    created_at: new Date().toISOString()
  })
  try {
    await sendMessage(content, {
      conversationId: chat.conversationId,
      agentId: chat.agentId,
      // 仅新会话需要：已有会话的模式/无痕走 PATCH 单独落库
      initMode: chat.conversationId ? undefined : chat.mode,
      ephemeral: chat.conversationId ? false : chat.ephemeral,
      // 语音来源：服务端注入短回答约束；正文边到边切句合成，done 后收尾。
      // 退出语音时 flush 的话按普通消息提交（voiceExitFlush），不朗读。
      voiceMode: !voiceExitFlush,
      ...(imageIds.length ? { attachments: imageIds } : {}),
      ...(voiceExitFlush ? {} : {
        onAssistantReplyDelta: (text) => appendVoiceReply(text),
        onAssistantReply: (reply, ok = true) => (ok ? endVoiceReply(reply) : stopSpeaking())
      })
    })
  } finally {
    releaseQueueAfterManualSend()
  }
}

// 长按说话：一次松手 = 一轮。与连续档的区别是**不挂 TTS 回调**，回复只进气泡不朗读。
async function submitHoldSentence(text) {
  const content = String(text || '').trim()
  if (!content) return
  // 同连续档：先挂起自动出队，语音轮自己占这一轮
  holdQueueForManualSend()
  // 切档瞬间上一轮连续档的朗读可能还在播：先停掉，别叠音
  stopSpeaking()
  if (chat.isStreaming) stopLocalStream()
  // 松手时图可能还在传：等传完再连同文字一起发（见 docs/bugs/061）
  holdSubmitting.value = true
  let imageIds
  try {
    imageIds = await takeVoiceImages({ wait: true })
  } finally {
    holdSubmitting.value = false
  }
  chat.addMessage({
    id: `voice-${Date.now()}`,
    role: 'user',
    content,
    imageIds,
    created_at: new Date().toISOString()
  })
  try {
    await sendMessage(content, {
      conversationId: chat.conversationId,
      agentId: chat.agentId,
      // 仅新会话需要：已有会话的模式/无痕走 PATCH 单独落库
      initMode: chat.conversationId ? undefined : chat.mode,
      ephemeral: chat.conversationId ? false : chat.ephemeral,
      // 仍走语音轮的短回答约束，但不出声
      voiceMode: true,
      ...(imageIds.length ? { attachments: imageIds } : {})
    })
  } finally {
    releaseQueueAfterManualSend()
  }
}

function handleStop() {
  const convId = chat.conversationId
  stopSpeaking()
  // 停止 = 全部停下：先清队列，避免 isStreaming 转 false 后 watcher 又把排队消息发出去
  clearPendingWithNotice()
  // 语音会话不在这里收：停生成后麦克风还要继续听（用户可能接着说）
  // 先断 SSE 让 UI 立即停下，再通知服务端终止 ReAct（与 macOS 客户端顺序一致）。
  // shortcut: 新会话首条消息流式时前端还拿不到 conversation_id，只能断流，
  // 服务端会照常跑完；等 done 回传 id 后才有服务端停止能力。
  stopLocalStream()
  if (!convId) return
  api.post('/api/chat/stop', { conversation_id: convId }).catch(err => {
    console.error('Stop chat failed', err)
  })
}

// SSE 断开不会终止服务端执行，重新拉一次通常能拿到完整结果。
async function retryAfterError() {
  chat.error = null
  if (!chat.conversationId) return
  const seq = routeSeq
  await loadHistory(chat.conversationId)
  if (seq !== routeSeq) return
  await nextTick()
  requestAnimationFrame(() => pinToBottom())
}

// 会话的模式/无痕状态来自列表接口（mode / ephemeral）；冷启动时列表可能晚于
// 路由 watcher 到达，所以用 watch 挂在「当前会话对象」上，而不是在切换分支里读一次。
watch(
  () => convs.list.find(c => c.id === chat.conversationId),
  (conv) => {
    if (!conv) return
    chat.mode = conv.mode || 'work'
    chat.ephemeral = !!conv.ephemeral
  },
  { immediate: true }
)

// --- 待发图片 ---
// 参数对齐 macOS 端：上限 20 张、长边 2048（压缩在 utils/image.js）、上传并发 2。
// 选完即传：发送时不用等，失败的单张可重试；没被引用的图由服务端 orphan TTL 自动清。
const MAX_DRAFTS = 20
const draftImages = ref([])
const fileInputRef = ref(null)
let draftSeq = 0
let activeUploads = 0
const uploadQueue = []

const canSend = computed(() => {
  // 上传中不给发；失败态故意放行到这里，让 handleSend 弹出「有图片上传失败」的提示
  if (draftImages.value.some(d => d.status === 'uploading')) return false
  return !!inputValue.value.trim() || draftImages.value.length > 0
})

function pickImages() {
  fileInputRef.value?.click()
}

async function addImages(files) {
  const images = files.filter(f => f.type.startsWith('image/'))
  for (const file of images) {
    if (draftImages.value.length >= MAX_DRAFTS) {
      nMessage.warning(`最多 ${MAX_DRAFTS} 张图片`)
      break
    }
    let prepared
    try {
      prepared = await prepareImage(file)
    } catch (e) {
      console.error('Failed to prepare image', file.name, e)
      nMessage.error(`「${file.name}」处理失败，换一张试试`)
      continue
    }
    if (draftImages.value.some(d => d.key === prepared.key)) continue
    const item = reactive({
      id: ++draftSeq,
      key: prepared.key,
      name: prepared.name,
      blob: prepared.blob,
      preview: URL.createObjectURL(prepared.blob),
      status: 'uploading',
      imageId: null,
      error: '',
      notified: false
    })
    draftImages.value.push(item)
    enqueueUpload(item)
  }
}

function enqueueUpload(item) {
  uploadQueue.push(item)
  pumpUploads()
}

function pumpUploads() {
  while (activeUploads < 2 && uploadQueue.length) {
    const item = uploadQueue.shift()
    activeUploads++
    uploadOne(item).finally(() => {
      activeUploads--
      pumpUploads()
    })
  }
}

async function uploadOne(item) {
  item.status = 'uploading'
  item.error = ''
  item.notified = false
  try {
    const file = new File([item.blob], item.name, { type: item.blob.type || 'image/jpeg' })
    const res = await api.upload('/api/images/upload', file)
    item.imageId = res.image_id
    item.status = 'done'
  } catch (e) {
    console.error('Image upload failed', item.name, e)
    item.status = 'error'
    item.error = e?.message || '上传失败'
  }
}

function retryDraft(item) {
  if (item.status === 'uploading') return
  enqueueUpload(item)
}

function removeDraft(item) {
  URL.revokeObjectURL(item.preview)
  draftImages.value = draftImages.value.filter(d => d.id !== item.id)
}

function clearDrafts() {
  for (const item of draftImages.value) URL.revokeObjectURL(item.preview)
  draftImages.value = []
  uploadQueue.length = 0
}

function onFilesChosen(e) {
  const files = [...(e.target.files || [])]
  e.target.value = ''
  if (files.length) addImages(files)
}

// 粘贴图片：剪贴板同时带文字时不接管，让用户拿到文字（与 macOS 端规则一致）
function onComposerPaste(e) {
  const files = [...(e.clipboardData?.files || [])].filter(f => f.type.startsWith('image/'))
  if (!files.length) return
  if (e.clipboardData.getData('text/plain').trim()) return
  e.preventDefault()
  addImages(files)
}

function onComposerDrop(e) {
  const files = [...(e.dataTransfer?.files || [])].filter(f => f.type.startsWith('image/'))
  if (files.length) addImages(files)
}

// --- 会话模式 / 无痕 ---
// 新会话只在本地记意图，首条消息随 init_mode / ephemeral 落库；
// 已有会话直接 PATCH（默认会话禁止无痕，服务端会 400，这里把原因透出来）。
async function toggleMode() {
  const next = chat.mode === 'plan' ? 'work' : 'plan'
  // 提示只弹一次、几秒自动消失；模式状态由 pill 高亮保留，不常驻黄条
  const hint = next === 'plan' ? '计划模式已开启：只读，不会写文档或改数据' : '已退出计划模式'
  if (!chat.conversationId) {
    chat.mode = next
    nMessage.info(hint)
    return
  }
  try {
    await api.patch(`/api/conversations/${chat.conversationId}/mode`, { mode: next })
    chat.mode = next
    convs.updateMode(chat.conversationId, next)
    nMessage.info(hint)
  } catch (e) {
    console.error('Failed to switch conversation mode', e)
    nMessage.error(e?.message || '切换模式失败')
  }
}

async function toggleEphemeral() {
  const next = !chat.ephemeral
  const hint = next ? '无痕会话已开启：结束后自动销毁' : '已关闭无痕会话'
  if (!chat.conversationId) {
    chat.ephemeral = next
    nMessage.info(hint)
    return
  }
  try {
    await api.patch(`/api/conversations/${chat.conversationId}/ephemeral`, { ephemeral: next })
    chat.ephemeral = next
    convs.updateEphemeral(chat.conversationId, next)
    nMessage.info(hint)
  } catch (e) {
    console.error('Failed to toggle ephemeral', e)
    nMessage.error(e?.message || '切换无痕失败')
  }
}

function handleScroll(e) {
  const container = e.target
  const dist = container.scrollHeight - container.scrollTop - container.clientHeight
  // 先认「已在底部」：键盘弹出 / 输入卡变高 / 缩窗口会让视口变小，浏览器把 scrollTop 夹小，
  // 那不是用户上滑（否则整轮不再跟随，正是 063 的症状）。
  if (dist <= STICK_SLACK) stickToBottom = true
  // 否则只认用户把视图往上挪：内容长高不改 scrollTop，不能拿距底距离当判据。
  // 容差取 0.5px：HiDPI 触控板每个事件可能只降 0.5px，容差过大就永远判不出上滑
  else if (container.scrollTop < lastScrollTop - 0.5) stickToBottom = false
  lastScrollTop = container.scrollTop
  updateAwayFromBottom()

  const threshold = container.clientHeight * 0.15
  const nearTop = container.scrollTop <= threshold
  if (isInitialScrollDone.value && nearTop && chat.hasMoreBefore && !isLoadingHistory.value) {
    loadHistory(chat.conversationId, 'before', chat.oldestCursor)
  }
}

function handleContainerClick(e) {
  if (!isIOS) return

  const container = messagesContainerRef.value
  if (!container) return

  const rect = container.getBoundingClientRect()
  const clickY = e.clientY - rect.top
  const topThird = rect.height / 3

  if (clickY <= topThird) {
    showBar()
  }
}
</script>

<template>
  <div class="chat-page">
    <PageTopBar
      show-conversation
      align-card
      :agent-id="currentAgentId"
      :agent-name="currentAgentName"
      :visible="!isMobile || isBarVisible"
      :agents="agentsList"
      @select-agent="handleSelectAgent"
      @popover-open="fetchAgents"
    >
    </PageTopBar>

    <SelectionActionBar />

    <div class="chat-card">
    <div
      ref="messagesContainerRef"
      @scroll="handleScroll"
      @touchstart="isMobile ? showBar() : null"
      @click="handleContainerClick"
      class="chat-messages"
    >
      <div class="chat-column" ref="messagesColumnRef">

        <div v-if="isLoadingHistory" style="text-align: center; padding: 20px 0;">
          <div style="width: 24px; height: 24px; border: 2px solid var(--color-border); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 0.8s linear infinite; display: inline-block;"></div>
        </div>

        <div v-if="chat.messages.length === 0 && !chat.streamingMessage" style="padding: 100px 0; text-align: center; color: var(--color-text-muted);">
          开始聊天吧
        </div>

        <MessageBubble
          v-for="msg in messagesWithRoundKeys"
          :key="msg.id"
          :message="msg"
          :is-history="true"
          :round-key="msg.roundKey"
          :speech="!chat.streamingMessage && voiceSpeech && msg.id === lastAssistantId ? voiceSpeech : null"
        />
        <div style="position: relative;">
          <div v-if="chat.streamingMessage">
            <MessageBubble
              :message="chat.streamingMessage"
              :is-history="false"
              :speech="voiceSpeech"
            />
          </div>
          <ToolCallToast />
        </div>

        <!-- 排队消息：只在 pendingSends 里，不写 messages（done 的 findLast(user) 要认本轮真实消息） -->
        <div v-if="chat.pendingSends.length" class="pending-queue">
          <div class="pending-divider">
            <span :class="{ paused: chat.queuePaused }">
              {{ chat.queuePaused ? '已暂停 · 上一条发送失败' : `当前轮结束后依次发送 · ${chat.pendingSends.length} 条` }}
            </span>
            <button v-if="chat.queuePaused" type="button" class="pending-resume" @click="resumePending">继续发送</button>
          </div>
          <div v-for="p in chat.pendingSends" :key="p.id" class="pending-bubble">
            <div class="pending-tag">排队中</div>
            <div class="pending-text">{{ p.content }}</div>
            <div v-if="p.imageIds?.length" class="pending-meta">{{ p.imageIds.length }} 张图</div>
            <div class="pending-actions">
              <button type="button" class="pending-del" @click="chat.removePendingSend(p.id)">删除</button>
            </div>
          </div>
        </div>

        <!-- PlanGraph 暂时隐藏，工具使用简单时图无意义 -->
        <!--
        <PlanGraph
          v-if="chat.streamingMessage || chat.messages.length > 0"
          :nodes="chat.planNodes"
          :edges="chat.planEdges"
          :visible="planVisible"
        />
        -->
        <div v-if="chat.error && !chat.streamingMessage" class="chat-error">
          <span class="chat-error-text">{{ chat.error }}</span>
          <button type="button" class="chat-error-retry" @click="retryAfterError">
            {{ chat.conversationId ? '重新加载' : '知道了' }}
          </button>
        </div>

        <div
          ref="messagesEndRef"
          :style="{
            height: '15px',
            transition: 'height 0.2s ease-out'
          }"
        />
      </div>
    </div>

    <!-- 查看历史消息时，滚动到底部按钮 -->
    <div
      v-if="isAwayFromBottom"
      @click="handleScrollToBottomBtn"
      class="scroll-bottom-btn"
      :style="{ bottom: scrollBtnBottom + 'px' }"
    >
      ↓
    </div>

    <div
      ref="mobileInputRef"
      :inert="!inputVisible"
      :style="[
        !isMobile ? {
          padding: inputVisible ? '8px 18px 14px' : '0 18px',
          maxWidth: 'min(960px, 100%)',
          marginLeft: 'auto',
          marginRight: 'auto',
          width: '100%',
          background: 'transparent',
          borderTop: 'none',
          flexShrink: 0,
          height: inputVisible ? 'auto' : '0',
          overflow: inputVisible ? 'visible' : 'hidden',
          opacity: inputVisible ? 1 : 0,
          transform: inputVisible ? 'scaleY(1)' : 'scaleY(0.8)',
          transformOrigin: 'bottom right',
          transition: skipInputTransition.value ? 'none' : 'height 0.25s ease-out, padding 0.25s ease-out, opacity 0.2s ease-out, transform 0.25s ease-out'
        } : {
          padding: inputVisible ? '6px 12px 8px' : '0 12px',
          background: 'transparent',
          borderTop: 'none',
          flexShrink: 0,
          height: inputVisible ? 'auto' : '0',
          overflow: inputVisible ? 'visible' : 'hidden',
          opacity: inputVisible ? 1 : 0,
          transition: skipInputTransition.value ? 'none' : 'height 0.25s ease-out, padding 0.25s ease-out, opacity 0.2s ease-out'
        }
      ]"
    >
      <input
        ref="fileInputRef"
        type="file"
        accept="image/*"
        multiple
        class="file-input"
        @change="onFilesChosen"
      />
      <div class="chat-composer-card" :class="{ 'voice-open': voiceBarOpen }" @paste="onComposerPaste" @dragover.prevent @drop.prevent="onComposerDrop">
        <!-- 移动端上滑取消提示：浮在输入卡上方，不占布局 -->
        <div v-if="isMobile && voiceHoldRecording" class="hold-hint" :class="{ armed: holdCancelArmed }">
          {{ holdCancelArmed ? '松开取消' : '上滑取消' }}
        </div>
        <AttachmentTray
          v-if="draftImages.length"
          :items="draftImages"
          :limit="MAX_DRAFTS"
          @add="pickImages"
          @remove="removeDraft"
          @retry="retryDraft"
          @clear="clearDrafts"
        />
        <VoiceWaveLine
          v-if="voiceBarOpen"
          :phase="voicePhase"
          :level="voiceLevel"
          @interrupt="voiceInterrupt"
        />
        <NInput
          v-else
          v-model:value="inputValue"
          type="textarea"
          :placeholder="chat.isStreaming ? '继续输入，Enter 加入队列...' : '发消息...'"
          :autosize="{ minRows: 1, maxRows: isMobile ? 4 : 6 }"
          class="chat-input"
          @keydown="(e) => { if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); handleSend() }}"
          :bordered="false"
          style="background: transparent; flex: 1;"
        />
        <div class="composer-row" :class="{ 'voice-row': voiceBarOpen }">
          <template v-if="!voiceBarOpen">
            <div class="composer-left">
              <NTooltip v-if="voiceAvailable" :disabled="isMobile" placement="top">
                <template #trigger>
                  <button class="icon-pill" :class="{ on: voiceActive }" type="button"
                    :aria-label="voiceActive ? '结束语音对话' : '语音对话'"
                    :aria-pressed="voiceActive"
                    :disabled="voiceCalling"
                    @click="toggleVoice">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3a3 3 0 0 0-3 3v6a3 3 0 0 0 6 0V6a3 3 0 0 0-3-3z"/><path d="M19 11a7 7 0 0 1-14 0"/><path d="M12 18v3"/></svg>
                  </button>
                </template>
                {{ voiceActive ? '结束语音对话' : (voiceMode === 'hold' ? (isMobile ? '语音对话（长按说话）' : '语音对话（点击说话）') : '语音对话（连续对话）') }}
              </NTooltip>
              <NTooltip :disabled="isMobile" placement="top">
                <template #trigger>
                  <button class="icon-pill" type="button" aria-label="添加图片" @click="pickImages">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 11.5l-8.4 8.4a5 5 0 0 1-7.1-7.1l8.5-8.5a3.4 3.4 0 0 1 4.8 4.8l-8.5 8.5a1.8 1.8 0 0 1-2.6-2.6l7.8-7.8"/></svg>
                  </button>
                </template>
                添加图片
              </NTooltip>
              <NTooltip :disabled="isMobile" placement="top">
                <template #trigger>
                  <button class="icon-pill" :class="{ on: chat.mode === 'plan' }" type="button" :aria-pressed="chat.mode === 'plan'" :aria-label="chat.mode === 'plan' ? '退出计划模式' : '计划模式（只读）'" @click="toggleMode">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h10M4 12h10M4 17h6"/><path d="M17 15l2 2 3-3"/></svg>
                  </button>
                </template>
                {{ chat.mode === 'plan' ? '退出计划模式' : '计划模式（只读）' }}
              </NTooltip>
              <NTooltip :disabled="isMobile" placement="top">
                <template #trigger>
                  <button class="icon-pill" :class="{ on: chat.ephemeral }" type="button" :aria-pressed="chat.ephemeral" :aria-label="chat.ephemeral ? '关闭无痕会话' : '无痕会话（结束后销毁）'" @click="toggleEphemeral">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 10c2-2 4-2 6 0s4 2 6 0 4-2 6 0"/><path d="M3 16c2-2 4-2 6 0s4 2 6 0 4-2 6 0"/></svg>
                  </button>
                </template>
                {{ chat.ephemeral ? '关闭无痕会话' : '无痕会话（结束后销毁）' }}
              </NTooltip>
              <button v-if="isMobile" class="icon-pill" type="button" aria-label="收起输入框" @click="toggleInput">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9l6 6 6-6"/></svg>
              </button>
            </div>
            <div class="composer-actions">
              <NButton
                v-if="!chat.isStreaming"
                type="primary"
                size="small"
                :disabled="!canSend"
                @click="handleSend"
                style="border-radius: 50%; width: 40px; height: 40px; padding: 0;"
              >↑</NButton>
              <template v-else>
                <NButton
                  type="error"
                  size="small"
                  @click="handleStop"
                  style="border-radius: 50%; width: 40px; height: 40px; padding: 0;"
                >■</NButton>
                <NButton
                  type="primary"
                  size="small"
                  :disabled="!canSend"
                  title="加入队列（Enter）"
                  @click="handleSend"
                  style="border-radius: 50%; width: 40px; height: 40px; padding: 0;"
                >↑</NButton>
              </template>
            </div>
          </template>
          <div v-else class="voice-group">
            <!-- 录音发送档：移动端「长按说话」小手；桌面端「点击说话」两态按钮 -->
            <NTooltip v-if="voiceMode === 'hold'" :disabled="isMobile" placement="top">
              <template #trigger>
                <span class="hold-slot">
                  <!-- 桌面端：取消键的槽位常驻（只有图标随录音状态出现/消失）。
                       绝对定位会让它飘到胶囊外，窄窗直接越界；占位随状态增减又会让发送键从鼠标底下挪走。 -->
                  <span v-if="!isMobile" class="hold-cancel-slot">
                    <button v-if="voiceHoldRecording" class="icon-pill hold-cancel" type="button"
                      aria-label="取消本次发送" @click="onHoldTalkCancel">
                      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.1" stroke-linecap="round">
                        <path d="M7 7l10 10M17 7L7 17"/>
                      </svg>
                    </button>
                  </span>
                  <button class="icon-pill hold-btn"
                    :class="{ holding: voiceHoldRecording, cancel: isMobile && holdCancelArmed }"
                    type="button"
                    :aria-label="holdTalkLabel"
                    :aria-pressed="voiceHoldRecording"
                    :disabled="voiceHoldTranscribing"
                    @pointerdown="onHoldTalkPointerDown"
                    @pointermove="onHoldTalkPointerMove"
                    @pointerup="onHoldTalkPointerUp"
                    @pointercancel="onHoldTalkPointerCancel"
                    @click="onHoldTalkToggle"
                    @contextmenu.prevent>
                    <span v-if="isMobile" class="hold-icon" aria-hidden="true"></span>
                    <svg v-else-if="!voiceHoldRecording" class="hold-mic" viewBox="0 0 24 24" fill="none"
                      stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                      <rect x="9" y="3" width="6" height="11" rx="3"/>
                      <path d="M5.5 11.5a6.5 6.5 0 0 0 13 0"/>
                      <path d="M12 18v3"/>
                    </svg>
                    <!-- 录音中＝发送：绿色上箭头，与主输入栏发送键同款；红色只留给取消/停止/挂断 -->
                    <svg v-else class="hold-send" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                      stroke-width="2.1" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                      <path d="M12 19V5M5 12l7-7 7 7"/>
                    </svg>
                  </button>
                </span>
              </template>
              {{ holdTalkTooltip }}
            </NTooltip>
            <!-- 连续档没有左侧录音键，但留同宽空位：这一行居中，少了它会整行横跳，胶囊跟着挪位 -->
            <span v-else class="hold-slot-ghost" aria-hidden="true"></span>
            <!-- 两段式胶囊：只切档，不录音。恒用图标（文字胶囊约 150px，最窄容器放不下），
                 功能靠桌面端 hover 说明；档位名留在 aria-label -->
            <div class="mode-switch" role="group" aria-label="语音模式">
              <NTooltip :disabled="isMobile" placement="top">
                <template #trigger>
                  <button class="seg" :class="{ on: voiceMode === 'continuous' }" type="button"
                    :aria-pressed="voiceMode === 'continuous'" aria-label="连续对话" @click="onSwitchVoiceMode('continuous')">
                    <svg class="seg-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9"
                      stroke-linecap="round" aria-hidden="true"><path d="M4 12h2M9 7v10M14 4v16M19 9v6"/></svg>
                  </button>
                </template>
                连续对话 · 一直收音，说完自动发送
              </NTooltip>
              <NTooltip :disabled="isMobile" placement="top">
                <template #trigger>
                  <button class="seg" :class="{ on: voiceMode === 'hold' }" type="button"
                    :aria-pressed="voiceMode === 'hold'" :aria-label="isMobile ? '长按说话' : '点击说话'" @click="onSwitchVoiceMode('hold')">
                    <!-- 与录音键同一份 SF Symbol hand.tap 蒙版：手绘版在 15px 下读作「举手/停止」 -->
                    <span class="seg-ico-mask" aria-hidden="true"></span>
                  </button>
                </template>
                点击说话 · 按一下开始录音，再按一下发送
              </NTooltip>
            </div>
            <NTooltip :disabled="isMobile" placement="top">
              <template #trigger>
                <!-- 挂断：通话条惯例，别再用波形圈冒充退出键 -->
                <button class="icon-pill voice-continuous" type="button" aria-label="退出语音对话" @click="endVoiceSession">
                  <!-- Material call_end：与 Mac 的 phone.down.fill 同一个语义，实心才在 17px 下认得出来 -->
                  <svg viewBox="0 0 24 24" fill="currentColor">
                    <path d="M12 9c-1.6 0-3.15.25-4.6.72v3.1c0 .39-.23.74-.56.9-.98.49-1.87 1.12-2.66 1.85-.18.18-.43.28-.7.28-.28 0-.53-.11-.71-.29L.29 13.08c-.18-.17-.29-.42-.29-.7 0-.28.11-.53.29-.71C3.34 8.78 7.46 7 12 7s8.66 1.78 11.71 4.67c.18.18.29.43.29.71 0 .28-.11.53-.29.7l-2.48 2.48c-.18.18-.43.29-.71.29-.27 0-.52-.11-.7-.28-.79-.74-1.69-1.36-2.67-1.85-.33-.16-.56-.5-.56-.9v-3.1C15.15 9.25 13.6 9 12 9z"/>
                  </svg>
                </button>
              </template>
              退出语音对话
            </NTooltip>
            <!-- 停止键槽位常驻：这一行居中，生成开始/结束时增删按钮会让整行横跳 -->
            <span class="voice-stop-slot">
              <button v-if="chat.isStreaming" class="icon-pill voice-stop" type="button"
                aria-label="停止生成" @click="handleStop">
                <span class="stop-core"></span>
              </button>
            </span>
          </div>
        </div>
      </div>
    </div>
    </div><!-- /chat-card -->
    <button v-if="isMobile && !inputVisible && !selectionMode" class="composer-reopen" type="button" @click="toggleInput">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 5v14M5 12h14"/></svg>
      发消息
    </button>
  </div>
</template>

<style scoped>
@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
.chat-page {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
}
.chat-card {
  position: relative;
  flex: 1;
  min-height: 0;
  width: calc(100% - 48px);
  max-width: 1080px;
  margin: 0 auto 24px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: 14px;
  border: 0.5px solid var(--color-border);
  background: linear-gradient(180deg, var(--color-surface), rgba(255, 255, 255, 0.26));
  box-shadow: var(--shadow-card);
}
[data-theme="dark"] .chat-card {
  background: linear-gradient(180deg, rgba(255, 255, 255, 0.06), rgba(255, 255, 255, 0.03));
}
.chat-messages {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-lg);
  display: flex;
  justify-content: center;
}
.chat-column {
  width: 100%;
  max-width: 960px;
  position: relative;
  /* 不拉伸到容器高度：否则盒子高度恒等于视口、内容长高不触发 ResizeObserver（贴底靠它兜图片/异步渲染） */
  align-self: flex-start;
}
.chat-composer-card {
  position: relative;
  border: 0.5px solid var(--color-border);
  border-radius: 22px;
  padding: 8px 10px;
  background: var(--color-input);
  height: 100%;
  display: flex;
  flex-direction: column;
  box-shadow: var(--shadow-sm);
}
/* 语音档不要外框：输入卡在流内（消息列表 flex:1 在它上方），正文不会从它后面穿过，
   所以直接去掉描边/底色/投影，控件落在聊天卡自己的背景上 */
.chat-composer-card.voice-open {
  border-color: transparent;
  background: transparent;
  box-shadow: none;
}
.composer-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
}
.composer-left {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}
.composer-actions {
  display: flex;
  align-items: center;
  flex: 0 0 auto;
  gap: 6px;
}

/* 排队消息：虚线气泡紧跟流式回复后面，视觉在对话流里，数据不写 messages */
.pending-queue {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 8px;
  margin-top: 6px;
}
.pending-divider {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--color-text-muted);
  font-size: 11.5px;
}
.pending-divider::before,
.pending-divider::after {
  content: "";
  flex: 1;
  height: 0;
  border-top: 1px dashed var(--color-border);
}
.pending-divider .paused { color: var(--color-danger); }
.pending-resume {
  border: 0.5px solid var(--color-border-strong);
  background: var(--color-surface);
  color: var(--color-primary);
  border-radius: 999px;
  padding: 3px 9px;
  font-size: 11.5px;
  cursor: pointer;
}
.pending-bubble {
  max-width: 85%;
  padding: 9px 13px;
  border-radius: 16px 16px 4px 16px;
  border: 1px dashed var(--color-border-strong);
  color: var(--color-text-secondary);
  font-size: var(--chat-font-size);
  line-height: 1.6;
}
.pending-tag {
  font-size: 10.5px;
  color: var(--color-text-muted);
  margin-bottom: 3px;
}
.pending-text {
  white-space: pre-wrap;
  word-break: break-word;
}
.pending-meta {
  margin-top: 4px;
  font-size: 10.5px;
  color: var(--color-text-muted);
}
.pending-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 4px;
}
.pending-del {
  border: 0;
  background: transparent;
  color: var(--color-text-muted);
  font-size: 11px;
  cursor: pointer;
  padding: 2px 4px;
  border-radius: 5px;
}
.pending-del:hover {
  color: var(--color-danger);
  background: var(--color-danger-soft);
}
.composer-reopen {
  position: fixed;
  right: 14px;
  bottom: calc(var(--bottom-tabs-height) + var(--safe-bottom) + 12px);
  z-index: 101;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  border-radius: 999px;
  border: 0.5px solid var(--color-border);
  background: var(--color-surface-strong);
  color: var(--color-primary);
  font-size: 12.5px;
  box-shadow: var(--shadow-md);
  cursor: pointer;
}
.composer-reopen svg { width: 14px; height: 14px; }
.composer-row.voice-row { justify-content: center; }
/* 语音条是纯控件（长按=录音手势）：不禁选中，iOS 会把隔壁胶囊的「连续对话」当选中锚点，
   上滑取消时还会一路延伸选到消息正文。touch-action / pointerdown.prevent 都拦不住选中手势。 */
.composer-row.voice-row {
  -webkit-user-select: none;
  user-select: none;
  -webkit-touch-callout: none;
}
.voice-group {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
/* 语音模式固定两个 36pt 按钮（icon-pill 的 32/34 与灰字色不能覆盖） */
.icon-pill.hold-btn,
.icon-pill.voice-continuous,
.icon-pill.voice-stop { width: 36px; height: 36px; }
.icon-pill.hold-btn { position: relative; color: var(--color-primary); background: var(--color-primary-soft); border-color: var(--color-border); touch-action: none; }
.icon-pill.hold-btn:hover:not(:disabled) { background: var(--color-primary-soft); border-color: var(--color-primary); }
/* 模式切换：两段式胶囊，当前档高亮（只切档，不录音） */
.mode-switch {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 2px;
  border-radius: 999px;
  border: 0.5px solid var(--color-border);
  background: var(--color-primary-soft);
}
.seg {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 30px;
  padding: 0 9px;
  border: 0;
  border-radius: 999px;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  transition: background var(--transition-fast), color var(--transition-fast), box-shadow var(--transition-fast);
  -webkit-tap-highlight-color: transparent;
}
.seg:hover { color: var(--color-primary); }
.seg-ico { display: block; width: 15px; height: 15px; }
.seg.on {
  background: var(--color-surface-strong);
  color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}
[data-theme="dark"] .seg.on { background: rgba(255, 255, 255, 0.12); }
/* SF Symbol hand.tap 的位图蒙版，跟随 currentColor（与 mockup 同源） */
.hold-icon,
.seg-ico-mask {
  display: block;
  background: currentColor;
  -webkit-mask: url("data:image/png;base64,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") center / contain no-repeat;
  mask: url("data:image/png;base64,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") center / contain no-repeat;
}
.hold-icon { width: 21px; height: 21px; }
.seg-ico-mask { width: 15px; height: 15px; }
.icon-pill.hold-btn.holding {
  background: var(--color-primary);
  color: #fff;
  animation: hold-pulse 1.6s ease-in-out infinite;
}
.icon-pill.hold-btn.holding::before {
  content: '';
  position: absolute;
  inset: -3px;
  border-radius: 50%;
  border: 1.5px solid color-mix(in srgb, var(--color-primary) 55%, transparent);
}
/* 录音中 hover 保持深绿：.hold-btn:hover 特异性更高，不写这条会把底色盖成浅绿 */
.icon-pill.hold-btn.holding:hover:not(:disabled) {
  background: var(--color-primary);
  border-color: var(--color-primary);
}
.icon-pill.hold-btn.holding .hold-icon { background: #fff; }
@keyframes hold-pulse {
  0%, 100% { box-shadow: 0 0 0 0 color-mix(in srgb, var(--color-primary) 42%, transparent); }
  50% { box-shadow: 0 0 0 7px color-mix(in srgb, var(--color-primary) 0%, transparent); }
}
/* 移动端滑进取消区：红底白图标，停掉绿色呼吸（必须写在 .holding 之后才盖得住）。
   桌面端录音中不再是红色——红色只表示取消/停止/挂断，发送是绿色箭头。 */
.icon-pill.hold-btn.cancel,
.icon-pill.hold-btn.cancel:hover:not(:disabled) {
  background: var(--color-danger);
  border-color: var(--color-danger);
  color: #fff;
  animation: none;
}
.icon-pill.hold-btn.cancel::before { border-color: rgba(255, 255, 255, 0.5); }
.icon-pill.hold-btn .hold-mic { width: 20px; height: 20px; display: block; }
.icon-pill.hold-btn .hold-send { width: 20px; height: 20px; display: block; }
/* 桌面端：录音中的「取消本次」占一个常驻槽位（绝对定位会飘到胶囊外，窄窗直接越界） */
.hold-slot { display: inline-flex; align-items: center; gap: 4px; }
/* 连续档左侧空位：桌面＝取消槽 36 + gap 4 + 按钮 36；移动端没有取消槽＝按钮 36 */
.hold-slot-ghost { display: block; width: 76px; height: 34px; }
.hold-cancel-slot {
  width: 36px;
  height: 36px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.icon-pill.hold-cancel {
  width: 36px;
  height: 36px;
  padding: 0;
  border: 0.5px solid color-mix(in srgb, var(--color-danger) 34%, transparent);
  background: var(--color-danger-soft);
  color: var(--color-danger);
}
.icon-pill.hold-cancel:hover:not(:disabled) {
  background: var(--color-danger);
  border-color: var(--color-danger);
  color: #fff;
}
.icon-pill.hold-cancel svg { width: 17px; height: 17px; }
/* 移动端：上滑取消提示（浮在输入卡上方，不占布局） */
.hold-hint {
  position: absolute;
  left: 50%;
  bottom: calc(100% + 8px);
  transform: translateX(-50%);
  padding: 7px 13px;
  border-radius: 999px;
  border: 0.5px solid var(--color-border);
  background: var(--color-surface-strong);
  color: var(--color-text-secondary);
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
  box-shadow: var(--shadow-md);
  pointer-events: none;
  /* 高于「↓ 回到底部」按钮（101），否则看历史时它正好盖住提示 */
  z-index: 102;
}
.hold-hint.armed {
  background: var(--color-danger);
  border-color: var(--color-danger);
  color: #fff;
}
.icon-pill.voice-continuous { color: var(--color-danger); }
.icon-pill.voice-continuous:hover:not(:disabled) {
  background: var(--color-danger-soft);
  color: var(--color-danger);
}
.icon-pill.voice-stop {
  color: var(--color-danger);
  background: var(--color-danger-soft);
}
.icon-pill.voice-stop:hover:not(:disabled) {
  background: var(--color-danger-soft);
  color: var(--color-danger);
}
.voice-stop .stop-core {
  width: 11px;
  height: 11px;
  border-radius: 2px;
  background: currentColor;
}
/* 停止键槽位常驻：语音行居中，生成开始/结束时增删按钮会让整行横跳 */
.voice-stop-slot {
  width: 36px;
  height: 36px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.icon-pill {
  width: 32px;
  height: 32px;
  padding: 0;
  border-radius: 999px;
  border: 0.5px solid transparent;
  background: transparent;
  color: var(--color-text-muted);
  display: grid;
  place-items: center;
  cursor: pointer;
  transition: background var(--transition-fast), color var(--transition-fast), border-color var(--transition-fast);
}
.icon-pill svg { width: 17px; height: 17px; }
.icon-pill:hover:not(:disabled) { background: var(--color-primary-soft); color: var(--color-primary); }
.icon-pill:disabled { opacity: 0.35; cursor: default; }
.icon-pill.on {
  background: var(--color-primary-soft);
  border-color: var(--color-border);
  color: var(--color-primary);
}
.file-input {
  position: absolute;
  width: 1px;
  height: 1px;
  opacity: 0;
  pointer-events: none;
}
.scroll-bottom-btn {
  position: absolute;
  left: 50%;
  transform: translateX(-50%);
  z-index: 101;
  width: 38px;
  height: 38px;
  border-radius: 50%;
  background: var(--color-surface-strong);
  border: 0.5px solid var(--color-border);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  box-shadow: var(--shadow-md);
  color: var(--color-text-secondary);
  font-size: 18px;
  user-select: none;
}
.chat-error {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  margin: var(--space-md) 0;
  color: var(--color-danger);
  font-size: 12.5px;
}
.chat-error-text { line-height: 1.5; overflow-wrap: anywhere; }
.chat-error-retry {
  flex-shrink: 0;
  padding: 3px 10px;
  border-radius: 999px;
  border: 0.5px solid currentColor;
  background: transparent;
  color: inherit;
  font-size: 12px;
  cursor: pointer;
}
@media (max-width: 1099px) {
  .chat-composer-card { border-radius: 18px; padding: 6px 8px; }
  .composer-row { margin-top: 6px; }
  .icon-pill { width: 34px; height: 34px; }
  /* 图标档每段只有 33px 宽，撑到 40px 让手指好点（胶囊总宽仍远小于原来的文字档） */
  .seg { min-width: 40px; }
  .hold-slot-ghost { width: 36px; }
  .chat-card {
    width: 100%;
    margin: 0;
    border-radius: 0;
    border: 0;
    box-shadow: none;
    background: transparent;
  }
  .chat-messages { padding: 8px 14px; }
  .chat-column { max-width: 100%; }
}
</style>
