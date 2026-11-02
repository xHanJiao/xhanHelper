<script setup>
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { NPopover } from 'naive-ui'
import { useMobile } from '@/composables/useMobile'
import { useGoBack } from '@/composables/useGoBack'

const props = defineProps({
  title: { type: String, required: true },
  searchable: { type: Boolean, default: false },
  keyword: { type: String, default: '' },
  searchPlaceholder: { type: String, default: '搜索' },
  filterable: { type: Boolean, default: false },
  filterCount: { type: Number, default: 0 }
})

// clear = 清面板草稿；reset = 清已生效的筛选（胶囊行上的「清除」）
const emit = defineEmits(['update:keyword', 'search', 'apply', 'clear', 'reset', 'open-filter'])

const route = useRoute()
const { isMobile, isDesktop } = useMobile()
const goBack = useGoBack()

// 手机顶部 Tab 页不重复给返回；桌面没有底部 Tab，除聊天外一律显示
const showBack = computed(() => isDesktop.value || route.meta.topLevel !== true)

const searchOpen = ref(false)
const searchInput = ref(null)
const filterOpen = ref(false)
const sheetEl = ref(null)
const filterBtn = ref(null)

function openSearch() {
  searchOpen.value = true
  nextTick(() => searchInput.value?.focus())
}

function onInput(e) {
  emit('update:keyword', e.target.value)
}

function runSearch() {
  emit('search')
}

function clearKeyword() {
  emit('update:keyword', '')
  emit('search')
}

function openFilter() {
  emit('open-filter')
  filterOpen.value = true
}

function clearFilters() {
  emit('clear')
}

function applyFilters() {
  emit('apply')
  filterOpen.value = false
}

// 手机底部面板是模态：Esc 关闭 + 锁背景滚动（桌面 popover 不锁）
const sheetOpen = computed(() => isMobile.value && filterOpen.value)
function onKeydown(e) {
  if (e.key === 'Escape') filterOpen.value = false
}
// shortcut: 只把焦点移进面板、关闭时还原，不做完整的 Tab 焦点环 trap（面板里最多两个控件）
watch(sheetOpen, async (open) => {
  if (open) {
    window.addEventListener('keydown', onKeydown)
    document.body.style.overflow = 'hidden'
    await nextTick()
    sheetEl.value?.focus()
    return
  }
  window.removeEventListener('keydown', onKeydown)
  document.body.style.overflow = ''
  filterBtn.value?.focus({ preventScroll: true })
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  if (sheetOpen.value) document.body.style.overflow = ''
})

// 选择态等把 filterable 关掉时，面板要跟着卸载，否则退出后自己弹回来
watch(() => props.filterable, (v) => {
  if (!v) filterOpen.value = false
})
</script>

<template>
  <div class="ph">
    <div class="ph-row">
      <button v-if="showBack" class="ph-back" type="button" @click="goBack">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M15 6l-6 6 6 6"/></svg>
        <span>返回</span>
      </button>

      <h1 class="ph-title page-heading">{{ title }}</h1>

      <div class="ph-actions">
        <button
          v-if="searchable && !searchOpen"
          class="ph-icon"
          type="button"
          aria-label="搜索"
          @click="openSearch"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="11" cy="11" r="6.5"/><path d="M16 16l4.5 4.5"/></svg>
        </button>

        <NPopover
          v-if="filterable && isDesktop"
          trigger="click"
          placement="bottom-end"
          :show="filterOpen"
          @update:show="v => filterOpen = v"
        >
          <template #trigger>
            <button class="ph-icon" type="button" aria-label="筛选" @click="openFilter">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M4 6h16M7 12h10M10 18h4"/></svg>
              <span v-if="filterCount" class="ph-badge">{{ filterCount }}</span>
            </button>
          </template>
          <div class="ph-pop">
            <div class="ph-sheet-head">
              <b>筛选</b>
              <button type="button" @click="clearFilters">清除</button>
            </div>
            <div class="ph-filter-body"><slot name="filters"></slot></div>
            <button class="ph-apply" type="button" @click="applyFilters">查看结果</button>
          </div>
        </NPopover>

        <button
          v-else-if="filterable"
          ref="filterBtn"
          class="ph-icon"
          type="button"
          aria-label="筛选"
          @click="openFilter"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M4 6h16M7 12h10M10 18h4"/></svg>
          <span v-if="filterCount" class="ph-badge">{{ filterCount }}</span>
        </button>

        <slot name="actions"></slot>
      </div>
    </div>

    <div v-if="searchable && searchOpen" class="ph-search-row">
      <div class="ph-search">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="11" cy="11" r="6.5"/><path d="M16 16l4.5 4.5"/></svg>
        <input
          ref="searchInput"
          :value="keyword"
          type="search"
          enterkeyhint="search"
          :placeholder="searchPlaceholder"
          @input="onInput"
          @keyup.enter="runSearch"
        />
        <button v-if="keyword" class="ph-clear-input" type="button" aria-label="清空" @click="clearKeyword">×</button>
      </div>
      <button class="page-action" type="button" @click="searchOpen = false">取消</button>
    </div>

    <div v-if="(keyword && !searchOpen) || filterCount" class="ph-pills">
      <span v-if="keyword && !searchOpen" class="filter-pill">
        搜索：{{ keyword }}
        <button type="button" aria-label="清空搜索" @click="clearKeyword">×</button>
      </span>
      <slot name="pills"></slot>
      <button v-if="filterCount" class="ph-clear-all" type="button" @click="emit('reset')">清除</button>
    </div>

    <Teleport to="body">
      <div v-if="isMobile && filterOpen" class="ph-scrim" @click="filterOpen = false"></div>
      <div
        v-if="isMobile && filterOpen"
        ref="sheetEl"
        class="ph-sheet"
        role="dialog"
        aria-modal="true"
        aria-label="筛选"
        tabindex="-1"
      >
        <div class="ph-handle"></div>
        <div class="ph-sheet-head">
          <b>筛选</b>
          <button type="button" @click="clearFilters">清除</button>
        </div>
        <div class="ph-filter-body"><slot name="filters"></slot></div>
        <button class="ph-apply" type="button" @click="applyFilters">查看结果</button>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.ph { padding: 0 2px 14px; }
