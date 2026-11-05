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
 *       → nextTick + scrollToBottom   滚动到底部
 *       → setTimeout 200ms            激活上滑加载监听（防误触发）
 *       → fetchLlmConfig()            根据智能体重拉 LLM 参数
 *     【分支 B】同会话重载 (needsRefetch=true)
 *       → 流程同分支 A，完成后 chat.needsRefetch = false
 *     【分支 C】无会话 ID
 *       → 冷启动首次：fetchList() 后 router.replace 到列表第一条（最新会话）
 *       → 其余情况：chat.reset()（空白欢迎页，会话由首条消息懒创建）
 *
 * ── 三、用户发送消息 ─────────────────────────────────────────────────────────
 *   handleSend() (L459):
 *     → chat.addMessage(user msg)    本地添加用户消息到列表
 *     → convs.prepend()              当前会话置顶
 *     → nextTick + scrollToBottom    滚到底部
 *     → sendMessage(content, opts)   SSE 流式对话（来自 useChatSSE composable）
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
 *   watch(chat.streamingMessage?.content)   (L450)  → scrollToBottom()
 *   watch(chat.streamingMessage?.reasoning) (L451)  → scrollToBottom()
 *   watch(chat.llmParams)                   (L107)  → 同步更新 llmSettings
 *
 * ── 七、输入框收起 ──────────────────────────────────────────────────────────
 *   toggleInput()          → 手机上 ⌄ 按钮收起/展开整张输入卡（收起要同时 blur 退键盘）
 *   watch(inputVisible)    → nextTick + 触发 textarea input 事件（自适应高度）
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
import ToolCallToast from '@/components/ToolCallToast.vue'
import SelectionActionBar from '@/components/SelectionActionBar.vue'
import AttachmentTray from '@/components/chat/AttachmentTray.vue'
import { prepareImage } from '@/utils/image'

import PageTopBar from '@/components/PageTopBar.vue'
import { useMobile } from '@/composables/useMobile'

const route = useRoute()
const router = useRouter()
const chat = useChatStore()
const convs = useConversationsStore()
const { sendMessage, cancel } = useChatSSE()
const nMessage = useMessage()
const { isMobile } = useMobile()
const { isIOS } = useDevice()

onMounted(() => {
  fetchAgents()
  document.addEventListener('visibilitychange', onVisibilityChange)
  // 输入区高度变化（收起/展开/挂图）时重算「↓」的位置
  if (mobileInputRef.value) {
    composerObserver = new ResizeObserver(onLayoutResize)
    composerObserver.observe(mobileInputRef.value)
  }
  window.addEventListener('resize', onLayoutResize)
  nextTick(onLayoutResize)
  // 复用已存在会话（从其它 tab 返回，route id 与 store 中 conversationId 相同，
  // 导致 watcher 三分支全跳过）时，主动同步一次：拉取最新消息 + 滚到底，
  // 避免看到陈旧列表且停在旧滚动位置。
  // 注意：仅当 route id === store conversationId 时才补，避免与 watcher 分支 A（切换会话）重复请求。
  if (chat.conversationId && route.params.id === chat.conversationId) {
    syncCurrentConversation()
  }
})

const inputValue = ref('')
const messagesEndRef = ref(null)
const streamingBubbleRef = ref(null)
const isLoadingHistory = ref(false)
const isFirstLoad = ref(true)
const messagesContainerRef = ref(null)
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
}
let hideCooldownTimer = null
let showCooldownActive = false
let scrollRafPending = false

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
  if (!convId) return

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
      if (chat.isStreaming && streamingBubbleRef.value) {
        const container = messagesContainerRef.value
        if (!container) return
        const distFromBottom = container.scrollHeight - container.scrollTop - container.clientHeight
        if (distFromBottom > 80) return
        container.scrollTop = container.scrollHeight
      } else {
        const container = messagesContainerRef.value
        if (container) container.scrollTop = container.scrollHeight
      }
    })
  })
}

