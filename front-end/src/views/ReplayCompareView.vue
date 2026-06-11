<script setup>
import { ref, onMounted } from 'vue'
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
const results = ref({ a: null, b: null })

const requestJSON = ref(null)
const useRequestJSON = ref(false)

const bPromptPreview = ref('')
const bPromptLoading = ref(false)
const bShowPrompt = ref(false)
const bTrace = ref(null)

const defaultConfigA = {
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
const defaultConfigB = { ...defaultConfigA }

const configA = ref({ ...defaultConfigA })
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
    requestJSON.value = res.request_json || null
    if (!res.request_json) useRequestJSON.value = false
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
  results.value = { a: null, b: null }
  error.value = ''

  // A 永远不用 override
  const cfgA = { ...configA.value }
  cfgA.override_system_prompt = null

  const body = {
    anchor_message_id: msgId,
    history_count: 10,
    config_a: cfgA,
    config_b: { ...configB.value },
    use_request_json: useRequestJSON.value && !!requestJSON.value,
    request_json: useRequestJSON.value ? requestJSON.value : undefined,
  }

  try {
    const res = await api.stream('/api/admin/replay/compare', body)
    if (!res.ok) {
      if (res.status === 401) { auth.logout(); return }
      throw new Error(`HTTP ${res.status}`)
    }
    await parseReplaySSE(res)
  } catch (e) {
    if (e.name === 'AbortError') return
    error.value = '对比失败: ' + e.message
  } finally {
    running.value = false
  }
}

async function parseReplaySSE(res) {
  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let currentEvent = ''

  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split('\n')
    buffer = lines.pop()
    for (const line of lines) {
      if (line.startsWith('event: ')) {
        currentEvent = line.slice(7).trim()
      } else if (line.startsWith('data: ')) {
        const raw = line.slice(6)
        try {
          const data = JSON.parse(raw)
          if (currentEvent === 'replay_done') {
            if (data.side === 'a') results.value.a = data.result
            if (data.side === 'b') results.value.b = data.result
          } else if (currentEvent === 'error') {
            error.value = data.error || raw
            return
          }
        } catch {
          // ignore
        }
      }
    }
  }
}

