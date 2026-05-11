<script setup>
import { ref, computed } from 'vue'
import { NTag } from 'naive-ui'

const props = defineProps({
  toolCalls: { type: Array, default: () => [] },
  isHistory: { type: Boolean, default: false }
})

const showJson = ref({})

function toggleJson(idx) {
  showJson.value[idx] = !showJson.value[idx]
}

const runningCount = computed(() => props.toolCalls.filter(tc => tc.status === 'running').length)
const doneCount = computed(() => props.toolCalls.filter(tc => tc.status === 'done').length)
const failedCount = computed(() => props.toolCalls.filter(tc => tc.status === 'failed').length)
</script>

<template>
  <div v-if="toolCalls.length > 0" class="tool-call-section">
    <div class="tool-summary" @click="toggleJson(-1)">
      <span class="summary-text">
        调用 {{ doneCount + failedCount }}/{{ toolCalls.length }} 个工具
        <span v-if="failedCount" class="count-failed">{{ failedCount }} 失败</span>
      </span>
      <span class="summary-arrow">{{ showJson[-1] ? '▾' : '▸' }}</span>
    </div>
    <div v-if="showJson[-1]" class="tool-detail-list">
      <div v-for="(tc, idx) in toolCalls" :key="idx" class="tool-item">
        <div class="tool-item-header" @click="toggleJson(idx)">
          <NTag
            :type="tc.status === 'done' ? 'success' : tc.status === 'failed' ? 'error' : 'info'"
            size="tiny"
          >
            {{ tc.status === 'running' ? '执行中' : tc.status === 'done' ? '✓' : '✗' }}
          </NTag>
          <span class="tool-func">{{ tc.tool }}.{{ tc.function }}</span>
        </div>
        <div v-if="tc.result && showJson[idx]" class="tool-json">
          <pre>{{ JSON.stringify(tc.result, null, 2) }}</pre>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.tool-call-section {
  margin-top: var(--space-md);
}
.tool-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  cursor: pointer;
  font-size: 12px;
  color: var(--color-text-muted);
  padding: var(--space-xs) 0;
  user-select: none;
}
.tool-summary:hover {
  color: var(--color-text-secondary);
}
.summary-text {
  display: flex;
  align-items: center;
  gap: var(--space-xs);
}
.summary-arrow {
  font-size: 14px;
}
.count-failed {
  color: #d03050;
}

.tool-detail-list {
  padding-left: 4px;
}
.tool-item {
  margin-bottom: 2px;
}
.tool-item-header {
  display: flex;
  align-items: center;
  gap: var(--space-xs);
  cursor: pointer;
  padding: 2px 0;
  font-size: 13px;
}
.tool-func {
  color: var(--color-text-secondary);
  font-family: monospace;
}
.tool-json {
  margin-left: 28px;
  margin-bottom: var(--space-xs);
}
.tool-json pre {
  margin: 0;
  font-size: 11px;
  color: var(--color-text-muted);
  white-space: pre-wrap;
  background: var(--color-bg);
  padding: var(--space-xs) var(--space-sm);
  border-radius: var(--radius-sm);
  max-height: 100px;
  overflow: auto;
}
</style>
