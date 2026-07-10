import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const routes = [
  {
    path: '/login',
    name: 'Login',
    component: () => import('@/views/LoginView.vue')
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
    meta: { backToChat: true }
  },
  {
    path: '/notes',
    name: 'Notes',
    component: () => import('@/views/NotesView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/bills',
    name: 'Bills',
    component: () => import('@/views/BillsView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/agents',
    name: 'Agents',
    component: () => import('@/views/AgentView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/prompts',
    name: 'Prompts',
    component: () => import('@/views/PromptView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/tools',
    name: 'Tools',
    component: () => import('@/views/ToolView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/summaries',
    name: 'Summaries',
    component: () => import('@/views/SummaryView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/summaries/:id/messages',
    name: 'SummaryMessages',
    component: () => import('@/views/SummaryMessagesView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/themes',
    name: 'Themes',
    component: () => import('@/views/ThemeView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/missions',
    name: 'Missions',
    component: () => import('@/views/MissionsView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/mailbox',
    name: 'Mailbox',
    component: () => import('@/views/MailboxView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/documents',
    name: 'Documents',
    component: () => import('@/views/DocumentsView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/admin',
    name: 'Admin',
    component: () => import('@/views/AdminView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/admin/users',
    name: 'AdminUsers',
    component: () => import('@/views/AdminUsersView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/admin/message-debug/:id',
    name: 'MessageDebug',
    component: () => import('@/views/MessageDebugView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/admin/replay-compare/:id',
    name: 'ReplayCompare',
    component: () => import('@/views/ReplayCompareView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/admin/eval',
    name: 'EvalAnnotator',
    component: () => import('@/views/EvalAnnotatorView.vue'),
    meta: { backToChat: true }
  },
  {
    path: '/s/messages/:token',
    name: 'SharedMessages',
    component: () => import('@/views/SharedMessagesView.vue')
  },
  {
    path: '/s/:token',
    name: 'SharedDocument',
    component: () => import('@/views/SharedDocumentView.vue')
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
