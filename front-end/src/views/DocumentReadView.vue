<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { NButton, NSpin } from 'naive-ui'
import { api } from '@/api/client'
import PageTopBar from '@/components/PageTopBar.vue'
import md from '@/utils/markdown'

// 文档最小阅读页：M2 只读（目录/批注在 M3）。列宽对齐 macOS 阅读器 728px。
const route = useRoute()
const doc = ref(null)
const loading = ref(false)
const failed = ref('')

const title = computed(() => doc.value?.title || '文档')
const html = computed(() => (doc.value?.content ? md.render(doc.value.content) : ''))

async function load() {
  loading.value = true
  failed.value = ''
  try {
    doc.value = await api.get(`/api/documents/${route.params.id}`)
  } catch (e) {
    console.error('Failed to load document', e)
    failed.value = e?.message || '加载失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch(() => route.params.id, (id) => { if (id) load() })
</script>

<template>
  <div class="page">
    <PageTopBar :title="title" />
    <div class="scroll">
      <div v-if="loading" class="center"><NSpin size="small" /></div>
      <div v-else-if="failed" class="center">
        <p class="err">{{ failed }}</p>
        <NButton size="small" @click="load">重试</NButton>
      </div>
      <article v-else class="paper message-content" v-html="html"></article>
    </div>
  </div>
</template>

<style scoped>
.page { height: 100%; display: flex; flex-direction: column; min-height: 0; }
.scroll { flex: 1; overflow-y: auto; padding: 6px 16px 40px; }
.paper {
  max-width: 728px;
  margin: 0 auto;
  padding: 24px 26px 32px;
  border-radius: 14px;
  background: var(--color-surface);
  border: 0.5px solid var(--color-border);
  box-shadow: var(--shadow-sm);
  font-size: 15px;
  line-height: 1.75;
  color: var(--color-text);
}
.paper :deep(h1) { font-size: 24px; margin: 0 0 10px; }
.paper :deep(h2) { font-size: 19px; margin: 22px 0 8px; }
.paper :deep(h3) { font-size: 16px; margin: 18px 0 6px; }
.paper :deep(p) { margin: 0 0 10px; }
.paper :deep(ul),
.paper :deep(ol) { margin: 0 0 10px; padding-left: 22px; }
.paper :deep(blockquote) {
  margin: 0 0 10px;
  padding: 4px 12px;
  border-left: 3px solid var(--color-primary-soft);
  color: var(--color-text-secondary);
}
.paper :deep(a) { color: var(--color-primary); text-decoration: none; }
.paper :deep(a:hover) { text-decoration: underline; }
.center { display: grid; place-items: center; gap: 10px; padding: 60px 0; }
.err { font-size: 12.5px; color: var(--color-text-secondary); margin: 0; }
@media (max-width: 1099px) {
  .scroll { padding: 4px 10px 28px; }
  .paper { padding: 18px 16px 26px; border-radius: 12px; font-size: 16px; }
}
</style>
