<script setup>
import { computed, ref, watch } from 'vue'
import { NCollapse, NCollapseItem } from 'naive-ui'

const props = defineProps({
  reasoning: { type: String, default: '' },
  done: { type: Boolean, default: false },
  isHistory: { type: Boolean, default: false }
})

const expandedNames = ref([])

const showToggle = computed(() => props.reasoning?.length > 0)

watch(() => props.done, (val) => {
  if (val) {
    expandedNames.value = []
  }
}, { immediate: false })

watch(() => props.reasoning, (val) => {
  if (!props.done && val?.length > 0) {
    expandedNames.value = ['reasoning']
  }
}, { immediate: true })
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
  margin-bottom: 10px;
}
.thinking-content {
  white-space: pre-wrap;
  font-size: 14px;
  color: #666;
  background: #f5f5f5;
  padding: 10px;
  border-radius: 8px;
}
.thinking-content.is-done {
  opacity: 0.7;
}
</style>
