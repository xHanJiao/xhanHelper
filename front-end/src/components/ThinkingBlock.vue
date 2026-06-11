<script setup>
import { computed, ref, watch } from 'vue'

const props = defineProps({
  reasoning: { type: String, default: '' },
  done: { type: Boolean, default: false },
  isHistory: { type: Boolean, default: false },
  hasContent: { type: Boolean, default: false }
})

const showBlock = computed(() => props.reasoning?.length > 0)
const expanded = ref(false)

// 流式消息：正文开始输出时自动收起思考过程
if (!props.isHistory) {
  watch(() => props.hasContent, (val) => { if (val) expanded.value = false })
}
</script>

<template>
  <div v-if="showBlock" class="thinking-block">
    <div class="thinking-header" :class="{ 'is-active': !done }" @click="expanded = !expanded">
      <span class="thinking-label">思考过程</span>
      <span class="thinking-arrow">{{ expanded ? '▲' : '▼' }}</span>
    </div>
    <div
      v-if="expanded"
      class="thinking-content"
      :class="{ 'is-done': done }"
      @click="expanded = false"
    >{{ reasoning }}</div>
  </div>
</template>

<style scoped>
.thinking-block {
  margin-top: var(--space-sm);
  margin-bottom: var(--space-md);
}
.thinking-header {
  font-size: 12px;
  color: var(--color-text-muted);
  margin-bottom: 4px;
  padding: 4px 2px;
  display: flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
  user-select: none;
}
.thinking-arrow {
  font-size: 10px;
}
.thinking-label {
  transition: color 0.4s;
}
.thinking-header.is-active .thinking-label {
  animation: thinking-pulse 1.2s ease-in-out infinite;
}
@keyframes thinking-pulse {
  0%, 100% { color: var(--color-text-muted); }
  50% { color: var(--color-primary); }
}
.thinking-content {
  white-space: pre-wrap;
  font-size: 14px;
  line-height: 1.5;
  color: var(--color-text-secondary);
  background: var(--color-bg);
  padding: 10px var(--space-md);
  border-radius: var(--radius-md);
}
.thinking-content.is-done {
  opacity: 0.7;
}
</style>
