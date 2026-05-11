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
  <div style="display: flex; margin-bottom: var(--space-md);">
    <!-- 用户消息 -->
    <div v-if="isUser" style="width: 100%; display: flex; justify-content: flex-end;">
      <div class="user-bubble" style="max-width: 85%; border-radius: var(--radius-lg); padding: var(--space-sm) var(--space-lg); font-size: 15px; line-height: 1.6;">
        <div v-html="contentHtml" style="margin: 0;"></div>
      </div>
    </div>

    <!-- AI 消息 -->
    <div v-else style="width: 100%; display: flex;">
      <div style="flex: 1; min-width: 0; max-width: 85%; border-radius: var(--radius-lg); padding: var(--space-sm) var(--space-lg);">
        <ThinkingBlock
          :reasoning="message.reasoning"
          :done="message.reasoningDone"
          :is-history="isHistory"
        />

        <div v-if="contentHtml" style="font-size: 15px; line-height: 1.7; color: var(--color-text);">
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
.user-bubble {
  background: var(--color-primary);
  color: #fff;
}
[data-theme="dark"] .user-bubble {
  background: var(--color-primary);
  color: #fff;
}
.status-badge {
  margin-top: var(--space-sm);
}
</style>
