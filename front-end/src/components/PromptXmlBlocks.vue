<script setup>
import { computed, ref } from 'vue'
import { useThemeStore } from '@/stores/theme'

// 检索上下文 XML（memories/notes/events/profile/summaries…）的美化渲染。
// 解析失败时原样回落 <pre>：宁可难看，也不要静默丢内容。
const props = defineProps({ xml: { type: String, default: '' } })

const themeStore = useThemeStore()
const collapsedBlocks = ref(new Set())

function parseXMLToBlocks(xmlText) {
  try {
    const parser = new DOMParser()
    const wrapped = `<root>${xmlText}</root>`
    const doc = parser.parseFromString(wrapped, 'text/xml')
    if (doc.querySelector('parsererror')) return null
    const blocks = []
    const root = doc.documentElement
    for (const el of root.children) {
      const block = parseBlockElement(el)
      if (block) blocks.push(block)
    }
    return blocks
  } catch {
    return null
  }
}

// parseBlockElement — 已知块走专门分支，未知标签走通用树（不丢块，不必每加一个标签就改这里）。
function parseBlockElement(el) {
  const block = parseKnownBlock(el)
  if (block && block.type !== 'generic') {
    block.extraAttrs = extraAttrs(el, ['description'])
  }
  return block
}

// extraAttrs 取未被专门分支消费的属性，避免新增属性静默消失。
function extraAttrs(el, known) {
  const out = {}
  for (const a of Array.from(el.attributes || [])) {
    if (!known.includes(a.name)) out[a.name] = a.value
  }
  return out
}

// flattenGeneric 把元素拍平成行列表（tag + 属性 + 直接文本），模板按 depth 缩进渲染，无需递归组件。
function flattenGeneric(el, depth, out) {
  const attrs = Object.fromEntries(Array.from(el.attributes || []).map(a => [a.name, a.value]))
  const text = Array.from(el.childNodes)
    .filter(n => n.nodeType === 3)
    .map(n => n.textContent.trim())
    .filter(Boolean)
    .join(' ')
  out.push({ depth, tag: el.tagName, attrs, text })
  for (const child of el.children) flattenGeneric(child, depth + 1, out)
  return out
}

function parseGenericBlock(el) {
  const rows = []
  for (const child of el.children) flattenGeneric(child, 0, rows)
  if (rows.length === 0) flattenGeneric(el, 0, rows) // 无子元素的纯文本块：直接展示自身
  return {
    type: 'generic',
    tag: el.tagName,
    description: el.getAttribute('description') || '',
    items: [],
    rows,
    extraAttrs: extraAttrs(el, ['description']),
  }
}

