import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const routes = [
  {
    path: '/login',
    name: 'Login',
    component: () => import('@/views/LoginView.vue'),
    meta: { bare: true }
  },
  {
    path: '/chat',
    name: 'Chat',
    component: () => import('@/views/ChatView.vue')
  },
  {
    path: '/chat/:id',
    name: 'ChatWithId',
    component: () => import('@/views/ChatView.vue')
  },
  {
    path: '/memories',
    name: 'Memories',
    component: () => import('@/views/MemoryView.vue'),
    meta: { backToChat: true, hideTabs: true, contentHeader: true }
  },
  {
    path: '/notes',
    name: 'Notes',
    component: () => import('@/views/NotesView.vue'),
    meta: { backToChat: true, hideTabs: true, contentHeader: true }
  },
  {
    path: '/bills',
    name: 'Bills',
    component: () => import('@/views/BillsView.vue'),
    meta: { backToChat: true, hideTabs: true, contentHeader: true }
  },
  {
    path: '/agents',
    name: 'Agents',
    component: () => import('@/views/AgentView.vue'),
    meta: { backToChat: true, hideTabs: true, contentHeader: true }
  },
  {
    path: '/prompts',
    name: 'Prompts',
    component: () => import('@/views/PromptView.vue'),
    meta: { backToChat: true, hideTabs: true, contentHeader: true }
  },
  {
    path: '/tools',
    name: 'Tools',
    component: () => import('@/views/ToolView.vue'),
    meta: { backToChat: true, hideTabs: true, contentHeader: true }
  },
  {
    path: '/summaries',
    name: 'Summaries',
    component: () => import('@/views/SummaryView.vue'),
    meta: { backToChat: true, hideTabs: true, contentHeader: true }
  },
  {
    path: '/summaries/:id/messages',
    name: 'SummaryMessages',
    component: () => import('@/views/SummaryMessagesView.vue'),
    meta: { hideTabs: true }
  },
  {
    path: '/themes',
    name: 'Themes',
    component: () => import('@/views/ThemeView.vue'),
    meta: { backToChat: true, hideTabs: true, contentHeader: true }
  },
  {
    path: '/documents',
    name: 'Documents',
    component: () => import('@/views/DocumentsView.vue'),
    meta: { topLevel: true, backToChat: true, contentHeader: true }
  },
  {
    path: '/documents/:id',
    name: 'DocumentRead',
    component: () => import('@/views/DocumentReadView.vue'),
    meta: { hideTabs: true }
  },
  {
    path: '/artifacts',
    name: 'Artifacts',
    component: () => import('@/views/ArtifactsView.vue'),
    meta: { topLevel: true, backToChat: true, contentHeader: true }
  },
  {
    path: '/artifacts/:id',
    name: 'ArtifactDetail',
    component: () => import('@/views/ArtifactDetailView.vue'),
    meta: { hideTabs: true }
  },
  {
    path: '/images',
    name: 'Images',
    component: () => import('@/views/ImagesView.vue'),
    meta: { hideTabs: true, backToChat: true }
  },
  {
    path: '/settings',
    name: 'Settings',
    component: () => import('@/views/SettingsView.vue'),
    meta: { hideTabs: true, backToChat: true, contentHeader: true }
  },
  {
    path: '/me',
    name: 'Me',
    component: () => import('@/views/MeView.vue'),
    meta: { topLevel: true, backToChat: true, contentHeader: true }
  },
  {
    path: '/admin',
    name: 'Admin',
    component: () => import('@/views/AdminView.vue'),
    meta: { backToChat: true, adminOnly: true }
  },
  {
    path: '/admin/users',
    name: 'AdminUsers',
    component: () => import('@/views/AdminUsersView.vue'),
    meta: { backToChat: true, adminOnly: true }
  },
  {
    path: '/admin/message-debug/:id',
    name: 'MessageDebug',
    component: () => import('@/views/MessageDebugView.vue'),
    meta: { adminOnly: true }
  },
  {
    path: '/admin/replay-compare/:id',
    name: 'ReplayCompare',
    component: () => import('@/views/ReplayCompareView.vue'),
    meta: { adminOnly: true }
  },
  {
    path: '/admin/eval',
    name: 'EvalAnnotator',
    component: () => import('@/views/EvalAnnotatorView.vue'),
    meta: { backToChat: true, adminOnly: true }
  },
  {
    path: '/s/messages/:token',
    name: 'SharedMessages',
    component: () => import('@/views/SharedMessagesView.vue'),
    meta: { bare: true }
  },
  {
    path: '/s/:token',
    name: 'SharedDocument',
    component: () => import('@/views/SharedDocumentView.vue'),
    meta: { bare: true }
  },
  {
    path: '/',
    redirect: '/chat'
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

router.beforeEach((to, from, next) => {
  const auth = useAuthStore()
  if (to.path !== '/login' && !to.path.startsWith('/s/') && !auth.token) {
    // 尝试从 localStorage 恢复一次，防止 storage 事件误清或浏览器挂起丢状态
    auth.restore()
    if (!auth.token) {
      next('/login')
    } else {
      next()
    }
  } else if (to.path === '/login' && auth.token) {
    next('/chat')
  } else {
    next()
  }
})

export default router
