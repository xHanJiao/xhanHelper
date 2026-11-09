<script setup>
import { computed, ref, inject, onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import { NDropdown, useMessage } from 'naive-ui'
import ThinkingBlock from '@/components/ThinkingBlock.vue'
import WaitingAnimation from '@/components/WaitingAnimation.vue'
import AuthImage from '@/components/media/AuthImage.vue'
import ImageLightbox from '@/components/media/ImageLightbox.vue'
import ArtifactCards from '@/components/chat/ArtifactCards.vue'
import md from '@/utils/markdown'
import { writeClipboard } from '@/utils/document'
import { useAuthStore } from '@/stores/auth'
import { useMobile } from '@/composables/useMobile'

const router = useRouter()
const auth = useAuthStore()
const { isMobile } = useMobile()
const isAdmin = computed(() => auth.user?.isAdmin === true)
const nMessage = useMessage()

const props = defineProps({
  message: { type: Object, required: true },
  isHistory: { type: Boolean, default: true },
  isSharedView: { type: Boolean, default: false },
  roundKey: { type: String, default: '' },
  // 句级朗读进度：{ sentences: string[], index: number }；只由正在念的那条消息传入
  speech: { type: Object, default: null },
})

// --- Inject selection mode state ---
const selectionMode = inject('selectionMode', ref(false))
const selectedUserMsgIds = inject('selectedUserMsgIds', ref(new Set()))
const toggleRound = inject('toggleRound', () => {})
const enterSelection = inject('enterSelectionMode', () => {})

const isUser = computed(() => props.message.role === 'user')
const isTool = computed(() => props.message.role === 'tool')
const isAssistant = computed(() => props.message.role === 'assistant')

const contentHtml = computed(() => md.render(props.message.content || ''))

// 句子切片必须拼回原文才认（否则跨端切分规则不一致时退回 markdown，别丢字）
const speechActive = computed(() => {
  const s = props.speech
  const content = props.message.content || ''
  return !!s && Array.isArray(s.sentences) && s.sentences.length > 0 && s.sentences.join('') === content
})

// 用户消息附带的图片（后端存 JSON 数组字符串；本地刚发出的消息是数组）
const imageIds = computed(() => {
  const raw = props.message.imageIds ?? props.message.image_ids
  if (!raw) return []
  if (Array.isArray(raw)) return raw
  try {
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed : []
  } catch {
    console.error('Failed to parse image_ids', props.message.id, raw)
    return []
  }
})

const previewSrc = ref('')

const timeText = computed(() => {
  if (!props.message.created_at) return ''
  return new Date(props.message.created_at).toLocaleString()
})

const isSelected = computed(() => {
  if (!selectionMode.value || !props.roundKey || isTool.value) return false
  return selectedUserMsgIds.value.has(props.roundKey)
})

const canSelect = computed(() => {
  return selectionMode.value && props.roundKey && !isTool.value
})

function handleBubbleClick(e) {
  // 正文里的内链（xhanhelper://document/<id>）走应用内跳转，不做整页刷新
  const link = e?.target?.closest?.('a[data-route]')
  if (link) {
    e.preventDefault()
    router.push(link.getAttribute('data-route'))
    return
  }
  if (canSelect.value) {
    toggleRound(props.roundKey)
  }
}

const memExpanded = ref(false)

const showCat = computed(() =>
  !props.isHistory && !contentHtml.value
)

const hasMemories = computed(() => {
  return props.message.role === 'user'
    && props.message.associatedMemories
    && props.message.associatedMemories.length > 0
})

const associatedMemories = computed(() => {
  return hasMemories.value ? props.message.associatedMemories : []
})

// --- Right-click / long-press context menu ---
const showDropdown = ref(false)
const dropX = ref(0)
const dropY = ref(0)

let longPressTimer = null

const dropdownOptions = computed(() => {
  if (selectionMode.value) {
    return [{ label: '复制内容', key: 'copy' }]
  }
  const options = [
    { label: '复制内容', key: 'copy' },
    { label: '分享…', key: 'share' },
  ]
  // 与 macOS 一致：只在用户消息上给管理员留调试入口；手机端后台不可进，不显示
  if (isUser.value && isAdmin && !props.isSharedView && !isMobile.value) {
    options.push({ label: '调试此消息', key: 'debug' })
  }
  return options
})

function openDropdown(x, y) {
  dropX.value = x
  dropY.value = y
  showDropdown.value = true
}

function onContextMenu(e) {
  if (props.isSharedView) return
  e.preventDefault()
  openDropdown(e.clientX, e.clientY)
}

function onTouchStart(e) {
  if (props.isSharedView) return
  longPressTimer = setTimeout(() => {
    const touch = e.touches[0]
    openDropdown(touch.clientX, touch.clientY)
  }, 500)
}

function onTouchEnd() {
  clearTimeout(longPressTimer)
}

function onTouchMove() {
  clearTimeout(longPressTimer)
}

function handleDropdownSelect(key) {
  showDropdown.value = false
  if (key === 'copy') {
    // 局域网 http 或权限被拒时 clipboard API 不可用，utils/document 里有 execCommand 兜底
    writeClipboard(props.message.content || '')
      .then(() => nMessage.success('已复制'))
      .catch((e) => {
        console.error('Copy message failed', e)
        nMessage.error('复制失败')
      })
  } else if (key === 'share') {
    // enter selection mode, auto-select current round
    enterSelection(props.roundKey)
  } else if (key === 'debug') {
    router.push(`/admin/message-debug/${props.message.id}`)
  }
}

function onClickOutside() {
  showDropdown.value = false
}

onBeforeUnmount(() => {
  clearTimeout(longPressTimer)
})
</script>

<template>
  <div
    class="msg-wrapper"
    :class="{ 'selected-round': isSelected, 'selectable': canSelect, 'shared-view': isSharedView }"
    :style="{ display: 'flex', marginBottom: isTool ? '0' : 'var(--space-md)' }"
    @contextmenu="onContextMenu"
    @touchstart.passive="onTouchStart"
    @touchend="onTouchEnd"
    @touchmove.passive="onTouchMove"
    @click="handleBubbleClick"
  >
    <!-- Context menu dropdown -->
    <NDropdown
      placement="bottom-start"
      trigger="manual"
      :show="showDropdown"
      :options="dropdownOptions"
      :x="dropX"
      :y="dropY"
      @select="handleDropdownSelect"
      @clickoutside="onClickOutside"
    />

    <!-- 用户消息 -->
    <div v-if="isUser" style="width: 100%; display: flex; justify-content: flex-end;">
      <div style="display: flex; flex-direction: column; align-items: flex-end; max-width: 85%;">
        <div class="user-bubble" style="border-radius: 16px 16px 4px 16px; padding: 10px 14px; font-size: var(--chat-font-size); line-height: 1.6; position: relative;">
          <div v-if="imageIds.length" class="user-images" :class="{ single: imageIds.length === 1 }">
            <button
              v-for="id in imageIds"
              :key="id"
              type="button"
              class="user-image-btn"
              @click.stop="previewSrc = `/api/images/${id}/file`"
            >
              <AuthImage :src="`/api/images/${id}/file`" />
            </button>
          </div>
          <div v-if="message.content" class="message-content" v-html="contentHtml" style="margin: 0; overflow-wrap: break-word;"></div>
        </div>
        <div v-if="hasMemories && !isSharedView" class="associated-memories">
          <div class="mem-toggle" @click="memExpanded = !memExpanded">
            <span class="mem-toggle-icon">{{ memExpanded ? '▾' : '▸' }}</span>
            关联了 {{ associatedMemories.length }} 条记忆
          </div>
          <div v-if="memExpanded" class="mem-list">
            <div v-for="mem in associatedMemories" :key="mem.id" class="mem-card">
              <div class="mem-key">{{ mem.memory_key }}</div>
              <div class="mem-value">{{ mem.memory_value }}</div>
            </div>
          </div>
        </div>
        <div v-if="timeText" class="msg-time user-time">{{ timeText }}</div>
      </div>
    </div>

    <!-- 工具结果消息 -->
    <div v-else-if="isTool" style="width: 100%; display: flex;">
      <div style="flex: 1; min-width: 0; max-width: 100%; padding: 0 var(--space-sm);">
        <div class="tool-result-line">
          <span class="tool-check">✅</span>
          <span class="tool-summary-text">{{ message.content }}</span>
        </div>
      </div>
    </div>

    <!-- AI 消息 -->
    <div v-else style="width: 100%; display: flex;">
      <div style="flex: 1; min-width: 0; max-width: 100%; border-radius: var(--radius-lg); padding: var(--space-xs) var(--space-sm);">
        <div v-if="speechActive || contentHtml" class="assistant-content" style="font-size: var(--chat-font-size); line-height: 1.7; color: var(--color-text);">
          <div v-if="speechActive" class="message-content speech-content" style="margin: 0; white-space: pre-wrap; overflow-wrap: break-word;"><span
            v-for="(sentence, i) in speech.sentences"
            :key="i"
            :class="i < speech.index ? 'speech-done' : (i === speech.index ? 'speech-now' : 'speech-todo')"
          >{{ sentence }}</span></div>
          <div v-else class="message-content" v-html="contentHtml" style="margin: 0; overflow-wrap: break-word;"></div>
        </div>
        <ArtifactCards :items="message.artifacts || []" />
        <WaitingAnimation v-if="showCat" class="bubble-cat" />
        <ThinkingBlock v-if="!isSharedView" :reasoning="message.reasoning" :done="message.reasoningDone" :is-history="isHistory" :has-content="!!(message.content)" />
        <div v-if="timeText" class="msg-time assistant-time">{{ timeText }}</div>
      </div>
    </div>

    <ImageLightbox v-if="previewSrc" :src="previewSrc" :visible="true" @close="previewSrc = ''" />
  </div>
</template>

<style scoped>
.speech-todo { opacity: 0.45; }
.speech-now { font-weight: 600; color: var(--app-accent); }
.speech-done { opacity: 1; }

.user-bubble {
  background: var(--color-user-bubble);
  color: var(--color-user-text);
  border: 0.5px solid rgba(42, 127, 88, 0.12);
}
.user-bubble :deep(p) { margin: 0; }
.user-images {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-bottom: 6px;
}
.user-images.single { display: block; }
.user-image-btn {
  width: 84px;
  height: 84px;
  padding: 0;
  border: 0;
  border-radius: 10px;
  overflow: hidden;
  cursor: zoom-in;
  background: var(--color-tool);
}
.user-images.single .user-image-btn { width: 168px; height: 168px; }
.user-image-btn:hover { opacity: 0.92; }
.user-bubble .message-content:last-child { margin-bottom: 0; }
.assistant-content :deep(p) { margin: 0; }
[data-theme="dark"] .user-bubble {
  background: var(--color-user-bubble);
  color: var(--color-user-text);
  border-color: rgba(144, 212, 168, 0.16);
}
.status-badge { margin-top: var(--space-sm); }
.msg-time {
  font-size: 11px;
  color: var(--color-text-muted);
  margin-top: 2px;
  opacity: 0;
  transition: opacity 0.2s;
}
.msg-wrapper:hover .msg-time { opacity: 1; }
.user-time { margin-right: 2px; }

.bubble-cat {
  width: 34px;
  margin-top: 4px;
}
.bubble-cat :deep(svg) {
  transform: scale(0.4);
  transform-origin: top left;
}
.assistant-time { margin-left: 2px; }

/* --- Selection mode styles --- */
.msg-wrapper {
  cursor: default;
  border-left: 3px solid transparent;
  border-radius: var(--radius-md);
  transition: background 0.15s, border-color 0.15s;
}

.msg-wrapper.selectable {
  cursor: pointer;
  border-left: 3px solid transparent;
}

.msg-wrapper.selectable:hover {
  background: var(--color-bg-hover, rgba(0,0,0,0.03));
}

.msg-wrapper.selected-round {
  border-left: 3px solid var(--color-primary);
}

[data-theme="dark"] .msg-wrapper.selectable:hover {
  background: var(--color-bg-hover, rgba(255,255,255,0.04));
}

/* Shared view — suppress all interaction hints */
.msg-wrapper.shared-view {
  cursor: default;
}
.msg-wrapper.shared-view:hover .msg-time {
  opacity: 1;
}

.associated-memories {
  margin-top: 6px;
  max-width: 280px;
}
.mem-toggle {
  font-size: 12px;
  color: var(--color-text-muted);
  cursor: pointer;
  user-select: none;
  padding: 2px 0;
}
.mem-toggle:hover {
  color: var(--color-primary);
}
.mem-toggle-icon {
  display: inline-block;
  width: 12px;
}
.mem-list {
  margin-top: 4px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.mem-card {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 6px 10px;
  text-align: left;
}
.mem-key {
  font-size: 12px;
  font-weight: 600;
  color: var(--color-text);
  margin-bottom: 2px;
}
.mem-value {
  font-size: 12px;
  color: var(--color-text-muted);
  line-height: 1.5;
}

.tool-result-line {
  display: flex;
  align-items: baseline;
  gap: 6px;
  padding: 0;
}
.tool-check {
  font-size: 11px;
  flex-shrink: 0;
}
.tool-summary-text {
  font-size: 12px;
  color: var(--color-text-muted);
  line-height: 1.5;
}
.tool-time {
  margin-left: auto;
  padding-left: 12px;
  flex-shrink: 0;
}

/* 触摸设备上禁用文本选中，避免长按弹出菜单时文字进入选中态 */
@media (hover: none) and (pointer: coarse) {
  .user-bubble {
    user-select: none;
    -webkit-user-select: none;
    -webkit-touch-callout: none;
  }
}
</style>
