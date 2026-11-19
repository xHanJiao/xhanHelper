<script setup>
import { onBeforeUnmount, ref, watch } from 'vue'
import { cachedImage, isAuthImageSrc, loadImage } from '@/components/media/imageBlobCache'

// 图床私有读要 Authorization 头，<img src> 带不上，所以先 fetch 成 blob 再转 objectURL。
// blob 归模块缓存所有（见 imageBlobCache.js），卸载时不撤销。
const props = defineProps({
  src: { type: String, required: true },
  alt: { type: String, default: '' }
})

const url = ref('')
const failed = ref(false)
// 快速切图时旧请求可能后回来：只认最后一次
let seq = 0

async function load() {
  const my = ++seq
  failed.value = false
  if (!props.src) {
    url.value = ''
    return
  }
  if (!isAuthImageSrc(props.src)) {
    url.value = props.src
    return
  }
  const hit = cachedImage(props.src)
  if (hit) {
    url.value = hit
    return
  }
  url.value = ''
  try {
    const fresh = await loadImage(props.src)
    if (my !== seq) return
    url.value = fresh
  } catch (e) {
    if (my !== seq) return
    console.error('Failed to load image', props.src, e)
    failed.value = true
  }
}

watch(() => props.src, load, { immediate: true })

onBeforeUnmount(() => { seq++ })
</script>

<template>
  <span class="auth-image">
    <img v-if="url" :src="url" :alt="alt" />
    <span v-else-if="failed" class="ph" title="图片加载失败">!</span>
    <span v-else class="ph"></span>
  </span>
</template>

<style scoped>
.auth-image,
.auth-image img,
.ph {
  display: block;
  width: 100%;
  height: 100%;
}
.auth-image img { object-fit: cover; }
.ph {
  background: var(--color-tool);
  color: var(--color-text-muted);
  display: grid;
  place-items: center;
  font-size: 12px;
}
</style>
