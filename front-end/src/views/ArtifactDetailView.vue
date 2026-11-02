<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NSpin, useMessage } from 'naive-ui'
import { api } from '@/api/client'
import PageTopBar from '@/components/PageTopBar.vue'

// 灵阅详情：整页 HTML 放进 iframe，背景全透明交给纸张底。
// 安全：产物入库时已清洗（script/iframe/form 删除、on* 与外部 URL 过滤）并内嵌 CSP
// （default-src 'none'），iframe 只给 allow-same-origin，不给 allow-scripts / forms /
// top-navigation。
// 为什么不用 srcdoc + sandbox=""：产物自带的 CSS `:target` 放大层靠点 `#锚点` 跳转，
// srcdoc 里点它会导航到 about:srcdoc#… 把文档卸载成白屏；sandbox="" 的不透明源又不让
// 跳到 blob: 文档。所以内容走 blob URL、sandbox 放开 allow-same-origin —— 锚点是同文档
// 跳转，放大层就能像 macOS 客户端一样展开。
const route = useRoute()
const router = useRouter()
const message = useMessage()

const artifact = ref(null)
const frameSrc = ref('')
const loading = ref(false)
const failed = ref('')
let frameUrl = ''

const title = computed(() => artifact.value?.title || '灵阅')

function setFrame(markup) {
  clearFrame()
  if (!markup) return
  frameUrl = URL.createObjectURL(new Blob([markup], { type: 'text/html' }))
  frameSrc.value = frameUrl
}

function clearFrame() {
  if (frameUrl) URL.revokeObjectURL(frameUrl)
  frameUrl = ''
  frameSrc.value = ''
}

async function load() {
  loading.value = true
  failed.value = ''
  try {
    const res = await api.get(`/api/artifacts/${route.params.id}`)
    artifact.value = res
    setFrame(res.html || '')
  } catch (e) {
    console.error('Failed to load artifact', e)
    failed.value = e?.message || '加载失败'
  } finally {
    loading.value = false
  }
}

async function share() {
  try {
    const res = await api.post(`/api/artifacts/${route.params.id}/share`, {})
    const url = window.location.origin + (res.url || '')
    await navigator.clipboard.writeText(url)
    message.success('分享链接已复制')
  } catch (e) {
    console.error('Failed to share artifact', e)
    message.error('创建分享链接失败')
  }
}

async function remove() {
  if (!window.confirm(`删除「${title.value}」？删除后不可恢复。`)) return
  try {
    await api.delete(`/api/artifacts/${route.params.id}`)
    message.success('已删除')
    router.replace('/artifacts')
  } catch (e) {
    console.error('Failed to delete artifact', e)
    message.error('删除失败')
  }
}

onMounted(load)
watch(() => route.params.id, (id) => { if (id) load() })
onBeforeUnmount(clearFrame)
</script>

<template>
  <div class="page">
    <PageTopBar :title="title">
      <template #right>
        <NButton v-if="artifact" size="small" quaternary @click="share">分享</NButton>
        <NButton v-if="artifact" size="small" quaternary style="color: var(--color-danger)" @click="remove">删除</NButton>
      </template>
    </PageTopBar>

    <div class="body">
      <div v-if="loading" class="center"><NSpin size="small" /></div>
      <div v-else-if="failed" class="center error">
        <p>{{ failed }}</p>
        <NButton size="small" @click="load">重试</NButton>
      </div>
      <iframe
        v-else-if="frameSrc"
        class="frame"
        :src="frameSrc"
        sandbox="allow-same-origin"
        referrerpolicy="no-referrer"
        :title="title"
      ></iframe>
    </div>
  </div>
</template>

<style scoped>
.page { height: 100%; display: flex; flex-direction: column; min-height: 0; }
.body { flex: 1; min-height: 0; display: flex; padding: 0 12px 12px; }
.frame {
  flex: 1;
  width: 100%;
  border: 0.5px solid var(--color-border);
  border-radius: 12px;
  background: #fff;
  box-shadow: var(--shadow-md);
}
[data-theme="dark"] .frame { background: #FBF6EC; }
.center { flex: 1; display: grid; place-items: center; gap: 10px; }
.error p { font-size: 12.5px; color: var(--color-text-secondary); margin: 0; }
@media (max-width: 1099px) {
  .body { padding: 0 8px 8px; }
  .frame { border-radius: 10px; }
}
</style>
