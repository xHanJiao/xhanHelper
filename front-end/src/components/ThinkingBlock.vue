<script setup>
import { computed, ref } from 'vue'
import { NCollapse, NCollapseItem } from 'naive-ui'

const props = defineProps({
  reasoning: { type: String, default: '' },
  done: { type: Boolean, default: false },
  isHistory: { type: Boolean, default: false }
})

const expandedNames = ref([])

const showToggle = computed(() => props.reasoning?.length > 0)
</script>

<template>
  <div v-if="showToggle" class="thinking-block">
    <NCollapse v-model:expanded-names="expandedNames">
      <NCollapseItem title="思考过程" name="reasoning">
        <div class="thinking-content" :class="{ 'is-done': done }">{{ reasoning }}</div>
      </NCollapseItem>
    </NCollapse>
  </div>
</template>

<style scoped>
.thinking-block {
  margin-bottom: var(--space-md);
}
.thinking-content {
  white-space: pre-wrap;
  font-size: 14px;
  color: var(--color-text-secondary);
  background: var(--color-bg);
  padding: var(--space-md);
  border-radius: var(--radius-md);
}
.thinking-content.is-done {
  opacity: 0.7;
}
</style>
