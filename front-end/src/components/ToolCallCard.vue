<script setup>
import { ref } from 'vue'
import { NCollapse, NCollapseItem, NSpin, NTag } from 'naive-ui'

const props = defineProps({
  toolCalls: { type: Array, default: () => [] },
  isHistory: { type: Boolean, default: false }
})

const expandedNames = ref(props.isHistory ? [] : ['toolcalls'])

const allDone = computed(() => props.toolCalls.every(tc => tc.status !== 'running'))

const runningCount = computed(() => props.toolCalls.filter(tc => tc.status === 'running').length)
const doneCount = computed(() => props.toolCalls.filter(tc => tc.status === 'done').length)
const failedCount = computed(() => props.toolCalls.filter(tc => tc.status === 'failed').length)
</script>

<script>
import { computed } from 'vue'
</script>

<template>
  <div v-if="toolCalls.length > 0" class="tool-call-section">
    <NCollapse v-model:expanded-names="expandedNames">
      <NCollapseItem name="toolcalls">
        <template #header>
          <span class="summary">
            已调用 {{ toolCalls.length }} 个工具
            <span v-if="runningCount" class="count-running">{{ runningCount }} 执行中</span>
            <span v-if="doneCount" class="count-done">{{ doneCount }} 成功</span>
            <span v-if="failedCount" class="count-failed">{{ failedCount }} 失败</span>
          </span>
        </template>
        <div v-for="(tc, idx) in toolCalls" :key="idx" class="tool-call-item">
          <div class="tool-call-header">
            <NSpin v-if="tc.status === 'running'" size="small" />
            <span class="tool-name">{{ tc.tool }}.{{ tc.function }}</span>
            <NTag
              :type="tc.status === 'done' ? 'success' : tc.status === 'failed' ? 'error' : 'info'"
              size="small"
            >
              {{ tc.status === 'running' ? '执行中...' : tc.status === 'done' ? '成功' : '失败' }}
            </NTag>
          </div>
          <div v-if="tc.result" class="tool-call-result">
            <pre>{{ JSON.stringify(tc.result, null, 2) }}</pre>
          </div>
        </div>
      </NCollapseItem>
    </NCollapse>
  </div>
</template>

<style scoped>
.tool-call-section {
  margin-top: 12px;
}
.summary {
  font-size: 14px;
  color: #666;
  display: flex;
  align-items: center;
  gap: 8px;
}
.count-running { color: #2080f0; font-size: 12px; }
.count-done { color: #18a058; font-size: 12px; }
.count-failed { color: #d03050; font-size: 12px; }

.tool-call-item {
  border: 1px solid #e8e8e8;
  border-radius: 12px;
  padding: 10px 14px;
  background: #fafafa;
  margin-bottom: 8px;
}
.tool-call-header {
  display: flex;
  align-items: center;
  gap: 8px;
}
.tool-name {
  font-weight: 600;
  font-size: 14px;
}
.tool-call-result {
  margin-top: 8px;
  font-size: 13px;
  color: #666;
  max-height: 120px;
  overflow: auto;
  background: #fff;
  border-radius: 8px;
  padding: 8px;
}
.tool-call-result pre {
  margin: 0;
  white-space: pre-wrap;
  font-family: inherit;
}
</style>
