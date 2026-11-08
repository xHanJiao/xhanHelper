<script setup>
import { computed } from 'vue'

// C2 全屏语音环：听 / 想 / 说 三态 + 麦克风音量。纯展示，动作由父组件处理。
const props = defineProps({
  phase: { type: String, required: true }, // listening | thinking | speaking
  level: { type: Number, default: 0 }
})
defineEmits(['close'])

const PHASES = {
  listening: { title: '听', hint: '说完停顿一下就会提交' },
  thinking: { title: '想', hint: '正在整理回答' },
  speaking: { title: '说', hint: '点「打断」可以随时插话' }
}
const info = computed(() => PHASES[props.phase] || PHASES.listening)
const coreScale = computed(() => {
  const level = Math.min(1, Math.max(0, Number(props.level) || 0))
  return props.phase === 'listening' ? 1 + level * 0.45 : 1.12
})
</script>

<template>
  <div class="voice-orb" role="dialog" aria-label="语音会话">
    <div class="voice-orb-rings" :class="`is-${phase}`">
      <span class="voice-orb-ring" />
      <span class="voice-orb-ring ring-2" />
      <span class="voice-orb-ring ring-3" />
      <span class="voice-orb-core" :style="{ transform: `scale(${coreScale})` }">
        <svg v-if="phase === 'listening'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 3a3 3 0 0 0-3 3v6a3 3 0 0 0 6 0V6a3 3 0 0 0-3-3z" />
          <path d="M19 11a7 7 0 0 1-14 0" /><path d="M12 18v3" />
        </svg>
        <span v-else-if="phase === 'thinking'" class="voice-orb-dots"><i /><i /><i /></span>
        <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
          <path d="M11 5 6 9H3v6h3l5 4V5z" /><path d="M15.5 8.5a5 5 0 0 1 0 7" /><path d="M18.5 5.5a9 9 0 0 1 0 13" />
        </svg>
      </span>
    </div>
    <p class="voice-orb-title">{{ info.title }}</p>
    <p class="voice-orb-hint">{{ info.hint }}</p>
    <button class="voice-orb-close" type="button" @click="$emit('close')">
      {{ phase === 'speaking' ? '打断' : '结束' }}
    </button>
  </div>
</template>

<style scoped>
.voice-orb {
  position: fixed;
  inset: 0;
  z-index: 1200;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16px;
  padding: calc(var(--safe-top) + 24px) 24px calc(var(--safe-bottom) + 24px);
  background: color-mix(in srgb, var(--color-bg) 62%, transparent);
  backdrop-filter: blur(28px) saturate(1.1);
  -webkit-backdrop-filter: blur(28px) saturate(1.1);
  animation: voice-orb-in 0.18s ease-out;
}

.voice-orb-rings {
  position: relative;
  display: grid;
  place-items: center;
  width: min(72vw, 300px);
  aspect-ratio: 1;
}

.voice-orb-ring {
  position: absolute;
  width: 62%;
  aspect-ratio: 1;
  border-radius: 50%;
  border: 1.5px solid color-mix(in srgb, var(--app-accent) 34%, transparent);
}
.voice-orb-ring.ring-2 {
  width: 78%;
  border-color: color-mix(in srgb, var(--app-accent) 24%, transparent);
}
.voice-orb-ring.ring-3 {
  width: 94%;
  border-color: color-mix(in srgb, var(--app-accent) 15%, transparent);
}

.voice-orb-core {
  display: grid;
  place-items: center;
  width: 52%;
  aspect-ratio: 1;
  border-radius: 50%;
  color: var(--app-accent);
  background: color-mix(in srgb, var(--app-accent) 16%, transparent);
  transition: transform 0.12s ease-out;
}
.voice-orb-core svg {
  width: 34%;
  height: 34%;
}

.is-thinking .voice-orb-core {
  animation: voice-orb-breathe 1.6s ease-in-out infinite;
}

.voice-orb-dots {
  display: flex;
  gap: 8px;
}
.voice-orb-dots i {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: currentColor;
  animation: voice-orb-dot 1.2s ease-in-out infinite;
}
.voice-orb-dots i:nth-child(2) { animation-delay: 0.16s; }
.voice-orb-dots i:nth-child(3) { animation-delay: 0.32s; }

.voice-orb-title {
  margin: 0;
  font-size: 24px;
  font-weight: 500;
  color: var(--color-text);
}
.voice-orb-hint {
  margin: 0;
  font-size: var(--font-size-base);
  color: var(--color-text-secondary);
}

.voice-orb-close {
  margin-top: 12px;
  padding: 10px 26px;
  border: none;
  border-radius: 999px;
  background: var(--app-accent);
  color: #fff;
  font-size: var(--font-size-md);
  cursor: pointer;
}
[data-theme="dark"] .voice-orb-close { color: #141416; }

@keyframes voice-orb-in {
  from { opacity: 0; }
  to { opacity: 1; }
}
@keyframes voice-orb-breathe {
  0%, 100% { opacity: 0.75; }
  50% { opacity: 1; }
}
@keyframes voice-orb-dot {
  0%, 100% { opacity: 0.3; transform: translateY(0); }
  50% { opacity: 1; transform: translateY(-3px); }
}

@media (prefers-reduced-motion: reduce) {
  .is-thinking .voice-orb-core,
  .voice-orb-dots i { animation: none; }
  .voice-orb-core { transition: none; }
}
</style>
