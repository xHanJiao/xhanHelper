<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useMobile } from '@/composables/useMobile'
import PageTopBar from '@/components/PageTopBar.vue'

const router = useRouter()
const auth = useAuthStore()
const { isDesktop } = useMobile()
const isAdmin = computed(() => auth.user?.isAdmin === true)
const name = computed(() => auth.user?.username || 'xhanHelper')

const items = [
  { label: '图片', sub: '图床', path: '/images', icon: '◍' },
  { label: '账单', sub: '记录', path: '/bills', icon: '¥' },
  { label: '记忆', sub: '长期', path: '/memories', icon: '◎' },
  { label: '主题', sub: '状态页', path: '/themes', icon: '✳' },
  { label: '智能体', sub: '自定义', path: '/agents', icon: '◇' },
  { label: '设置', sub: '外观与账号', path: '/settings', icon: '⚙' }
]

function go(path) {
  router.push(path)
}
</script>

<template>
  <div class="me-page">
    <PageTopBar title="我的" />
    <div class="me-scroll">
      <div class="me-heading">我的</div>

      <div class="profile card">
        <div class="avatar">{{ name.slice(0, 1).toUpperCase() }}</div>
        <div class="profile-text">
          <div class="name">{{ name }}</div>
          <div class="sub">已连接 · 本机</div>
        </div>
        <span v-if="isAdmin" class="badge">管理员</span>
      </div>

      <div class="grid">
        <button v-for="item in items" :key="item.path" class="item card" type="button" @click="go(item.path)">
          <span class="item-ico">{{ item.icon }}</span>
          <span class="item-text">
            <span class="item-name">{{ item.label }}</span>
            <span class="item-sub">{{ item.sub }}</span>
          </span>
        </button>
      </div>

      <div class="section">
        <div class="section-title">会话</div>
        <button class="row card" type="button" @click="go('/settings')">
          <span>已归档会话</span><span class="row-meta">在设置中查看</span><span class="chev">›</span>
        </button>
        <button class="row card" type="button" @click="go('/settings')">
          <span>无痕会话</span><span class="row-meta">关闭</span><span class="chev">›</span>
        </button>
      </div>

      <div class="section">
        <div class="section-title">帮助</div>
        <button class="row card" type="button">
          <span>快捷操作说明</span><span class="chev">›</span>
        </button>
        <button class="row card" type="button" @click="go('/settings')">
          <span>设置</span><span class="chev">›</span>
        </button>
        <button v-if="isAdmin && isDesktop" class="row card" type="button" @click="go('/admin')">
          <span>管理后台</span><span class="row-meta">仅桌面端</span><span class="chev">›</span>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.me-page {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.me-scroll {
  flex: 1;
  overflow-y: auto;
  padding: 0 16px 28px;
  max-width: 760px;
  width: 100%;
  margin: 0 auto;
}
.me-heading {
  font-size: 28px;
  font-weight: 700;
  letter-spacing: -0.02em;
  padding: 2px 2px 14px;
}
.card {
  background: var(--color-surface);
  border: 0.5px solid var(--color-border);
  box-shadow: var(--shadow-sm);
  border-radius: 12px;
}
.profile {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px;
  margin-bottom: 14px;
}
.avatar {
  width: 48px;
  height: 48px;
  border-radius: 16px;
  display: grid;
  place-items: center;
  background: var(--color-primary);
  color: #fff;
  font-size: 19px;
  font-weight: 700;
}
[data-theme="dark"] .avatar { color: #10221B; }
.profile-text { flex: 1; min-width: 0; }
.name { font-size: 15.5px; font-weight: 650; }
.sub { font-size: 11.5px; color: var(--color-text-secondary); margin-top: 2px; }
.badge {
  font-size: 10px;
  padding: 3px 7px;
  border-radius: 99px;
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
.grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  margin-bottom: 16px;
}
.item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 13px 12px;
  cursor: pointer;
  text-align: left;
  transition: background var(--transition-fast), transform var(--transition-fast);
}
.item:hover { background: var(--color-primary-soft); transform: translateY(-1px); }
.item-ico {
  width: 30px;
  height: 30px;
  border-radius: 9px;
  display: grid;
  place-items: center;
  background: var(--color-primary-soft);
  color: var(--color-primary);
  flex: 0 0 auto;
}
.item-text { display: flex; flex-direction: column; min-width: 0; }
.item-name { font-size: 13px; font-weight: 600; }
.item-sub { font-size: 10px; color: var(--color-text-muted); margin-top: 1px; }
.section { margin-top: 6px; }
.section-title { font-size: 11px; color: var(--color-text-muted); padding: 0 4px 7px; letter-spacing: 0.04em; }
.row {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px;
  margin-bottom: 7px;
  font-size: 13px;
  cursor: pointer;
  text-align: left;
}
.row:hover { background: var(--color-primary-soft); }
.row-meta { margin-left: auto; color: var(--color-text-muted); font-size: 11px; }
.chev { color: var(--color-text-muted); }
</style>
