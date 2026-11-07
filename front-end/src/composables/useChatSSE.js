import { useRouter } from 'vue-router'
import { useChatStore } from '@/stores/chat'
import { useConversationsStore } from '@/stores/conversations'
import { useAuthStore } from '@/stores/auth'
import { api } from '@/api/client'
import { createIdleWatchdog, createSSEParser } from '@/utils/sse'

// 流式空闲上限：服务端每 15s 发一次 `: ping`，60s 没收到任何字节就按半死连接处理。
// （挂起期间看门狗会被 pause，回前台重连时给 STREAM_RESUME_GRACE_MS 的宽限。）
const STREAM_IDLE_TIMEOUT_MS = 60000
const STREAM_RESUME_GRACE_MS = 15000

// 旧格式兜底映射（新工具由后端 FillDisplay 下发，勿在此新增条目）
const DISPLAY_NAMES = {
  create_bill: '记账', update_bill: '修改账单', cancel_bill: '作废账单',
  delete_bill: '删除账单', query_bills: '查询账单', get_monthly_stats: '月度统计',
  get_category_stats: '分类统计', get_current_time: '获取时间', evaluate: '计算',
  generate: '生成UUID', record_memory: '记录记忆', search: '搜索笔记',
  update_note: '更新笔记', delete_note: '删除笔记', list_notes: '列出笔记',
  create_agent: '创建智能体', update_agent: '更新智能体', delete_agent: '删除智能体',
  list_agents: '列出智能体'
}
function buildToolSummary(tc, result) {
  const display = tc.display || DISPLAY_NAMES[tc.function] || tc.function
  if (result) {
    return `${display} ${result.success ? '✅' : `❌ ${result.error || '失败'}`}`
  }
  return display
}

// 网络层失败的判定：fetch 被掐（iOS 挂起/断网）在 WebKit 是 TypeError + "Load failed"，
// Chrome 是 "Failed to fetch"。这不是「服务出错」，不该把英文原文丢给用户。
function isNetworkError(err) {
  return err?.name === 'TypeError'
}

// 失败 → 给用户看的文案 + 错误类别（'network' 才能被「拉历史成功」清掉）
function classifyError(err) {
  if (isNetworkError(err)) return { message: '网络连接失败，请重试', kind: 'network' }
  if (String(err?.message || '').startsWith('HTTP ')) return { message: '服务暂时不可用，请重试', kind: 'server' }
  return { message: err?.message || '网络连接失败，请重试', kind: 'network' }
}

