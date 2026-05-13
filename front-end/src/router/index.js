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
    component: () => import('@/views/MemoryView.vue')
  },
  {
    path: '/notes',
    name: 'Notes',
    component: () => import('@/views/NotesView.vue')
  },
  {
    path: '/bills',
    name: 'Bills',
    component: () => import('@/views/BillsView.vue')
  },
  {
    path: '/agents',
    name: 'Agents',
    component: () => import('@/views/AgentView.vue')
  },
  {
    path: '/prompts',
    name: 'Prompts',
    component: () => import('@/views/PromptView.vue')
  },
  {
    path: '/tools',
    name: 'Tools',
    component: () => import('@/views/ToolView.vue')
  },
  {
    path: '/admin',
    name: 'Admin',
    component: () => import('@/views/AdminView.vue')
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
  if (to.path !== '/login' && !auth.token) {
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
