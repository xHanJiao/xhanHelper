<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import PageTopBar from '@/components/PageTopBar.vue'
import { api } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import MarkdownIt from 'markdown-it'

const md = new MarkdownIt()

const route = useRoute()
const auth = useAuthStore()
const msgId = route.params.id

const loading = ref(true)
const error = ref('')
const anchorMsg = ref(null)
const historyMsgs = ref([])

const running = ref(false)
const sideA = ref(null)
const sideB = ref(null)

const systemPromptSource = ref('live') // 'historical' | 'live'
const historicalPrompt = ref('')

const bPromptPreview = ref('')
const bPromptLoading = ref(false)
const bShowPrompt = ref(false)
const bTrace = ref(null)

const defaultConfigB = {
  skip_retrieval: false,
  max_tool_result_chars: 2000,
  max_iterations: 8,
  memory: { threshold: 0.5, limit: 8 },
  note: { threshold: 0.5, limit: 5 },
  history: { threshold: 0.8, limit: 5 },
  theme: { threshold: 0.5, limit: 10 },
  deep_mode: false,
  override_system_prompt: null,
}

const configB = ref({ ...defaultConfigB })

const roleLabel = (role) => ({ user: '用户', assistant: '助手', tool: '工具' }[role] || role)

function goBack() { window.history.back() }

async function loadAnchor() {
  try {
    const res = await api.post('/api/admin/replay/preview', {
      anchor_message_id: msgId,
      history_count: 10,
      max_tool_result_chars: 2000,
    })
    anchorMsg.value = res.anchor_message
    historyMsgs.value = res.messages || []
    historicalPrompt.value = res.historical_prompt || ''
  } catch (e) {
    error.value = '加载失败: ' + e.message
  } finally {
    loading.value = false
  }
}

async function previewBPrompt() {
  bPromptLoading.value = true
  bPromptPreview.value = ''
  bTrace.value = null
  bShowPrompt.value = true
  try {
    const res = await api.post('/api/admin/replay/preview', {
      anchor_message_id: msgId,
      history_count: 10,
      skip_retrieval: configB.value.skip_retrieval,
      max_tool_result_chars: configB.value.max_tool_result_chars,
      memory: { threshold: configB.value.memory.threshold, limit: configB.value.memory.limit },
      note: { threshold: configB.value.note.threshold, limit: configB.value.note.limit },
      history: { threshold: configB.value.history.threshold, limit: configB.value.history.limit },
      theme: { threshold: configB.value.theme.threshold, limit: configB.value.theme.limit },
    })
    bPromptPreview.value = res.system_prompt || ''
    bTrace.value = res.trace || null
    if (!res.system_prompt) error.value = '该配置下系统提示词为空（可能跳过了检索）'
  } catch (e) {
    error.value = '预览失败: ' + e.message
  } finally {
    bPromptLoading.value = false
  }
}

function editBPrompt() {
  configB.value.override_system_prompt = bPromptPreview.value
}

function clearBPrompt() {
  configB.value.override_system_prompt = ''
}

function resetBPrompt() {
  configB.value.override_system_prompt = null
  bShowPrompt.value = false
  bPromptPreview.value = ''
}

async function runCompare() {
  running.value = true
  sideA.value = null
  sideB.value = null
  error.value = ''

  const body = {
    anchor_message_id: msgId,
    history_count: 10,
    config_b: { ...configB.value },
    system_prompt_source: configB.value.override_system_prompt != null ? 'live' : systemPromptSource.value,
  }

  // 如果用户手动覆盖了 system prompt，始终传给后端
  // 后端在 override 非空时直接使用，不管 source

  try {
    const res = await api.post('/api/admin/replay/compare', body)
    sideA.value = res.side_a
    sideB.value = res.side_b
    // 更新 anchor 和历史（以防有变化）
    if (res.anchor_message) anchorMsg.value = res.anchor_message
    if (res.history_messages) historyMsgs.value = res.history_messages
  } catch (e) {
    if (e.name === 'AuthError') { auth.logout(); return }
    error.value = '对比失败: ' + e.message
  } finally {
    running.value = false
  }
}