function parseKnownBlock(el) {
  const tag = el.tagName
  const attrs = Object.fromEntries(Array.from(el.attributes || []).map(a => [a.name, a.value]))

  switch (tag) {
    case 'memories':
    case 'notes': {
      const items = []
      for (const child of el.children) {
        items.push({
          topic: child.getAttribute('topic') || '',
          date: child.getAttribute('date') || '',
          score: parseFloat(child.getAttribute('score')) || 0,
          content: child.textContent?.trim() || '',
          extraAttrs: extraAttrs(child, ['topic', 'date', 'score']),
        })
      }
      return { type: tag === 'memories' ? 'memory' : 'note', description: attrs.description || '', items }
    }
    case 'topics': {
      const items = []
      for (const child of el.children) {
        items.push({
          state: child.getAttribute('desc') || '',
          score: parseFloat(child.getAttribute('score')) || 0,
          content: child.textContent?.trim() || '',
          extraAttrs: extraAttrs(child, ['desc', 'score']),
        })
      }
      return { type: 'theme', description: attrs.description || '', items }
    }
    case 'summaries': {
      const items = []
      for (const child of el.children) {
        items.push({
          topics: child.getAttribute('topics') || '',
          score: parseFloat(child.getAttribute('score')) || 0,
          content: child.textContent?.trim() || '',
          extraAttrs: extraAttrs(child, ['topics', 'score']),
        })
      }
      return { type: 'summary', description: attrs.description || '', items }
    }
    case 'history': {
      const rounds = []
      for (const child of el.children) {
        const msgs = []
        for (const m of child.children) {
          msgs.push({ role: m.getAttribute('role') || '', content: m.textContent?.trim() || '' })
        }
        rounds.push({
          date: child.getAttribute('date') || '',
          score: parseFloat(child.getAttribute('score')) || 0,
          messages: msgs,
        })
      }
      return { type: 'history', description: attrs.description || '', rounds }
    }
    case 'reminders': {
      const items = []
      for (const child of el.children) {
        items.push({
          id: child.getAttribute('id') || '',
          content: child.textContent?.trim() || '',
          extraAttrs: extraAttrs(child, ['id']),
        })
      }
      return { type: 'reminder', description: attrs.description || '', items }
    }
    case 'recent': {
      const items = []
      for (const child of el.children) {
        items.push({
          date: child.getAttribute('date') || '',
          content: child.textContent?.trim() || '',
          extraAttrs: extraAttrs(child, ['date']),
        })
      }
      return { type: 'recent', description: attrs.description || '', items }
    }
    case 'events': {
      const items = []
      for (const child of el.children) {
        // summary / memories 是子元素：textContent 会连记忆正文一起带出来，必须按直接子节点取
        const kids = Array.from(child.children)
        const summaryEl = kids.find(c => c.tagName === 'summary')
        const memoriesEl = kids.find(c => c.tagName === 'memories')
        const memories = []
        for (const m of memoriesEl?.children || []) {
          memories.push({
            id: m.getAttribute('id') || '',
            topic: m.getAttribute('topic') || '',
            date: m.getAttribute('date') || '',
            content: m.textContent?.trim() || '',
          })
        }
        items.push({
          id: child.getAttribute('id') || '',
          title: child.getAttribute('title') || '',
          mode: child.getAttribute('mode') || '',
          score: parseFloat(child.getAttribute('score')) || 0,
          updated: child.getAttribute('updated') || '',
          memoryCount: parseInt(child.getAttribute('memory_count'), 10) || 0,
          pinned: child.getAttribute('pinned') === '1',
          summary: summaryEl?.textContent?.trim() || '',
          memories,
          extraAttrs: extraAttrs(child, ['id', 'title', 'mode', 'score', 'updated', 'memory_count', 'pinned']),
        })
      }
      return { type: 'event', description: attrs.description || '', items }
    }
    case 'current_datetime':
      return { type: 'current_datetime', content: el.textContent?.trim() || '' }
    case 'notice':
      return { type: 'notice', content: el.textContent?.trim() || '' }
    case 'profile': {
      const items = []
      for (const child of el.children) {
        items.push({
          key: child.getAttribute('key') || '',
          updatedAt: child.getAttribute('updated_at') || '',
          source: child.getAttribute('source') || '',
          value: child.textContent?.trim() || '',
          extraAttrs: extraAttrs(child, ['key', 'updated_at', 'source']),
        })
      }
      return { type: 'profile', description: attrs.description || '', items }
    }
    case 'user_query':
      return { type: 'user_query', content: el.textContent?.trim() || '' }
    default:
      return parseGenericBlock(el)
  }
}

const parsedBlocks = computed(() => parseXMLToBlocks(props.xml || ''))

function blockTypeLabel(type) {
  const map = { memory: '记忆 (memories)', note: '笔记 (notes)', theme: '主题 (themes)', history: '聊天记录 (history)', summary: '摘要 (summaries)', event: '事件 (events)', reminder: '提醒 (reminders)', current_datetime: '当前时间', profile: '用户画像 (profile)', notice: '系统提示 (notice)', user_query: '用户查询', recent: '最近用户消息 (recent)' }
  return map[type] || type
}