export function useChatSSE() {
  const chat = useChatStore()
  const convs = useConversationsStore()
  const auth = useAuthStore()
  const router = useRouter()
  let abortController = null
  let activeWatchdog = null
  let suspended = false
  // 本轮曾经被切到后台：rejection 何时执行由引擎决定（iOS 恢复后才跑，那时已经 visible），
  // 不能只靠 catch 里现查 visibilityState。sendMessage 开头清零。
  let hiddenSinceSend = false
  // 续传锚点：本轮最后一帧的 seq / turn_id（meta 帧下发）。每轮 sendMessage 归零。
  let lastSeq = null
  let lastTurnId = ''
  // 本轮流式被我方打断（挂起期失败）：保持 isStreaming，回前台续传接上
  let suspendedDuringStream = false
  // 流结束原因：done | error | idle | reset | closed
  let streamOutcome = null
  // 上一次失败是不是网络层（决定「拉历史成功」时能不能清掉错误）
  let lastErrorKind = null
  // 新会话（懒创建）在 meta 帧就先拿到 id；模式/无痕用发送那一刻的快照
  let streamMode = 'work'
  let streamEphemeral = false
  // failStream 本次收尾的消息 id：done 只认它，避免把真实 id/灵阅卡片
  // 补到上一轮「停止」遗留的 local- 消息上（那条永远不会有真实 id）。
  let failedLocalId = null

  function isCurrent(controller) {
    return abortController === controller
  }

  // 流式失败时别把气泡留在「流式中」：有正文或思考就收尾成普通消息，再露出可重试的错误块。
  // 服务端随后仍会发 done（带本轮灵阅卡片），由 done 分支补到这条本地消息上。
  function failStream(message, kind) {
    const partial = chat.streamingMessage
    failedLocalId = null
    lastErrorKind = kind || 'server'
    if (partial?.content || partial?.reasoning) {
      failedLocalId = `local-${Date.now()}`
      chat.addMessage({ ...partial, id: failedLocalId, toolCalls: [] })
    }
    chat.streamingMessage = null
    chat.statusMessage = null
    chat.error = message
  }

  function finishStream() {
    abortController?.abort()
    abortController = null
    activeWatchdog?.stop()
    activeWatchdog = null
    chat.isStreaming = false
    chat.streamingMessage = null
    chat.statusMessage = null
  }

  function makeWatchdog(controller) {
    return createIdleWatchdog(STREAM_IDLE_TIMEOUT_MS, () => {
      // 挂起期间到达的超时不算（浏览器会把冻结的定时器在恢复时补触发）
      if (suspended) return
      // 已经 abort 过（用户停止/切会话）或已被新一轮取代：当迟到的心跳，别碰新一轮的状态
      if (controller.signal.aborted || !isCurrent(controller)) return
      chat.isStreaming = false
      failStream('连接超时（长时间没有响应），请重试', 'network')
      controller.abort()
    })
  }

  // 新会话（懒创建）adopt：meta 帧比 done 早到，让「首轮挂起」也能续传。
  // 收口在这里，避免 meta / done 各写一份 prepend 逻辑。
  function adoptConversation(id) {
    if (!id || chat.conversationId === id) return
    const isNew = !chat.conversationId
    chat.conversationId = id
    if (!isNew) return
    router.replace(`/chat/${id}`)
    convs.prepend({
      id,
      title: '新对话',
      agent_id: chat.agentId,
      agent_name: chat.agentName,
      mode: streamMode,
      ephemeral: streamEphemeral
    })
  }

  async function sendMessage(message, { conversationId, deepMode, agentId, temperature, maxTokens, thinking, attachments, initMode, ephemeral } = {}) {
    if (chat.isStreaming) return

    // 新会话在 done 回传 id 后要入会话列表，用发送这一刻的模式快照（流式中途再点开关不该改写已落库的值）
    streamMode = initMode || 'work'
    streamEphemeral = !!ephemeral
    lastSeq = null
    lastTurnId = ''
    streamOutcome = null
    lastErrorKind = null
    suspendedDuringStream = false
    hiddenSinceSend = false

    const controller = new AbortController()
    abortController = controller
    // 看门狗覆盖「请求发出 → 响应头 → 流结束」整条链路：半开连接可能停在任一阶段。
    // 超时就先把 UI 收尾（别指望 AbortController 一定能叫醒半死的 read），再断流。
    const watchdog = makeWatchdog(controller)
    activeWatchdog = watchdog
    chat.isStreaming = true
    chat.error = null
    chat.statusMessage = null
    chat.streamingMessage = {
      role: 'assistant',
      content: '',
      reasoning: '',
      reasoningDone: false,
      toolCalls: [],
      verifyStatus: null,  // { status: 'checking'|'done'|'retry', retry: 0, reason: '' }
      created_at: new Date().toISOString()
    }
    chat.planNodes = {}
    chat.planEdges = []

    try {
      watchdog.touch()
      const body = {
        message,
        // 空串而不是 undefined：undefined 会被 JSON.stringify 丢掉，服务端就按「老入口」落默认会话
        conversation_id: conversationId ?? '',
        deep_mode: deepMode || false,
        agent_id: agentId || undefined
      }
      if (attachments && attachments.length) body.attachments = attachments
      // 仅新会话生效：服务端对已有会话忽略这两个字段
      if (initMode) body.init_mode = initMode
      if (ephemeral) body.ephemeral = true
      if (temperature !== undefined && temperature !== null) {
        body.temperature = temperature
      }
      if (maxTokens !== undefined && maxTokens !== null) {
        body.max_tokens = maxTokens
      }
      if (thinking !== undefined && thinking !== null) {
        body.thinking = thinking
      }
      const res = await api.stream('/api/chat', body, controller.signal)

      if (!res.ok) {
        if (res.status === 401) {
          auth.logout()
          return
        }
        throw new Error(`HTTP ${res.status}`)
      }

      await parseSSEStream(res, controller, watchdog)
    } catch (err) {
      // 用户点「停止」、看门狗超时、旧流被续流取代：都不该再碰当前状态
      if (err.name === 'AbortError' || !isCurrent(controller)) return
      // 挂起期间网络断开：不亮错误、不结束流式，回前台按 seq 续传接上。
      // 用「本轮曾进过后台」而不是现查 visibilityState——iOS 的 rejection 要等恢复后才执行。
      if (isNetworkError(err) && (hiddenSinceSend || document.visibilityState === 'hidden')) {
        suspendedDuringStream = true
        streamOutcome = 'background-interrupted'
        return
      }
      if (err.name === 'AuthError') {
        auth.logout()
        return
      }
      console.error('Chat error', err)
      const { message: text, kind } = classifyError(err)
      failStream(text, kind)
    } finally {
      watchdog.stop()
      if (activeWatchdog === watchdog) activeWatchdog = null
      // 超时后可能已经开了新一轮：别把新一轮的 isStreaming 误清掉；
      // 挂起期失败也一样保留，等回前台续传收尾。
      if (isCurrent(controller) && !suspendedDuringStream) chat.isStreaming = false
    }
  }

  // 回前台续传：带 turn + from 重连，补发断点后的事件并继续跟流。
  // 不需要判断「老连接是不是还活着」——seq 天然去重。
  async function resumeStream() {
    if (!chat.isStreaming) return 'none'
    // 半截已经被 failStream 收尾（streamingMessage 已清空）：续上会另起一条气泡、和旧半截重复，
    // 直接按 reset 处理——收尾后以服务端历史为准。
    if (!chat.streamingMessage) return 'reset'
    const conversationId = chat.conversationId
    if (!conversationId) return 'no-conversation'

    const controller = new AbortController()
    const previous = abortController
    abortController = controller
    previous?.abort()
    // 续流已接管：旧连接的迟到 rejection 会被代次守卫丢掉；此后（可视为前台的）断网要正常亮错，
    // 否则 hiddenSinceSend 会一直把真实失败当「挂起期断流」，UI 卡在流式中。
    hiddenSinceSend = false
    activeWatchdog?.stop()
    const watchdog = makeWatchdog(controller)
    activeWatchdog = watchdog
    streamOutcome = null
    suspendedDuringStream = false

    try {
      watchdog.touch()
      const from = lastSeq ?? 0
      const path = `/api/chat/stream?conversation_id=${encodeURIComponent(conversationId)}` +
        `&turn=${encodeURIComponent(lastTurnId)}&from=${from}`
      const res = await api.streamGet(path, controller.signal)
      // 旧后端没有续传能力：优雅降级成「拉历史」，不弹错
      if (res.status === 404 || res.status === 501) return 'unsupported'
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      await parseSSEStream(res, controller, watchdog)
      return streamOutcome || 'closed'
    } catch (err) {
      if (err.name === 'AbortError' || !isCurrent(controller)) return 'aborted'
      if (err.name === 'AuthError') {
        auth.logout()
        return 'aborted'
      }
      console.error('Chat resume error', err)
      // 续传刚发出就切后台：同样别亮错、别结束流式，下次回前台再续
      if (isNetworkError(err) && document.visibilityState === 'hidden') {
        suspendedDuringStream = true
        return 'suspended'
      }
      // 半截内容不能因为续传失败就消失：收尾成普通消息，再露出可重试的错误
      const { message: text, kind } = classifyError(err)
      failStream(text, kind)
      return 'failed'
    } finally {
      watchdog.stop()
      if (activeWatchdog === watchdog) activeWatchdog = null
      if (isCurrent(controller) && !suspendedDuringStream) chat.isStreaming = false
    }
  }

  async function parseSSEStream(res, controller, watchdog) {
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    const parser = createSSEParser((event, rawData, id) => {
      // abort 时已经缓冲在手里的帧不能再派发：否则切会话后旧流的 done
      // 会把 user_message_id / memory_ids 写到新会话的最后一条用户消息上。
      // lastSeq 也必须在守卫之后推进：被丢弃的帧不能成为下一次续传的断点（会漏一个 delta）。
      if (controller.signal.aborted || !isCurrent(controller)) return
      if (id !== null && id !== undefined && id !== '') lastSeq = Number(id)
      try {
        dispatchEvent(event, rawData)
      } catch (e) {
        console.error('SSE dispatch error', event, e)
      }
    })

    watchdog.touch()
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      watchdog.touch()
      parser.feed(decoder.decode(value, { stream: true }))
    }
    // 冲掉解码器里残留的多字节片段，再派发可能未收尾的最后一个事件
    parser.feed(decoder.decode())
    parser.end()
  }

  function dispatchEvent(event, rawData) {
    switch (event) {
      case 'meta': {
        // 本轮身份 + 会话 id：懒创建的会话在 done 之前也能续传
        const m = JSON.parse(rawData)
        if (lastTurnId && m.turn_id && m.turn_id !== lastTurnId) {
          // 手里是另一轮（另一台设备刚发消息把这一轮取消了）：别把别人的内容拼进来
          streamOutcome = 'reset'
          return
        }
        if (m.turn_id) lastTurnId = m.turn_id
        if (m.conversation_id) adoptConversation(m.conversation_id)
        break
      }
      case 'idle':
      case 'reset':
        streamOutcome = event
        break
      case 'message': {
        const parsed = JSON.parse(rawData)
        chat.streamingMessage.content += parsed.delta
        chat.statusMessage = null // 正文开始推送，清除「正在分析」提示
        break
      }
      case 'thought_delta':
        chat.streamingMessage.reasoning += rawData
        break
      case 'thought_done':
        chat.streamingMessage.reasoningDone = true
        break
      case 'tool_call': {
        const tc = JSON.parse(rawData)
        chat.streamingMessage.toolCalls.push({ ...tc, status: 'running', result: null })
        break
      }
      case 'tool_result': {
        const tr = JSON.parse(rawData)
        // 新服务端带 tool_call_id：按 id 配对，别赌事件顺序（客户端工具后发会错位）。
        // 旧服务端没有该字段 → 回退「最后一条 running」。
        const matchByID = tr.tool_call_id
          ? chat.streamingMessage.toolCalls.find(tc => tc.id === tr.tool_call_id && tc.status === 'running')
          : null
        const last = matchByID || chat.streamingMessage.toolCalls.findLast(tc => tc.status === 'running')
        if (last) {
          last.status = tr.success ? 'done' : 'failed'
          last.result = tr
        }
        break
      }
      case 'plan_node_start': {
        const node = JSON.parse(rawData)
        chat.planNodes[node.id] = { label: node.label, status: 'running' }
        break
      }
      case 'plan_node_done': {
        const node = JSON.parse(rawData)
        chat.planNodes[node.id] = { label: node.label, status: node.status }
        break
      }
      case 'plan': {
        const graph = JSON.parse(rawData)
        chat.planNodes = {}
        for (const n of graph.nodes || []) {
          chat.planNodes[n.id] = { label: n.label, status: n.status }
        }
        chat.planEdges = graph.edges || []
        break
      }
      case 'verify': {
        try {
          chat.streamingMessage.verifyStatus = JSON.parse(rawData)
        } catch {
          chat.streamingMessage.verifyStatus = { status: 'checking', retry: 0, reason: '' }
        }
        break
      }
      case 'status': {
        try {
          const s = JSON.parse(rawData)
          chat.statusMessage = s.message || s.status || rawData
        } catch {
          chat.statusMessage = rawData
        }
        break
      }
      case 'agent_matched': {
        const am = JSON.parse(rawData)
        chat.agentId = am.agent_id
        chat.agentName = am.agent_name
        chat.llmParams = am.params || null
        break
      }
      case 'done': {
        const d = JSON.parse(rawData)
        streamOutcome = 'done'
        // 服务端可能「先 error 再 done」（收尾轮未收敛 / 另一台设备停止），
        // failStream 已经把半截消息收尾了；这时只更新元数据，别再推一条空消息。
        if (chat.streamingMessage) {
          const finalMsg = {
            ...chat.streamingMessage,
            id: d.message_id,
            toolCalls: []
          }
          // 本轮产出的灵阅卡片：done 直接带标题摘要，不用等下一次拉消息列表
          if (Array.isArray(d.artifacts) && d.artifacts.length) {
            finalMsg.artifacts = d.artifacts
          }
          chat.addMessage(finalMsg)
        } else {
          // failStream 已把半截消息收尾成普通消息：卡片和真实 id 补到它上面，
          // 否则本轮灵阅卡片要等重新拉消息列表才出来。
          const partial = failedLocalId ? chat.messages.find(m => m.id === failedLocalId) : null
          if (partial) {
            if (d.message_id) partial.id = d.message_id
            if (Array.isArray(d.artifacts) && d.artifacts.length) partial.artifacts = d.artifacts
          }
        }
        failedLocalId = null
        chat.streamingMessage = null

        // 将前端临时 ID 替换为后端真实数据库 ID
        if (d.user_message_id) {
          const userMsg = chat.messages.findLast(m => m.role === 'user')
          if (userMsg) userMsg.id = d.user_message_id
        }

        // 将关联记忆 ID 挂到用户消息上，并批量获取记忆详情
        if (d.memory_ids && d.memory_ids.length > 0) {
          const userMsg = chat.messages.findLast(m => m.role === 'user')
          if (userMsg) {
            userMsg.memoryIds = d.memory_ids
            api.post('/api/memories/batch', { ids: d.memory_ids }).then(memories => {
              userMsg.associatedMemories = Array.isArray(memories) ? memories : []
            }).catch(err => {
              console.error('Failed to fetch associated memories', err)
            })
          }
        }

        if (d.conversation_id) adoptConversation(d.conversation_id)
        if (d.title) {
          convs.updateTitle(chat.conversationId, d.title)
        }
        if (d.error) {
          chat.error = d.error
          lastErrorKind = 'server'
        }
        break
      }
      case 'error':
        streamOutcome = 'error'
        failStream(rawData, 'server')
        break
    }
  }

  // 页面切后台：暂停看门狗（浏览器会把冻结的定时器在恢复时补触发）
  function setSuspended(value) {
    suspended = value
    if (value) {
      hiddenSinceSend = true
      activeWatchdog?.pause()
      return
    }
    if (chat.isStreaming) activeWatchdog?.resume(STREAM_RESUME_GRACE_MS)
  }

  // 拉历史成功时清掉「网络层」错误；服务端 error 一律保留（用户需要知道这一轮失败了）
  function clearNetworkError() {
    if (lastErrorKind !== 'network') return
    chat.error = null
    lastErrorKind = null
  }

  function cancel() {
    if (chat.isStreaming) {
      chat.needsRefetch = true
    }
    finishStream()
  }

  return { sendMessage, cancel, resumeStream, setSuspended, clearNetworkError, finishStream }
}

export { buildToolSummary }
