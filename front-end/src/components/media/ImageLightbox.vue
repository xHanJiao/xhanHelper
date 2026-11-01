<script setup>
import { onBeforeUnmount, onMounted } from 'vue'
import AuthImage from '@/components/media/AuthImage.vue'

const props = defineProps({
  src: { type: String, required: true },
  visible: { type: Boolean, default: false }
})
const emit = defineEmits(['close'])

function onKey(e) {
  if (e.key === 'Escape') emit('close')
}

onMounted(() => document.addEventListener('keydown', onKey))
onBeforeUnmount(() => document.removeEventListener('keydown', onKey))
</script>

<template>
  <Teleport to="body">
    <div v-if="visible" class="lightbox" @click="emit('close')">
      <div class="frame" @click.stop>
        <AuthImage :src="src" />
        <button class="close" type="button" aria-label="关闭" @click="emit('close')">×</button>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.lightbox {
  position: fixed;
  inset: 0;
  z-index: 3000;
  display: grid;
  place-items: center;
  padding: 24px;
  background: rgba(0, 0, 0, 0.72);
  backdrop-filter: blur(6px);
  -webkit-backdrop-filter: blur(6px);
}
.frame {
  position: relative;
  max-width: min(92vw, 1100px);
  max-height: 88dvh;
}
.frame :deep(img) {
  max-width: min(92vw, 1100px);
  max-height: 88dvh;
  width: auto;
  height: auto;
  object-fit: contain;
  border-radius: 12px;
  box-shadow: 0 18px 50px rgba(0, 0, 0, 0.42);
}
.frame :deep(.ph) { width: 240px; height: 160px; border-radius: 12px; }
.close {
  position: absolute;
  top: -12px;
  right: -12px;
  width: 32px;
  height: 32px;
  border-radius: 50%;
  border: 0.5px solid rgba(255, 255, 255, 0.3);
  background: rgba(30, 30, 32, 0.86);
  color: #fff;
  font-size: 18px;
  line-height: 1;
  cursor: pointer;
}
</style>
