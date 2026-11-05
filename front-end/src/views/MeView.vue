<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useMobile } from '@/composables/useMobile'
import PageHeader from '@/components/PageHeader.vue'

const router = useRouter()
const auth = useAuthStore()
const { isDesktop } = useMobile()
const isAdmin = computed(() => auth.user?.isAdmin === true)
const name = computed(() => auth.user?.username || 'xhanHelper')

const items = [
  { label: '账单', path: '/bills', icon: '¥' },
  { label: '记忆', path: '/memories', icon: '◎' },
  { label: '事件', path: '/event-threads', icon: '✳' },
  { label: '智能体', path: '/agents', icon: '◇' }
]

function go(path) {
  router.push(path)
}
</script>

<template>
  <div class="me-page">
    <div class="page-scroll me-scroll">
      <PageHeader title="我的">
        <template #actions-right>
          <button class="icon-btn" type="button" aria-label="设置" title="设置" @click="go('/settings')">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1.08-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1.08 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h.09a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v.09a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>
          </button>
        </template>
      </PageHeader>

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
          <span class="item-name">{{ item.label }}</span>
        </button>
      </div>

      <div class="section">
        <div class="section-title">会话</div>
        <button class="row card" type="button" @click="go('/settings')">
          <span>已归档会话</span><span class="row-meta">在设置中查看</span><span class="chev">›</span>
        </button>
      </div>

      <div v-if="isAdmin && isDesktop" class="section">
        <div class="section-title">帮助</div>
        <button class="row card" type="button" @click="go('/admin')">
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
  /* 容器保持全宽（宽屏两侧也能滚），用内边距把内容收进 760 */
  padding-left: max(16px, calc((100% - 760px) / 2));
  padding-right: max(16px, calc((100% - 760px) / 2));
}
@media (max-width: 1099px) {
  .me-scroll { padding: calc(var(--safe-top) + 4px) 12px 20px; }
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
/* 奇数张时最后一张独占一行，铺满整行 */
.grid > .item:last-child:nth-child(odd) { grid-column: 1 / -1; }
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
.item-name { font-size: 13.5px; font-weight: 600; }
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