function blockTypeIcon(type) {
  const map = { memory: '📋', note: '📝', theme: '🔗', history: '💬', summary: '📄', event: '📌', reminder: '⏰', current_datetime: '🕐', profile: '👤', notice: '📢', user_query: '💭', recent: '🕘' }
  return map[type] || '📦'
}

function blockItemCount(block) {
  if (block.type === 'history') return block.rounds?.length || 0
  if (block.type === 'generic') return block.rows?.length || 0
  if (block.type === 'notice') return 1
  return block.items?.length || 0
}

function toggleBlockCollapse(key) {
  if (collapsedBlocks.value.has(key)) {
    collapsedBlocks.value.delete(key)
  } else {
    collapsedBlocks.value.add(key)
  }
  collapsedBlocks.value = new Set(collapsedBlocks.value)
}

function isBlockCollapsed(key) {
  return collapsedBlocks.value.has(key)
}

function blockHeaderBg(type) {
  const base = { memory: '59,130,246', note: '34,197,94', theme: '245,158,11', history: '59,130,246', summary: '20,184,166', event: '236,72,153', reminder: '99,102,241', profile: '139,92,246', notice: '120,113,108', recent: '249,115,22' }
  const rgb = base[type] || '100,100,100'
  return themeStore.isDark ? `rgba(${rgb},0.12)` : `rgba(${rgb},0.06)`
}

// 事件注入形态（full=全貌 / hit=本轮命中 / summary=窗口内 / index=只有标题）
const EVENT_MODE_META = {
  full: { label: '全貌 full', color: '#7c3aed' },
  hit: { label: '命中 hit', color: '#d97706' },
  summary: { label: '摘要 summary', color: '#0d9488' },
  index: { label: '索引 index', color: '#64748b' },
}

function eventModeLabel(mode) {
  return EVENT_MODE_META[mode]?.label || mode
}

function eventModeStyle(mode) {
  const color = EVENT_MODE_META[mode]?.color || '#64748b'
  return {
    fontSize: '11px', padding: '1px 6px', borderRadius: '4px', fontWeight: 500,
    color, background: themeStore.isDark ? `${color}33` : `${color}1a`,
  }
}

// extraAttrsText — 未适配属性的单行展示（新增属性不至于完全看不见）。
function extraAttrsText(extra) {
  if (!extra) return ''
  return Object.entries(extra).map(([k, v]) => `${k}=${v}`).join(' · ')
}

function scorePercent(score) {
  return Math.round(score * 100)
}

function scoreBarColor(score) {
  if (score >= 0.85) return '#34c759'
  if (score >= 0.7) return '#ff9f0a'
  return '#ff3b30'
}
</script>

