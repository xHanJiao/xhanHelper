<script setup>
import { ref, computed, watch, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import MarkdownIt from 'markdown-it'
import PageTopBar from '@/components/PageTopBar.vue'
import { api } from '@/api/client'
import { useThemeStore } from '@/stores/theme'

const route = useRoute()
const router = useRouter()
const msgId = route.params.id
const themeStore = useThemeStore()

const md = new MarkdownIt()

const loading = ref(true)
const error = ref('')
const anchorMsg = ref(null)
const roundMsgs = ref([])
const expandedId = ref(null)
const detailLoading = ref(false)
const activeTab = ref('prompt')
const detailData = ref({ request_json: null, extra: null })

const tabs = [
  { key: 'user_msg', label: '用户消息体' },
  { key: 'prompt', label: '系统提示词' },
  { key: 'request_json', label: '请求JSON' },
  { key: 'history_msgs', label: '历史消息' },
  { key: 'resources', label: '资源取舍明细' },
]

const roleLabel = (role) => ({ user: '用户', assistant: '助手', tool: '工具', system: '检验' }[role] || role)
const roleType = (role) => ({ user: 'info', assistant: 'success', tool: 'default', system: 'default' }[role] || 'default')

const systemPrompt = computed(() => {
  const rj = detailData.value.request_json
  if (!rj) return ''
  try {
    const parsed = typeof rj === 'string' ? JSON.parse(rj) : rj
    const systemMsg = parsed.messages?.find(m => m.role === 'system')
    return systemMsg?.content || ''
  } catch { return '' }
})

const promptHtml = computed(() => md.render(systemPrompt.value || '(无)'))

const requestJsonText = computed(() => {
  const rj = detailData.value.request_json
  if (!rj) return '(无)'
  try {
    const parsed = typeof rj === 'string' ? JSON.parse(rj) : rj
    return JSON.stringify(parsed, null, 2)
  } catch { return String(rj) }
})

const bubbleMessages = computed(() => {
  const rj = detailData.value.request_json
  if (!rj) return []
  try {
    const parsed = typeof rj === 'string' ? JSON.parse(rj) : rj
    return parsed.messages || []
  } catch { return [] }
})

const expandedBubbleIdx = ref(null)
const copiedBubbleIdx = ref(null)

const lastUserMsgIdx = computed(() => {
  const msgs = bubbleMessages.value
  for (let i = msgs.length - 1; i >= 0; i--) {
    if (msgs[i].role === 'user') return i
  }
  return -1
})

function isHistorical(idx) {
  if (lastUserMsgIdx.value < 0) return false
  return idx < lastUserMsgIdx.value
}

// 兼容旧格式 { providers: [...] } 和新格式 { prompt_trace: { providers: [...] }, history_builder_msg_ids: [...] }
function parseExtra(raw) {
  if (!raw) return null
  try {
    return typeof raw === 'string' ? JSON.parse(raw) : raw
  } catch { return null }
}

const extraParsed = computed(() => parseExtra(detailData.value.extra))

// 从 request_json.messages 中提取最后一条 user 消息的完整内容
// （已包含检索上下文 XML + <user_query> 标签，是发给 LLM 的原文）
const userMsgBody = computed(() => {
  const rj = detailData.value.request_json
  if (!rj) return ''
  try {
    const parsed = typeof rj === 'string' ? JSON.parse(rj) : rj
    const msgs = parsed.messages
    if (!msgs || !msgs.length) return ''
    for (let i = msgs.length - 1; i >= 0; i--) {
      if (msgs[i].role === 'user') return msgs[i].content || ''
    }
  } catch { return '' }
  return ''
})

const userMsgBodyHtml = computed(() => {
  const text = userMsgBody.value
  if (!text) return '<span style="color:var(--color-text-muted)">(本轮无检索上下文，或请求 JSON 未记录)</span>'
  let html = text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/\n/g, '<br>')
  html = html.replace(/&lt;(\/?)(memories|notes|topics|history|user_query)&gt;/g,
    '<span style="color:var(--color-primary);font-weight:600">&lt;$1$2&gt;</span>')
  return html
})

