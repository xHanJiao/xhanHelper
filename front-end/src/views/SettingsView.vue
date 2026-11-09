<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NInput, NModal, useMessage } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { useConversationsStore } from '@/stores/conversations'
import { useMobile } from '@/composables/useMobile'
import { api } from '@/api/client'
import { CHAT_FONT_OPTIONS, readChatFont, setChatFont } from '@/utils/chatFont'
import { readVoiceSpeaker, setVoiceSpeaker } from '@/utils/voicePref'
import { isMacEmbedded } from '@/utils/embed'
import PageHeader from '@/components/PageHeader.vue'

const router = useRouter()
const auth = useAuthStore()
const theme = useThemeStore()
const convs = useConversationsStore()
const message = useMessage()
const { isDesktop, isMobile } = useMobile()
const envMode = import.meta.env.MODE

const isAdmin = computed(() => auth.user?.isAdmin === true)
const username = computed(() => auth.user?.username || 'xhanHelper')
const fontScale = ref(readChatFont())
const appVersion = __APP_VERSION__
const isEmbedded = isMacEmbedded()

function chooseFont(name) {
  fontScale.value = name
  setChatFont(name)
}

const showPassword = ref(false)
const oldPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const saving = ref(false)

// 语音音色：只存本地；服务端下发白名单与默认值。
const voiceConfig = ref(null)
const voiceSpeaker = ref(readVoiceSpeaker())
const voiceNotice = ref('')
const previewState = ref('idle') // idle | loading | playing
const PREVIEW_TEXT = '你好，我是你的聊天助手，今天过得怎么样？'
// iOS Safari 自动播放策略：点击手势内先播静音源解锁，拿到 mp3 后换 src 再播。
const SILENT_WAV = 'data:audio/wav;base64,UklGRiQAAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQAAAAA='
let previewAudio = null
let previewURL = ''
let previewToken = 0

const voiceSpeakers = computed(() => voiceConfig.value?.speakers || [])
const voiceReady = computed(() => voiceConfig.value?.ready === true && voiceSpeakers.value.length > 0)

async function loadVoiceConfig() {
  try {
    const cfg = await api.get('/api/voice/config')
    voiceConfig.value = cfg
    if (!cfg?.ready) return
    const list = cfg.speakers || []
    if (!list.length) return
    const stored = readVoiceSpeaker()
    if (stored && !list.some(s => s.id === stored)) {
      // 本地音色已下架：回落默认并清掉，本次进页给一行说明。
      const fallbackName = list.find(s => s.id === cfg.speaker)?.name || cfg.speaker
      voiceNotice.value = `已选音色不可用，已切回 ${fallbackName}`
      setVoiceSpeaker('')
      voiceSpeaker.value = cfg.speaker || list[0]?.id || ''
    } else {
      voiceSpeaker.value = stored || cfg.speaker || list[0]?.id || ''
    }
  } catch (e) {
    // 未启用或网络失败：整组不渲染，不影响其他设置。
    console.error('Failed to load voice config', e)
    voiceConfig.value = null
  }
}

function chooseSpeaker(e) {
  const id = e.target.value
  voiceSpeaker.value = id
  setVoiceSpeaker(id)
  voiceNotice.value = ''
  stopPreview()
}

function stopPreview() {
  previewToken += 1
  if (previewAudio) {
    previewAudio.pause()
    previewAudio.src = ''
    previewAudio = null
  }
  if (previewURL) {
    URL.revokeObjectURL(previewURL)
    previewURL = ''
  }
  previewState.value = 'idle'
}

async function togglePreview() {
  if (previewState.value === 'playing') {
    stopPreview()
    return
  }
  if (previewState.value === 'loading') return
  stopPreview()
  const token = previewToken
  previewState.value = 'loading'
  // 先在手势内解锁音频元素，网络回来后才允许出声。
  const audio = new Audio(SILENT_WAV)
  previewAudio = audio
  audio.play().catch(() => {})
  try {
    const speaker = voiceSpeaker.value
    const blob = await api.postBlob('/api/voice/tts',
      speaker ? { text: PREVIEW_TEXT, speaker } : { text: PREVIEW_TEXT })
    if (token !== previewToken) return
    previewURL = URL.createObjectURL(blob)
    audio.onended = () => { if (previewAudio === audio) stopPreview() }
    audio.onerror = () => {
      if (previewAudio !== audio) return
      message.error('试听失败')
      stopPreview()
    }
    audio.src = previewURL
    audio.load()
    await audio.play()
    if (token !== previewToken) return
    previewState.value = 'playing'
  } catch (e) {
    if (token !== previewToken) return
    console.error('Voice preview failed', e)
    message.error(e?.message || '试听失败')
    stopPreview()
  }
}

