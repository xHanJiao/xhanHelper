<script setup>
import { computed } from 'vue'
import MarkdownIt from 'markdown-it'
import { NTag } from 'naive-ui'
import ThinkingBlock from '@/components/ThinkingBlock.vue'
import ToolCallCard from '@/components/ToolCallCard.vue'

const props = defineProps({
  message: {
    type: Object,
    required: true
  },
  isHistory: {
    type: Boolean,
    default: true
  },
  statusMessage: {
    type: String,
    default: ''
  }
})

const md = new MarkdownIt()

const isUser = computed(() => props.message.role === 'user')
const isAssistant = computed(() => props.message.role === 'assistant')

const contentHtml = computed(() => md.render(props.message.content || ''))
</script>

<template>
  <div style="display: flex; margin-bottom: 16px;">
    <!-- 用户消息 -->
    <div v-if="isUser" style="width: 100%; display: flex; justify-content: flex-end;">
      <div class="user-bubble" style="max-width: 70%; background: #18a058; color: #fff; border-radius: 12px; padding: 4px 10px; font-size: 15px; line-height: 1.4;">
        <div v-html="contentHtml" style="margin: 0;"></div>
      </div>
      <div style="width: 36px; height: 36px; border-radius: 50%; background: linear-gradient(135deg, #f093fb 0%, #f5576c 100%); display: flex; align-items: center; justify-content: center; color: #fff; font-weight: bold; margin-left: 10px; flex-shrink: 0;">我</div>
    </div>

    <!-- AI 消息 -->
    <div v-else style="width: 100%; display: flex;">
      <div style="width: 36px; height: 36px; border-radius: 50%; background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); display: flex; align-items: center; justify-content: center; color: #fff; font-weight: bold; margin-right: 10px; flex-shrink: 0;">AI</div>
      <div style="flex: 1; min-width: 0;">
        <ThinkingBlock
          :reasoning="message.reasoning"
          :done="message.reasoningDone"
          :is-history="isHistory"
        />

        <div v-if="contentHtml" style="font-size: 15px; line-height: 1.7; color: #333;">
          <div v-html="contentHtml" style="margin: 0;"></div>
        </div>

        <div v-if="statusMessage && !isHistory" class="status-badge">
          <NTag type="info" size="small">{{ statusMessage }}</NTag>
        </div>

        <ToolCallCard
          :tool-calls="message.toolCalls || []"
          :is-history="isHistory"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.user-bubble p {
  margin: 0;
}
.status-badge {
  margin-top: 8px;
}
</style>
