import { createApp } from 'vue'
import { createPinia } from 'pinia'
import './style.css'
import App from './App.vue'
import router from './router'
import { applyChatFont, readChatFont } from '@/utils/chatFont'

// 手机 PWA 里能缩放就一眼看出是网页，整体禁掉（单指滚动/长按选择不受影响）
document.addEventListener('gesturestart', (e) => e.preventDefault()) // iOS 捏合，viewport 的 user-scalable 在 iOS 无效
document.addEventListener('touchmove', (e) => {
  if (e.touches.length > 1) e.preventDefault()
}, { passive: false })

applyChatFont(readChatFont())

const app = createApp(App)
const pinia = createPinia()

app.use(pinia)
app.use(router)

app.mount('#app')