onBeforeUnmount(stopPreview)

const archived = ref([])
const archivedTotal = ref(0)
const archivedLoading = ref(false)
const archivedError = ref(false)
const restoringId = ref('')

async function loadArchived(offset = 0) {
  if (archivedLoading.value) return
  archivedLoading.value = true
  archivedError.value = false
  try {
    const res = await convs.fetchArchived(offset)
    if (offset === 0) {
      archived.value = res.list
    } else {
      const merged = new Map(archived.value.map(c => [c.id, c]))
      for (const c of res.list) merged.set(c.id, c)
      archived.value = [...merged.values()]
    }
    archivedTotal.value = res.total
  } catch (e) {
    console.error('Failed to load archived conversations', e)
    if (!archived.value.length) archivedError.value = true
    message.error('归档会话加载失败')
  } finally {
    archivedLoading.value = false
  }
}

async function loadMoreArchived() {
  await loadArchived(archived.value.length)
}

async function restore(conv) {
  if (restoringId.value) return
  restoringId.value = conv.id
  try {
    await api.post(`/api/conversations/${conv.id}/restore`, {})
    archived.value = archived.value.filter(c => c.id !== conv.id)
    archivedTotal.value = Math.max(0, archivedTotal.value - 1)
    convs.fetchList()
    message.success('已恢复')
    // 当前页刚好删空：自动补下一页，别让列表停在空状态
    if (!archived.value.length && archivedTotal.value > 0) loadArchived(0)
  } catch (e) {
    console.error('Failed to restore conversation', e)
    message.error('恢复失败')
  } finally {
    restoringId.value = ''
  }
}

async function changePassword() {
  if (!oldPassword.value || !newPassword.value) {
    message.warning('请填写旧密码和新密码')
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    message.warning('两次输入的新密码不一致')
    return
  }
  if (newPassword.value.length < 10) {
    message.warning('新密码至少 10 个字符')
    return
  }
  saving.value = true
  try {
    await api.post('/api/auth/change-password', {
      old_password: oldPassword.value,
      new_password: newPassword.value
    })
    message.success('密码修改成功')
    showPassword.value = false
    oldPassword.value = ''
    newPassword.value = ''
    confirmPassword.value = ''
  } catch (e) {
    message.error(e?.message || '修改失败，请检查旧密码是否正确')
  } finally {
    saving.value = false
  }
}

async function logout() {
  await auth.logout()
  router.push('/login')
}

onMounted(() => {
  loadArchived()
  loadVoiceConfig()
})
</script>

