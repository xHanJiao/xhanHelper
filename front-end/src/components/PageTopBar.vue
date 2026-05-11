<script setup>
import { useMobile } from '@/composables/useMobile'

const props = defineProps({
  title: {
    type: String,
    default: ''
  },
  visible: {
    type: Boolean,
    default: true
  }
})

const { isMobile, toggleDrawer } = useMobile()
</script>

<template>
  <div
    :style="{
      height: visible ? (isMobile ? '56px' : 'auto') : '0',
      padding: visible ? 'var(--space-md) var(--space-lg)' : '0 var(--space-lg)',
      borderBottom: visible ? '1px solid var(--color-border)' : 'none',
      background: 'var(--color-surface)',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'space-between',
      flexShrink: 0,
      overflow: 'hidden',
      transition: 'height 0.2s ease-out, padding 0.2s ease-out, border-width 0.2s ease-out'
    }"
  >
    <span style="font-size: var(--font-size-lg); font-weight: 500; color: var(--color-text); display: flex; align-items: center; gap: var(--space-sm);">
      <span v-if="isMobile" @click="toggleDrawer()" style="cursor: pointer; font-size: 20px;">☰</span>
      {{ title }}
    </span>
    <slot name="right"></slot>
  </div>
</template>
