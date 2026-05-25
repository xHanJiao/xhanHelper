<script setup>
import { computed, ref } from 'vue'
import MarkdownIt from 'markdown-it'
import { NTag } from 'naive-ui'
import ThinkingBlock from '@/components/ThinkingBlock.vue'
import ToolCallCard from '@/components/ToolCallCard.vue'
import WaitingAnimation from '@/components/WaitingAnimation.vue'

const props = defineProps({
  message: { type: Object, required: true },
  isHistory: { type: Boolean, default: true },
})

const md = new MarkdownIt()

const isUser = computed(() => props.message.role === 'user')
const isAssistant = computed(() => props.message.role === 'assistant')

const contentHtml = computed(() => md.render(props.message.content || ''))

const timeText = computed(() => {
  if (!props.message.created_at) return ''
  return new Date(props.message.created_at).toLocaleString()
})

const memExpanded = ref(false)

const showCat = computed(() =>
  !props.isHistory && !contentHtml.value
)

const hasMemories = computed(() => {
  return props.message.role === 'user'
    && props.message.associatedMemories
    && props.message.associatedMemories.length > 0
})

const associatedMemories = computed(() => {
  return hasMemories.value ? props.message.associatedMemories : []
})

const downloadableFiles = computed(() => {
  const files = []
  for (const tc of props.message.toolCalls || []) {
    if (tc.status !== 'done' || !tc.result?.success) continue
    let data = tc.result.data
    if (typeof data === 'string') {
      try { data = JSON.parse(data) } catch { continue }
    }
    if (data?.url) {
      files.push({
        url: data.url,
        filename: data.filename || new URL(data.url).pathname.split('/').pop() || '下载',
        message: data.message || ''
      })
    }
  }
  return files
})
</script>

<template>
  <div class="msg-wrapper" style="display: flex; margin-bottom: var(--space-md);">
    <!-- 用户消息 -->
    <div v-if="isUser" style="width: 100%; display: flex; justify-content: flex-end;">
      <div style="display: flex; flex-direction: column; align-items: flex-end; max-width: 85%;">
        <div class="user-bubble" style="border-radius: var(--radius-lg); padding: var(--space-sm) var(--space-lg); font-size: 15px; line-height: 1.6;">
          <div v-html="contentHtml" style="margin: 0;"></div>
        </div>
        <div v-if="hasMemories" class="associated-memories">
          <div class="mem-toggle" @click="memExpanded = !memExpanded">
            <span class="mem-toggle-icon">{{ memExpanded ? '▾' : '▸' }}</span>
            关联了 {{ associatedMemories.length }} 条记忆
          </div>
          <div v-if="memExpanded" class="mem-list">
            <div v-for="mem in associatedMemories" :key="mem.id" class="mem-card">
              <div class="mem-key">{{ mem.memory_key }}</div>
              <div class="mem-value">{{ mem.memory_value }}</div>
            </div>
          </div>
        </div>
        <div v-if="timeText" class="msg-time user-time">{{ timeText }}</div>
      </div>
    </div>

    <!-- AI 消息 -->
    <div v-else style="width: 100%; display: flex;">
      <div style="flex: 1; min-width: 0; max-width: 85%; border-radius: var(--radius-lg); padding: var(--space-sm) var(--space-lg);">
        <ToolCallCard :tool-calls="message.toolCalls || []" :is-history="isHistory" />
        <div v-if="contentHtml" style="font-size: 15px; line-height: 1.7; color: var(--color-text);">
          <div v-html="contentHtml" style="margin: 0;"></div>
        </div>
        <WaitingAnimation v-if="showCat" class="bubble-cat" />
        <ThinkingBlock :reasoning="message.reasoning" :done="message.reasoningDone" :is-history="isHistory" />

        <div v-if="downloadableFiles.length > 0" class="download-section">
          <a
            v-for="file in downloadableFiles"
            :key="file.url"
            :href="file.url"
            :download="file.filename"
            class="download-link"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
            {{ file.filename }}
          </a>
        </div>

        <div v-if="timeText" class="msg-time assistant-time">{{ timeText }}</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.user-bubble { background: var(--color-primary); color: #fff; }
.user-bubble p { margin: 0; }
[data-theme="dark"] .user-bubble { background: var(--color-primary); color: #fff; }
.status-badge { margin-top: var(--space-sm); }
.msg-time {
  font-size: 11px;
  color: var(--color-text-muted);
  margin-top: 2px;
  opacity: 0;
  transition: opacity 0.2s;
}
.msg-wrapper:hover .msg-time { opacity: 1; }
.user-time { margin-right: 2px; }

.bubble-cat {
  width: 34px;
  margin-top: 4px;
}
.bubble-cat :deep(svg) {
  transform: scale(0.4);
  transform-origin: top left;
}
.assistant-time { margin-left: 2px; }

.associated-memories {
  margin-top: 6px;
  max-width: 280px;
}
.mem-toggle {
  font-size: 12px;
  color: var(--color-text-muted);
  cursor: pointer;
  user-select: none;
  padding: 2px 0;
}
.mem-toggle:hover {
  color: var(--color-primary);
}
.mem-toggle-icon {
  display: inline-block;
  width: 12px;
}
.mem-list {
  margin-top: 4px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.mem-card {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 6px 10px;
  text-align: left;
}
.mem-key {
  font-size: 12px;
  font-weight: 600;
  color: var(--color-text);
  margin-bottom: 2px;
}
.mem-value {
  font-size: 12px;
  color: var(--color-text-muted);
  line-height: 1.5;
}

.download-section {
  margin-top: var(--space-md);
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-xs);
}
.download-link {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 14px;
  border-radius: var(--radius-md);
  background: var(--color-primary-soft);
  color: var(--color-primary);
  font-size: 13px;
  font-weight: 500;
  text-decoration: none;
  transition: background var(--transition-fast);
}
.download-link:hover {
  background: var(--color-primary);
  color: #fff;
}
</style>
