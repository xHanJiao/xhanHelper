import { useRoute, useRouter } from 'vue-router'

// 列表页固定回聊天（不吃历史栈）；详情页按上一页回，无历史兜底 /chat。
export function useGoBack() {
  const route = useRoute()
  const router = useRouter()
  return function goBack() {
    if (route.meta.backToChat) {
      router.push('/chat')
      return
    }
    if (window.history.length > 1) router.back()
    else router.push('/chat')
  }
}
