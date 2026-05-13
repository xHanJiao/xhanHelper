<script setup>
import { computed, ref, watch, nextTick } from 'vue'

const props = defineProps({
  reasoning: { type: String, default: '' },
  done: { type: Boolean, default: false },
  isHistory: { type: Boolean, default: false },
  maxLines: { type: Number, default: 3 }
})

const contentRef = ref(null)

const showBlock = computed(() => props.reasoning?.length > 0)

const maxHeight = computed(() => {
  // 14px font-size * 1.5 line-height * maxLines + padding
  return `${14 * 1.5 * props.maxLines + 20}px`
})

watch(() => props.reasoning, async () => {
  await nextTick()
  if (contentRef.value) {
    contentRef.value.scrollTop = contentRef.value.scrollHeight
  }
})
</script>

<template>
  <div v-if="showBlock" class="thinking-block">
    <div class="thinking-header">思考过程</div>
    <div
      ref="contentRef"
      class="thinking-content"
      :class="{ 'is-done': done }"
      :style="{ maxHeight }"
    >{{ reasoning }}</div>
  </div>
</template>

<style scoped>
.thinking-block {
  margin-bottom: var(--space-md);
}
.thinking-header {
  font-size: 12px;
  color: var(--color-text-muted);
  margin-bottom: 4px;
  padding-left: 2px;
}
.thinking-content {
  white-space: pre-wrap;
  font-size: 14px;
  line-height: 1.5;
  color: var(--color-text-secondary);
  background: var(--color-bg);
  padding: 10px var(--space-md);
  border-radius: var(--radius-md);
  overflow-y: auto;
}
.thinking-content.is-done {
  opacity: 0.7;
}
</style>