// 将 A 侧原始消息处理为 B 侧 Result 风格的结构，方便复用渲染逻辑
const sideADisplay = computed(() => {
  if (!sideA.value?.messages) return null
  const toolCalls = []
  const reasoning = []
  let reply = ''

  for (const m of sideA.value.messages) {
    if (m.role === 'assistant') {
      if (m.reasoning_content) reasoning.push(m.reasoning_content)
      if (m.content) reply += (reply ? '\n' : '') + m.content
      if (m.tool_calls) {
        let tcs = m.tool_calls
        if (typeof tcs === 'string') {
          try { tcs = JSON.parse(tcs) } catch { continue }
        }
        for (const tc of tcs) {
          const tool = tc.tool || (tc.function?.name || '').split('.')[0] || ''
          const func = tc.function?.name || tc.function || ''
          let args = tc.args || tc.function?.arguments || {}
          if (typeof args === 'string') {
            try { args = JSON.parse(args) } catch { /* raw string */ }
          }
          toolCalls.push({ tool, function: func, args, result: null })
        }
      }
    } else if (m.role === 'tool') {
      const pending = toolCalls.find(tc => !tc.result && tc.tool_call_id === m.tool_call_id) || toolCalls.find(tc => !tc.result)
      if (pending) {
        try { pending.result = typeof m.content === 'string' ? JSON.parse(m.content) : m.content }
        catch { pending.result = m.content }
      }
    }
  }

  return { tool_calls: toolCalls, reasoning, reply }
})

function displayContent(msg) {
  if (msg.content) return msg.content.slice(0, 100)
  if (msg.tool_calls_json) {
    try {
      const parsed = typeof msg.tool_calls_json === 'string' ? JSON.parse(msg.tool_calls_json) : msg.tool_calls_json
      const tcNames = Array.isArray(parsed) ? parsed.map(tc => tc.function?.name || tc.tool + '.' + tc.function).join(', ') : '[工具调用]'
      return tcNames.slice(0, 100)
    } catch { return String(msg.tool_calls_json).slice(0, 100) }
  }
  return ''
}

function hasDiff(tcA, tcB) {
  if (!tcA || !tcB) return true
  return tcA.tool !== tcB.tool || tcA.function !== tcB.function
}

function formatArgs(args) {
  if (!args) return ''
  try {
    const obj = typeof args === 'string' ? JSON.parse(args) : args
    return JSON.stringify(obj, null, 2)
  } catch {
    return String(args)
  }
}

function formatResult(r) {
  if (!r) return ''
  try {
    const obj = typeof r === 'string' ? JSON.parse(r) : r
    if (obj.success === false) return '❌ ' + (obj.error || '失败')
    return JSON.stringify(obj, null, 2)
  } catch {
    return String(r)
  }
}

function selectedItems(items) { return (items || []).filter(i => !i.discard_reason) }
function discardedItems(items) { return (items || []).filter(i => i.discard_reason) }

onMounted(loadAnchor)
</script>

