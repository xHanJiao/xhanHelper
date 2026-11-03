import { useRoute, useRouter } from 'vue-router'
import { useChatStore } from '@/stores/chat'

// 列表页固定回聊天（不吃历史栈）；详情页按上一页回，无历史兜底回聊天。
// 回聊天时落到上次看的会话，别把 store 里还在的会话丢掉；没有会话才开空白新对话。
export function useGoBack() {
  const route = useRoute()
  const router = useRouter()
  const chat = useChatStore()
  const chatPath = () => (chat.conversationId ? `/chat/${chat.conversationId}` : '/chat')

  return function goBack() {
    if (route.meta.backToChat) {
      router.push(chatPath())
      return
    }
    if (window.history.length > 1) router.back()
    else router.push(chatPath())
  }
}
