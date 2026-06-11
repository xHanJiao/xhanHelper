<script setup>
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import MarkdownIt from 'markdown-it'
import { NSpin, NCard, NDivider } from 'naive-ui'
import { api } from '@/api/client'

const route = useRoute()
const token = route.params.token

const loading = ref(true)
const error = ref(null)
const doc = ref(null)

const md = new MarkdownIt()

function parseTags(tags) {
  if (!tags) return []
  return tags.split(',').map(t => t.trim()).filter(Boolean)
}

onMounted(async () => {
  try {
    const data = await api.publicGet('/s/' + token)
    doc.value = data
  } catch (e) {
    error.value = e.message || '加载失败'
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="shared-page">
    <div class="shared-container">
      <NSpin :show="loading">
        <NCard v-if="error" class="shared-error-card">
          <div class="shared-error">
            <h2>无法查看此文档</h2>
            <p>{{ error }}</p>
          </div>
        </NCard>

        <NCard v-if="doc" class="shared-doc-card">
          <div class="doc-header">
            <h1>{{ doc.title }}</h1>
            <div v-if="parseTags(doc.tags).length > 0" class="doc-tags">
              <span v-for="tag in parseTags(doc.tags)" :key="tag" class="doc-tag">{{ tag }}</span>
            </div>
          </div>

          <NDivider />

          <article class="doc-content markdown-body" v-html="md.render(doc.content)"></article>

          <NDivider />

          <div class="doc-footer">
            <span>创建于 {{ new Date(doc.created_at).toLocaleDateString() }}</span>
          </div>
        </NCard>
      </NSpin>
    </div>
  </div>
</template>

<style scoped>
.shared-page {
  min-height: 100vh;
  overflow-y: auto;
  background: var(--color-bg-page);
}

.shared-container {
  max-width: 820px;
  margin: 0 auto;
  padding: var(--space-xl) var(--space-lg);
}

.shared-error-card {
  text-align: center;
}

.shared-error h2 {
  font-size: var(--font-size-lg);
  font-weight: 600;
  color: var(--color-text);
  margin: 0 0 var(--space-md);
}

.shared-error p {
  color: var(--color-text-muted);
  margin: 0;
}

.doc-header h1 {
  font-size: var(--font-size-lg);
  font-weight: 600;
  color: var(--color-text);
  margin: 0;
  letter-spacing: -0.01em;
}

.doc-tags {
  margin-top: 12px;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.doc-tag {
  border: none;
  background: var(--color-bg);
  color: var(--color-text-secondary);
  font-size: var(--font-size-sm);
  font-weight: 500;
  letter-spacing: 0.02em;
  padding: 4px 10px;
  border-radius: 999px;
}

/* ----- Markdown content (mirror of ListItemCard global styles) ----- */

.doc-content {
  white-space: normal;
  line-height: 1.8;
  color: var(--color-text-secondary);
}

.doc-content :deep(h1),
.doc-content :deep(h2),
.doc-content :deep(h3) {
  margin-top: 0.8em;
  margin-bottom: 0.4em;
  font-weight: 600;
  color: var(--color-text);
}

.doc-content :deep(h1) { font-size: 1.4em; }
.doc-content :deep(h2) { font-size: 1.2em; }
.doc-content :deep(h3) { font-size: 1.05em; }

.doc-content :deep(p) {
  margin: 0.4em 0;
}

.doc-content :deep(ul),
.doc-content :deep(ol) {
  padding-left: 1.5em;
  margin: 0.4em 0;
}

.doc-content :deep(code) {
  background: var(--color-bg);
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 0.9em;
}

.doc-content :deep(pre) {
  background: var(--color-bg);
  padding: 12px;
  border-radius: 8px;
  overflow-x: auto;
}

.doc-content :deep(pre code) {
  background: none;
  padding: 0;
}

.doc-content :deep(blockquote) {
  margin: 0.5em 0;
  padding-left: 1em;
  border-left: 3px solid var(--color-primary);
  color: var(--color-text-muted);
}

.doc-content :deep(table) {
  border-collapse: collapse;
  margin: 0.5em 0;
  width: 100%;
}

.doc-content :deep(th),
.doc-content :deep(td) {
  border: 1px solid var(--color-border);
  padding: 6px 10px;
  text-align: left;
}

.doc-content :deep(th) {
  background: var(--color-bg);
  font-weight: 600;
}

/* ----- Footer metadata ----- */

.doc-footer {
  font-size: 12px;
  color: var(--color-text-muted);
  font-variant-numeric: tabular-nums;
}
</style>