<template>
  <div style="height: 100%; display: flex; flex-direction: column; background: transparent;">
    <PageTopBar title="对话重放对比" :visible="true">
      <template #right>
        <span @click="goBack" style="cursor: pointer; font-size: 14px; color: var(--color-primary);">← 返回</span>
      </template>
    </PageTopBar>

    <div style="flex: 1; overflow-y: auto; padding: var(--space-lg);">
      <div v-if="loading" style="text-align: center; padding: 60px 0; color: var(--color-text-muted);">加载中...</div>
      <div v-else-if="error && !anchorMsg" style="text-align: center; padding: 60px 0; color: var(--color-error);">{{ error }}</div>

      <template v-else>
        <!-- 锚点消息 -->
        <div style="margin-bottom: var(--space-lg);">
          <div style="font-size: 13px; color: var(--color-text-muted); margin-bottom: var(--space-sm);">锚点消息</div>
          <div style="background: var(--color-primary); color: #fff; border-radius: var(--radius-lg); padding: 12px 18px; font-size: 15px; line-height: 1.6;">
            {{ anchorMsg?.content }}
          </div>
        </div>

        <!-- 历史消息 -->
        <details v-if="historyMsgs.length > 0" style="margin-bottom: var(--space-lg);">
          <summary style="cursor: pointer; font-size: 13px; color: var(--color-text-muted);">
            前置历史（{{ historyMsgs.length }} 条）
          </summary>
          <div v-for="(msg, i) in historyMsgs" :key="i"
            style="padding: 4px 12px; font-size: 12px; color: var(--color-text-muted); font-family: monospace; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">
            [{{ roleLabel(msg.role) }}] {{ msg.content?.slice(0, 120) || displayContent(msg) }}
          </div>
        </details>

        <!-- 配置面板（仅 B 侧） -->
        <div style="background: var(--color-surface); border: 1px solid var(--color-border); border-radius: var(--radius-lg); padding: var(--space-md); margin-bottom: var(--space-xl);">
          <div style="font-weight: 600; font-size: 14px; margin-bottom: var(--space-sm); color: var(--color-warning);">配置 B（实验组）</div>

          <!-- System Prompt 来源 -->
          <div v-if="historicalPrompt" style="margin-bottom: var(--space-sm); padding: 6px 8px; background: var(--color-bg-page); border-radius: var(--radius-md); font-size: 12px;">
            <span style="color: var(--color-text-muted); margin-right: 8px;">System Prompt 起点：</span>
            <label style="margin-right: 16px; cursor: pointer;">
              <input type="radio" v-model="systemPromptSource" value="live" style="accent-color: var(--color-primary);" />
              实时构建
            </label>
            <label style="cursor: pointer;">
              <input type="radio" v-model="systemPromptSource" value="historical" style="accent-color: var(--color-primary);" />
              历史原始
            </label>
          </div>

          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 6px 24px; font-size: 12px;">
            <label class="cfg-row"><span class="cfg-label">检索</span><select v-model="configB.skip_retrieval" class="replay-select"><option :value="false">开启</option><option :value="true">关闭</option></select></label>
            <label class="cfg-row"><span class="cfg-label">模型</span><select v-model="configB.deep_mode" class="replay-select"><option :value="false">Flash</option><option :value="true">Pro</option></select></label>
            <label class="cfg-row"><span class="cfg-label">截断长度</span><input type="number" v-model.number="configB.max_tool_result_chars" class="replay-input" /></label>
            <label class="cfg-row"><span class="cfg-label">最大轮次</span><input type="number" v-model.number="configB.max_iterations" class="replay-input" /></label>
          </div>
          <div style="display: grid; grid-template-columns: 1fr 1fr 1fr 1fr; gap: 6px 12px; font-size: 12px; margin-top: 8px; padding-top: 8px; border-top: 1px solid var(--color-border);">
            <div class="cfg-section">记忆检索</div>
            <div class="cfg-section">笔记检索</div>
            <div class="cfg-section">历史检索</div>
            <div class="cfg-section">主题检索</div>
            <label class="cfg-row"><span class="cfg-label">阈值</span><input type="number" v-model.number="configB.memory.threshold" step="0.05" class="replay-input" /></label>
            <label class="cfg-row"><span class="cfg-label">阈值</span><input type="number" v-model.number="configB.note.threshold" step="0.05" class="replay-input" /></label>
            <label class="cfg-row"><span class="cfg-label">阈值</span><input type="number" v-model.number="configB.history.threshold" step="0.05" class="replay-input" /></label>
            <label class="cfg-row"><span class="cfg-label">阈值</span><input type="number" v-model.number="configB.theme.threshold" step="0.05" class="replay-input" /></label>
            <label class="cfg-row"><span class="cfg-label">条数</span><input type="number" v-model.number="configB.memory.limit" class="replay-input" /></label>
            <label class="cfg-row"><span class="cfg-label">条数</span><input type="number" v-model.number="configB.note.limit" class="replay-input" /></label>
            <label class="cfg-row"><span class="cfg-label">条数</span><input type="number" v-model.number="configB.history.limit" class="replay-input" /></label>
            <label class="cfg-row"><span class="cfg-label">条数</span><input type="number" v-model.number="configB.theme.limit" class="replay-input" /></label>
          </div>
        </div>

        <!-- 运行按钮行 -->
        <div style="text-align: center; margin-bottom: var(--space-lg);">
          <button @click="runCompare" :disabled="running" class="run-btn" :style="{ opacity: running ? 0.6 : 1, cursor: running ? 'not-allowed' : 'pointer' }">
            {{ running ? '运行中...' : '运行对比' }}
          </button>
          <button
            v-if="!bShowPrompt"
            @click="previewBPrompt"
            :disabled="bPromptLoading"
            class="preview-btn"
            :style="{ opacity: bPromptLoading ? 0.6 : 1 }"
          >
            {{ bPromptLoading ? '加载中...' : '预览 B 系统提示词' }}
          </button>
        </div>

        <!-- B 系统提示词预览/编辑区 -->
        <div v-if="bShowPrompt" style="margin-bottom: var(--space-xl);">
          <div style="display: flex; align-items: center; gap: 8px; margin-bottom: var(--space-sm);">
            <span style="font-size: 13px; color: var(--color-warning); font-weight: 600;">配置 B 系统提示词</span>
            <span style="font-size: 11px; color: var(--color-text-muted);">（{{ bPromptPreview.length }} 字符，基于 B 的检索参数构建）</span>
            <button
              v-if="configB.override_system_prompt == null"
              @click="editBPrompt"
              :disabled="!bPromptPreview"
              style="font-size: 12px; padding: 2px 10px; border-radius: 4px; border: 1px solid var(--color-primary); background: var(--color-surface); color: var(--color-primary); cursor: pointer;"
            >编辑</button>
            <template v-else>
              <button @click="clearBPrompt" style="font-size: 12px; padding: 2px 10px; border-radius: 4px; border: 1px solid var(--color-danger); background: var(--color-surface); color: var(--color-danger); cursor: pointer;">清空</button>
              <button @click="resetBPrompt" style="font-size: 12px; padding: 2px 10px; border-radius: 4px; border: 1px solid var(--color-border); background: var(--color-surface); color: var(--color-text-muted); cursor: pointer;">恢复默认</button>
            </template>
          </div>

          <div v-if="configB.override_system_prompt == null" class="md-preview" v-html="bPromptPreview ? md.render(bPromptPreview) : '<em>(空 — 可能跳过了检索)</em>'"></div>

          <div v-else>
            <textarea v-model="configB.override_system_prompt" class="prompt-editor"></textarea>
            <div style="font-size: 11px; color: var(--color-warning); margin-top: 4px;">B 将使用此自定义提示词运行，跳过自动检索。点击「恢复默认」后自动构建。</div>
          </div>

          <!-- 检索 Trace（预览时） -->
          <div v-if="bTrace?.providers?.length" style="margin-top: var(--space-md);">
            <details style="font-size: 13px;">
              <summary style="cursor: pointer; color: var(--color-text-muted); font-weight: 600;">检索 Trace（{{ bTrace.providers.length }} 个数据源）</summary>
              <div v-for="(prov, pi) in bTrace.providers" :key="pi" style="margin-top: 8px; padding: 8px 12px; background: var(--color-surface); border: 1px solid var(--color-border); border-radius: var(--radius-md);">
                <div style="font-weight: 600; font-size: 12px; margin-bottom: 4px;">{{ prov.name }} <span style="color: var(--color-text-muted); font-weight: 400;">({{ prov.type }})</span></div>
                <div v-if="prov.error" style="color: var(--color-error); font-size: 11px;">错误: {{ prov.error }}</div>
                <table v-if="prov.items?.length" style="width: 100%; font-size: 11px; border-collapse: collapse;">
                  <thead>
                    <tr style="border-bottom: 1px solid var(--color-border); text-align: left;">
                      <th style="padding: 2px 6px;">ID</th>
                      <th style="padding: 2px 6px;">文本</th>
                      <th style="padding: 2px 6px;">向量分</th>
                      <th style="padding: 2px 6px;">状态</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="item in selectedItems(prov.items)" :key="item.id" style="border-bottom: 1px solid var(--color-border);">
                      <td style="padding: 2px 6px; font-family: monospace;">{{ item.id?.slice(0, 8) }}</td>
                      <td style="padding: 2px 6px; max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ item.text || '-' }}</td>
                      <td style="padding: 2px 6px;">{{ item.score?.toFixed(3) || '-' }}</td>
                      <td style="padding: 2px 6px; color: var(--color-success);">✓</td>
                    </tr>
                    <tr v-for="item in discardedItems(prov.items)" :key="item.id" style="border-bottom: 1px solid var(--color-border);">
                      <td style="padding: 2px 6px; font-family: monospace;">{{ item.id?.slice(0, 8) }}</td>
                      <td style="padding: 2px 6px; max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ item.text || '-' }}</td>
                      <td style="padding: 2px 6px;">{{ item.score?.toFixed(3) || '-' }}</td>
                      <td style="padding: 2px 6px; color: var(--color-warning);">{{ item.discard_reason }}</td>
                    </tr>
                  </tbody>
                </table>
                <div v-else style="font-size: 11px; color: var(--color-text-muted);">无检索结果</div>
              </div>
            </details>
          </div>
        </div>

        <div v-if="error" style="text-align: center; margin-bottom: var(--space-md); color: var(--color-error); font-size: 13px;">{{ error }}</div>

        <!-- 对比结果 -->
        <div v-if="sideA || sideB" style="margin-bottom: var(--space-xl);">
          <div style="font-weight: 600; font-size: 15px; margin-bottom: var(--space-md); padding-bottom: 8px; border-bottom: 2px solid var(--color-border);">对比结果</div>

          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-md);">
            <!-- Side A — 原始响应（DB） -->
            <div>
              <div style="font-weight: 600; font-size: 14px; margin-bottom: var(--space-sm); color: var(--color-primary);">
                原始响应（数据库）
              </div>
              <div v-if="sideA?.model_name" style="font-size: 11px; color: var(--color-text-muted); margin-bottom: var(--space-sm);">
                模型: {{ sideA.model_name }}<span v-if="sideA.temperature != null"> · T={{ sideA.temperature }}</span>
              </div>

              <!-- A 侧 system prompt（只读折叠） -->
              <details v-if="sideA?.system_prompt" style="margin-bottom: var(--space-sm);">
                <summary style="cursor: pointer; font-size: 12px; color: var(--color-text-muted);">原始 System Prompt（{{ sideA.system_prompt.length }} 字符）</summary>
                <div class="md-preview" v-html="md.render(sideA.system_prompt)" style="max-height: 300px; margin-top: 8px;"></div>
              </details>

              <!-- A 侧结果 -->
              <template v-if="sideADisplay">
                <div v-if="sideADisplay.tool_calls?.length" style="margin-bottom: var(--space-md);">
                  <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">工具调用</div>
                  <div v-for="(tc, idx) in sideADisplay.tool_calls" :key="idx"
                    :style="{
                      background: 'var(--color-surface)',
                      border: `1px solid ${sideB?.result?.tool_calls?.[idx] && hasDiff(tc, sideB.result.tool_calls[idx]) ? 'var(--color-warning)' : 'var(--color-border)'}`,
                      borderRadius: 'var(--radius-md)', padding: '10px 12px', marginBottom: '8px',
                    }"
                  >
                    <div style="font-weight: 600; font-size: 13px; margin-bottom: 4px;">{{ tc.tool }}.{{ tc.function }}</div>
                    <div style="font-size: 11px; color: var(--color-text-muted); margin-bottom: 4px;">参数:</div>
                    <pre class="tc-pre">{{ formatArgs(tc.args) }}</pre>
                    <div style="font-size: 11px; color: var(--color-text-muted); margin-bottom: 4px;">结果:</div>
                    <pre class="tc-pre" style="max-height: 200px; overflow-y: auto;">{{ formatResult(tc.result) }}</pre>
                  </div>
                </div>
                <div v-else style="font-size: 12px; color: var(--color-text-muted); margin-bottom: var(--space-md);">无工具调用</div>
                <details v-if="sideADisplay.reasoning?.length" style="margin-bottom: var(--space-md);">
                  <summary style="cursor: pointer; font-size: 12px; color: var(--color-text-muted);">思考过程（{{ sideADisplay.reasoning.length }} 轮）</summary>
                  <div v-for="(r, idx) in sideADisplay.reasoning" :key="idx" class="reasoning-box" style="margin-bottom: 8px;">
                    <div v-if="sideADisplay.reasoning.length > 1" style="font-size: 11px; color: var(--color-text-muted); margin-bottom: 4px;">第 {{ idx + 1 }} 轮</div>
                    {{ r }}
                  </div>
                </details>
                <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">回复</div>
                <div class="reply-box">{{ sideADisplay.reply || '(无文本回复)' }}</div>
              </template>
              <div v-else style="font-size: 12px; color: var(--color-text-muted); padding: 16px; text-align: center;">无响应数据</div>
            </div>

            <!-- Side B — 重放结果 -->
            <div>
              <div style="font-weight: 600; font-size: 14px; margin-bottom: var(--space-sm); color: var(--color-warning);">重放结果（配置 B）</div>

              <!-- B 侧 system prompt（折叠） -->
              <details v-if="sideB?.system_prompt" style="margin-bottom: var(--space-sm);">
                <summary style="cursor: pointer; font-size: 12px; color: var(--color-text-muted);">实际 System Prompt（{{ sideB.system_prompt.length }} 字符）</summary>
                <div class="md-preview" v-html="md.render(sideB.system_prompt)" style="max-height: 300px; margin-top: 8px;"></div>
              </details>

              <!-- B 侧检索溯源 -->
              <div v-if="sideB?.retrieval_trace?.providers?.length" style="margin-bottom: var(--space-md);">
                <details style="font-size: 13px;">
                  <summary style="cursor: pointer; color: var(--color-text-muted); font-weight: 600;">检索溯源（{{ sideB.retrieval_trace.providers.length }} 个数据源）</summary>
                  <div v-for="(prov, pi) in sideB.retrieval_trace.providers" :key="pi" style="margin-top: 8px; padding: 8px 12px; background: var(--color-surface); border: 1px solid var(--color-border); border-radius: var(--radius-md);">
                    <div style="font-weight: 600; font-size: 12px; margin-bottom: 4px;">{{ prov.name }} <span style="color: var(--color-text-muted); font-weight: 400;">({{ prov.type }})</span></div>
                    <div v-if="prov.error" style="color: var(--color-error); font-size: 11px;">错误: {{ prov.error }}</div>
                    <table v-if="prov.items?.length" style="width: 100%; font-size: 11px; border-collapse: collapse;">
                      <thead>
                        <tr style="border-bottom: 1px solid var(--color-border); text-align: left;">
                          <th style="padding: 2px 6px;">ID</th>
                          <th style="padding: 2px 6px;">文本</th>
                          <th style="padding: 2px 6px;">向量分</th>
                          <th style="padding: 2px 6px;">状态</th>
                        </tr>
                      </thead>
                      <tbody>
                        <tr v-for="item in selectedItems(prov.items)" :key="item.id" style="border-bottom: 1px solid var(--color-border);">
                          <td style="padding: 2px 6px; font-family: monospace;">{{ item.id?.slice(0, 8) }}</td>
                          <td style="padding: 2px 6px; max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ item.text || '-' }}</td>
                          <td style="padding: 2px 6px;">{{ item.score?.toFixed(3) || '-' }}</td>
                          <td style="padding: 2px 6px; color: var(--color-success);">✓</td>
                        </tr>
                        <tr v-for="item in discardedItems(prov.items)" :key="item.id" style="border-bottom: 1px solid var(--color-border);">
                          <td style="padding: 2px 6px; font-family: monospace;">{{ item.id?.slice(0, 8) }}</td>
                          <td style="padding: 2px 6px; max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ item.text || '-' }}</td>
                          <td style="padding: 2px 6px;">{{ item.score?.toFixed(3) || '-' }}</td>
                          <td style="padding: 2px 6px; color: var(--color-warning);">{{ item.discard_reason }}</td>
                        </tr>
                      </tbody>
                    </table>
                    <div v-else style="font-size: 11px; color: var(--color-text-muted);">无检索结果</div>
                  </div>
                </details>
              </div>

              <div v-if="sideB?.result?.tool_calls?.length" style="margin-bottom: var(--space-md);">
                <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">工具调用</div>
                <div v-for="(tc, idx) in sideB.result.tool_calls" :key="idx"
                  :style="{
                    background: 'var(--color-surface)',
                    border: `1px solid ${sideADisplay?.tool_calls?.[idx] && hasDiff(sideADisplay.tool_calls[idx], tc) ? 'var(--color-warning)' : 'var(--color-border)'}`,
                    borderRadius: 'var(--radius-md)', padding: '10px 12px', marginBottom: '8px',
                  }"
                >
                  <div style="font-weight: 600; font-size: 13px; margin-bottom: 4px;">{{ tc.tool }}.{{ tc.function }}</div>
                  <div style="font-size: 11px; color: var(--color-text-muted); margin-bottom: 4px;">参数:</div>
                  <pre class="tc-pre">{{ formatArgs(tc.args) }}</pre>
                  <div style="font-size: 11px; color: var(--color-text-muted); margin-bottom: 4px;">结果:</div>
                  <pre class="tc-pre" style="max-height: 200px; overflow-y: auto;">{{ formatResult(tc.result) }}</pre>
                </div>
              </div>
              <div v-else style="font-size: 12px; color: var(--color-text-muted); margin-bottom: var(--space-md);">无工具调用</div>
              <details v-if="sideB?.result?.reasoning?.length" style="margin-bottom: var(--space-md);">
                <summary style="cursor: pointer; font-size: 12px; color: var(--color-text-muted);">思考过程（{{ sideB.result.reasoning.length }} 轮）</summary>
                <div v-for="(r, idx) in sideB.result.reasoning" :key="idx" class="reasoning-box" style="margin-bottom: 8px;">
                  <div v-if="sideB.result.reasoning.length > 1" style="font-size: 11px; color: var(--color-text-muted); margin-bottom: 4px;">第 {{ idx + 1 }} 轮</div>
                  {{ r }}
                </div>
              </details>
              <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">回复</div>
              <div class="reply-box">{{ sideB?.result?.reply || '(无文本回复)' }}</div>
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.cfg-row {
  display: flex;
  align-items: center;
  gap: 4px;
}
.cfg-label {
  color: var(--color-text-muted);
  width: 56px;
  font-size: 11px;
}
.cfg-section {
  padding-top: 2px;
  font-size: 11px;
  color: var(--color-text-muted);
  font-weight: 600;
}

