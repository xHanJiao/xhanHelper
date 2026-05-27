<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PageTopBar from '@/components/PageTopBar.vue'
import { api } from '@/api/client'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const msgId = route.params.id

const loading = ref(true)
const error = ref('')
const anchorMsg = ref(null)
const historyMsgs = ref([])

const running = ref(false)
const results = ref({ a: null, b: null })

const defaultConfigA = {
  skip_retrieval: false,
  max_tool_result_chars: 2000,
  max_iterations: 8,
  memory_threshold: 0.5,
  memory_limit: 8,
  note_threshold: 0.5,
  note_limit: 5,
  history_threshold: 0.8,
  history_limit: 5,
  deep_mode: false,
}
const defaultConfigB = { ...defaultConfigA }

const configA = ref({ ...defaultConfigA })
const configB = ref({ ...defaultConfigB })

const roleLabel = (role) => ({ user: '用户', assistant: '助手', tool: '工具' }[role] || role)

async function loadPreview() {
  try {
    const res = await api.post('/api/admin/replay/preview', {
      anchor_message_id: msgId,
      history_count: 10,
      max_tool_result_chars: 2000,
    })
    anchorMsg.value = res.anchor_message
    historyMsgs.value = res.messages || []
  } catch (e) {
    error.value = '加载失败: ' + e.message
  } finally {
    loading.value = false
  }
}

function goBack() {
  window.history.back()
}

async function runCompare() {
  running.value = true
  results.value = { a: null, b: null }
  error.value = ''

  const body = {
    anchor_message_id: msgId,
    history_count: 10,
    config_a: { ...configA.value },
    config_b: { ...configB.value },
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
          // start 事件已使用预览数据，无需额外处理
        } catch (e) {
          // 忽略解析错误
        }
      }
    }
  }
}

const hasDiff = (tcA, tcB) => {
  if (!tcA || !tcB) return true
  return tcA.tool !== tcB.tool || tcA.function !== tcB.function
}

