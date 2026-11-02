<script setup>
import { NSpin } from 'naive-ui'

defineProps({
  items: { type: Array, default: () => [] },
  total: { type: Number, default: 0 },
  loading: Boolean,
  failed: { type: String, default: '' },
  activeId: { type: String, default: '' }
})

const emit = defineEmits(['select', 'retry'])
</script>

<template>
  <div class="anno-list">
    <div v-if="loading" class="anno-center"><NSpin size="small" /></div>
    <div v-else-if="failed" class="anno-center">
      <p class="anno-empty">{{ failed }}</p>
      <button type="button" class="anno-retry" @click="emit('retry')">重试</button>
    </div>
    <p v-else-if="!items.length" class="anno-empty">还没有划线批注</p>
    <template v-else>
      <button
        v-for="a in items"
        :key="a.id"
        type="button"
        class="anno-item"
        :class="{ active: a.id === activeId }"
        @click="emit('select', a)"
      >
        <span class="anno-quote">{{ a.quote }}</span>
        <span v-if="a.note" class="anno-note">{{ a.note }}</span>
        <span v-if="a.orphaned || a.quote_rewritten || a.reply_count" class="anno-foot">
          <span v-if="a.orphaned" class="badge orphan">原文已改写</span>
          <span v-else-if="a.quote_rewritten" class="badge rewrite">原文有改动</span>
          <span v-if="a.reply_count" class="reply-count">{{ a.reply_count }} 条回复</span>
        </span>
      </button>
    </template>
    <p v-if="!loading && !failed && total > items.length" class="anno-more">
      仅显示前 {{ items.length }} / {{ total }} 条
    </p>
  </div>
</template>

<style scoped>
.anno-list { display: flex; flex-direction: column; gap: 8px; padding-bottom: 16px; }
.anno-center { display: grid; place-items: center; gap: 8px; padding: 28px 0; }
.anno-empty { margin: 0; font-size: 12.5px; color: var(--color-text-muted); text-align: center; }
.anno-retry {
  border: 0.5px solid var(--color-border);
  background: transparent;
  color: var(--color-primary);
  border-radius: 999px;
  padding: 4px 12px;
  font-size: 12px;
  cursor: pointer;
}
.anno-item {
  display: flex;
  flex-direction: column;
  gap: 5px;
  width: 100%;
  padding: 10px 12px;
  border: 0.5px solid var(--color-border);
  border-left: 3px solid var(--color-primary-soft);
  border-radius: 10px;
  background: transparent;
  text-align: left;
  cursor: pointer;
}
.anno-item:hover { background: var(--color-primary-soft); }
.anno-item.active { border-left-color: var(--color-primary); background: var(--color-primary-soft); }
.anno-quote {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  overflow: hidden;
  font-size: 12.5px;
  line-height: 1.5;
  color: var(--color-text);
}
.anno-note {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 4;
  overflow: hidden;
  font-size: 12px;
  line-height: 1.5;
  color: var(--color-text-secondary);
}
.anno-foot { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
.badge {
  padding: 1px 7px;
  border-radius: 999px;
  font-size: 10.5px;
  line-height: 1.6;
}
.badge.orphan { background: var(--color-danger-soft); color: var(--color-danger); }
.badge.rewrite { background: var(--color-warning-soft); color: var(--color-warning); }
.reply-count { font-size: 10.5px; color: var(--color-text-muted); }
.anno-more { margin: 2px 0 0; font-size: 11px; color: var(--color-text-muted); text-align: center; }
</style>