<template>
  <div class="settings-page">
    <div class="page-scroll settings-scroll">
      <PageHeader title="设置" />

      <div class="group">
        <div class="group-title">外观</div>
        <div class="card">
          <div class="row">
            <span class="label">主题</span>
            <div class="seg">
              <button type="button" :disabled="isEmbedded" :title="isEmbedded ? '跟随应用外观' : undefined" :aria-pressed="theme.mode === 'light'" :class="{ on: theme.mode === 'light' }" @click="theme.setMode('light')">浅色</button>
              <button type="button" :disabled="isEmbedded" :title="isEmbedded ? '跟随应用外观' : undefined" :aria-pressed="theme.mode === 'dark'" :class="{ on: theme.mode === 'dark' }" @click="theme.setMode('dark')">深色</button>
              <button type="button" :disabled="isEmbedded" :title="isEmbedded ? '跟随应用外观' : undefined" :aria-pressed="theme.mode === 'system'" :class="{ on: theme.mode === 'system' }" @click="theme.setMode('system')">跟随系统</button>
            </div>
          </div>
          <div class="row">
            <span class="label">字号</span>
            <div class="seg">
              <button
                v-for="opt in CHAT_FONT_OPTIONS"
                :key="opt.value"
                type="button"
                :aria-pressed="fontScale === opt.value"
                :class="{ on: fontScale === opt.value }"
                @click="chooseFont(opt.value)"
              >{{ opt.label }}</button>
            </div>
          </div>
        </div>
        <div class="note">字号只作用于聊天气泡；文档阅读器的字号在阅读页单独调。</div>
      </div>

      <div v-if="voiceReady" class="group">
        <div class="group-title">语音</div>
        <div class="card">
          <div class="row">
            <span class="label">音色</span>
            <select class="voice-select" :value="voiceSpeaker" @change="chooseSpeaker">
              <option v-for="s in voiceSpeakers" :key="s.id" :value="s.id">{{ s.name }}</option>
            </select>
          </div>
          <button class="row row-btn" type="button" :disabled="previewState === 'loading'" @click="togglePreview">
            <span class="label">试听当前音色</span>
            <span v-if="previewState === 'loading'" class="voice-preview">
              <span class="voice-spinner"></span>合成中…
            </span>
            <span v-else-if="previewState === 'playing'" class="voice-preview">
              <span class="voice-eq"><i></i><i></i><i></i><i></i></span>停止
            </span>
            <span v-else class="voice-preview">
              <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M8 5.5v13l11-6.5z"/></svg>播放
            </span>
          </button>
        </div>
        <div v-if="voiceNotice" class="note warn">{{ voiceNotice }}</div>
        <div class="note">只影响语音回复的朗读；切换后下一句生效。</div>
      </div>

      <div class="group">
        <div class="group-title">账号</div>
        <div class="card">
          <div class="row"><span class="label">{{ username }}</span><span class="value">{{ isAdmin ? '管理员' : '用户' }}</span></div>
          <button class="row row-btn" type="button" @click="showPassword = true"><span class="label">修改密码</span><span class="chev">›</span></button>
          <button class="row row-btn danger" type="button" @click="logout"><span class="label">退出登录</span><span class="chev">›</span></button>
        </div>
      </div>

      <div class="group">
        <div class="group-title">会话</div>
        <div class="card">
          <div class="row"><span class="label">已归档会话</span><span class="value">{{ archivedError ? '—' : archivedTotal }}</span></div>
          <div v-if="archivedLoading && !archived.length" class="archived-empty">加载中…</div>
          <div v-else-if="archivedError" class="archived-empty">
            加载失败<button type="button" class="archived-retry" @click="loadArchived(0)">重试</button>
          </div>
          <div v-else-if="!archived.length" class="archived-empty">没有已归档会话</div>
          <div v-for="conv in archived" :key="conv.id" class="archived-row">
            <span class="archived-title">{{ conv.title || '未命名对话' }}</span>
            <button type="button" :disabled="archivedLoading || !!restoringId" @click="restore(conv)">
              {{ restoringId === conv.id ? '恢复中…' : '恢复' }}
            </button>
          </div>
          <button
            v-if="archived.length < archivedTotal"
            class="archived-more"
            type="button"
            :disabled="archivedLoading || !!restoringId"
            @click="loadMoreArchived"
          >{{ archivedLoading ? '加载中…' : '加载更多' }}</button>
        </div>
      </div>

      <div class="group">
        <div class="group-title">关于</div>
        <div class="card">
          <div class="row"><span class="label">版本</span><span class="value">Web {{ appVersion }}</span></div>
          <div class="row"><span class="label">前端构建</span><span class="value">{{ envMode }}</span></div>
          <div v-if="isAdmin && isDesktop" class="row row-btn" @click="router.push('/admin')"><span class="label">管理后台</span><span class="chev">›</span></div>
        </div>
        <div v-if="isMobile" class="note">安装到桌面：iPhone 用 Safari 分享 → 添加到主屏幕；Android 用浏览器菜单 → 安装应用。</div>
        <div class="note">管理后台只在桌面端提供；手机上不显示入口。</div>
      </div>
    </div>

    <NModal v-model:show="showPassword" preset="card" title="修改密码" style="max-width: 420px; width: calc(100vw - 32px);">
      <div class="pwd-form">
        <NInput v-model:value="oldPassword" type="password" placeholder="旧密码" show-password-on="click" />
        <NInput v-model:value="newPassword" type="password" placeholder="新密码" show-password-on="click" />
        <NInput v-model:value="confirmPassword" type="password" placeholder="确认新密码" show-password-on="click" />
      </div>
      <template #footer>
        <div class="modal-foot">
          <NButton @click="showPassword = false">取消</NButton>
          <NButton type="primary" :loading="saving" @click="changePassword">保存</NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.settings-page { height: 100%; display: flex; flex-direction: column; min-height: 0; }
