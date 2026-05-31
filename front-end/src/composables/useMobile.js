import { ref, onMounted, onUnmounted } from 'vue'

const drawerOpen = ref(false)
const isMobile = ref(false)
const sidebarCollapsed = ref(true)

function check() {
  isMobile.value = window.innerWidth <= 768
}

export function useMobile() {
  onMounted(() => {
    check()
    window.addEventListener('resize', check)
  })
  onUnmounted(() => {
    window.removeEventListener('resize', check)
  })

  return {
    isMobile,
    drawerOpen,
    toggleDrawer: () => { drawerOpen.value = !drawerOpen.value },
    closeDrawer: () => { drawerOpen.value = false },
    sidebarCollapsed,
    toggleSidebar: () => { sidebarCollapsed.value = !sidebarCollapsed.value }
  }
}