const providers = computed(() => {
  const ex = extraParsed.value
  if (!ex) return []
  // 新格式: prompt_trace.providers
  if (ex.prompt_trace) return ex.prompt_trace.providers || []
  // 旧格式: providers 直接在顶层
  return ex.providers || []
})

const msgUsage = computed(() => {
  const ex = extraParsed.value
  if (!ex?.usage) return null
  return ex.usage
})

const cacheHitRate = computed(() => {
  const u = msgUsage.value
  if (!u || !u.prompt_tokens || u.prompt_tokens === 0) return null
  if (u.prompt_cache_hit_tokens == null && u.prompt_cache_miss_tokens == null) return null
  const hit = u.prompt_cache_hit_tokens || 0
  const miss = u.prompt_cache_miss_tokens || 0
  if (hit + miss === 0) return null
  return ((hit / (hit + miss)) * 100).toFixed(1)
})

const historyBuilderMsgIds = computed(() => {
  const ex = extraParsed.value
  if (!ex || !ex.history_builder_msg_ids) return []
  return ex.history_builder_msg_ids
})

const expandedContent = ref(new Set())

function toggleContent(msgId, content) {
  if (expandedContent.value.has(msgId)) {
    expandedContent.value.delete(msgId)
    activeMsgResultId.value = null
  } else {
    expandedContent.value.add(msgId)
    // 如果内容含 data.results，默认进入预览模式
    formatContent(content)
    if (markdownResult.value) {
      activeMsgResultId.value = msgId
    }
  }
  expandedContent.value = new Set(expandedContent.value)
}

function isExpanded(msgId) {
  return expandedContent.value.has(msgId)
}

const copiedId = ref(null)

async function copyContent(msgId, content) {
  await navigator.clipboard.writeText(formatContent(content))
  copiedId.value = msgId
  setTimeout(() => { copiedId.value = null }, 1500)
}

const markdownResult = ref('')

function displayContent(msg) {
  if (msg.content) return msg.content
  if (msg.role === 'assistant' && msg.tool_calls_json) {
    try {
      const parsed = typeof msg.tool_calls_json === 'string' ? JSON.parse(msg.tool_calls_json) : msg.tool_calls_json
      return JSON.stringify(parsed, null, 2)
    } catch { return msg.tool_calls_json }
  }
  return '(空)'
}

function rawContent(msg) {
  if (msg.content) return msg.content
  if (msg.role === 'assistant' && msg.tool_calls_json) return msg.tool_calls_json
  return ''
}

function formatContent(content) {
  if (!content) return ''
  try {
    const parsed = JSON.parse(content)
    // 工具返回中的 markdown 文本
    if (parsed?.data?.results && typeof parsed.data.results === 'string') {
      markdownResult.value = parsed.data.results
    // 助手回复中的 markdown 文本
    } else if (parsed?.reply && typeof parsed.reply === 'string') {
      markdownResult.value = parsed.reply
    } else {
      markdownResult.value = ''
    }
    return JSON.stringify(parsed, null, 2)
  } catch {
    markdownResult.value = ''
    return content
  }
}

const bubbleRoleLabel = (role) => ({ user: '用户', assistant: '助手', tool: '工具', system: '系统' }[role] || role)

function bubbleHeaderBg(role) {
  if (role === 'user') return 'var(--color-primary)'
  if (role === 'assistant') return 'var(--color-surface)'
  if (role === 'system') return themeStore.isDark ? 'rgba(168,85,247,0.12)' : '#f3e8ff'
  if (role === 'tool') return themeStore.isDark ? 'rgba(245,158,11,0.1)' : '#fef3c7'
  return 'var(--color-surface)'
}

function bubbleBodyBg(role) {
  if (role === 'user') return 'var(--color-primary)'
  if (role === 'assistant') return 'var(--color-surface)'
  if (role === 'system') return themeStore.isDark ? 'rgba(168,85,247,0.06)' : '#faf5ff'
  if (role === 'tool') return themeStore.isDark ? 'rgba(245,158,11,0.05)' : '#fffbeb'
  return 'var(--color-surface)'
}

