<script setup>
import { computed } from 'vue'
import { NTooltip } from 'naive-ui'

const props = defineProps({
  icon: { type: String, default: '' },
  label: { type: String, default: '' },
  tooltip: { type: String, default: '' },
  variant: { type: String, default: 'default' }
})

defineEmits(['click'])

const hasTooltip = computed(() => !!props.tooltip)
</script>

<template>
  <NTooltip v-if="hasTooltip" trigger="click">
    <template #trigger>
      <span
        class="topbar-btn"
        :class="[variant, { 'has-label': label }]"
        :style="{ cursor: tooltip ? 'pointer' : 'default' }"
      >
        <span v-if="icon">{{ icon }}</span>
        <span v-if="label">{{ label }}</span>
      </span>
    </template>
    {{ tooltip }}
  </NTooltip>
  <span
    v-else
    class="topbar-btn"
    :class="[variant, { 'has-label': label, clickable: !!$attrs.onClick }]"
    @click="$emit('click')"
  >
    <span v-if="icon">{{ icon }}</span>
    <span v-if="label">{{ label }}</span>
  </span>
</template>

<style scoped>
.topbar-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 6px;
  font-size: 14px;
  color: var(--color-text-muted);
  border-radius: var(--radius-md);
  transition: background 0.15s, color 0.15s;
  user-select: none;
  line-height: 1.5;
}
.clickable {
  cursor: pointer;
}
.clickable:hover {
  background: var(--color-surface-hover);
  color: var(--color-text);
}

/* pill 变体 — 与"新对话"胶囊按钮风格一致 */
.topbar-btn.pill {
  padding: 2px 10px;
  font-size: 13px;
  color: var(--color-text-secondary);
  background: var(--color-primary-soft);
  border: 1px solid var(--color-border);
  border-radius: 24px;
  cursor: pointer;
  transition: all 0.15s;
}
.topbar-btn.pill:hover {
  color: #fff;
  background: var(--color-primary);
  border-color: var(--color-primary);
}
</style>