.settings-scroll {
  /* 容器保持全宽（宽屏两侧也能滚），用内边距把内容收进 760 */
  padding-left: max(16px, calc((100% - 760px) / 2));
  padding-right: max(16px, calc((100% - 760px) / 2));
}
@media (max-width: 1099px) {
  .settings-scroll { padding: calc(var(--safe-top) + 4px) 12px 20px; }
}
.group { margin-bottom: 16px; }
.group-title { font-size: 10.5px; letter-spacing: 0.08em; color: var(--color-text-muted); padding: 0 4px 7px; }
.card { border-radius: 12px; background: var(--color-surface); border: 0.5px solid var(--color-border); box-shadow: var(--shadow-sm); overflow: hidden; }
.row { display: flex; align-items: center; gap: 10px; padding: 12px 13px; font-size: 13px; border-bottom: 0.5px solid var(--color-border); }
.row:last-child { border-bottom: 0; }
.row-btn { width: 100%; background: transparent; border-left: 0; border-right: 0; border-top: 0; cursor: pointer; text-align: left; }
.row-btn:hover { background: var(--color-primary-soft); }
.row-btn.danger { color: var(--color-danger); }
.label { flex: 1; }
.value { color: var(--color-text-muted); font-size: 11.5px; }
.chev { color: var(--color-text-muted); }
.seg { display: flex; gap: 2px; background: var(--color-tool); border-radius: 9px; padding: 2px; }
.seg button { border: 0; background: transparent; border-radius: 7px; padding: 5px 9px; font-size: 11px; color: var(--color-text-secondary); cursor: pointer; }
.seg button.on { background: var(--color-primary); color: #fff; }
[data-theme="dark"] .seg button.on { color: #10221B; }
.seg button:disabled { opacity: 0.45; cursor: default; }
.note { font-size: 10.5px; color: var(--color-text-muted); padding: 8px 4px 0; line-height: 1.5; }
.note.warn { color: var(--color-warning); }
.voice-select {
  max-width: 58%;
  min-width: 118px;
  border: 1px solid var(--color-border);
  border-radius: 7px;
  background: var(--color-input);
  color: var(--color-text);
  font-family: inherit;
  font-size: 12.5px;
  padding: 5px 8px;
}
.voice-preview { display: inline-flex; align-items: center; gap: 6px; color: var(--color-primary); font-size: 12.5px; font-weight: 500; }
.voice-preview svg { width: 15px; height: 15px; }
.voice-spinner {
  width: 13px; height: 13px; border-radius: 50%;
  border: 1.8px solid color-mix(in srgb, var(--color-primary) 28%, transparent);
  border-top-color: var(--color-primary);
  animation: voice-spin .7s linear infinite;
}
@keyframes voice-spin { to { transform: rotate(360deg); } }
.voice-eq { display: flex; gap: 2px; align-items: flex-end; height: 14px; }
.voice-eq i { width: 2.5px; border-radius: 2px; background: var(--color-primary); animation: voice-eq .9s ease-in-out infinite; }
.voice-eq i:nth-child(1) { height: 6px; animation-delay: 0s; }
.voice-eq i:nth-child(2) { height: 12px; animation-delay: .12s; }
.voice-eq i:nth-child(3) { height: 8px; animation-delay: .24s; }
.voice-eq i:nth-child(4) { height: 13px; animation-delay: .36s; }
@keyframes voice-eq { 0%, 100% { transform: scaleY(.45); } 50% { transform: scaleY(1); } }
.archived-empty { font-size: 11.5px; color: var(--color-text-muted); padding: 12px 13px; }
.archived-row { display: flex; align-items: center; gap: 8px; padding: 10px 13px; border-top: 0.5px solid var(--color-border); font-size: 12.5px; }
.archived-title { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.archived-row button { border: 0; background: var(--color-primary-soft); color: var(--color-primary); border-radius: 7px; padding: 4px 8px; font-size: 11px; cursor: pointer; }
.archived-row button:disabled { opacity: 0.5; cursor: default; }
.archived-retry { margin-left: 8px; border: 0; background: transparent; color: var(--color-primary); font-size: 11.5px; padding: 0; cursor: pointer; }
.archived-more { width: 100%; border: 0; border-top: 0.5px solid var(--color-border); background: transparent; color: var(--color-primary); padding: 10px; font-size: 12px; cursor: pointer; }
.archived-more:hover { background: var(--color-primary-soft); }
.archived-more:disabled { color: var(--color-text-muted); cursor: default; }
.pwd-form { display: flex; flex-direction: column; gap: 10px; }
.modal-foot { display: flex; justify-content: flex-end; gap: 8px; }
</style>
