<script setup>
import { computed, ref } from 'vue'
import PromptXmlBlocks from './PromptXmlBlocks.vue'
import md from '@/utils/markdown'
import { splitPromptSections, isXmlSection } from '@/utils/promptSections'

// system prompt 只读预览：按 `## ` 切段渲染成卡片，正文是检索上下文 XML 的段
// 交给 PromptXmlBlocks 美化（否则 markdown 会把 <profile> 折成一段墙）。
// markdown 实例是 html:false，XML 文本不会被当标签执行。
const props = defineProps({ text: { type: String, default: '' } })

const raw = ref(false)
const collapsed = ref(new Set())

const sections = computed(() => splitPromptSections(props.text))

// 只认「整段就是 XML」：解析失败时 PromptXmlBlocks 自己回落原文，不会吞块。
const isXml = isXmlSection

function toggle(i) {
  if (collapsed.value.has(i)) collapsed.value.delete(i)
  else collapsed.value.add(i)
  collapsed.value = new Set(collapsed.value)
}
</script>

<template>
  <div>
    <div style="display: flex; justify-content: flex-end; margin-bottom: 6px;">
      <span
        @click="raw = !raw"
        style="cursor: pointer; font-size: 12px; color: var(--color-primary); user-select: none;"
      >{{ raw ? '美化视图' : '原文' }}</span>
    </div>

    <pre
      v-if="raw"
      style="font-size: 12px; line-height: 1.6; white-space: pre-wrap; word-break: break-all; margin: 0; padding: 12px; background: var(--color-bg-page); border-radius: var(--radius-md);"
    >{{ text }}</pre>

    <div v-else-if="sections.length === 0" style="color: var(--color-text-muted); font-size: 13px;">(空)</div>

    <template v-else>
      <div v-for="(sec, i) in sections" :key="i" style="margin-bottom: 10px; border: 1px solid var(--color-border); border-radius: var(--radius-md); overflow: hidden;">
        <div
          v-if="sec.title"
          @click="toggle(i)"
          style="display: flex; align-items: center; justify-content: space-between; padding: 8px 14px; cursor: pointer; background: var(--color-bg-page);"
          :style="{ borderBottom: collapsed.has(i) ? 'none' : '1px solid var(--color-border)' }"
        >
          <span style="font-size: 13px; font-weight: 600; color: var(--color-text);">{{ sec.title }}</span>
          <span style="font-size: 11px; color: var(--color-text-muted);">{{ collapsed.has(i) ? '▶' : '▼' }}</span>
        </div>
        <div v-if="!collapsed.has(i)" style="padding: 10px 14px 14px; background: var(--color-surface);">
          <PromptXmlBlocks v-if="isXml(sec.body)" :xml="sec.body" />
          <div v-else class="markdown-body" style="font-size: 14px; line-height: 1.7;" v-html="md.render(sec.body)"></div>
        </div>
      </div>
    </template>
  </div>
</template>