function bubbleBorderColor(role) {
  if (role === 'system') return themeStore.isDark ? 'rgba(168,85,247,0.5)' : '#a855f7'
  if (role === 'assistant') return 'var(--color-success)'
  if (role === 'tool') return themeStore.isDark ? 'rgba(245,158,11,0.5)' : '#f59e0b'
  return 'none'
}

function bubbleBadgeBg(role) {
  if (role === 'user') return 'rgba(255,255,255,0.25)'
  if (role === 'system') return themeStore.isDark ? 'rgba(168,85,247,0.5)' : '#a855f7'
  if (role === 'tool') return themeStore.isDark ? 'rgba(245,158,11,0.5)' : '#d97706'
  return 'var(--color-success)'
}

function bubbleHeaderTextColor(role) {
  return role === 'user' ? 'rgba(255,255,255,0.7)' : 'var(--color-text-muted)'
}

function toggleBubble(idx) {
  expandedBubbleIdx.value = expandedBubbleIdx.value === idx ? null : idx
}

function isBubbleExpanded(idx) {
  return expandedBubbleIdx.value === idx
}

function bubbleContent(msg) {
  if (msg.content) {
    if (msg.role === 'tool') {
      try { return JSON.stringify(JSON.parse(msg.content), null, 2) } catch { return msg.content }
    }
    return msg.content
  }
  if (msg.tool_calls && msg.tool_calls.length > 0) {
    return msg.tool_calls.map(c => `调用: ${c.function?.name || '(无)'}`).join(', ')
  }
  return '(空)'
}

function rawBubbleContent(msg) {
  if (msg.content) return msg.content
  if (msg.tool_calls) return JSON.stringify(msg.tool_calls, null, 2)
  return ''
}

async function copyBubbleContent(idx, msg) {
  await navigator.clipboard.writeText(rawBubbleContent(msg))
  copiedBubbleIdx.value = idx
  setTimeout(() => { copiedBubbleIdx.value = null }, 1500)
}

function formatToolCallArgs(tc) {
  if (!tc.function?.arguments) return ''
  try {
    const parsed = typeof tc.function.arguments === 'string' ? JSON.parse(tc.function.arguments) : tc.function.arguments
    return JSON.stringify(parsed, null, 2)
  } catch {
    return String(tc.function.arguments)
  }
}

const activeMsgResultId = ref(null)

function showResult(msgId) {
  activeMsgResultId.value = activeMsgResultId.value === msgId ? null : msgId
}

function estimateTokens(content) {
  if (!content) return 0
  let tokens = 0
  for (const ch of content) {
    const code = ch.charCodeAt(0)
    if (code >= 0x4e00 && code <= 0x9fff) {
      tokens += 1.5  // CJK: ~1-2 tokens per char
    } else if (code < 128) {
      tokens += 0.3  // ASCII: ~4 chars per token
    } else {
      tokens += 1.0  // other unicode
    }
  }
  return Math.round(tokens)
}

const resultHtml = computed(() => {
  if (!markdownResult.value) return ''
  return md.render(markdownResult.value)
})

const lookupCache = ref({})
const lookupLoading = ref(false)

async function batchLookup(providers) {
  // 收集所有 near-miss 项的 (type, id)
  const items = []
  for (const prov of providers) {
    for (const item of (prov.items || [])) {
      if (item.discard_reason && !lookupCache.value[`${prov.name}:${item.id}`]) {
        items.push({ type: prov.name, id: item.id })
      }
    }
  }
  if (items.length === 0) return

  lookupLoading.value = true
  try {
    const res = await api.post('/api/admin/resource-lookup/batch', items)
    for (const r of (res.results || [])) {
      lookupCache.value[`${r.type}:${r.id}`] = r.content || '(空)'
    }
  } finally {
    lookupLoading.value = false
  }
}

function lookedUpContent(provType, id) {
  return lookupCache.value[`${provType}:${id}`]
}

watch(activeTab, (tab) => {
  if (tab === 'resources' && providers.value.length > 0) {
    batchLookup(providers.value)
  }
})

const expandedTraceItem = ref(null)

function toggleTraceItem(itemId) {
  expandedTraceItem.value = expandedTraceItem.value === itemId ? null : itemId
}