function displayContent(msg) {
  if (msg.content) return msg.content.slice(0, 100)
  if (msg.role === 'assistant' && msg.tool_calls_json) {
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
  <div style="height: 100vh; display: flex; flex-direction: column; background: var(--color-bg-page);">
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
            [{{ roleLabel(msg.role) }}] {{ displayContent(msg) }}
          </div>
        </details>

        <!-- 配置面板 -->
        <div style="display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-md); margin-bottom: var(--space-xl);">
          <!-- Config A -->
          <div style="background: var(--color-surface); border: 1px solid var(--color-border); border-radius: var(--radius-lg); padding: var(--space-md);">
            <div style="font-weight: 600; font-size: 14px; margin-bottom: var(--space-sm); color: var(--color-primary);">配置 A（对照组）</div>

            <!-- 历史 request_json 开关 -->
            <label v-if="requestJSON" style="display: flex; align-items: center; gap: 8px; font-size: 12px; padding: 6px 8px; margin-bottom: var(--space-sm); background: var(--color-bg-page); border-radius: var(--radius-md); cursor: pointer;">
              <input type="checkbox" v-model="useRequestJSON" style="accent-color: var(--color-primary);" />
              <span>使用历史 request_json（直接重放原 LLM 请求）</span>
            </label>
            <div v-if="!requestJSON" style="font-size: 11px; color: var(--color-text-muted); margin-bottom: var(--space-sm); padding: 4px 8px; background: var(--color-bg-page); border-radius: var(--radius-md);">
              此消息无可用的历史 request_json
            </div>

            <div v-if="!useRequestJSON" style="display: grid; gap: 6px; font-size: 12px;">
              <label class="cfg-row"><span class="cfg-label">检索</span><select v-model="configA.skip_retrieval" class="replay-select"><option :value="false">开启</option><option :value="true">关闭</option></select></label>
              <label class="cfg-row"><span class="cfg-label">模型</span><select v-model="configA.deep_mode" class="replay-select"><option :value="false">Flash</option><option :value="true">Pro</option></select></label>
              <label class="cfg-row"><span class="cfg-label">截断长度</span><input type="number" v-model.number="configA.max_tool_result_chars" class="replay-input" /></label>
              <label class="cfg-row"><span class="cfg-label">最大轮次</span><input type="number" v-model.number="configA.max_iterations" class="replay-input" /></label>
              <div class="cfg-section">记忆检索</div>
              <label class="cfg-row"><span class="cfg-label">阈值</span><input type="number" v-model.number="configA.memory.threshold" step="0.05" class="replay-input" /></label>
              <label class="cfg-row"><span class="cfg-label">条数</span><input type="number" v-model.number="configA.memory.limit" class="replay-input" /></label>
              <div class="cfg-section">笔记检索</div>
              <label class="cfg-row"><span class="cfg-label">阈值</span><input type="number" v-model.number="configA.note.threshold" step="0.05" class="replay-input" /></label>
              <label class="cfg-row"><span class="cfg-label">条数</span><input type="number" v-model.number="configA.note.limit" class="replay-input" /></label>
              <div class="cfg-section">历史检索</div>
              <label class="cfg-row"><span class="cfg-label">阈值</span><input type="number" v-model.number="configA.history.threshold" step="0.05" class="replay-input" /></label>
              <label class="cfg-row"><span class="cfg-label">条数</span><input type="number" v-model.number="configA.history.limit" class="replay-input" /></label>
              <div class="cfg-section">主题检索</div>
              <label class="cfg-row"><span class="cfg-label">阈值</span><input type="number" v-model.number="configA.theme.threshold" step="0.05" class="replay-input" /></label>
              <label class="cfg-row"><span class="cfg-label">条数</span><input type="number" v-model.number="configA.theme.limit" class="replay-input" /></label>
            </div>
            <div v-else style="font-size: 12px; color: var(--color-text-muted); padding: 8px; background: var(--color-bg-page); border-radius: var(--radius-md);">
              A 组将直接发送历史 request_json 到 LLM，配置参数无效
            </div>
          </div>

          <!-- Config B -->
          <div style="background: var(--color-surface); border: 1px solid var(--color-border); border-radius: var(--radius-lg); padding: var(--space-md);">
            <div style="font-weight: 600; font-size: 14px; margin-bottom: var(--space-sm); color: var(--color-warning);">配置 B（实验组）</div>
            <div style="display: grid; gap: 6px; font-size: 12px;">
              <label class="cfg-row"><span class="cfg-label">检索</span><select v-model="configB.skip_retrieval" class="replay-select"><option :value="false">开启</option><option :value="true">关闭</option></select></label>
              <label class="cfg-row"><span class="cfg-label">模型</span><select v-model="configB.deep_mode" class="replay-select"><option :value="false">Flash</option><option :value="true">Pro</option></select></label>
              <label class="cfg-row"><span class="cfg-label">截断长度</span><input type="number" v-model.number="configB.max_tool_result_chars" class="replay-input" /></label>
              <label class="cfg-row"><span class="cfg-label">最大轮次</span><input type="number" v-model.number="configB.max_iterations" class="replay-input" /></label>
              <div class="cfg-section">记忆检索</div>
              <label class="cfg-row"><span class="cfg-label">阈值</span><input type="number" v-model.number="configB.memory.threshold" step="0.05" class="replay-input" /></label>
              <label class="cfg-row"><span class="cfg-label">条数</span><input type="number" v-model.number="configB.memory.limit" class="replay-input" /></label>
              <div class="cfg-section">笔记检索</div>
              <label class="cfg-row"><span class="cfg-label">阈值</span><input type="number" v-model.number="configB.note.threshold" step="0.05" class="replay-input" /></label>
              <label class="cfg-row"><span class="cfg-label">条数</span><input type="number" v-model.number="configB.note.limit" class="replay-input" /></label>
              <div class="cfg-section">历史检索</div>
              <label class="cfg-row"><span class="cfg-label">阈值</span><input type="number" v-model.number="configB.history.threshold" step="0.05" class="replay-input" /></label>
              <label class="cfg-row"><span class="cfg-label">条数</span><input type="number" v-model.number="configB.history.limit" class="replay-input" /></label>
              <div class="cfg-section">主题检索</div>
              <label class="cfg-row"><span class="cfg-label">阈值</span><input type="number" v-model.number="configB.theme.threshold" step="0.05" class="replay-input" /></label>
              <label class="cfg-row"><span class="cfg-label">条数</span><input type="number" v-model.number="configB.theme.limit" class="replay-input" /></label>
            </div>
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

          <!-- 检索 Trace -->
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
                      <th style="padding: 2px 6px;">综合</th>
                      <th style="padding: 2px 6px;">向量</th>
                      <th style="padding: 2px 6px;">状态</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="item in selectedItems(prov.items)" :key="item.id" style="border-bottom: 1px solid var(--color-border);">
                      <td style="padding: 2px 6px; font-family: monospace;">{{ item.id?.slice(0, 8) }}</td>
                      <td style="padding: 2px 6px; max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ item.text || '-' }}</td>
                      <td style="padding: 2px 6px;">{{ item.score?.toFixed(3) || '-' }}</td>
                      <td style="padding: 2px 6px;">{{ item.vector_score?.toFixed(3) || '-' }}</td>
                      <td style="padding: 2px 6px; color: var(--color-success);">✓</td>
                    </tr>
                    <tr v-for="item in discardedItems(prov.items)" :key="item.id" style="border-bottom: 1px solid var(--color-border);">
                      <td style="padding: 2px 6px; font-family: monospace;">{{ item.id?.slice(0, 8) }}</td>
                      <td style="padding: 2px 6px; max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ item.text || '-' }}</td>
                      <td style="padding: 2px 6px;">-</td>
                      <td style="padding: 2px 6px;">{{ item.vector_score?.toFixed(3) || '-' }}</td>
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
        <div v-if="results.a || results.b" style="margin-bottom: var(--space-xl);">
          <div style="font-weight: 600; font-size: 15px; margin-bottom: var(--space-md); padding-bottom: 8px; border-bottom: 2px solid var(--color-border);">对比结果</div>

          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-md);">
            <!-- Side A -->
            <div>
              <div style="font-weight: 600; font-size: 14px; margin-bottom: var(--space-sm); color: var(--color-primary);">配置 A（对照组）</div>
              <div v-if="results.a?.tool_calls?.length" style="margin-bottom: var(--space-md);">
                <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">工具调用</div>
                <div v-for="(tc, idx) in results.a.tool_calls" :key="idx"
                  :style="{
                    background: 'var(--color-surface)',
                    border: `1px solid ${results.b?.tool_calls?.[idx] && hasDiff(tc, results.b.tool_calls[idx]) ? 'var(--color-warning)' : 'var(--color-border)'}`,
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
              <details v-if="results.a?.reasoning?.length" style="margin-bottom: var(--space-md);">
                <summary style="cursor: pointer; font-size: 12px; color: var(--color-text-muted);">思考过程（{{ results.a.reasoning.length }} 轮）</summary>
                <div v-for="(r, idx) in results.a.reasoning" :key="idx" class="reasoning-box" style="margin-bottom: 8px;">
                  <div v-if="results.a.reasoning.length > 1" style="font-size: 11px; color: var(--color-text-muted); margin-bottom: 4px;">第 {{ idx + 1 }} 轮</div>
                  {{ r }}
                </div>
              </details>
              <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">回复</div>
              <div class="reply-box">{{ results.a?.reply || '(无文本回复)' }}</div>
            </div>

            <!-- Side B -->
            <div>
              <div style="font-weight: 600; font-size: 14px; margin-bottom: var(--space-sm); color: var(--color-warning);">配置 B（实验组）</div>
              <div v-if="results.b?.tool_calls?.length" style="margin-bottom: var(--space-md);">
                <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">工具调用</div>
                <div v-for="(tc, idx) in results.b.tool_calls" :key="idx"
                  :style="{
                    background: 'var(--color-surface)',
                    border: `1px solid ${results.a?.tool_calls?.[idx] && hasDiff(results.a.tool_calls[idx], tc) ? 'var(--color-warning)' : 'var(--color-border)'}`,
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
              <details v-if="results.b?.reasoning?.length" style="margin-bottom: var(--space-md);">
                <summary style="cursor: pointer; font-size: 12px; color: var(--color-text-muted);">思考过程（{{ results.b.reasoning.length }} 轮）</summary>
                <div v-for="(r, idx) in results.b.reasoning" :key="idx" class="reasoning-box" style="margin-bottom: 8px;">
                  <div v-if="results.b.reasoning.length > 1" style="font-size: 11px; color: var(--color-text-muted); margin-bottom: 4px;">第 {{ idx + 1 }} 轮</div>
                  {{ r }}
                </div>
              </details>
              <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">回复</div>
              <div class="reply-box">{{ results.b?.reply || '(无文本回复)' }}</div>
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
  gap: 8px;
}
.cfg-label {
  color: var(--color-text-muted);
  width: 72px;
}
.cfg-section {
  margin-top: 4px;
  padding-top: 6px;
  border-top: 1px solid var(--color-border);
  font-size: 11px;
  color: var(--color-text-muted);
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
