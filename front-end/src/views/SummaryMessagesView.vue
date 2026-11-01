<script setup>
import { ref, onMounted, computed } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import { useMobile } from '@/composables/useMobile'
import { useScrollTopBar } from '@/composables/useScrollTopBar'
import { useDevice } from '@/composables/useDevice'
import { NTag, NEmpty, NSpin, useMessage } from 'naive-ui'
import MessageBubble from '@/components/MessageBubble.vue'
import PageTopBar from '@/components/PageTopBar.vue'

const route = useRoute()
const message = useMessage()
const { isMobile } = useMobile()
const { isIOS } = useDevice()

const summary = ref(null)
const messages = ref([])
const loading = ref(false)
const contentContainerRef = ref(null)
const { isBarVisible, showBar } = useScrollTopBar(contentContainerRef)
const summaryId = computed(() => route.params.id)

function parseTopics(topics) {
  if (!topics) return []
  return topics.split(',').map(t => t.trim()).filter(Boolean)
}

function statusText(status) {
  if (status === 'superseded') return '已替代'
  return status
}

function formatDate(d) {
  if (!d) return ''
  return new Date(d).toLocaleString()
}

function handleContainerClick(e) {
  if (!isIOS) return

  const container = contentContainerRef.value
  if (!container) return

  const rect = container.getBoundingClientRect()
  const clickY = e.clientY - rect.top
  const topThird = rect.height / 3

  if (clickY <= topThird) {
    showBar()
  }
}

async function fetchSummaryMessages() {
  loading.value = true
  try {
    const res = await api.get(`/api/summaries/${summaryId.value}/messages`)
    summary.value = res.summary
    messages.value = res.messages || []
  } catch (e) {
    message.error('加载摘要消息失败')
  } finally {
    loading.value = false
  }
}

onMounted(fetchSummaryMessages)
</script>

<template>
  <div style="height: 100%; display: flex; flex-direction: column; background: transparent;">
    <PageTopBar title="摘要详情" :visible="!isMobile || isBarVisible" />

    <div
      ref="contentContainerRef"
      @touchstart="isMobile ? showBar() : null"
      @click="handleContainerClick"
      style="flex: 1; overflow-y: auto;"
    >
      <NSpin :show="loading">
        <div v-if="!loading && summary" style="padding: var(--space-lg);">
          <div style="margin-bottom: var(--space-lg); padding: var(--space-lg); background: var(--color-bg-card); border-radius: var(--radius-lg);">
            <div style="font-size: 14px; font-weight: 600; margin-bottom: var(--space-sm);">
              摘要内容
              <NTag v-if="summary.status !== 'active'" size="small" type="warning" :bordered="false" style="margin-left: var(--space-sm);">
                {{ statusText(summary.status) }}
              </NTag>
            </div>
            <div style="color: var(--color-text-secondary); font-size: 13px; line-height: 1.6; white-space: pre-wrap; word-break: break-word;">
              {{ summary.summary_text }}
            </div>
            <div v-if="summary.topics" style="margin-top: var(--space-sm); display: flex; gap: 4px; flex-wrap: wrap;">
              <NTag v-for="t in parseTopics(summary.topics)" :key="t" size="small" :bordered="false">
                {{ t }}
              </NTag>
            </div>
            <div style="margin-top: var(--space-sm); color: var(--color-text-muted); font-size: 12px;">
              消息范围：{{ formatDate(summary.created_at) }}
            </div>
          </div>

          <div style="margin-bottom: var(--space-md); font-size: 13px; color: var(--color-text-muted); text-align: center; padding: var(--space-sm);">
            — 以下为被摘要覆盖的对话消息 —
          </div>

          <div v-if="messages.length > 0" style="max-width: 900px; margin: 0 auto;">
            <MessageBubble
              v-for="msg in messages"
              :key="msg.id"
              :message="msg"
              :is-history="true"
            />
          </div>
          <NEmpty v-else description="没有关联消息" />
        </div>
      </NSpin>
    </div>
  </div>
</template>
