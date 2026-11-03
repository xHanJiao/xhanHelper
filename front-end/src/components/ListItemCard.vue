<script setup>
import { ref, computed, watch, nextTick, onMounted, onBeforeUnmount } from 'vue'
import { useMobile } from '@/composables/useMobile'
import MarkdownIt from 'markdown-it'
import {
  NCard,
  NButton,
  NDropdown,
  NSpace,
  NPopconfirm,
  NTag,
  NModal
} from 'naive-ui'

const md = new MarkdownIt({
  html: false,
  breaks: true,
  linkify: true
})

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
  },
  contentType: {
    type: String,
    default: 'text'
  }
})

const renderedContent = computed(() => {
  if (props.contentType === 'markdown') {
    return md.render(props.content || '')
  }
  return ''
})

const { isMobile } = useMobile()

const confirmModalVisible = ref(false)
const pendingConfirmAction = ref(null)
const expanded = ref(false)

// 内容是否真的被 max-height 裁掉了：只有真溢出才加底部淡出。
// 光靠 content.length 判断会误伤，mask 淡出在「没超长」时看着就是丢字。
const wrapperEl = ref(null)
const overflowing = ref(false)
let resizeObserver = null

function measureOverflow() {
  const el = wrapperEl.value
  if (el) overflowing.value = el.scrollHeight > el.clientHeight + 1
}

onMounted(() => {
  measureOverflow()
  if (!wrapperEl.value) return
  resizeObserver = new ResizeObserver(measureOverflow)
  resizeObserver.observe(wrapperEl.value)
})

onBeforeUnmount(() => resizeObserver?.disconnect())

watch(() => props.content, () => nextTick(measureOverflow))

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

function handleContentClick() {
  if (props.content.length <= 120) return
  // 展开后内容可能很长，手机上不必再划到底部找「收起」，点正文即收起
  if (isMobile.value || !expanded.value) expanded.value = !expanded.value
}
</script>

<template>
  <NCard :hoverable="hoverable" class="list-item-card" size="small">
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

    <div
      v-if="content"
      ref="wrapperEl"
      class="card-content-wrapper"
      :class="{ 'clickable': content.length > 120 && (!expanded || isMobile), fading: overflowing && !expanded }"
      :style="{ maxHeight: expanded ? 'none' : maxContentHeight }"
      @click="handleContentClick"
    >
      <div v-if="contentType === 'markdown'" class="card-content markdown-body" v-html="renderedContent"></div>
      <div v-else class="card-content">{{ content }}</div>
    </div>

    <div v-if="tags && tags.length > 0" class="card-tags">
      <NTag v-for="tag in tags" :key="tag.text || tag" size="small" :type="tag.type || 'default'" class="card-tag">
        {{ tag.text || tag }}
      </NTag>
    </div>

    <div class="card-footer">
      <span v-if="metadata" class="card-metadata">
        {{ metadata }}
      </span>
      <NSpace v-if="primaryActions.length > 0 || (content.length > 120 && expanded)" size="small">
        <NButton
          v-if="content.length > 120 && expanded"
          size="tiny"
          quaternary
          @click="expanded = false"
        >
          收起
        </NButton>
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
  background: var(--color-surface);
  transition: transform var(--transition-normal),
              box-shadow var(--transition-normal),
              border-color var(--transition-normal);
}

.list-item-card:hover {
  transform: translateY(-2px);
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
}

.card-title {
  font-weight: 600;
  font-size: var(--font-size-md);
  color: var(--color-text);
  letter-spacing: -0.01em;
}

.desktop-actions {
  opacity: 0;
  transition: opacity var(--transition-fast);
}

.list-item-card:hover .desktop-actions {
  opacity: 1;
}

.card-content-wrapper {
  position: relative;
  overflow: hidden;
}

.card-content-wrapper.clickable {
  cursor: pointer;
}

.card-content {
  font-size: 14px;
  color: var(--color-text-secondary);
  line-height: 1.7;
  white-space: pre-wrap;
}

/* 内容被裁断时让文字自己淡出（mask），不靠覆盖一层白雾：
   卡片是半透明的，覆盖色永远对不准底色，深色主题更对不上。
   只在实测溢出时加（见 measureOverflow），否则短内容最后两行会凭空消失。 */
.card-content-wrapper.fading {
  -webkit-mask-image: linear-gradient(to bottom, #000 0, #000 calc(100% - 88px), transparent 100%);
  mask-image: linear-gradient(to bottom, #000 0, #000 calc(100% - 88px), transparent 100%);
}

.card-tags {
  margin-top: 12px;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.card-tag {
  border: none !important;
  background: var(--color-bg) !important;
  color: var(--color-text-secondary) !important;
  font-size: 11px;
  font-weight: 500;
  letter-spacing: 0.02em;
  padding: 4px 10px;
  height: auto;
  border-radius: 999px;
}

.card-footer {
  margin-top: 16px;
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.card-metadata {
  font-size: 12px;
  color: var(--color-text-muted);
  font-variant-numeric: tabular-nums;
}

/* Markdown body styles */
.markdown-body {
  white-space: normal;
  line-height: 1.8;
}

.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3) {
  margin-top: 0.8em;
  margin-bottom: 0.4em;
  font-weight: 600;
  color: var(--color-text);
}

.markdown-body :deep(h1) { font-size: 1.4em; }
.markdown-body :deep(h2) { font-size: 1.2em; }
.markdown-body :deep(h3) { font-size: 1.05em; }

.markdown-body :deep(p) {
  margin: 0.4em 0;
}

.markdown-body :deep(ul),
.markdown-body :deep(ol) {
  padding-left: 1.5em;
  margin: 0.4em 0;
}

.markdown-body :deep(code) {
  background: var(--color-bg);
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 0.9em;
}

.markdown-body :deep(pre) {
  background: var(--color-bg);
  padding: 12px;
  border-radius: 8px;
  overflow-x: auto;
}

.markdown-body :deep(pre code) {
  background: none;
  padding: 0;
}

.markdown-body :deep(blockquote) {
  margin: 0.5em 0;
  padding-left: 1em;
  border-left: 3px solid var(--color-primary);
  color: var(--color-text-muted);
}

.markdown-body :deep(table) {
  border-collapse: collapse;
  margin: 0.5em 0;
  width: 100%;
}

.markdown-body :deep(th),
.markdown-body :deep(td) {
  border: 1px solid var(--color-border);
  padding: 6px 10px;
  text-align: left;
}

.markdown-body :deep(th) {
  background: var(--color-bg);
  font-weight: 600;
}

.markdown-body :deep(img) {
  max-width: 100%;
  height: auto;
  border-radius: 6px;
}
</style>
