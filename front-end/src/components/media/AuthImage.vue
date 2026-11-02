<script setup>
import { onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/client'

// 图床私有读要 Authorization 头，<img src> 带不上，所以先 fetch 成 blob 再转 objectURL。
// 不做跨组件缓存：同一张图在一轮对话里重复出现的概率极低，省掉一套 revoke 引用计数的坑。
const props = defineProps({
  src: { type: String, required: true },
  alt: { type: String, default: '' }
})

const url = ref('')
const failed = ref(false)
// 只有私有图床要鉴权；/images/ 是公开图，外链/blob 直接交给 <img>，
// 既不会拼错 URL，也不会把 Bearer 发给第三方
const NEEDS_AUTH = /^\/api\/images\//
let owned = false

async function load() {
  url.value = ''
  failed.value = false
  owned = false
  if (!props.src) return
  if (!NEEDS_AUTH.test(props.src)) {
    url.value = props.src
    return
  }
  try {
    const blob = await api.blob(props.src)
    if (!blob.type.startsWith('image/')) throw new Error(`unexpected content-type: ${blob.type}`)
    url.value = URL.createObjectURL(blob)
    owned = true
  } catch (e) {
    console.error('Failed to load image', props.src, e)
    failed.value = true
  }
}

watch(() => props.src, () => {
  if (owned && url.value) URL.revokeObjectURL(url.value)
  load()
}, { immediate: true })

onBeforeUnmount(() => { if (owned && url.value) URL.revokeObjectURL(url.value) })
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