/* 桌面：标题/返回跟着滚动区吸顶。手机不吸顶（键盘与安全区判断复杂，且留白本就紧张） */
@media (min-width: 1100px) {
  .ph {
    position: sticky;
    top: 0;
    z-index: 5;
    background: color-mix(in srgb, var(--color-bg) 86%, transparent);
    -webkit-backdrop-filter: blur(12px);
    backdrop-filter: blur(12px);
  }
}
.ph-row { display: flex; align-items: center; gap: 8px; }
.ph-back {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
  border: 0;
  background: transparent;
  color: var(--color-text-secondary);
  font-size: 12.5px;
  padding: 4px 2px;
  cursor: pointer;
  white-space: nowrap;
}
.ph-back svg { width: 15px; height: 15px; }
.ph-back:hover { color: var(--color-primary); }
.ph-title { flex: 1; min-width: 0; margin: 0; padding: 0; }
.ph-actions { display: flex; align-items: center; gap: 6px; flex: 0 0 auto; }
.ph-icon {
  position: relative;
  width: 32px;
  height: 32px;
  border-radius: 999px;
  border: 0.5px solid var(--color-border);
  background: rgba(255, 255, 255, 0.24);
  color: var(--color-text-secondary);
  display: grid;
  place-items: center;
  padding: 0;
  cursor: pointer;
  flex: 0 0 auto;
}
[data-theme="dark"] .ph-icon { background: rgba(255, 255, 255, 0.04); }
.ph-icon:hover { background: var(--color-primary-soft); color: var(--color-primary); }
.ph-icon svg { width: 16px; height: 16px; }
.ph-badge {
  position: absolute;
  top: -3px;
  right: -3px;
  min-width: 15px;
  height: 15px;
  padding: 0 4px;
  border-radius: 999px;
  background: var(--color-primary);
  color: #fff;
  font-size: 9px;
  font-weight: 700;
  display: grid;
  place-items: center;
}
[data-theme="dark"] .ph-badge { color: #10221B; }
.ph-search-row { display: flex; align-items: center; gap: 8px; padding-top: 12px; }
.ph-search {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  height: 38px;
  padding: 0 10px;
  border-radius: 10px;
  background: var(--color-input);
  border: 0.5px solid var(--color-border);
  color: var(--color-text-muted);
}
.ph-search svg { width: 15px; height: 15px; flex: 0 0 auto; }
.ph-search input {
  flex: 1;
  min-width: 0;
  border: 0;
  outline: 0;
  background: transparent;
  font: inherit;
  font-size: 13px;
  color: var(--color-text);
}
.ph-search input::-webkit-search-cancel-button { display: none; }
.ph-clear-input {
  border: 0;
  background: transparent;
  color: var(--color-text-muted);
  font-size: 17px;
  line-height: 1;
  padding: 2px 4px;
  cursor: pointer;
}
.ph-pills { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; padding-top: 12px; }
.ph-clear-all {
  border: 0;
  background: transparent;
  color: var(--color-text-muted);
  font-size: 11px;
  padding: 4px 2px;
  cursor: pointer;
}
.ph-scrim {
  position: fixed;
  inset: 0;
  z-index: 998;
  background: rgba(24, 20, 14, 0.3);
}
.ph-sheet {
  position: fixed;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: 999;
  display: flex;
  flex-direction: column;
  padding: 8px 18px calc(18px + var(--safe-bottom, 0px));
  border-radius: 22px 22px 0 0;
  background: var(--color-surface-strong);
  box-shadow: 0 -18px 50px rgba(0, 0, 0, 0.2);
  backdrop-filter: blur(18px);
  -webkit-backdrop-filter: blur(18px);
}
.ph-sheet:focus { outline: none; }
.ph-handle {
  width: 36px;
  height: 4px;
  border-radius: 999px;
  background: var(--color-border-strong);
  margin: 0 auto 10px;
}
.ph-sheet-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 2px 2px 12px;
  font-size: 16px;
}
.ph-sheet-head button {
  border: 0;
  background: transparent;
  color: var(--color-text-muted);
  font-size: 12px;
  cursor: pointer;
}
.ph-filter-body { padding-bottom: 16px; }
.ph-apply {
  width: 100%;
  height: 44px;
  border: 0;
  border-radius: 12px;
  background: var(--color-primary);
  color: #fff;
  font-size: 14px;
  font-weight: 650;
  cursor: pointer;
}
[data-theme="dark"] .ph-apply { color: #10221B; }
.ph-pop { width: 300px; }
.ph-pop .ph-apply { height: 40px; font-size: 13px; }

@media (max-width: 1099px) {
  /* 手机端筛选/搜索是唯一入口，按钮撑到接近 44px 的触控区 */
  .ph-icon { width: 38px; height: 38px; }
  .ph-back { padding: 8px 4px; }
}
</style>