function traceItemText(provName, item) {
  const looked = lookedUpContent(provName, item.id)
  if (looked) return looked
  return item.text || '-'
}

const selectedItems = (items) => items.filter(i => !i.discard_reason)
const discardedItems = (items) => items.filter(i => i.discard_reason)

async function loadRound() {
  try {
    const res = await api.get(`/api/admin/messages/${msgId}/debug`)
    anchorMsg.value = res.anchor_msg
    roundMsgs.value = res.round_messages || []
  } catch (e) {
    error.value = '加载失败: ' + e.message
  } finally {
    loading.value = false
  }
}

async function loadDetail(msgId) {
  detailLoading.value = true
  detailData.value = { request_json: null, extra: null }
  activeTab.value = 'prompt'
  try {
    const [rj, extra] = await Promise.all([
      api.get(`/api/admin/messages/${msgId}/debug-detail?field=request_json`),
      api.get(`/api/admin/messages/${msgId}/debug-detail?field=extra`),
    ])
    detailData.value.request_json = rj.value
    detailData.value.extra = extra.value
  } catch (e) {
    detailData.value.request_json = '加载失败: ' + e.message
  } finally {
    detailLoading.value = false
  }
}

function toggleDetail(msg) {
  if (msg.role !== 'assistant' && msg.role !== 'system') return
  if (expandedId.value === msg.id) {
    expandedId.value = null
    return
  }
  expandedId.value = msg.id
  loadDetail(msg.id)
}

function goBack() {
  window.history.back()
}

onMounted(loadRound)
</script>

