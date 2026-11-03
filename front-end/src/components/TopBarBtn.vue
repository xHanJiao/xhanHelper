<script setup>
import { computed } from 'vue'
import { NTooltip } from 'naive-ui'

const props = defineProps({
  icon: { type: String, default: '' },
  label: { type: String, default: '' },
  tooltip: { type: String, default: '' },
  variant: { type: String, default: 'pill' }
})

defineEmits(['click'])

const hasTooltip = computed(() => !!props.tooltip)
</script>

<template>
  <NTooltip v-if="hasTooltip" trigger="click">
    <template #trigger>
      <span class="topbar-btn" :class="variant">
        <span v-if="icon">{{ icon }}</span>
        <span v-if="label">{{ label }}</span>
      </span>
    </template>
    {{ tooltip }}
  </NTooltip>
  <span
    v-else
    class="topbar-btn"
    :class="variant"
    @click="$emit('click')"
  >
    <span v-if="icon">{{ icon }}</span>
    <span v-if="label">{{ label }}</span>
  </span>
</template>

<style scoped>
/* pill 默认样式 — 与"新对话"胶囊按钮风格一致 */
.topbar-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 0 12px;
  height: 34px;
  font-size: 13px;
  color: var(--color-text-secondary);
  background: var(--color-primary-soft);
  border: 1px solid var(--color-border);
  border-radius: 24px;
  cursor: pointer;
  user-select: none;
  line-height: 1.5;
  transition: all 0.15s;
}
.topbar-btn:hover {
  color: #fff;
  background: var(--color-primary);
  border-color: var(--color-primary);
}

/* text 变体 — 无背景无边框，hover 微亮 */
.topbar-btn.text {
  padding: 2px 6px;
  font-size: 14px;
  color: var(--color-text-muted);
  background: none;
  border: none;
  border-radius: var(--radius-md);
  cursor: default;
  transition: background 0.15s, color 0.15s;
}
.topbar-btn.text:hover {
  color: var(--color-text);
  background: var(--color-surface-hover);
  border-color: transparent;
}
</style>