.replay-input,
.replay-select {
  flex: 1;
  padding: 3px 6px;
  border-radius: 4px;
  border: 1px solid var(--color-border);
  background: var(--color-bg-page);
  color: var(--color-text);
  font-size: 12px;
}
.replay-input:focus,
.replay-select:focus {
  outline: none;
  border-color: var(--color-primary);
}
.replay-select option {
  color: var(--color-text);
  background: var(--color-surface);
}

.run-btn {
  padding: 10px 40px;
  font-size: 15px;
  font-weight: 600;
  border: none;
  border-radius: var(--radius-md);
  background: var(--color-primary);
  color: #fff;
  margin-right: 12px;
}
.preview-btn {
  padding: 10px 24px;
  font-size: 13px;
  font-weight: 500;
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-md);
  background: var(--color-surface);
  color: var(--color-warning);
  cursor: pointer;
}

.md-preview {
  font-size: 13px;
  line-height: 1.7;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 16px;
  margin: 0;
  max-height: 500px;
  overflow-y: auto;
  color: var(--color-text);
}
.md-preview :deep(h1),
.md-preview :deep(h2),
.md-preview :deep(h3) { margin-top: 1em; margin-bottom: 0.5em; }
.md-preview :deep(h2) { font-size: 15px; border-bottom: 1px solid var(--color-border); padding-bottom: 4px; }
.md-preview :deep(code) { font-size: 12px; background: var(--color-bg-page); padding: 1px 4px; border-radius: 3px; }
.md-preview :deep(pre) { background: var(--color-bg-page); padding: 10px; border-radius: var(--radius-md); overflow-x: auto; }
.md-preview :deep(ul), .md-preview :deep(ol) { padding-left: 1.5em; }
.md-preview :deep(blockquote) { border-left: 3px solid var(--color-primary); margin-left: 0; padding-left: 12px; color: var(--color-text-muted); }

.prompt-editor {
  width: 100%;
  min-height: 400px;
  font-size: 12px;
  font-family: monospace;
  line-height: 1.5;
  background: var(--color-bg-page);
  border: 2px solid var(--color-primary);
  border-radius: var(--radius-md);
  padding: 12px;
  color: var(--color-text);
  resize: vertical;
}

.tc-pre {
  font-size: 11px;
  background: var(--color-bg-page);
  padding: 6px;
  border-radius: 4px;
  margin: 0 0 8px 0;
  overflow-x: auto;
  white-space: pre-wrap;
}
.reply-box {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 10px 12px;
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
}
.reasoning-box {
  font-size: 12px;
  line-height: 1.6;
  color: var(--color-text-muted);
  background: var(--color-bg-page);
  border: 1px solid var(--color-border);
  border-left: 3px solid var(--color-warning);
  border-radius: var(--radius-md);
  padding: 10px 12px;
  white-space: pre-wrap;
  max-height: 300px;
  overflow-y: auto;
}

pre { color: var(--color-text); }

[data-theme="dark"] .replay-input,
[data-theme="dark"] .replay-select {
  color-scheme: dark;
}
[data-theme="dark"] .replay-select option {
  color: #f1f5f9;
  background: #1e293b;
}
</style>
