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
 *     三个分支，都会先 cancel() 旧 SSE 流
 *     【分支 A】切换到新会话 (newId !== convId)
 *       → loadHistory(newId)          GET messages-v2        拉取最近 20 条消息
 *       → fetchAssociatedMemories()   POST /memories/batch   批量获取关联记忆详情
 *       → nextTick + scrollToBottom   滚动到底部
 *       → setTimeout 200ms            激活上滑加载监听（防误触发）
 *       → fetchLlmConfig()            根据智能体重拉 LLM 参数
 *     【分支 B】同会话重载 (needsRefetch=true)
 *       → 流程同分支 A，完成后 chat.needsRefetch = false
 *     【分支 C】无会话 ID
 *       → GET /api/conversations/default   尝试获取默认会话
 *       → 有则 router.replace 跳转（重新触发分支 A）
 *       → 无则 chat.reset() + fetchLlmConfig()（显示空白页）
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
 * ── 七、输入框展开/收起 ──────────────────────────────────────────────────────
 *   watch(inputVisible)  (L60)   → nextTick + 触发 textarea input 事件（自适应高度）
 *   watch(inputExpanded) (L452)  → 条件滚动到底部
 *
 * ── 八、页面卸载 ─────────────────────────────────────────────────────────────
 *   onBeforeUnmount (L446): cancel() 中断正在进行的 SSE 流
 * ============================================================================
 */

import { ref, computed, watch, nextTick, onBeforeUnmount, onMounted } from 'vue'
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
  NPopover,
  NInputNumber,
  NSwitch,
  useMessage
} from 'naive-ui'
import MessageBubble from '@/components/MessageBubble.vue'
import ToolCallToast from '@/components/ToolCallToast.vue'

import PageTopBar from '@/components/PageTopBar.vue'
import TopBarBtn from '@/components/TopBarBtn.vue'
import { useDebugMode } from '@/composables/useDebugMode'
import { useMobile } from '@/composables/useMobile'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'

const auth = useAuthStore()
const isAdmin = computed(() => auth.user?.isAdmin === true)
const theme = useThemeStore()
const route = useRoute()
const router = useRouter()
const chat = useChatStore()
const convs = useConversationsStore()
const { sendMessage, cancel } = useChatSSE()
const nMessage = useMessage()
const { isMobile } = useMobile()
const { isIOS } = useDevice()
const { debugMode, toggle: toggleDebug } = useDebugMode()

onMounted(() => {
  fetchLlmConfig()
  fetchAgents()
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

const { isBarVisible, showBar } = useScrollTopBar(messagesContainerRef)

const inputVisible = ref(true)
const inputExpanded = ref(false)
let hideCooldownTimer = null
let showCooldownActive = false
let scrollRafPending = false

if (isMobile.value) inputVisible.value = false
watch(isMobile, (val) => { if (val) inputVisible.value = false })
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
      document.querySelector('.chat-input')?.focus()
    })
  }
}

function toggleExpand() {
  inputExpanded.value = !inputExpanded.value
}

// LLM 参数（从后端 API 获取，不存 localStorage）
const showLlmSettings = ref(false)
const llmSettings = ref({ temperature: null, max_tokens: null, thinking: true, deepMode: true })

async function fetchLlmConfig() {
  try {
    const conv = convs.list.find(c => c.id === chat.conversationId)
    const agentId = conv?.agent_id || chat.agentId
    const qs = agentId ? `?agent_id=${encodeURIComponent(agentId)}` : ''
    const res = await api.get('/api/llm-config' + qs)
    if (res && typeof res === 'object') {
      llmSettings.value = {
        temperature: res.temperature ?? null,
        max_tokens: res.max_tokens ?? null,
        thinking: res.thinking ?? true,
        deepMode: res.deep_mode ?? llmSettings.value.deepMode
      }
    }
  } catch (e) {
    console.error('Failed to fetch LLM config', e)
  }
}

// agent_matched 后更新参数
watch(() => chat.llmParams, (params) => {
  if (params) {
    llmSettings.value = {
      temperature: params.temperature ?? llmSettings.value.temperature,
      max_tokens: params.max_tokens ?? llmSettings.value.max_tokens,
      thinking: params.thinking ?? llmSettings.value.thinking,
      deepMode: params.deep_mode ?? llmSettings.value.deepMode
    }
  }
})

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

    // 刷新 LLM 配置
    fetchLlmConfig()
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
  inputVisible.value = true
  nextTick(() => {
    requestAnimationFrame(() => {
      const el = messagesContainerRef.value
      if (el) el.scrollTop = el.scrollHeight
    })
  })
}

