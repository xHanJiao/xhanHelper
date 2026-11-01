import { ref } from 'vue'

// 单例：整个应用只挂一个 resize 监听，避免每个页面各挂一份。
const width = ref(typeof window !== 'undefined' ? window.innerWidth : 1280)
const isMobile = ref(width.value < 1100)
const isPhone = ref(width.value < 768)
const isDesktop = ref(width.value >= 1100)

function sync() {
  if (typeof window === 'undefined') return
  width.value = window.innerWidth
  isMobile.value = width.value < 1100
  isPhone.value = width.value < 768
  isDesktop.value = width.value >= 1100
}

if (typeof window !== 'undefined') {
  sync()
  window.addEventListener('resize', sync, { passive: true })
}

// 旧接口保留：侧栏/抽屉下线后不再使用，避免逐个页面改 import 时炸掉。
const drawerOpen = ref(false)
const sidebarCollapsed = ref(true)

export function useMobile() {
  return {
    width,
    isMobile,
    isPhone,
    isDesktop,
    drawerOpen,
    toggleDrawer: () => { drawerOpen.value = !drawerOpen.value },
    closeDrawer: () => { drawerOpen.value = false },
    sidebarCollapsed,
    toggleSidebar: () => { sidebarCollapsed.value = !sidebarCollapsed.value }
  }
}
