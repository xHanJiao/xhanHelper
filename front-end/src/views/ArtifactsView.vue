<script setup>
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NInput, NSpin, useMessage } from 'naive-ui'
import { api } from '@/api/client'
import PageTopBar from '@/components/PageTopBar.vue'

// 灵阅列表：单列大卡（手机）、桌面自动 2–3 列。卡片只显示标题/摘要/标签/日期，
// 正文点进去再取（列表接口不带 html）。
const router = useRouter()
const message = useMessage()

const items = ref([])
const total = ref(0)
const loading = ref(false)
const keyword = ref('')
const pageSize = 20

async function fetchList(reset = true) {
  loading.value = true
  try {
    const offset = reset ? 0 : items.value.length
    let url = `/api/artifacts?limit=${pageSize}&offset=${offset}`
    if (keyword.value.trim()) url += `&keyword=${encodeURIComponent(keyword.value.trim())}`
    const res = await api.get(url)
    const list = res.items || []
    items.value = reset ? list : [...items.value, ...list]
    total.value = res.total || 0
  } catch (e) {
    console.error('Failed to load artifacts', e)
    message.error('加载灵阅列表失败')
  } finally {
    loading.value = false
  }
}

function parseTags(tags) {
  return (tags || '').split(',').map(t => t.trim()).filter(Boolean)
}

function formatDate(value) {
  if (!value) return ''
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' })
}

onMounted(() => fetchList())
</script>

<template>
  <div class="page">
    <PageTopBar />
    <div class="scroll">
      <div class="head">
        <div class="heading">灵阅</div>
        <NInput
          v-model:value="keyword"
          size="small"
          clearable
          placeholder="搜索标题/摘要"
          class="search"
          @keyup.enter="fetchList()"
          @clear="fetchList()"
        />
      </div>

      <div v-if="loading && !items.length" class="hint"><NSpin size="small" /></div>
      <div v-else-if="!items.length" class="empty">
        <div class="empty-ico">✦</div>
        <p>还没有灵阅。在对话里让模型做一页，就会出现在这里。</p>
        <router-link to="/chat" class="btn">去对话</router-link>
      </div>

      <div v-else class="grid">
        <button
          v-for="a in items"
          :key="a.id"
          type="button"
          class="card"
          @click="router.push(`/artifacts/${a.id}`)"
        >
          <span class="title">{{ a.title || '未命名页面' }}</span>
          <span v-if="a.summary" class="summary">{{ a.summary }}</span>
          <span class="foot">
            <span class="tags">
              <span v-for="t in parseTags(a.tags)" :key="t" class="tag">{{ t }}</span>
            </span>
            <span class="date">{{ formatDate(a.created_at) }}</span>
          </span>
        </button>
      </div>

      <div v-if="items.length < total" class="more">
        <NButton size="small" :loading="loading" @click="fetchList(false)">加载更多</NButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page { height: 100%; display: flex; flex-direction: column; min-height: 0; }
.scroll { flex: 1; overflow-y: auto; padding: 0 16px 28px; max-width: 900px; width: 100%; margin: 0 auto; }
.head { display: flex; align-items: center; gap: 12px; padding: 2px 2px 14px; }
.heading { font-size: 28px; font-weight: 700; letter-spacing: -0.02em; flex: 1; }
.search { max-width: 260px; }
.hint { display: grid; place-items: center; padding: 40px 0; }
.empty { text-align: center; padding: 60px 12px; color: var(--color-text-secondary); }
.empty-ico {
  width: 44px; height: 44px; margin: 0 auto 12px; border-radius: 14px;
  display: grid; place-items: center; background: var(--color-primary-soft);
  color: var(--color-primary); font-size: 20px;
}
.empty p { font-size: 12.5px; line-height: 1.6; margin: 0 0 14px; }
.btn {
  display: inline-block; border-radius: 999px; background: var(--color-primary);
  color: #fff; padding: 7px 16px; font-size: 12.5px; text-decoration: none;
}
[data-theme="dark"] .btn { color: #10221B; }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 10px; }
.card {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 14px;
  text-align: left;
  cursor: pointer;
  border: 0.5px solid var(--color-border);
  border-radius: 12px;
  background: var(--color-surface);
  box-shadow: var(--shadow-sm);
  transition: background var(--transition-fast), transform var(--transition-fast);
}
.card:hover { background: var(--color-primary-soft); transform: translateY(-1px); }
.title { font-size: 15px; font-weight: 600; }
.summary {
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--color-text-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.foot { display: flex; align-items: center; gap: 8px; margin-top: 2px; }
.tags { display: flex; flex-wrap: wrap; gap: 4px; flex: 1; min-width: 0; }
.tag {
  font-size: 10px;
  padding: 2px 7px;
  border-radius: 99px;
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
.date { font-size: 10px; color: var(--color-text-muted); flex: 0 0 auto; }
.more { display: flex; justify-content: center; padding: 18px 0 4px; }
@media (max-width: 1099px) {
  .scroll { padding: 0 12px 20px; }
  .heading { font-size: 24px; }
  .head { flex-wrap: wrap; }
  .search { max-width: none; flex: 1 1 100%; }
  .grid { grid-template-columns: 1fr; }
}
</style>