<template>
  <div style="height: 100vh; display: flex; flex-direction: column; background: var(--color-bg-page);">
    <PageTopBar title="消息调试" :visible="true">
      <template #right>
        <span @click="router.push(`/admin/replay-compare/${msgId}`)" style="cursor: pointer; font-size: 13px; color: var(--color-warning); margin-right: 16px;">对比重放</span>
        <span @click="goBack" style="cursor: pointer; font-size: 14px; color: var(--color-primary);">← 返回</span>
      </template>
    </PageTopBar>

    <div style="flex: 1; overflow-y: auto; padding: var(--space-lg);">
      <div v-if="loading" style="text-align: center; padding: 60px 0; color: var(--color-text-muted);">加载中...</div>
      <div v-else-if="error" style="text-align: center; padding: 60px 0; color: var(--color-error);">{{ error }}</div>

      <template v-else>
        <!-- 锚点消息 -->
        <div v-if="anchorMsg" style="margin-bottom: var(--space-xl);">
          <div style="font-size: 13px; color: var(--color-text-muted); margin-bottom: var(--space-sm);">锚点消息</div>
          <div style="background: var(--color-primary); color: #fff; border-radius: var(--radius-lg); padding: 12px 18px; font-size: 15px; line-height: 1.6;">
            {{ anchorMsg.content }}
          </div>
        </div>

        <!-- 消息轮列表 -->
        <div style="font-size: 13px; color: var(--color-text-muted); margin-bottom: var(--space-sm);">
          消息轮（{{ roundMsgs.length }} 条）
        </div>

        <div v-for="msg in roundMsgs" :key="msg.id"
          :style="{
            padding: '10px 14px',
            marginBottom: '8px',
            borderRadius: 'var(--radius-md)',
            background: 'var(--color-surface)',
            border: '1px solid var(--color-border)',
            cursor: (msg.role === 'assistant' || msg.role === 'system') ? 'pointer' : 'default',
            opacity: msg.role === 'tool' ? 0.7 : 1,
          }"
          @click="toggleDetail(msg)"
        >
          <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 4px;">
            <span :style="{
              fontSize: '11px',
              padding: '1px 6px',
              borderRadius: '4px',
              background: msg.role === 'user' ? 'var(--color-primary)' : msg.role === 'assistant' ? 'var(--color-success)' : msg.role === 'system' ? 'var(--color-warning)' : 'var(--color-border)',
              color: '#fff',
            }">{{ roleLabel(msg.role) }}</span>
            <span style="font-size: 11px; color: var(--color-text-muted);">{{ new Date(msg.created_at).toLocaleString() }}</span>
            <span v-if="(msg.role === 'assistant' || msg.role === 'system') && expandedId === msg.id" style="font-size: 11px; color: var(--color-primary);">▼ 已展开</span>
          </div>
          <div
            v-if="!isExpanded(msg.id)"
            @click.stop="toggleContent(msg.id, rawContent(msg))"
            style="font-size: 13px; color: var(--color-text); line-height: 1.5; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; cursor: pointer;"
          >{{ displayContent(msg) }}</div>
          <div v-else style="position: relative;">
            <pre
              v-if="!markdownResult || activeMsgResultId !== msg.id"
              @click.stop="toggleContent(msg.id, rawContent(msg))"
              style="font-size: 12px; line-height: 1.5; background: var(--color-bg-page); padding: 8px 12px; border-radius: var(--radius-md); margin: 4px 0 0 0; overflow-x: auto; white-space: pre-wrap; word-break: break-all; cursor: pointer; max-height: 400px; overflow-y: auto;"
            >{{ formatContent(rawContent(msg)) }}</pre>
            <span
              @click.stop="copyContent(msg.id, rawContent(msg))"
              style="position: absolute; top: 8px; right: 8px; font-size: 14px; cursor: pointer; opacity: 0.6; line-height: 1;"
              :title="copiedId === msg.id ? '已复制' : '复制'"
            >{{ copiedId === msg.id ? '✓' : '📋' }}</span>
            <span
              v-if="markdownResult"
              @click.stop="showResult(msg.id)"
              style="position: absolute; top: 8px; right: 36px; font-size: 14px; cursor: pointer; opacity: 0.6; line-height: 1; z-index: 1;"
              :title="activeMsgResultId === msg.id ? '切换 JSON 视图' : '切换预览视图'"
            >{{ activeMsgResultId === msg.id ? '🔽' : '👁' }}</span>
            <div
              v-if="activeMsgResultId === msg.id && markdownResult"
              @click.stop="toggleContent(msg.id, rawContent(msg))"
              class="markdown-body"
              v-html="resultHtml"
              style="cursor: pointer; font-size: 13px; line-height: 1.7; background: var(--color-surface); padding: 12px 16px; border-radius: var(--radius-md); margin-top: 4px; border: 1px solid var(--color-border); max-height: 400px; overflow-y: auto;"
            ></div>
          </div>

          <!-- 展开的详情区 -->
          <div v-if="(msg.role === 'assistant' || msg.role === 'system') && expandedId === msg.id" style="margin-top: 12px;" @click.stop>
            <div v-if="detailLoading" style="padding: 20px 0; text-align: center; color: var(--color-text-muted);">加载详情中...</div>
            <template v-else>
              <!-- 用量信息 -->
              <div v-if="msgUsage" style="margin-bottom: 12px; padding: 8px 12px; background: var(--color-bg-page); border-radius: var(--radius-md); font-size: 12px; display: flex; flex-wrap: wrap; gap: 12px; align-items: center;">
                <span><b>Token 用量</b></span>
                <span style="color: var(--color-text-muted);">prompt={{ msgUsage.prompt_tokens?.toLocaleString() }}</span>
                <span style="color: var(--color-text-muted);">completion={{ msgUsage.completion_tokens?.toLocaleString() }}</span>
                <span style="color: var(--color-text-muted);">total={{ msgUsage.total_tokens?.toLocaleString() }}</span>
                <template v-if="msgUsage.prompt_cache_hit_tokens != null || msgUsage.prompt_cache_miss_tokens != null">
                  <span style="margin-left: 4px; padding-left: 12px; border-left: 1px solid var(--color-border);">
                    <span style="color: var(--color-success);">缓存命中={{ (msgUsage.prompt_cache_hit_tokens || 0).toLocaleString() }}</span>
                    <span style="color: var(--color-text-muted); margin-left: 8px;">未命中={{ (msgUsage.prompt_cache_miss_tokens || 0).toLocaleString() }}</span>
                    <span v-if="cacheHitRate" style="font-weight: 600; color: var(--color-primary); margin-left: 8px;">命中率={{ cacheHitRate }}%</span>
                  </span>
                </template>
              </div>

              <!-- Tab 切换 -->
              <div style="display: flex; gap: 4px; margin-bottom: 12px; border-bottom: 1px solid var(--color-border);">
                <span v-for="tab in tabs" :key="tab.key"
                  @click="activeTab = tab.key"
                  :style="{
                    padding: '6px 12px',
                    fontSize: '13px',
                    cursor: 'pointer',
                    color: activeTab === tab.key ? 'var(--color-primary)' : 'var(--color-text-muted)',
                    borderBottom: activeTab === tab.key ? '2px solid var(--color-primary)' : '2px solid transparent',
                  }"
                >{{ tab.label }}</span>
              </div>

              <!-- 用户消息体 -->
              <div v-if="activeTab === 'user_msg'">
                <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 8px;">
                  发给 LLM 的完整 user message（含检索上下文 XML 前缀）
                </div>
                <div
                  v-html="userMsgBodyHtml"
                  style="font-size: 13px; line-height: 1.7; max-height: 600px; overflow-y: auto; background: var(--color-bg-page); padding: 14px 16px; border-radius: var(--radius-md); font-family: 'SF Mono', 'Menlo', 'Monaco', monospace; white-space: pre-wrap; word-break: break-all;">
                </div>
              </div>

              <!-- 系统提示词 -->
              <div v-if="activeTab === 'prompt'"
                class="markdown-body"
                v-html="promptHtml"
                style="font-size: 14px; line-height: 1.7; max-height: 600px; overflow-y: auto;">
              </div>

              <!-- 请求JSON -->
              <div v-if="activeTab === 'request_json'">
                <pre style="font-size: 12px; line-height: 1.4; max-height: 600px; overflow: auto; background: var(--color-bg-page); padding: 12px; border-radius: var(--radius-md); margin: 0;">{{ requestJsonText }}</pre>
              </div>

              <!-- 历史消息 -->
              <div v-if="activeTab === 'history_msgs'">
                <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 12px;">
                  共 {{ bubbleMessages.length }} 条消息
                </div>
                <div v-if="bubbleMessages.length === 0" style="color: var(--color-text-muted); padding: 20px; text-align: center;">
                  无消息数据
                </div>
                <template v-for="(msg, idx) in bubbleMessages" :key="idx">
                  <!-- 历史上下文分隔线 -->
                  <div v-if="idx === lastUserMsgIdx && lastUserMsgIdx > 0"
                    style="text-align: center; margin: 14px 0; font-size: 11px; color: var(--color-text-muted); position: relative;">
                    <span style="position: absolute; top: 50%; left: 0; right: 0; border-top: 1px dashed var(--color-border);"></span>
                    <span style="position: relative; background: var(--color-bg-page); padding: 0 12px;">── 以上为历史上下文 ──</span>
                  </div>
                  <div :style="{
                    marginBottom: '10px',
                    borderRadius: 'var(--radius-md)',
                    overflow: 'hidden',
                    borderLeft: isHistorical(idx) ? `5px solid ${bubbleBorderColor(msg.role)}` : msg.role === 'user' ? 'none' : `3px solid ${bubbleBorderColor(msg.role)}`,
                  }">
                    <!-- 消息头 -->
                    <div :style="{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                      padding: '6px 12px',
                      background: bubbleHeaderBg(msg.role),
                      borderBottom: isBubbleExpanded(idx) ? '1px solid var(--color-border)' : 'none',
                    }">
                      <div style="display: flex; align-items: center; gap: 8px;">
                        <span :style="{
                          fontSize: '11px',
                          padding: '1px 6px',
                          borderRadius: '4px',
                          background: bubbleBadgeBg(msg.role),
                          color: '#fff',
                        }">{{ bubbleRoleLabel(msg.role) }}</span>
                        <span :style="{ fontSize: '11px', color: bubbleHeaderTextColor(msg.role) }">#{{ idx + 1 }}</span>
                        <span v-if="msg.role === 'tool' && msg.tool_call_id" :style="{ fontSize: '11px', color: themeStore.isDark ? 'rgba(245,158,11,0.7)' : '#d97706', fontFamily: 'monospace' }">{{ msg.tool_call_id.slice(0, 20) }}...</span>
                      </div>
                      <span @click.stop="copyBubbleContent(idx, msg)" style="cursor: pointer; font-size: 14px; opacity: 0.7; line-height: 1;" :title="copiedBubbleIdx === idx ? '已复制' : '复制'">{{ copiedBubbleIdx === idx ? '✓' : '📋' }}</span>
                    </div>
                    <!-- 消息体 -->
                    <div @click="toggleBubble(idx)" :style="{
                      padding: '10px 14px',
                      background: bubbleBodyBg(msg.role),
                      cursor: 'pointer',
                    }">
                      <div v-if="!isBubbleExpanded(idx)" :style="{
                        fontSize: '12px',
                        lineHeight: '1.5',
                        color: msg.role === 'user' ? 'rgba(255,255,255,0.9)' : 'var(--color-text)',
                        overflow: 'hidden',
                        display: '-webkit-box',
                        WebkitLineClamp: '3',
                        WebkitBoxOrient: 'vertical',
                        whiteSpace: 'pre-wrap',
                        wordBreak: 'break-all',
                      }">{{ bubbleContent(msg) }}</div>
                      <pre v-else :style="{
                        fontSize: '12px',
                        lineHeight: '1.5',
                        color: msg.role === 'user' ? 'rgba(255,255,255,0.95)' : 'var(--color-text)',
                        whiteSpace: 'pre-wrap',
                        wordBreak: 'break-all',
                        margin: 0,
                        maxHeight: '400px',
                        overflowY: 'auto',
                      }">{{ bubbleContent(msg) }}</pre>
                    </div>
                    <!-- 助手消息的 tool_calls -->
                    <div v-if="msg.role === 'assistant' && msg.tool_calls && msg.tool_calls.length > 0" style="padding: 8px 14px; background: var(--color-bg-page); border-top: 1px solid var(--color-border);">
                      <div style="font-size: 11px; color: var(--color-text-muted); margin-bottom: 6px; font-weight: 600;">工具调用 ({{ msg.tool_calls.length }})</div>
                      <div v-for="(tc, tci) in msg.tool_calls" :key="tci" :style="{
                        padding: '6px 10px',
                        marginBottom: tci < msg.tool_calls.length - 1 ? '6px' : '0',
                        background: 'var(--color-surface)',
                        borderRadius: 'var(--radius-sm)',
                        border: '1px solid var(--color-border)',
                      }">
                        <div style="font-size: 11px; font-weight: 600; color: var(--color-primary); margin-bottom: 4px;">{{ tc.function?.name || '(无函数名)' }}</div>
                        <pre style="font-size: 11px; line-height: 1.4; margin: 0; white-space: pre-wrap; word-break: break-all; color: var(--color-text-muted);">{{ formatToolCallArgs(tc) }}</pre>
                      </div>
                    </div>
                  </div>
                </template>
              </div>

              <!-- 资源取舍明细 -->
              <div v-if="activeTab === 'resources'">
                <!-- HistoryBuilder 选用的历史消息 -->
                <div v-if="historyBuilderMsgIds.length > 0" style="margin-bottom: 16px;">
                  <div style="font-weight: 600; font-size: 14px; margin-bottom: 8px;">
                    HistoryBuilder <span style="font-size: 11px; color: var(--color-text-muted); font-weight: 400;">(上下文历史消息)</span>
                  </div>
                  <table style="width: 100%; font-size: 12px; border-collapse: collapse;">
                    <thead>
                      <tr style="border-bottom: 1px solid var(--color-border); text-align: left;">
                        <th style="padding: 4px 8px;">#</th>
                        <th style="padding: 4px 8px;">消息 ID</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="(id, idx) in historyBuilderMsgIds" :key="id" style="border-bottom: 1px solid var(--color-border);">
                        <td style="padding: 4px 8px; font-size: 11px; color: var(--color-text-muted);">{{ idx + 1 }}</td>
                        <td style="padding: 4px 8px; font-family: monospace; font-size: 11px;">
                          <router-link
                            v-if="roundMsgs.find(m => m.id === id)"
                            :to="`/admin/message-debug/${id}`"
                            style="color: var(--color-primary);"
                          >{{ id }}</router-link>
                          <span v-else style="color: var(--color-text-muted);">{{ id }}</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>

                <div v-if="providers.length === 0 && historyBuilderMsgIds.length === 0" style="color: var(--color-text-muted); padding: 20px;">无数据</div>
                <div v-for="(prov, pi) in providers" :key="pi" style="margin-bottom: 16px;">
                  <div style="font-weight: 600; font-size: 14px; margin-bottom: 8px;">
                    {{ prov.name }}
                    <span style="font-size: 11px; color: var(--color-text-muted); font-weight: 400;">({{ prov.type }})</span>
                  </div>
                  <table style="width: 100%; font-size: 12px; border-collapse: collapse;">
                    <thead>
                      <tr style="border-bottom: 1px solid var(--color-border); text-align: left;">
                        <th style="padding: 4px 8px;">ID</th>
                        <th style="padding: 4px 8px;">文本</th>
                        <th style="padding: 4px 8px;">向量分</th>
                        <th style="padding: 4px 8px;">状态</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="item in selectedItems(prov.items || [])" :key="item.id"
                        style="border-bottom: 1px solid var(--color-border);">
                        <td style="padding: 4px 8px; font-family: monospace; font-size: 11px;">{{ item.id?.slice(0, 8) }}</td>
                        <td style="padding: 4px 8px; max-width: 300px; font-size: 11px;">
                          <div
                            v-if="expandedTraceItem !== item.id"
                            @click="toggleTraceItem(item.id)"
                            style="overflow: hidden; text-overflow: ellipsis; white-space: nowrap; cursor: pointer;"
                          >{{ traceItemText(prov.name, item) }}</div>
                          <div v-else @click="toggleTraceItem(item.id)" style="cursor: pointer;">
                            <div v-if="prov.name === 'theme'" class="markdown-body" v-html="md.render(traceItemText(prov.name, item))" style="font-size: 12px; line-height: 1.5; max-height: 200px; overflow-y: auto;"></div>
                            <pre v-else style="font-size: 11px; line-height: 1.4; white-space: pre-wrap; margin: 0;">{{ traceItemText(prov.name, item) }}</pre>
                          </div>
                        </td>
                        <td style="padding: 4px 8px;">{{ item.score?.toFixed(3) || '-' }}</td>
                        <td style="padding: 4px 8px;"><span style="color: var(--color-success);">✓ 已选中</span></td>
                      </tr>
                      <tr v-for="item in discardedItems(prov.items || [])" :key="item.id"
                        style="border-bottom: 1px solid var(--color-border);">
                        <td style="padding: 4px 8px; font-family: monospace; font-size: 11px;">{{ item.id?.slice(0, 8) }}</td>
                        <td style="padding: 4px 8px; max-width: 300px; font-size: 11px;">
                          <div
                            v-if="expandedTraceItem !== item.id"
                            @click="toggleTraceItem(item.id)"
                            style="overflow: hidden; text-overflow: ellipsis; white-space: nowrap; cursor: pointer;"
                          >{{ traceItemText(prov.name, item) }}<span v-if="lookupLoading" style="color: var(--color-text-muted);"> ...</span></div>
                          <div v-else @click="toggleTraceItem(item.id)" style="cursor: pointer;">
                            <div v-if="prov.name === 'theme'" class="markdown-body" v-html="md.render(traceItemText(prov.name, item))" style="font-size: 12px; line-height: 1.5; max-height: 200px; overflow-y: auto;"></div>
                            <pre v-else style="font-size: 11px; line-height: 1.4; white-space: pre-wrap; margin: 0;">{{ traceItemText(prov.name, item) }}</pre>
                          </div>
                        </td>
                        <td style="padding: 4px 8px;">{{ item.score?.toFixed(3) || '-' }}</td>
                        <td style="padding: 4px 8px; color: var(--color-warning);">✗ {{ item.discard_reason }}</td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </div>
            </template>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>
