<script setup>
import { ref, computed, watch, nextTick, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useChatStore } from '@/stores/chat'
import { useConversationsStore } from '@/stores/conversations'
import { useChatSSE } from '@/composables/useChatSSE'
import { api } from '@/api/client'
import {
  NLayout,
  NLayoutSider,
  NLayoutHeader,
  NLayoutContent,
  NInput,
  NButton,
  NSwitch,
  NSelect,
  useMessage,
  NBadge,
  NEmpty
} from 'naive-ui'
import NavSidebar from '@/components/NavSidebar.vue'
import ConversationList from '@/components/ConversationList.vue'
import MessageBubble from '@/components/MessageBubble.vue'

const route = useRoute()
const chat = useChatStore()
const convs = useConversationsStore()
const { sendMessage } = useChatSSE()
const message = useMessage()

const inputValue = ref('')
const messagesEndRef = ref(null)
const isLoadingHistory = ref(false)

const currentTitle = computed(() => {
  const conv = convs.list.find(c => c.id === chat.conversationId)
  return conv?.title || '新对话'
})

// 滚动到底部
function scrollToBottom() {
  nextTick(() => {
    messagesEndRef.value?.scrollIntoView({ behavior: 'smooth' })
  })
}

// 加载历史消息
async function loadHistory(convId, direction = 'before', cursor = null) {
  if (!convId) return

  isLoadingHistory.value = true
  try {
    let url = `/api/conversations/${convId}/messages-v2?limit=20`
    if (cursor) {
      url += `&cursor=${cursor}&direction=${direction}`
    }
    const res = await api.get(url)

    if (res) {
      if (direction === 'before' && cursor) {
        chat.prependMessages(res.messages || [])
      } else {
        chat.setMessages(res.messages || [])
      }
      chat.oldestCursor = res.oldestCursor
      chat.hasMoreBefore = res.hasMoreBefore
    }
  } catch (e) {
    console.error('Failed to load history', e)
    message.error('加载历史消息失败')
  } finally {
    isLoadingHistory.value = false
  }
}

watch(() => route.params.id, (newId) => {
  if (newId && newId !== chat.conversationId) {
    chat.setConversation(newId)
    loadHistory(newId)
  } else if (!newId) {
    chat.reset()
  }
}, { immediate: true })

watch(() => chat.streamingMessage?.content, () => scrollToBottom())
watch(() => chat.messages.length, () => scrollToBottom())

async function handleSend() {
  const content = inputValue.value.trim()
  if (!content || chat.isStreaming) return

  chat.addMessage({ id: Date.now().toString(), role: 'user', content })
  inputValue.value = ''
  scrollToBottom()

  await sendMessage(content, {
    conversationId: chat.conversationId,
    deepMode: chat.deepMode,
    agentId: chat.agentId
  })
}

watch(() => chat.statusMessage, (msg) => msg && message.info(msg))
watch(() => chat.error, (err) => err && message.error(err))
</script>

<template>
  <div style="display: flex; height: 100vh; width: 100vw;">
    <!-- 侧边栏：会话列表 -->
    <ConversationList />

    <!-- 主内容区 -->
    <div style="flex: 1; display: flex; flex-direction: column; background: #f7f8fa; min-width: 0;">
      <!-- 顶部工具栏 -->
      <div style="padding: 16px 24px; border-bottom: 1px solid #e8e8e8; display: flex; align-items: center; justify-content: space-between; background: #fff; flex-shrink: 0;">
        <div style="font-size: 16px; font-weight: 500;">{{ currentTitle }}</div>
        <div style="display: flex; align-items: center; gap: 16px;">
          <div style="display: flex; align-items: center; gap: 8px;">
            <span style="font-size: 14px; color: #666;">深度思考</span>
            <NSwitch v-model:value="chat.deepMode" size="small" />
          </div>
          <NSelect v-model:value="chat.agentId" placeholder="选择智能体" style="width: 180px;" clearable size="small" />
          <NBadge v-if="chat.statusMessage" :value="chat.statusMessage" type="info" />
        </div>
      </div>

      <!-- 消息区域 -->
      <div style="flex: 1; overflow-y: auto; padding: 24px; display: flex; justify-content: center;">
        <div style="width: 100%; max-width: 900px;">
          <div v-if="chat.messages.length === 0 && !chat.streamingMessage" style="padding: 100px 0; text-align: center; color: #999;">
            开始聊天吧
          </div>

          <div v-if="chat.hasMoreBefore" style="text-align: center; margin-bottom: 24px;">
            <NButton size="small" quaternary :loading="isLoadingHistory" @click="loadHistory(chat.conversationId, 'before', chat.oldestCursor)">
              加载更多历史
            </NButton>
          </div>

          <MessageBubble v-for="msg in chat.messages" :key="msg.id" :message="msg" />
          <MessageBubble v-if="chat.streamingMessage" :message="chat.streamingMessage" />

          <div ref="messagesEndRef" />
        </div>
      </div>

      <!-- 输入区域 - 紧贴底部 -->
      <div style="padding: 16px 24px; background: #fff; border-top: 1px solid #e8e8e8; flex-shrink: 0;">
        <div style="max-width: 900px; margin: 0 auto;">
          <div style="border: 1px solid #e8e8e8; border-radius: 24px; padding: 12px 16px; background: #fff;">
            <NInput
              v-model:value="inputValue"
              type="textarea"
              placeholder="发消息..."
              :disabled="chat.isStreaming"
              :autosize="{ minRows: 1, maxRows: 6 }"
              @keydown="(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleSend() }}"
              :bordered="false"
            />
            <div style="display: flex; justify-content: space-between; align-items: center; margin-top: 8px;">
              <div style="display: flex; gap: 12px; color: #999;">
                <span style="cursor: pointer;">+</span>
                <span style="cursor: pointer;">📎</span>
              </div>
              <NButton
                type="primary"
                size="small"
                :disabled="chat.isStreaming || !inputValue.trim()"
                :loading="chat.isStreaming"
                @click="handleSend"
                style="border-radius: 50%; width: 40px; height: 40px; padding: 0;"
              >
                {{ chat.isStreaming ? '...' : '↑' }}
              </NButton>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