function jsonPreview(s) {
  if (!s) return ''
  try {
    return JSON.stringify(JSON.parse(s), null, 2)
  } catch {
    return s
  }
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

onMounted(loadPreview)
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
            [{{ roleLabel(msg.role) }}] {{ msg.content?.slice(0, 100) }}
          </div>
        </details>

        <!-- 配置面板 -->
        <div style="display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-md); margin-bottom: var(--space-xl);">
          <!-- Config A -->
          <div style="background: var(--color-surface); border: 1px solid var(--color-border); border-radius: var(--radius-lg); padding: var(--space-md);">
            <div style="font-weight: 600; font-size: 14px; margin-bottom: var(--space-sm); color: var(--color-primary);">配置 A</div>
            <div style="display: grid; gap: 6px; font-size: 12px;">
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">检索</span><select v-model="configA.skip_retrieval" class="replay-select"><option :value="false">开启</option><option :value="true">关闭</option></select></label>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">模型</span><select v-model="configA.deep_mode" class="replay-select"><option :value="false">Flash</option><option :value="true">Pro</option></select></label>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">截断长度</span><input type="number" v-model.number="configA.max_tool_result_chars" class="replay-input" /></label>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">最大轮次</span><input type="number" v-model.number="configA.max_iterations" class="replay-input" /></label>
              <div style="margin-top: 4px; padding-top: 6px; border-top: 1px solid var(--color-border); font-size: 11px; color: var(--color-text-muted);">记忆检索</div>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">阈值</span><input type="number" v-model.number="configA.memory_threshold" step="0.05" class="replay-input" /></label>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">条数</span><input type="number" v-model.number="configA.memory_limit" class="replay-input" /></label>
              <div style="margin-top: 4px; padding-top: 6px; border-top: 1px solid var(--color-border); font-size: 11px; color: var(--color-text-muted);">笔记检索</div>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">阈值</span><input type="number" v-model.number="configA.note_threshold" step="0.05" class="replay-input" /></label>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">条数</span><input type="number" v-model.number="configA.note_limit" class="replay-input" /></label>
              <div style="margin-top: 4px; padding-top: 6px; border-top: 1px solid var(--color-border); font-size: 11px; color: var(--color-text-muted);">历史检索</div>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">阈值</span><input type="number" v-model.number="configA.history_threshold" step="0.05" class="replay-input" /></label>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">条数</span><input type="number" v-model.number="configA.history_limit" class="replay-input" /></label>
            </div>
          </div>

          <!-- Config B -->
          <div style="background: var(--color-surface); border: 1px solid var(--color-border); border-radius: var(--radius-lg); padding: var(--space-md);">
            <div style="font-weight: 600; font-size: 14px; margin-bottom: var(--space-sm); color: var(--color-warning);">配置 B</div>
            <div style="display: grid; gap: 6px; font-size: 12px;">
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">检索</span><select v-model="configB.skip_retrieval" class="replay-select"><option :value="false">开启</option><option :value="true">关闭</option></select></label>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">模型</span><select v-model="configB.deep_mode" class="replay-select"><option :value="false">Flash</option><option :value="true">Pro</option></select></label>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">截断长度</span><input type="number" v-model.number="configB.max_tool_result_chars" class="replay-input" /></label>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">最大轮次</span><input type="number" v-model.number="configB.max_iterations" class="replay-input" /></label>
              <div style="margin-top: 4px; padding-top: 6px; border-top: 1px solid var(--color-border); font-size: 11px; color: var(--color-text-muted);">记忆检索</div>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">阈值</span><input type="number" v-model.number="configB.memory_threshold" step="0.05" class="replay-input" /></label>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">条数</span><input type="number" v-model.number="configB.memory_limit" class="replay-input" /></label>
              <div style="margin-top: 4px; padding-top: 6px; border-top: 1px solid var(--color-border); font-size: 11px; color: var(--color-text-muted);">笔记检索</div>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">阈值</span><input type="number" v-model.number="configB.note_threshold" step="0.05" class="replay-input" /></label>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">条数</span><input type="number" v-model.number="configB.note_limit" class="replay-input" /></label>
              <div style="margin-top: 4px; padding-top: 6px; border-top: 1px solid var(--color-border); font-size: 11px; color: var(--color-text-muted);">历史检索</div>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">阈值</span><input type="number" v-model.number="configB.history_threshold" step="0.05" class="replay-input" /></label>
              <label style="display: flex; align-items: center; gap: 8px;"><span style="color: var(--color-text-muted); width: 72px;">条数</span><input type="number" v-model.number="configB.history_limit" class="replay-input" /></label>
            </div>
          </div>
        </div>

        <!-- 运行按钮 -->
        <div style="text-align: center; margin-bottom: var(--space-xl);">
          <button
            @click="runCompare"
            :disabled="running"
            :style="{
              padding: '10px 40px',
              fontSize: '15px',
              fontWeight: 600,
              border: 'none',
              borderRadius: 'var(--radius-md)',
              cursor: running ? 'not-allowed' : 'pointer',
              background: running ? 'var(--color-border)' : 'var(--color-primary)',
              color: '#fff',
              opacity: running ? 0.6 : 1,
            }"
          >
            {{ running ? '运行中...' : '运行对比' }}
          </button>
          <div v-if="error" style="margin-top: 8px; color: var(--color-error); font-size: 13px;">{{ error }}</div>
        </div>

        <!-- 对比结果 -->
        <div v-if="results.a || results.b" style="margin-bottom: var(--space-xl);">
          <div style="font-weight: 600; font-size: 15px; margin-bottom: var(--space-md); padding-bottom: 8px; border-bottom: 2px solid var(--color-border);">对比结果</div>

          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-md);">
            <!-- Side A -->
            <div>
              <div style="font-weight: 600; font-size: 14px; margin-bottom: var(--space-sm); color: var(--color-primary);">配置 A</div>

              <!-- Tool Calls -->
              <div v-if="results.a?.tool_calls?.length" style="margin-bottom: var(--space-md);">
                <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">工具调用</div>
                <div v-for="(tc, idx) in results.a.tool_calls" :key="idx"
                  :style="{
                    background: 'var(--color-surface)',
                    border: `1px solid ${results.b?.tool_calls?.[idx] && hasDiff(tc, results.b.tool_calls[idx]) ? 'var(--color-warning)' : 'var(--color-border)'}`,
                    borderRadius: 'var(--radius-md)',
                    padding: '10px 12px',
                    marginBottom: '8px',
                  }"
                >
                  <div style="font-weight: 600; font-size: 13px; margin-bottom: 4px;">
                    {{ tc.tool }}.{{ tc.function }}
                  </div>
                  <div style="font-size: 11px; color: var(--color-text-muted); margin-bottom: 4px;">参数:</div>
                  <pre style="font-size: 11px; background: var(--color-bg-page); padding: 6px; border-radius: 4px; margin: 0 0 8px 0; overflow-x: auto; white-space: pre-wrap;">{{ formatArgs(tc.args) }}</pre>
                  <div style="font-size: 11px; color: var(--color-text-muted); margin-bottom: 4px;">结果:</div>
                  <pre style="font-size: 11px; background: var(--color-bg-page); padding: 6px; border-radius: 4px; margin: 0; overflow-x: auto; white-space: pre-wrap; max-height: 200px; overflow-y: auto;">{{ formatResult(tc.result) }}</pre>
                </div>
              </div>
              <div v-else style="font-size: 12px; color: var(--color-text-muted); margin-bottom: var(--space-md);">无工具调用</div>

              <!-- Reply -->
              <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">回复</div>
              <div style="background: var(--color-surface); border: 1px solid var(--color-border); border-radius: var(--radius-md); padding: 10px 12px; font-size: 13px; line-height: 1.6; white-space: pre-wrap;">{{ results.a?.reply || '(无文本回复)' }}</div>
            </div>

            <!-- Side B -->
            <div>
              <div style="font-weight: 600; font-size: 14px; margin-bottom: var(--space-sm); color: var(--color-warning);">配置 B</div>

              <!-- Tool Calls -->
              <div v-if="results.b?.tool_calls?.length" style="margin-bottom: var(--space-md);">
                <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">工具调用</div>
                <div v-for="(tc, idx) in results.b.tool_calls" :key="idx"
                  :style="{
                    background: 'var(--color-surface)',
                    border: `1px solid ${results.a?.tool_calls?.[idx] && hasDiff(results.a.tool_calls[idx], tc) ? 'var(--color-warning)' : 'var(--color-border)'}`,
                    borderRadius: 'var(--radius-md)',
                    padding: '10px 12px',
                    marginBottom: '8px',
                  }"
                >
                  <div style="font-weight: 600; font-size: 13px; margin-bottom: 4px;">
                    {{ tc.tool }}.{{ tc.function }}
                  </div>
                  <div style="font-size: 11px; color: var(--color-text-muted); margin-bottom: 4px;">参数:</div>
                  <pre style="font-size: 11px; background: var(--color-bg-page); padding: 6px; border-radius: 4px; margin: 0 0 8px 0; overflow-x: auto; white-space: pre-wrap;">{{ formatArgs(tc.args) }}</pre>
                  <div style="font-size: 11px; color: var(--color-text-muted); margin-bottom: 4px;">结果:</div>
                  <pre style="font-size: 11px; background: var(--color-bg-page); padding: 6px; border-radius: 4px; margin: 0; overflow-x: auto; white-space: pre-wrap; max-height: 200px; overflow-y: auto;">{{ formatResult(tc.result) }}</pre>
                </div>
              </div>
              <div v-else style="font-size: 12px; color: var(--color-text-muted); margin-bottom: var(--space-md);">无工具调用</div>

              <!-- Reply -->
              <div style="font-size: 12px; color: var(--color-text-muted); margin-bottom: 4px;">回复</div>
              <div style="background: var(--color-surface); border: 1px solid var(--color-border); border-radius: var(--radius-md); padding: 10px 12px; font-size: 13px; line-height: 1.6; white-space: pre-wrap;">{{ results.b?.reply || '(无文本回复)' }}</div>
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
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

pre {
  color: var(--color-text);
}

[data-theme="dark"] .replay-input,
[data-theme="dark"] .replay-select {
  color-scheme: dark;
}

[data-theme="dark"] .replay-select option {
  color: #f1f5f9;
  background: #1e293b;
}
</style>
