<script setup>
import { ref, computed, watch, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import MarkdownIt from 'markdown-it'
import PageTopBar from '@/components/PageTopBar.vue'
import { api } from '@/api/client'

const route = useRoute()
const msgId = route.params.id

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
  { key: 'prompt', label: '系统提示词' },
  { key: 'request_json', label: '请求JSON' },
  { key: 'resources', label: '资源取舍明细' },
]

const roleLabel = (role) => ({ user: '用户', assistant: '助手', tool: '工具' }[role] || role)
const roleType = (role) => ({ user: 'info', assistant: 'success', tool: 'default' }[role] || 'default')

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

const providers = computed(() => {
  const extra = detailData.value.extra
  if (!extra) return []
  try {
    const parsed = typeof extra === 'string' ? JSON.parse(extra) : extra
    return parsed.providers || []
  } catch { return [] }
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

function formatContent(content) {
  if (!content) return ''
  try {
    const parsed = JSON.parse(content)
    if (parsed?.data?.results && typeof parsed.data.results === 'string') {
      markdownResult.value = parsed.data.results
    } else {
      markdownResult.value = ''
    }
    return JSON.stringify(parsed, null, 2)
  } catch {
    markdownResult.value = ''
    return content
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
  if (msg.role !== 'assistant') return
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
            cursor: msg.role === 'assistant' ? 'pointer' : 'default',
            opacity: msg.role === 'tool' ? 0.7 : 1,
          }"
          @click="toggleDetail(msg)"
        >
          <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 4px;">
            <span :style="{
              fontSize: '11px',
              padding: '1px 6px',
              borderRadius: '4px',
              background: msg.role === 'user' ? 'var(--color-primary)' : msg.role === 'assistant' ? 'var(--color-success)' : 'var(--color-border)',
              color: msg.role === 'tool' ? 'var(--color-text-muted)' : '#fff',
            }">{{ roleLabel(msg.role) }}</span>
            <span style="font-size: 11px; color: var(--color-text-muted);">{{ new Date(msg.created_at).toLocaleString() }}</span>
            <span style="font-size: 11px; color: var(--color-text-muted);">~{{ estimateTokens(msg.content) }} tokens</span>
            <span v-if="msg.role === 'assistant' && expandedId === msg.id" style="font-size: 11px; color: var(--color-primary);">▼ 已展开</span>
          </div>
          <div
            v-if="!isExpanded(msg.id)"
            @click.stop="toggleContent(msg.id, msg.content)"
            style="font-size: 13px; color: var(--color-text); line-height: 1.5; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; cursor: pointer;"
          >{{ msg.content || '(空)' }}</div>
          <div v-else style="position: relative;">
            <pre
              v-if="!markdownResult || activeMsgResultId !== msg.id"
              @click.stop="toggleContent(msg.id, msg.content)"
              style="font-size: 12px; line-height: 1.5; background: var(--color-bg-page); padding: 8px 12px; border-radius: var(--radius-md); margin: 4px 0 0 0; overflow-x: auto; white-space: pre-wrap; word-break: break-all; cursor: pointer; max-height: 400px; overflow-y: auto;"
            >{{ formatContent(msg.content) }}</pre>
            <span
              @click.stop="copyContent(msg.id, msg.content)"
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
              @click.stop="toggleContent(msg.id, msg.content)"
              class="markdown-body"
              v-html="resultHtml"
              style="cursor: pointer; font-size: 13px; line-height: 1.7; background: var(--color-surface); padding: 12px 16px; border-radius: var(--radius-md); margin-top: 4px; border: 1px solid var(--color-border); max-height: 400px; overflow-y: auto;"
            ></div>
          </div>

          <!-- 展开的详情区 -->
          <div v-if="msg.role === 'assistant' && expandedId === msg.id" style="margin-top: 12px;" @click.stop>
            <div v-if="detailLoading" style="padding: 20px 0; text-align: center; color: var(--color-text-muted);">加载详情中...</div>
            <template v-else>
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

              <!-- 资源取舍明细 -->
              <div v-if="activeTab === 'resources'">
                <div v-if="providers.length === 0" style="color: var(--color-text-muted); padding: 20px;">无数据</div>
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
                        <th style="padding: 4px 8px;">综合分</th>
                        <th style="padding: 4px 8px;">向量分</th>
                        <th style="padding: 4px 8px;">关键词</th>
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
                        <td style="padding: 4px 8px;">{{ item.vector_score?.toFixed(3) || '-' }}</td>
                        <td style="padding: 4px 8px;">{{ item.keyword_score?.toFixed(3) || '-' }}</td>
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
                        <td style="padding: 4px 8px;">-</td>
                        <td style="padding: 4px 8px;">{{ item.vector_score?.toFixed(3) || '-' }}</td>
                        <td style="padding: 4px 8px;">-</td>
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