async function loadHistory(convId, direction = 'before', cursor = null) {
  if (!convId) return
  isLoadingHistory.value = true
  try {
    let url = `/api/conversations/${convId}/messages-v2?limit=20`
    if (cursor) {
      url += `&cursor=${cursor}&direction=${direction}`
    }
    const res = await api.get(url)
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
            const newScrollHeight = scrollContainer.scrollHeight
            scrollContainer.scrollTop = oldScrollTop + (newScrollHeight - oldScrollHeight)
            resolve()
          })
        })
      } else {
        chat.setMessages(msgs)
      }
      chat.oldestCursor = res.oldest_cursor
      chat.hasMoreBefore = res.has_more_before
      // 批量获取关联记忆详情
      await fetchAssociatedMemories()
    }
  } catch (e) {
    console.error('Failed to load history', e)
    nMessage.error('加载历史消息失败')
  } finally {
    isLoadingHistory.value = false
  }
}

async function fetchAssociatedMemories() {
  const allMemIds = new Set()
  for (const msg of chat.messages) {
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
    for (const msg of chat.messages) {
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
watch(() => route.params.id, async (newId, oldId) => {
  // --- 离开旧会话时，取消正在进行的 SSE 流 ---
  // 避免旧会话的流式响应继续污染新会话的消息列表
  if (oldId && chat.isStreaming) {
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
    await loadHistory(newId)

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
    setTimeout(() => { isInitialScrollDone.value = true }, 200)

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

    // ⑤ 根据当前智能体重新拉取 LLM 配置（temperature / max_tokens / thinking / deepMode）
    fetchLlmConfig()

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
    setTimeout(() => { isInitialScrollDone.value = true }, 200)

    const currentConv2 = convs.list.find(c => c.id === newId)
    if (currentConv2?.agent_id) {
      chat.agentId = currentConv2.agent_id
      chat.agentName = currentConv2.agent_name || ''
    } else {
      chat.agentId = null
      chat.agentName = null
    }
    fetchLlmConfig()

  // ====================================================================
  // 分支 C：路由里没有会话 ID（用户访问 /chat 或被重定向到无 id 状态）
  // 先尝试通过 API 获取默认会话并跳转，若无则显示空白欢迎页
  // ====================================================================
  } else if (!newId) {
    inputVisible.value = true
    isInitialScrollDone.value = false
    if (chat.isStreaming) cancel()

    // 通过 API 获取默认会话，避免拉全量列表
    try {
      const defaultConv = await api.get('/api/conversations/default')
      if (defaultConv?.id) {
        // 找到默认会话 → 清空状态并重定向到 /chat/:id
        chat.reset()
        router.replace(`/chat/${defaultConv.id}`)
        return // 提前返回，router.replace 会再次触发此 watcher 进入分支 A
      }
    } catch (e) {
      console.error('Failed to get default conversation', e)
    }

    // 无默认会话（首次使用），显示空白状态
    chat.reset()
    fetchLlmConfig()
  }
}, { immediate: true })

onBeforeUnmount(() => {
  clearTimeout(hideCooldownTimer)
  if (chat.isStreaming) cancel()
})

watch(() => chat.streamingMessage?.content, () => scrollToBottom())
watch(() => chat.streamingMessage?.reasoning, () => scrollToBottom())
watch(inputExpanded, () => {
  const container = messagesContainerRef.value
  if (!container) return
  const distFromBottom = container.scrollHeight - container.scrollTop - container.clientHeight
  if (distFromBottom <= 80) scrollToBottom()
})

async function handleSend() {
  const content = inputValue.value.trim()
  if (!content || chat.isStreaming) return

  chat.addMessage({ id: Date.now().toString(), role: 'user', content, created_at: new Date().toISOString() })
  inputValue.value = ''
  inputExpanded.value = false
  // 把当前会话移到列表最前面
  if (chat.conversationId && currentTitle.value) {
    convs.prepend({ id: chat.conversationId, title: currentTitle.value })
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
    deepMode: llmSettings.value.deepMode,
    agentId: chat.agentId
  }
  if (llmSettings.value.temperature !== null) {
    opts.temperature = llmSettings.value.temperature
  }
  if (llmSettings.value.max_tokens !== null) {
    opts.maxTokens = llmSettings.value.max_tokens
  }
  if (llmSettings.value.thinking !== null) {
    opts.thinking = llmSettings.value.thinking
  }
  await sendMessage(content, opts)
}

function handleStop() {
  cancel()
}

function handleScroll(e) {
  const container = e.target
  const distFromBottom = container.scrollHeight - container.scrollTop - container.clientHeight
  isAwayFromBottom.value = distFromBottom > 200

  // 显示/隐藏输入框（非移动端）
  // 显示：即时触发，无需延迟（跟手体验）
  // 隐藏：即时触发，但在「刚显示后的冷却期」内拦截，防止 iPad Safari 弹性滚动振荡导致闪烁
  if (isInitialScrollDone.value && !isMobile.value) {
    if (distFromBottom < 30 && !inputVisible.value) {
      inputVisible.value = true
      showCooldownActive = true
      clearTimeout(hideCooldownTimer)
      hideCooldownTimer = setTimeout(() => { showCooldownActive = false }, 300)
    } else if (distFromBottom > 200 && inputVisible.value && !showCooldownActive) {
      inputVisible.value = false
    }
  }

  const threshold = container.clientHeight * 0.15
  const nearTop = container.scrollTop <= threshold
  if (nearTop) {
    console.log('[scroll-debug]', JSON.stringify({
      isInitialScrollDone: isInitialScrollDone.value,
      scrollTop: container.scrollTop,
      threshold,
      hasMoreBefore: chat.hasMoreBefore,
      isLoadingHistory: isLoadingHistory.value,
      convId: chat.conversationId
    }))
  }
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
  <div style="display: flex; flex-direction: column; height: 100vh; background: var(--color-bg-page); min-width: 0;">
        <PageTopBar :agent-id="currentAgentId" :agent-name="currentAgentName" :visible="!isMobile || isBarVisible" :agents="agentsList" @select-agent="handleSelectAgent" @popover-open="fetchAgents" >
      <template #right>
        <TopBarBtn v-if="!isMobile && isAdmin" :label="debugMode ? '调试:开' : '调试'" @click="toggleDebug" />
        <TopBarBtn :label="theme.isDark ? '浅色' : '深色'" @click="theme.toggle()" />
      </template>
    </PageTopBar>

    <div
      ref="messagesContainerRef"
      @scroll="handleScroll"
      @touchstart="isMobile ? showBar() : null"
      @click="handleContainerClick"
      style="flex: 1; overflow-y: auto; padding: var(--space-lg); display: flex; justify-content: center;"
    >
      <div style="width: 100%; max-width: 900px; position: relative;">
        <div v-if="isLoadingHistory" style="text-align: center; padding: 20px 0;">
          <div style="width: 24px; height: 24px; border: 2px solid var(--color-border); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 0.8s linear infinite; display: inline-block;"></div>
        </div>

        <div v-if="chat.messages.length === 0 && !chat.streamingMessage" style="padding: 100px 0; text-align: center; color: var(--color-text-muted);">
          开始聊天吧
        </div>

        <MessageBubble
          v-for="msg in chat.messages"
          :key="msg.id"
          :message="msg"
          :is-history="true"
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
        <div v-if="chat.error && !chat.streamingMessage" style="text-align: center; margin: var(--space-md) 0;">
          <NButton size="small" type="error" quaternary>
            {{ chat.error }}
          </NButton>
        </div>

        <div
          ref="messagesEndRef"
          :style="{
            height: inputExpanded ? (isMobile ? '75vh' : '35vh') : '15px',
            transition: 'height 0.2s ease-out'
          }"
        />
      </div>
    </div>

    <div
      v-if="isMobile && !inputVisible"
      @click="toggleInput"
      :style="{
        width: '60px',
        height: '30px',
        background: 'rgba(91, 106, 191, 0.85)',
        borderRadius: '30px 30px 0 0',
        display: 'flex',
        alignItems: 'flex-end',
        justifyContent: 'center',
        marginLeft: 'auto',
        marginRight: 'auto',
        cursor: 'pointer',
        boxShadow: '0 -2px 10px rgba(0,0,0,0.1)',
        color: 'white',
        fontSize: '20px',
        fontWeight: 'bold',
        flexShrink: 0
      }"
    >
      ↑
    </div>

    <!-- 查看历史消息时，滚动到底部按钮 -->
    <div
      v-if="!isMobile && !inputVisible && isAwayFromBottom"
      @click="handleScrollToBottomBtn"
      :style="{
        position: 'fixed',
        bottom: '24px',
        left: '50%',
        transform: 'translateX(-50%)',
        zIndex: 101,
        width: '40px',
        height: '40px',
        borderRadius: '50%',
        background: 'rgba(91, 106, 191, 0.8)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        cursor: 'pointer',
        boxShadow: '0 2px 10px rgba(0,0,0,0.2)',
        color: 'white',
        fontSize: '20px',
        userSelect: 'none'
      }"
    >
      ↓
    </div>

    <div
      ref="mobileInputRef"
      :style="[
        !isMobile ? {
          padding: inputVisible ? 'var(--space-md) var(--space-lg)' : '0 var(--space-lg)',
          maxWidth: 'min(88%, 56rem)',
          marginLeft: 'auto',
          marginRight: 'auto',
          width: '100%',
          background: 'var(--color-bg-page)',
          borderTop: 'none',
          flexShrink: 0,
          height: inputVisible ? (inputExpanded ? '50vh' : 'auto') : '0',
          overflow: 'hidden',
          opacity: inputVisible ? 1 : 0,
          transform: inputVisible ? 'scaleY(1)' : 'scaleY(0.8)',
          transformOrigin: 'bottom right',
          transition: skipInputTransition.value ? 'none' : 'height 0.25s ease-out, padding 0.25s ease-out, opacity 0.2s ease-out, transform 0.25s ease-out'
        } : {
          padding: inputVisible ? 'var(--space-md) var(--space-lg)' : '0 var(--space-lg)',
          background: 'var(--color-bg-page)',
          borderTop: 'none',
          flexShrink: 0,
          height: inputVisible ? (inputExpanded ? '75vh' : 'auto') : '0',
          overflow: 'hidden',
          opacity: inputVisible ? 1 : 0,
          transition: skipInputTransition.value ? 'none' : 'height 0.25s ease-out, padding 0.25s ease-out, opacity 0.2s ease-out'
        }
      ]"
    >
      <div :style="{ border: `1px solid var(--color-bg-page)`, borderRadius: 'var(--radius-xl)', padding: 'var(--space-md) var(--space-md)', background: 'var(--color-surface)', height: '100%', display: 'flex', flexDirection: 'column', boxShadow: '0 2px 12px rgba(0,0,0,0.06)' }">
        <NInput
          v-model:value="inputValue"
          type="textarea"
          placeholder="发消息..."
          :disabled="chat.isStreaming"
          :autosize="(isMobile || inputExpanded) ? false : { minRows: 1, maxRows: 6 }"
          class="chat-input"
          @keydown="(e) => { if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && !inputExpanded) { e.preventDefault(); handleSend() }}"
          :bordered="false"
          style="background: transparent; flex: 1;"
        />
        <div :style="{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: '8px' }">
          <div>
            <NPopover trigger="click" :show="showLlmSettings" @update:show="(v) => showLlmSettings = v">
              <template #trigger>
                <NButton quaternary size="small" style="color: var(--color-text-muted)" @click="showLlmSettings = !showLlmSettings" title="LLM 参数">
                  ⚙
                </NButton>
              </template>
              <div style="width: 240px; padding: 4px 0;">
                <!-- 模型选择 -->
                <div style="margin-bottom: 6px; display: flex; align-items: center; justify-content: space-between;">
                  <div>
                    <span style="font-size: 13px; color: var(--color-text);">Pro 模型</span>
                    <div style="font-size: 11px; color: var(--color-text-muted);">v4-pro，推理更强但较慢</div>
                  </div>
                  <NSwitch
                    :value="llmSettings.deepMode"
                    @update:value="(v) => { llmSettings.deepMode = v }"
                    size="small"
                  />
                </div>
                <div style="border-top: 1px solid var(--color-border); margin: 8px 0;"></div>
                <!-- 思考模式 -->
                <div style="margin-bottom: 6px; display: flex; align-items: center; justify-content: space-between;">
                  <span style="font-size: 13px; color: var(--color-text);">思考模式</span>
                  <NSwitch
                    :value="llmSettings.deepMode ? true : llmSettings.thinking"
                    @update:value="(v) => { llmSettings.thinking = v }"
                    :disabled="llmSettings.deepMode"
                    size="small"
                  />
                </div>
                <div style="margin-bottom: 12px;">
                  <div style="font-size: 13px; margin-bottom: 4px; color: var(--color-text);">Temperature</div>
                  <NInputNumber
                    :value="llmSettings.temperature"
                    @update:value="(v) => { llmSettings.temperature = v }"
                    :min="0" :max="2" :step="0.1"
                    placeholder="默认 1.0"
                    size="small"
                    clearable
                    style="width: 100%"
                  />
                </div>
                <div>
                  <div style="font-size: 13px; margin-bottom: 4px; color: var(--color-text);">Max Tokens</div>
                  <NInputNumber
                    :value="llmSettings.max_tokens"
                    @update:value="(v) => { llmSettings.max_tokens = v }"
                    :min="1" :step="100"
                    placeholder="默认 4096"
                    size="small"
                    clearable
                    style="width: 100%"
                  />
                </div>
              </div>
            </NPopover>
            <NButton quaternary size="small" @click="toggleExpand" style="color: var(--color-text-muted)">
              {{ inputExpanded ? '⤡ 收起输入框' : '⤢ 展开输入框' }}
            </NButton>
            <NButton v-if="isMobile && inputVisible" quaternary size="small" @click="toggleInput" style="margin-left: 8px; color: var(--color-text-muted)">
              收起
            </NButton>
          </div>
          <div style="display: flex; justify-content: flex-end; align-items: center;">
            <NButton
              v-if="!chat.isStreaming"
              type="primary"
              size="small"
              :disabled="!inputValue.trim()"
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
  </div>
</template>

<style scoped>
@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