<template>
  <pre
    v-if="!parsedBlocks || parsedBlocks.length === 0"
    style="font-size: 12px; line-height: 1.6; white-space: pre-wrap; word-break: break-all; margin: 0; padding: 10px 12px; background: var(--color-bg-page); border-radius: var(--radius-md);"
  >{{ xml }}</pre>

  <template v-else>
    <div v-for="(block, idx) in parsedBlocks" :key="idx" style="margin-bottom: 10px; border: 1px solid var(--color-border); border-radius: var(--radius-md); overflow: hidden;">
      <!-- Block Header -->
      <div
        @click="toggleBlockCollapse(idx)"
        :style="{
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          padding: '8px 14px', cursor: 'pointer',
          background: blockHeaderBg(block.type),
          borderBottom: isBlockCollapsed(idx) ? 'none' : '1px solid var(--color-border)',
        }"
      >
        <div style="display: flex; align-items: center; gap: 8px;">
          <span style="font-size: 15px;">{{ blockTypeIcon(block.type) }}</span>
          <span style="font-size: 13px; font-weight: 600; color: var(--color-text);">{{ block.type === 'generic' ? `<${block.tag}>` : blockTypeLabel(block.type) }}</span>
          <span v-if="block.type === 'generic'" style="font-size: 11px; padding: 1px 6px; border-radius: 4px; background: var(--color-bg-page); color: var(--color-text-muted);">未适配</span>
          <span style="font-size: 11px; color: var(--color-text-muted); background: var(--color-bg-page); padding: 1px 6px; border-radius: 10px;">{{ blockItemCount(block) }}条</span>
        </div>
        <span style="font-size: 11px; color: var(--color-text-muted);">{{ isBlockCollapsed(idx) ? '▶' : '▼' }}</span>
      </div>

      <!-- Block Body -->
      <div v-if="!isBlockCollapsed(idx)" style="padding: 10px 14px 14px; background: var(--color-surface);">
        <!-- Description -->
        <div v-if="block.description" style="font-size: 11px; color: var(--color-text-muted); line-height: 1.5; margin-bottom: 10px; max-height: 3em; overflow: hidden;" :title="block.description">{{ block.description }}</div>
        <div v-if="extraAttrsText(block.extraAttrs)" style="font-size: 11px; color: var(--color-text-muted); font-family: monospace; margin-bottom: 10px;">{{ extraAttrsText(block.extraAttrs) }}</div>

        <!-- 未适配的块：通用树，保证新标签也能看见 -->
        <template v-if="block.type === 'generic'">
          <div v-for="(row, i) in block.rows" :key="i" :style="{
            marginLeft: row.depth * 14 + 'px',
            padding: '6px 10px', marginBottom: '4px',
            borderRadius: 'var(--radius-sm)',
            background: themeStore.isDark ? 'rgba(255,255,255,0.04)' : '#f8fafc',
            border: '1px solid var(--color-border)',
          }">
            <div style="font-family: monospace; font-size: 12px; color: var(--color-primary);">
              &lt;{{ row.tag }}&gt;
              <span v-for="(v, k) in row.attrs" :key="k" style="color: var(--color-text-muted); margin-left: 6px;">{{ k }}="{{ v }}"</span>
            </div>
            <div v-if="row.text" style="font-size: 13px; color: var(--color-text); line-height: 1.6; white-space: pre-wrap; word-break: break-all; margin-top: 2px;">{{ row.text }}</div>
          </div>
        </template>

        <!-- memory / note / theme / summary / reminder / recent items -->
        <template v-if="['memory','note','theme','summary','reminder','recent'].includes(block.type)">
          <div v-for="(item, i) in block.items" :key="i" :style="{
            padding: '10px 12px', marginBottom: i < block.items.length - 1 ? '8px' : '0',
            borderRadius: 'var(--radius-sm)',
            background: themeStore.isDark ? 'rgba(255,255,255,0.04)' : '#f8fafc',
            border: '1px solid var(--color-border)',
          }">
            <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin-bottom: 6px;">
              <span v-if="item.topic" style="font-size: 11px; padding: 1px 6px; border-radius: 4px; background: var(--color-primary); color: #fff; font-weight: 500;">{{ item.topic }}</span>
              <span v-if="item.state" style="font-size: 11px; padding: 1px 6px; border-radius: 4px; background: var(--color-warning); color: #fff; font-weight: 500;">{{ item.state }}</span>
              <span v-if="item.topics" style="font-size: 11px; padding: 1px 6px; border-radius: 4px; background: rgba(20,184,166,0.15); color: #14b8a6; font-weight: 500;">{{ item.topics }}</span>
              <span v-if="item.date" style="font-size: 11px; color: var(--color-text-muted);">{{ item.date }}</span>
              <span v-if="item.score" style="font-size: 11px; color: var(--color-text-muted); margin-left: auto;">{{ scorePercent(item.score) }}%</span>
            </div>
            <div v-if="item.score" style="height: 3px; border-radius: 2px; background: var(--color-border); margin-bottom: 6px;">
              <div :style="{ width: scorePercent(item.score) + '%', height: '100%', borderRadius: '2px', background: scoreBarColor(item.score) }"></div>
            </div>
            <div style="font-size: 13px; color: var(--color-text); line-height: 1.6; white-space: pre-wrap; word-break: break-all;">{{ item.content || '(空)' }}</div>
            <div v-if="extraAttrsText(item.extraAttrs)" style="font-size: 11px; color: var(--color-text-muted); font-family: monospace; margin-top: 6px;">{{ extraAttrsText(item.extraAttrs) }}</div>
          </div>
        </template>

        <!-- event items：标题 + 形态 + summary + 内嵌记忆 -->
        <template v-if="block.type === 'event'">
          <div v-for="(item, i) in block.items" :key="i" :style="{
            padding: '10px 12px', marginBottom: i < block.items.length - 1 ? '8px' : '0',
            borderRadius: 'var(--radius-sm)',
            background: themeStore.isDark ? 'rgba(255,255,255,0.04)' : '#f8fafc',
            border: '1px solid var(--color-border)',
          }">
            <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin-bottom: 6px;">
              <span style="font-size: 13px; font-weight: 600; color: var(--color-text);">{{ item.title || item.id }}</span>
              <span v-if="item.mode" :style="eventModeStyle(item.mode)">{{ eventModeLabel(item.mode) }}</span>
              <span v-if="item.pinned" style="font-size: 11px; padding: 1px 6px; border-radius: 4px; background: var(--color-primary); color: #fff; font-weight: 500;">关注</span>
              <span v-if="item.updated" style="font-size: 11px; color: var(--color-text-muted);">{{ item.updated }}</span>
              <span v-if="item.memoryCount" style="font-size: 11px; color: var(--color-text-muted);">记忆 {{ item.memoryCount }}</span>
              <span v-if="item.score" style="font-size: 11px; color: var(--color-text-muted); margin-left: auto;">{{ scorePercent(item.score) }}%</span>
            </div>
            <div v-if="item.summary" style="font-size: 13px; color: var(--color-text); line-height: 1.6; white-space: pre-wrap; word-break: break-all;">{{ item.summary }}</div>
            <div v-if="extraAttrsText(item.extraAttrs)" style="font-size: 11px; color: var(--color-text-muted); font-family: monospace; margin-top: 6px;">{{ extraAttrsText(item.extraAttrs) }}</div>
            <div v-if="item.memories.length" style="margin-top: 8px; padding-top: 8px; border-top: 1px dashed var(--color-border);">
              <div v-for="(m, mi) in item.memories" :key="mi" :style="{
                fontSize: '12px', lineHeight: 1.6, color: 'var(--color-text)',
                padding: '4px 8px', marginBottom: mi < item.memories.length - 1 ? '4px' : '0',
                borderRadius: 'var(--radius-sm)',
                background: themeStore.isDark ? 'rgba(255,255,255,0.03)' : 'var(--color-bg-page)',
                whiteSpace: 'pre-wrap', wordBreak: 'break-all',
              }">
                <span v-if="m.date" style="font-size: 10px; color: var(--color-text-muted); margin-right: 6px;">{{ m.date }}</span>
                <span v-if="m.topic" style="font-size: 10px; color: var(--color-text-muted); margin-right: 6px;">#{{ m.topic }}</span>
                {{ m.content }}
              </div>
            </div>
          </div>
        </template>

        <!-- profile 用户画像字段 -->
        <template v-if="block.type === 'profile'">
          <div v-for="(item, i) in block.items" :key="i" :style="{
            padding: '10px 12px', marginBottom: i < block.items.length - 1 ? '8px' : '0',
            borderRadius: 'var(--radius-sm)',
            background: themeStore.isDark ? 'rgba(255,255,255,0.04)' : '#f8fafc',
            border: '1px solid var(--color-border)',
          }">
            <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin-bottom: 6px;">
              <span style="font-size: 11px; padding: 1px 8px; border-radius: 4px; background: var(--color-primary); color: #fff; font-weight: 500;">{{ item.key }}</span>
              <span v-if="item.source" :style="{
                fontSize: '11px', padding: '1px 6px', borderRadius: 4,
                background: item.source === 'explicit' ? 'rgba(34,197,94,0.15)' : 'rgba(100,116,139,0.15)',
                color: item.source === 'explicit' ? '#16a34a' : 'var(--color-text-secondary)',
                fontWeight: 500,
              }">{{ item.source === 'explicit' ? '显式' : 'LLM补充' }}</span>
              <span v-if="item.updatedAt" style="font-size: 11px; color: var(--color-text-muted);">{{ item.updatedAt }}</span>
            </div>
            <div style="font-size: 13px; color: var(--color-text); line-height: 1.6; white-space: pre-wrap; word-break: break-all;">{{ item.value || '(空)' }}</div>
            <div v-if="extraAttrsText(item.extraAttrs)" style="font-size: 11px; color: var(--color-text-muted); font-family: monospace; margin-top: 6px;">{{ extraAttrsText(item.extraAttrs) }}</div>
          </div>
        </template>

        <!-- history rounds -->
        <template v-if="block.type === 'history'">
          <div v-for="(round, ri) in block.rounds" :key="ri" :style="{
            padding: '10px 12px', marginBottom: ri < block.rounds.length - 1 ? '10px' : '0',
            borderRadius: 'var(--radius-sm)',
            background: themeStore.isDark ? 'rgba(255,255,255,0.04)' : '#f8fafc',
            border: '1px solid var(--color-border)',
          }">
            <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 8px;">
              <span v-if="round.date" style="font-size: 11px; color: var(--color-text-muted);">{{ round.date }}</span>
              <span v-if="round.score" style="font-size: 11px; color: var(--color-text-muted); margin-left: auto;">{{ scorePercent(round.score) }}%</span>
            </div>
            <div v-if="round.score" style="height: 3px; border-radius: 2px; background: var(--color-border); margin-bottom: 8px;">
              <div :style="{ width: scorePercent(round.score) + '%', height: '100%', borderRadius: '2px', background: scoreBarColor(round.score) }"></div>
            </div>
            <div v-for="(m, mi) in round.messages" :key="mi" :style="{
              padding: '8px 12px', marginBottom: mi < round.messages.length - 1 ? '4px' : '0',
              borderRadius: 'var(--radius-sm)',
              borderLeft: `4px solid ${m.role === 'user' ? 'var(--color-primary)' : 'var(--color-success)'}`,
              background: themeStore.isDark ? (m.role === 'user' ? 'rgba(59,130,246,0.08)' : 'rgba(34,197,94,0.06)') : (m.role === 'user' ? '#eff6ff' : '#f0fdf4'),
            }">
              <span style="font-size: 10px; color: var(--color-text-muted); display: block; margin-bottom: 2px;">{{ m.role === 'user' ? '😀 用户' : m.role === 'assistant' ? '🤖 助手' : m.role }}</span>
              <div style="font-size: 12px; color: var(--color-text); line-height: 1.5; white-space: pre-wrap; word-break: break-all;">{{ m.content || '(空)' }}</div>
            </div>
          </div>
        </template>

        <!-- current_datetime / notice / user_query -->
        <template v-if="['current_datetime','notice','user_query'].includes(block.type)">
          <div :style="{
            padding: '10px 14px', borderRadius: 'var(--radius-md)',
            background: block.type === 'user_query' ? 'var(--color-primary)' : 'var(--color-bg-page)',
            color: block.type === 'user_query' ? '#fff' : 'var(--color-text)',
            fontSize: '14px', lineHeight: 1.6, whiteSpace: 'pre-wrap', wordBreak: 'break-all',
          }">
            <span v-if="block.type === 'current_datetime'" style="font-size: 11px; display: block; margin-bottom: 2px; opacity: 0.6;">🕐 当前时间</span>
            <span v-else-if="block.type === 'notice'" style="font-size: 11px; display: block; margin-bottom: 2px; opacity: 0.6;">📢 系统提示</span>
            <span v-else style="font-size: 11px; display: block; margin-bottom: 2px; opacity: 0.7;">💭 用户查询</span>
            {{ block.content || '(空)' }}
          </div>
        </template>
      </div>
    </div>
  </template>
</template>
