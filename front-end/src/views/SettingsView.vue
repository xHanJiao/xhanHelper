<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NInput, NModal, useMessage } from 'naive-ui'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { useConversationsStore } from '@/stores/conversations'
import { useMobile } from '@/composables/useMobile'
import { api } from '@/api/client'
import PageTopBar from '@/components/PageTopBar.vue'

const router = useRouter()
const auth = useAuthStore()
const theme = useThemeStore()
const convs = useConversationsStore()
const message = useMessage()
const { isDesktop } = useMobile()
const envMode = import.meta.env.MODE

const isAdmin = computed(() => auth.user?.isAdmin === true)
const username = computed(() => auth.user?.username || 'xhanHelper')

const showPassword = ref(false)
const oldPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const saving = ref(false)

const archived = ref([])
const archivedLoading = ref(false)

async function loadArchived() {
  archivedLoading.value = true
  const res = await convs.fetchArchived(0)
  archived.value = res.list
  archivedLoading.value = false
}

async function restore(conv) {
  try {
    await api.post(`/api/conversations/${conv.id}/restore`, {})
    archived.value = archived.value.filter(c => c.id !== conv.id)
    convs.fetchList()
    message.success('已恢复')
  } catch (e) {
    message.error('恢复失败')
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

onMounted(loadArchived)
</script>

<template>
  <div class="settings-page">
    <PageTopBar title="设置" />
    <div class="settings-scroll">
      <div class="settings-heading">设置</div>

      <div class="group">
        <div class="group-title">外观</div>
        <div class="card">
          <div class="row">
            <span class="label">主题</span>
            <div class="seg">
              <button type="button" :class="{ on: !theme.isDark }" @click="theme.setAndSave(false)">浅色</button>
              <button type="button" :class="{ on: theme.isDark }" @click="theme.setAndSave(true)">深色</button>
              <button type="button" disabled title="M3 接入">跟随系统</button>
            </div>
          </div>
        </div>
        <div class="note">字号设置会在 M3 接入；当前聊天正文使用 15px 基线。</div>
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
          <div class="row"><span class="label">已归档会话</span><span class="value">{{ archived.length }}</span></div>
          <div v-if="archivedLoading" class="archived-empty">加载中…</div>
          <div v-else-if="!archived.length" class="archived-empty">没有已归档会话</div>
          <div v-for="conv in archived" :key="conv.id" class="archived-row">
            <span class="archived-title">{{ conv.title || '未命名对话' }}</span>
            <button type="button" @click="restore(conv)">恢复</button>
          </div>
        </div>
      </div>

      <div class="group">
        <div class="group-title">关于</div>
        <div class="card">
          <div class="row"><span class="label">版本</span><span class="value">Web · {{ $route.meta.bare ? '公开页' : '主应用' }}</span></div>
          <div class="row"><span class="label">后端环境</span><span class="value">{{ envMode }}</span></div>
          <div v-if="isAdmin && isDesktop" class="row row-btn" @click="router.push('/admin')"><span class="label">管理后台</span><span class="chev">›</span></div>
        </div>
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
.settings-scroll { flex: 1; overflow-y: auto; padding: 0 16px 28px; max-width: 760px; width: 100%; margin: 0 auto; }
.settings-heading { font-size: 28px; font-weight: 700; letter-spacing: -0.02em; padding: 2px 2px 14px; }
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
.archived-empty { font-size: 11.5px; color: var(--color-text-muted); padding: 12px 13px; }
.archived-row { display: flex; align-items: center; gap: 8px; padding: 10px 13px; border-top: 0.5px solid var(--color-border); font-size: 12.5px; }
.archived-title { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.archived-row button { border: 0; background: var(--color-primary-soft); color: var(--color-primary); border-radius: 7px; padding: 4px 8px; font-size: 11px; cursor: pointer; }
.pwd-form { display: flex; flex-direction: column; gap: 10px; }
.modal-foot { display: flex; justify-content: flex-end; gap: 8px; }
</style>
