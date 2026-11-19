<script setup>
import { onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/client'

// 图床私有读要 Authorization 头，<img src> 带不上，所以先 fetch 成 blob 再转 objectURL。
// 不做跨组件缓存：同一张图在一轮对话里重复出现的概率极低，省掉一套 revoke 引用计数的坑。
const props = defineProps({
  src: { type: String, required: true },
  alt: { type: String, default: '' },
  // 多图切换时先留着上一张，等新图解码好再换：否则占位尺寸会把外层容器抽塌、按钮跟着跳
  keepPrevious: { type: Boolean, default: false }
})

const url = ref('')
const failed = ref(false)
// 只有私有图床要鉴权；/images/ 是公开图，外链/blob 直接交给 <img>，
// 既不会拼错 URL，也不会把 Bearer 发给第三方
const NEEDS_AUTH = /^\/api\/images\//
let owned = false
// 快速切图时旧请求可能后回来：只认最后一次
let seq = 0

function setUrl(next, blobOwned) {
  if (owned && url.value) URL.revokeObjectURL(url.value)
  url.value = next
  owned = blobOwned
}

async function load() {
  const my = ++seq
  failed.value = false
  if (!props.keepPrevious) setUrl('', false)
  if (!props.src) {
    setUrl('', false)
    return
  }
  if (!NEEDS_AUTH.test(props.src)) {
    setUrl(props.src, false)
    return
  }
  try {
    const blob = await api.blob(props.src)
    if (my !== seq) return
    if (!blob.type.startsWith('image/')) throw new Error(`unexpected content-type: ${blob.type}`)
    setUrl(URL.createObjectURL(blob), true)
  } catch (e) {
    if (my !== seq) return
    console.error('Failed to load image', props.src, e)
    failed.value = true
    // 失败不留旧图：留着会让用户以为切到了另一张
    setUrl('', false)
  }
}

// 撤销统一交给 setUrl：这里先 revoke 会把 keepPrevious 要留住的旧图提前撤掉
watch(() => props.src, load, { immediate: true })

onBeforeUnmount(() => {
  seq++ // 卸载后回来的请求别再造孤立的 blob URL
  if (owned && url.value) URL.revokeObjectURL(url.value)
})
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
