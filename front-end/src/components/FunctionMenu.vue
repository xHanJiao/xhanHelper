<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const auth = useAuthStore()
const open = ref(false)
const rootRef = ref(null)
const isAdmin = computed(() => auth.user?.isAdmin === true)

const groups = computed(() => {
  const main = [
    { label: '灵阅', sub: 'HTML 页面', path: '/artifacts', icon: '✦' },
    { label: '文档', sub: 'Markdown', path: '/documents', icon: '▤' },
    { label: '图片', sub: '图床', path: '/images', icon: '◍' },
    { label: '账单', sub: '记录', path: '/bills', icon: '¥' },
    { label: '记忆', sub: '长期', path: '/memories', icon: '◎' },
    { label: '主题', sub: '状态页', path: '/themes', icon: '✳' },
    { label: '智能体', sub: '自定义', path: '/agents', icon: '◇' },
    { label: '设置', sub: '偏好', path: '/settings', icon: '⚙' }
  ]
  if (!isAdmin.value) return [{ title: '功能', items: main }]
  return [
    { title: '功能', items: main },
    {
      title: '管理',
      items: [
        { label: '监控', sub: '看板', path: '/admin', icon: '▦' },
        { label: '用户', sub: '账号', path: '/admin/users', icon: '♙' },
        { label: '评测', sub: '检索', path: '/admin/eval', icon: '◫' }
      ]
    }
  ]
})

function go(path) {
  open.value = false
  router.push(path)
}

function onDocClick(e) {
  if (!open.value) return
  if (rootRef.value && !rootRef.value.contains(e.target)) open.value = false
}

onMounted(() => document.addEventListener('click', onDocClick))
onBeforeUnmount(() => document.removeEventListener('click', onDocClick))
</script>

<template>
  <div ref="rootRef" class="function-menu">
    <button class="function-trigger" type="button" @click.stop="open = !open">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" style="width:14px;height:14px"><path d="M4 6h16M4 12h16M4 18h16"/></svg>
      <span>功能</span>
    </button>

    <div v-if="open" class="function-panel" @click.stop>
      <div v-for="group in groups" :key="group.title" class="fn-group">
        <div class="fn-group-title">{{ group.title }}</div>
        <div class="fn-grid">
          <button
            v-for="item in group.items"
            :key="item.path"
            class="fn-item"
            type="button"
            @click="go(item.path)"
          >
            <span class="fn-ico">{{ item.icon }}</span>
            <span class="fn-name">{{ item.label }}</span>
            <span class="fn-sub">{{ item.sub }}</span>
          </button>
        </div>
      </div>
      <div class="fn-foot"><span>管理后台仅桌面端显示</span><span>⌘ K 搜索</span></div>
    </div>
  </div>
</template>

<style scoped>
.function-menu { position: relative; }
.function-trigger {
  height: 32px;
  padding: 0 11px;
  border-radius: 999px;
  border: 0.5px solid var(--color-border);
  background: rgba(255, 255, 255, 0.24);
  display: inline-flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  color: var(--color-text-secondary);
  font-size: 12.5px;
  transition: background var(--transition-fast), color var(--transition-fast);
}
[data-theme="dark"] .function-trigger { background: rgba(255, 255, 255, 0.04); }
.function-trigger:hover { background: var(--color-primary-soft); color: var(--color-primary); }
.function-panel {
  position: absolute;
  top: calc(100% + 8px);
  right: 0;
  width: 460px;
  max-width: calc(100vw - 32px);
  padding: 13px;
  border-radius: 14px;
  background: rgba(252, 248, 240, 0.96);
  border: 0.5px solid var(--color-border-strong);
  box-shadow: var(--shadow-lg);
  backdrop-filter: blur(18px) saturate(1.1);
  -webkit-backdrop-filter: blur(18px) saturate(1.1);
  z-index: 800;
}
[data-theme="dark"] .function-panel { background: rgba(38, 38, 40, 0.96); }
.fn-group + .fn-group { margin-top: 11px; padding-top: 10px; border-top: 0.5px solid var(--color-border); }
.fn-group-title {
  font-size: 10px;
  color: var(--color-text-muted);
  letter-spacing: 0.06em;
  padding: 0 4px 6px;
}
.fn-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 4px;
}
.fn-item {
  border: 0;
  background: transparent;
  border-radius: 10px;
  padding: 9px 8px;
  display: flex;
  flex-direction: column;
  gap: 3px;
  text-align: left;
  cursor: pointer;
}
.fn-item:hover { background: var(--color-primary-soft); }
.fn-ico {
  width: 26px;
  height: 26px;
  border-radius: 8px;
  display: grid;
  place-items: center;
  background: var(--color-primary-soft);
  color: var(--color-primary);
  margin-bottom: 2px;
  font-size: 13px;
}
.fn-name { font-size: 12.5px; font-weight: 600; color: var(--color-text); }
.fn-sub { font-size: 10px; color: var(--color-text-muted); }
.fn-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 11px;
  padding: 8px 4px 0;
  border-top: 0.5px solid var(--color-border);
  font-size: 10px;
  color: var(--color-text-muted);
}
</style>