function handleScrollToBottomBtn() {
  nextTick(() => {
    requestAnimationFrame(() => {
      const el = messagesContainerRef.value
      if (el) el.scrollTop = el.scrollHeight
    })
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
  if (!convId) return
  const seq = ++loadSeq
  isLoadingHistory.value = true
  try {
    let url = `/api/conversations/${convId}/messages-v2?limit=7`
    if (cursor) {
      url += `&cursor=${cursor}&direction=${direction}`
    }
    const res = await api.get(url)
    // convId 也要校验：点「新对话」走的是 chat.reset()（conversationId 变 null），
    // 它不会自增 loadSeq，只靠 seq 拦不住在途请求写回旧会话消息。
    if (isStale(seq, convId)) return
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
            resolve()
          })
        })
      } else {
        chat.setMessages(msgs)
      }
      // 上面可能有 await（前置历史要等 DOM 重排），期间又切了会话就到此为止
      if (isStale(seq, convId)) return
      chat.oldestCursor = res.oldest_cursor
      chat.hasMoreBefore = res.has_more_before
      // 批量获取关联记忆详情
      await fetchAssociatedMemories()
    }
  } catch (e) {
    if (isStale(seq, convId)) return
    console.error('Failed to load history', e)
    nMessage.error('加载历史消息失败')
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
async function syncCurrentConversation() {
  if (!chat.conversationId) return
  if (chat.isStreaming) return
  const now = Date.now()
  if (now - lastSyncAt < 3000) return
  lastSyncAt = now
  const seq = routeSeq
  await loadHistory(chat.conversationId)
  if (seq !== routeSeq) return
  await nextTick()
  requestAnimationFrame(() => {
    const el = messagesContainerRef.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

function onVisibilityChange() {
  if (document.visibilityState === 'visible') {
    syncCurrentConversation()
  }
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
  // 顶栏会话切换器是「先 chat.reset() 再 router.push」，reset 会把 isStreaming 置 false，
  // 那时旧流还在跑却已经没人拦它了。cancel() 对已结束的流是空操作，无条件调即可。
  // 懒创建场景不受影响：done 先写 chat.conversationId 再 router.replace，那时 newId === 当前会话。
  if (newId !== chat.conversationId) {
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
    await nextTick()
    requestAnimationFrame(() => {
      const el = messagesContainerRef.value
      if (el) el.scrollTop = el.scrollHeight
    })
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
    await nextTick()
    requestAnimationFrame(() => {
      const el = messagesContainerRef.value
      if (el) el.scrollTop = el.scrollHeight
    })
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
  // 其余情况（顶栏「新对话」）直接显示空白页，会话由首条消息懒创建。
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
    // 新对话没有在途的历史请求，旧请求的 spinner 不该继续顶着（它的 finally 还要等网络）
    isLoadingHistory.value = false
  }
}, { immediate: true })

onBeforeUnmount(() => {
  clearTimeout(hideCooldownTimer)
  composerObserver?.disconnect()
  window.removeEventListener('resize', onLayoutResize)
  document.removeEventListener('visibilitychange', onVisibilityChange)
  if (chat.isStreaming) cancel()
})

watch(() => chat.streamingMessage?.content, () => scrollToBottom())
watch(() => chat.streamingMessage?.reasoning, () => scrollToBottom())
async function handleSend() {
  if (chat.isStreaming) return

  const content = inputValue.value.trim()
  const ready = draftImages.value.filter(d => d.status === 'done')
  const failed = draftImages.value.some(d => d.status === 'error')
  const uploading = draftImages.value.some(d => d.status === 'uploading')

  if (!content && !ready.length) {
    if (failed) nMessage.warning('有图片上传失败，请重试或移除')
    return
  }
  if (uploading) {
    nMessage.info('图片还在上传，稍等')
    return
  }
  if (failed) {
    nMessage.warning('有图片上传失败，请重试或移除')
    return
  }

  exitSelectionMode()

  const imageIds = ready.map(d => d.imageId)
  chat.addMessage({
    id: Date.now().toString(),
    role: 'user',
    content,
    imageIds,
    created_at: new Date().toISOString()
  })
  inputValue.value = ''
  // 预览 URL 在这里回收：消息里的图改走鉴权接口读，不依赖本地 objectURL
  clearDrafts()

  // 把当前会话移到列表最前面
  const conv = convs.list.find(c => c.id === chat.conversationId)
  if (chat.conversationId && conv?.title) {
    convs.prepend({ id: chat.conversationId, title: conv.title })
  }
  // 立即滚到底部（不管用户在浏览什么位置）
  nextTick(() => {
    requestAnimationFrame(() => {
      const el = messagesContainerRef.value
      if (el) el.scrollTop = el.scrollHeight
    })
  })

  const opts = {
    conversationId: chat.conversationId,
    agentId: chat.agentId,
    attachments: imageIds,
    // 仅新会话需要：已有会话的模式/无痕走 PATCH 单独落库
    initMode: chat.conversationId ? undefined : chat.mode,
    ephemeral: chat.conversationId ? false : chat.ephemeral
  }
  await sendMessage(content, opts)
}

function handleStop() {
  const convId = chat.conversationId
  // 先断 SSE 让 UI 立即停下，再通知服务端终止 ReAct（与 macOS 客户端顺序一致）。
  // shortcut: 新会话首条消息流式时前端还拿不到 conversation_id，只能断流，
  // 服务端会照常跑完；等 done 回传 id 后才有服务端停止能力。
  const partial = chat.streamingMessage
  cancel()
  if (partial && (partial.content || partial.reasoning)) {
    chat.addMessage({ ...partial, id: `local-${Date.now()}`, toolCalls: [] })
  }
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
  requestAnimationFrame(() => {
    const el = messagesContainerRef.value
    if (el) el.scrollTop = el.scrollHeight
  })
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
  if (chat.isStreaming) return false
  // 上传中不给发；失败态故意放行到这里，让 handleSend 弹出「有图片上传失败」的提示
  if (draftImages.value.some(d => d.status === 'uploading')) return false
  return !!inputValue.value.trim() || draftImages.value.length > 0
})

function pickImages() {
  if (chat.isStreaming) return
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
      error: ''
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
      <div class="chat-column">

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
        />
        <div style="position: relative;">
          <div ref="streamingBubbleRef" v-if="chat.streamingMessage">
            <MessageBubble
              :message="chat.streamingMessage"
              :is-history="false"
            />
          </div>
          <ToolCallToast />
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
          overflow: 'hidden',
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
          overflow: 'hidden',
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
      <div class="chat-composer-card" @paste="onComposerPaste" @dragover.prevent @drop.prevent="onComposerDrop">
        <AttachmentTray
          v-if="draftImages.length"
          :items="draftImages"
          :limit="MAX_DRAFTS"
          @add="pickImages"
          @remove="removeDraft"
          @retry="retryDraft"
          @clear="clearDrafts"
        />
        <NInput
          v-model:value="inputValue"
          type="textarea"
          placeholder="发消息..."
          :disabled="chat.isStreaming"
          :autosize="{ minRows: 1, maxRows: isMobile ? 4 : 6 }"
          class="chat-input"
          @keydown="(e) => { if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); handleSend() }}"
          :bordered="false"
          style="background: transparent; flex: 1;"
        />
        <div class="composer-row">
          <div class="composer-left">
            <NTooltip :disabled="isMobile" placement="top">
              <template #trigger>
                <button class="icon-pill" type="button" aria-label="添加图片" :disabled="chat.isStreaming" @click="pickImages">
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
            <NButton
              v-else
              type="error"
              size="small"
              @click="handleStop"
              style="border-radius: 50%; width: 40px; height: 40px; padding: 0;"
            >■</NButton>
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
}
.chat-composer-card {
  border: 0.5px solid var(--color-border);
  border-radius: 22px;
  padding: 8px 10px;
  background: var(--color-input);
  height: 100%;
  display: flex;
  flex-direction: column;
  box-shadow: var(--shadow-sm);
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
