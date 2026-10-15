<script setup>
import { inject, ref, computed } from 'vue'
import { NButton, NModal, NSpace, NText, useMessage } from 'naive-ui'
import { api } from '@/api/client'
import { useChatStore } from '@/stores/chat'

const chat = useChatStore()
const nMessage = useMessage()

const selectionMode = inject('selectionMode')
const selectedUserMsgIds = inject('selectedUserMsgIds')
const exitSelectionMode = inject('exitSelectionMode')

const shareCreating = ref(false)
const showManualModal = ref(false)
const shareUrl = ref('')

const selectedCount = computed(() => selectedUserMsgIds.value.size)
const maxReached = computed(() => selectedCount.value >= 10)

function handleCancel() {
  exitSelectionMode()
}

async function handleShare() {
  if (selectedCount.value < 1) return
  shareCreating.value = true
  try {
    const data = await api.post(
      `/api/conversations/${chat.conversationId}/share-messages`,
      { user_message_ids: [...selectedUserMsgIds.value] }
    )
    const url = window.location.origin + '/s/messages/' + data.token
    shareUrl.value = url

    try {
      await navigator.clipboard.writeText(url)
      nMessage.success('已复制分享链接')
    } catch {
      // 剪贴板写入失败，弹 Modal
      showManualModal.value = true
    }
    exitSelectionMode()
  } catch (e) {
    nMessage.error(e.message || '创建分享失败')
  } finally {
    shareCreating.value = false
  }
}

function copyFromModal() {
  navigator.clipboard.writeText(shareUrl.value).then(() => {
    nMessage.success('已复制分享链接')
    showManualModal.value = false
  }).catch(() => {})
}
</script>

<template>
  <div v-if="selectionMode" class="selection-action-bar">
    <div class="bar-left">
      <span class="count-label">
        已选 <strong>{{ selectedCount }}</strong> / 10 轮
      </span>
      <span v-if="maxReached" class="max-hint">已达上限</span>
    </div>
    <div class="bar-right">
      <NButton size="small" secondary @click="handleCancel">取消</NButton>
      <NButton
        size="small"
        type="primary"
        :disabled="selectedCount < 1"
        :loading="shareCreating"
        @click="handleShare"
      >
        分享
      </NButton>
    </div>
  </div>

  <!-- 手动复制 Modal -->
  <NModal v-model:show="showManualModal" title="分享链接">
    <div class="manual-copy-modal">
      <p class="modal-hint">请手动复制以下链接：</p>
      <div class="url-box">{{ shareUrl }}</div>
      <NSpace justify="end" style="margin-top: 16px;">
        <NButton @click="showManualModal = false">关闭</NButton>
        <NButton type="primary" @click="copyFromModal">复制链接</NButton>
      </NSpace>
    </div>
  </NModal>
</template>

<style scoped>
.selection-action-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 16px;
  background: var(--color-surface);
  border-bottom: 1px solid var(--color-border);
  flex-shrink: 0;
}

.bar-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.count-label {
  font-size: 14px;
  color: var(--color-text);
}

.count-label strong {
  color: var(--color-primary);
}

.max-hint {
  font-size: 12px;
  color: var(--color-warning);
}

.bar-right {
  display: flex;
  gap: 8px;
}

.manual-copy-modal {
  padding: 8px 0;
}

.modal-hint {
  color: var(--color-text-muted);
  margin: 0 0 8px;
  font-size: 13px;
}

.url-box {
  background: var(--color-bg);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 10px 12px;
  font-size: 13px;
  word-break: break-all;
  font-family: monospace;
  color: var(--color-text);
}
</style>
