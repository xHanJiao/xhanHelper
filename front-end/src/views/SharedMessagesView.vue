<script setup>
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { NSpin, NCard } from 'naive-ui'
import { api } from '@/api/client'
import MessageBubble from '@/components/MessageBubble.vue'

const route = useRoute()
const token = route.params.token

const loading = ref(true)
const error = ref(null)
const messages = ref([])
const expiresAt = ref('')

onMounted(async () => {
  try {
    const data = await api.publicGet('/api/share/messages/' + token)
    messages.value = data.messages || []
    expiresAt.value = data.expires_at || ''
  } catch (e) {
    if (e.status === 410) {
      error.value = '分享链接已过期（有效期7天）'
    } else {
      error.value = '该分享不存在或已被删除'
    }
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="shared-page">
    <div class="shared-container">
      <NSpin :show="loading">
        <!-- Error state -->
        <NCard v-if="error" class="shared-error-card">
          <div class="shared-error">
            <h2>无法查看此分享</h2>
            <p>{{ error }}</p>
          </div>
        </NCard>

        <!-- Messages -->
        <div v-if="messages.length > 0" class="shared-messages">
          <div class="share-meta" v-if="expiresAt">
            分享链接 · {{ new Date(expiresAt).toLocaleDateString() }} 前有效
          </div>
          <MessageBubble
            v-for="msg in messages"
            :key="msg.id"
            :message="msg"
            :is-history="true"
            :is-shared-view="true"
          />
        </div>
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

.shared-messages {
  display: flex;
  flex-direction: column;
}

.share-meta {
  text-align: center;
  font-size: 12px;
  color: var(--color-text-muted);
  margin-bottom: var(--space-lg);
  padding-bottom: var(--space-md);
  border-bottom: 1px solid var(--color-border);
}
</style>
