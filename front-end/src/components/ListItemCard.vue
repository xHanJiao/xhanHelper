<script setup>
import { ref, computed } from 'vue'
import { useMobile } from '@/composables/useMobile'
import {
  NCard,
  NButton,
  NDropdown,
  NSpace,
  NPopconfirm,
  NTag,
  NModal
} from 'naive-ui'

const props = defineProps({
  title: {
    type: String,
    default: ''
  },
  content: {
    type: String,
    default: ''
  },
  tags: {
    type: Array,
    default: () => []
  },
  metadata: {
    type: String,
    default: ''
  },
  hoverable: {
    type: Boolean,
    default: true
  },
  actions: {
    type: Array,
    default: () => []
  },
  primaryActions: {
    type: Array,
    default: () => []
  },
  maxContentHeight: {
    type: String,
    default: '120px'
  }
})

const { isMobile } = useMobile()

const confirmModalVisible = ref(false)
const pendingConfirmAction = ref(null)

function handleActionClick(action) {
  if (action.confirm) {
    pendingConfirmAction.value = action
    confirmModalVisible.value = true
  } else {
    action.onClick?.()
  }
}

function handleConfirmOk() {
  if (pendingConfirmAction.value) {
    pendingConfirmAction.value.onClick?.()
  }
  confirmModalVisible.value = false
  pendingConfirmAction.value = null
}

function handleConfirmCancel() {
  confirmModalVisible.value = false
  pendingConfirmAction.value = null
}

const dropdownOptions = computed(() => {
  return props.actions.map(action => ({
    label: action.label,
    key: action.key
  }))
})

function handleDropdownSelect(key) {
  const action = props.actions.find(a => a.key === key)
  if (action) {
    handleActionClick(action)
  }
}
</script>

<template>
  <NCard size="small" :hoverable="hoverable" class="list-item-card">
    <template #header>
      <div class="card-header">
        <span class="card-title">{{ title }}</span>

        <NSpace v-if="!isMobile && actions.length > 0" class="desktop-actions">
          <template v-for="action in actions" :key="action.key">
            <NPopconfirm
              v-if="action.confirm"
              :positive-text="action.confirmPositiveText || '确定'"
              :negative-text="action.confirmNegativeText || '取消'"
              @positive-click="action.onClick"
            >
              <template #trigger>
                <NButton
                  size="tiny"
                  quaternary
                  :type="action.type || 'default'"
                >
                  {{ action.label }}
                </NButton>
              </template>
              {{ action.confirm }}
            </NPopconfirm>

            <NButton
              v-else
              size="tiny"
              quaternary
              :type="action.type || 'default'"
              @click="action.onClick"
            >
              {{ action.label }}
            </NButton>
          </template>
        </NSpace>

        <NDropdown
          v-if="isMobile && actions.length > 0"
          :options="dropdownOptions"
          @select="handleDropdownSelect"
        >
          <NButton size="tiny" quaternary>
            ⋯
          </NButton>
        </NDropdown>
      </div>
    </template>

    <div v-if="content" class="card-content" :style="{ maxHeight: maxContentHeight }">
      {{ content }}
    </div>

    <div v-if="tags && tags.length > 0" class="card-tags">
      <NTag v-for="tag in tags" :key="tag" size="small" style="margin-right: 4px; margin-bottom: 4px;">
        {{ tag }}
      </NTag>
    </div>

    <div class="card-footer">
      <span v-if="metadata" class="card-metadata">
        {{ metadata }}
      </span>
      <NSpace v-if="primaryActions.length > 0" size="small">
        <template v-for="action in primaryActions" :key="action.key">
          <NPopconfirm
            v-if="action.confirm"
            :positive-text="action.confirmPositiveText || '确定'"
            :negative-text="action.confirmNegativeText || '取消'"
            @positive-click="action.onClick"
          >
            <template #trigger>
              <NButton
                size="tiny"
                :type="action.type || 'default'"
                :quaternary="action.type !== 'primary'"
              >
                {{ action.label }}
              </NButton>
            </template>
            {{ action.confirm }}
          </NPopconfirm>

          <NButton
            v-else
            size="tiny"
            :type="action.type || 'default'"
            :quaternary="action.type !== 'primary'"
            @click="action.onClick"
          >
            {{ action.label }}
          </NButton>
        </template>
      </NSpace>
    </div>
  </NCard>

  <NModal
    v-model:show="confirmModalVisible"
    preset="dialog"
    title="确认"
    :positive-text="pendingConfirmAction?.confirmPositiveText || '确定'"
    :negative-text="pendingConfirmAction?.confirmNegativeText || '取消'"
    @positive-click="handleConfirmOk"
    @negative-click="handleConfirmCancel"
  >
    {{ pendingConfirmAction?.confirm }}
  </NModal>
</template>

<style scoped>
.list-item-card {
  height: 100%;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
}

.card-title {
  font-weight: 500;
  font-size: var(--font-size-md);
  color: var(--color-text);
}

.desktop-actions {
  opacity: 0;
  transition: opacity 0.2s ease;
}

.list-item-card:hover .desktop-actions {
  opacity: 1;
}

.card-content {
  font-size: 14px;
  color: var(--color-text-secondary);
  line-height: 1.6;
  white-space: pre-wrap;
  overflow: hidden;
}

.card-tags {
  margin-top: var(--space-sm);
  display: flex;
  flex-wrap: wrap;
}

.card-footer {
  margin-top: var(--space-md);
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.card-metadata {
  font-size: 12px;
  color: var(--color-text-muted);
}
</style>
