<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NButton,
  NDrawer,
  NDrawerContent,
  NDropdown,
  NInput,
  NModal,
  NPopover,
  NSpin,
  useMessage
} from 'naive-ui'
import { api } from '@/api/client'
import PageTopBar from '@/components/PageTopBar.vue'
import TopBarBtn from '@/components/TopBarBtn.vue'
import ImageLightbox from '@/components/media/ImageLightbox.vue'
import AnnotationList from '@/components/document/AnnotationList.vue'
import { useMobile } from '@/composables/useMobile'
import { useExportPDF } from '@/composables/useExportPDF'
import { ensureDocumentShareUrl, writeClipboard, downloadMarkdown } from '@/utils/document'
import md from '@/utils/markdown'

const route = useRoute()
const router = useRouter()
const message = useMessage()
const { isMobile } = useMobile()
const { exportPDF } = useExportPDF()

const doc = ref(null)
const loading = ref(true)
const failed = ref('')
const html = ref('')
const toc = ref([])
const paperRef = ref(null)
const tocOpen = ref(false)
const previewSrc = ref('')

const title = computed(() => doc.value?.title || '文档')
const tags = computed(() => parseTags(doc.value?.tags))

// ---- 字号（只作用于本页） ----
const FONT_KEY = 'doc.fontScale'
const FONT_STEPS = computed(() => (isMobile.value ? [100, 110, 120] : [100, 110, 120, 125]))
const scale = ref(readScale())

function readScale() {
  const n = Number(localStorage.getItem(FONT_KEY))
  return n >= 100 && n <= 125 ? n : 100
}

function setScale(n) {
  scale.value = n
  localStorage.setItem(FONT_KEY, String(n))
}

watch(isMobile, (m) => {
  // 只降显示值，不回写：桌面选过 125% 的人用一次手机也不该被永久降级
  if (m && scale.value > 120) scale.value = 120
}, { immediate: true })

// ---- 解析一次：生成目录 id + 渲染正文 ----
function inlineText(token) {
  return (token?.children || [])
    .filter((c) => c.type === 'text' || c.type === 'code_inline')
    .map((c) => c.content)
    .join('')
    .trim()
}

function build() {
  const tokens = md.parse(doc.value?.content || '', {})
  // 文档的 title 与 content 分列，模型常把标题又写成正文第一个 #。纸面已有大标题，跳过它。
  const first = tokens.findIndex((t) => t.type === 'heading_open')
  if (first >= 0 && tokens[first].tag === 'h1'
    && inlineText(tokens[first + 1]) === (doc.value?.title || '').trim()) {
    tokens.splice(first, 3)
  }
  const items = []
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i]
    if (t.type !== 'heading_open') continue
    const level = Number(t.tag.slice(1))
    if (level > 3) continue
    const text = inlineText(tokens[i + 1])
    if (!text) continue
    const id = `doc-h-${items.length}`
    t.attrSet('id', id)
    items.push({ id, level, text })
  }
  toc.value = items
  html.value = md.renderer.render(tokens, md.options, {})
}

// ---- 图片：私有图床要带 Bearer，渲染后换成 blob URL；外链保持原样 ----
let blobUrls = []
let renderSeq = 0

function clearBlobs() {
  blobUrls.forEach((u) => URL.revokeObjectURL(u))
  blobUrls = []
}

async function hydrateImages() {
  const seq = renderSeq
  await nextTick()
  const root = paperRef.value
  if (!root) return
  const imgs = [...root.querySelectorAll('img')]
  imgs.forEach((img) => { img.loading = 'lazy' })
  await Promise.allSettled(imgs.map(async (img) => {
    const src = img.getAttribute('src') || ''
    // /images/ 是公开图（文档配图），直接交给浏览器缓存；这里只补历史遗留的私有路径
    if (!src.startsWith('/api/images/')) return
    try {
      const blob = await api.blob(src)
      if (!blob.type.startsWith('image/')) throw new Error(`unexpected content-type: ${blob.type}`)
      const url = URL.createObjectURL(blob)
      if (seq !== renderSeq) {
        URL.revokeObjectURL(url)
        return
      }
      blobUrls.push(url)
      img.src = url
    } catch (e) {
      // 图被删/无权限：保留 alt，正文不因一张图整页失败
      console.error('Failed to load document image', src, e)
      img.classList.add('img-failed')
    }
  }))
}

// ---- 加载 ----
async function load() {
  const seq = ++renderSeq
  loading.value = true
  failed.value = ''
  // 起手清空：切换/失败时不能留着上一篇的正文、目录和分享入口
  doc.value = null
  toc.value = []
  html.value = ''
  tocOpen.value = false
  clearBlobs()
  annotations.value = []
  annoTotal.value = 0
  annoLoading.value = false
  annoFailed.value = ''
  annoOpen.value = false
  activeAnnoId.value = ''
  clearAnnoHighlight()
  try {
    const data = await api.get(`/api/documents/${route.params.id}`)
    if (seq !== renderSeq) return
    // 文档 404 是裸 {error}，unwrap 不会抛；没有 id 就当失败
    if (!data?.id) throw new Error('文档不存在或已删除')
    doc.value = data
    build()
    // 先把正文渲染出来，hydrateImages 才拿得到 DOM；补图失败不能把整篇文档打回错误页
    loading.value = false
    hydrateImages().catch((e) => console.error('Failed to hydrate document images', e))
    // 批注是附加信息，加载失败不影响正文
    loadAnnotations(seq)
  } catch (e) {
    if (seq !== renderSeq) return
    console.error('Failed to load document', e)
    doc.value = null
    toc.value = []
    html.value = ''
    failed.value = e?.message || '加载失败'
  } finally {
    if (seq === renderSeq) loading.value = false
  }
}

onMounted(load)
watch(() => route.params.id, (id) => { if (id) load() })
onBeforeUnmount(() => {
  renderSeq++ // 让在途的 hydrate 结果被丢弃
  clearBlobs()
  clearAnnoHighlight()
  wideMq?.removeEventListener('change', onWide)
})

// ---- 只读批注（列表 + 按 quote 定位；offset 是源文 rune 偏移，对不上渲染 DOM，不用） ----
const annotations = ref([])
const annoTotal = ref(0)
const annoLoading = ref(false)
const annoFailed = ref('')
const annoOpen = ref(false)
const activeAnnoId = ref('')
const showAnno = computed(() => annotations.value.length > 0 || !!annoFailed.value)
const annoLabel = computed(() => (annoTotal.value ? `批注 ${annoTotal.value}` : '批注'))
const annoDrawerSize = computed(() => (isMobile.value ? { height: '60vh' } : { width: 380 }))

// 桌面 ≥1200 才有右栏，比 useMobile 的断点（1100）晚一档，单独听
const wide = ref(window.matchMedia('(min-width: 1200px)').matches)
let wideMq = null
function onWide(e) { wide.value = e.matches }
onMounted(() => {
  wideMq = window.matchMedia('(min-width: 1200px)')
  wideMq.addEventListener('change', onWide)
})
// 抽屉打开时被拉到宽屏：右栏已能顶上，别两份列表同时展示
watch(wide, (v) => { if (v) annoOpen.value = false })

async function loadAnnotations(seq) {
  if (!doc.value || annoLoading.value) return
  annoLoading.value = true
  try {
    const data = await api.get(`/api/documents/${doc.value.id}/annotations?limit=200&offset=0`)
    if (seq !== renderSeq) return
    annotations.value = Array.isArray(data?.annotations) ? data.annotations : []
    annoTotal.value = Number(data?.total) || annotations.value.length
    // 成功才清错误；重试期间保留 failed 让侧栏不卸载、spinner 有机会渲染
    annoFailed.value = ''
  } catch (e) {
    if (seq !== renderSeq) return
    console.error('Failed to load annotations', e)
    annoFailed.value = '批注加载失败'
  } finally {
    if (seq === renderSeq) annoLoading.value = false
  }
}

let annoTimer = 0
const HL = 'doc-anno-flash'

function clearAnnoHighlight() {
  if (annoTimer) {
    clearTimeout(annoTimer)
    annoTimer = 0
  }
  window.CSS?.highlights?.delete?.(HL)
}

// quote 已由 macOS 端 fold() / 服务端保存时投影过（丢 * _ ~ | 与空白折叠）；
// 投影规则必须与 client/xHelper/.../DocumentReaderView.swift 的 fold() 保持一致，改一边要同步另一边。
const MD_MARKERS = '*_`~|'

// 渲染后的正文里一段话常被 <strong>/<code>/<a> 拆成多个文本节点，
// 所以拼成一条带位置的字符串再找，最后映射回 Range。
function findQuoteRange(root, rawQuote) {
  const quote = (rawQuote || '').split('').filter((c) => !MD_MARKERS.includes(c)).join('')
    .replace(/\s+/g, ' ').trim()
  if (!quote) return null
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  const nodes = []
  let text = ''
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    nodes.push({ node: n, start: text.length })
    text += n.data
  }
  let norm = ''
  const map = []
  let space = false
  for (let i = 0; i < text.length; i++) {
    const c = text[i]
    if (/\s/.test(c)) {
      space = true
      continue
    }
    if (MD_MARKERS.includes(c)) continue // 丢行内标记，不占投影下标
    if (space && norm) {
      norm += ' '
      map.push(i)
    }
    space = false
    norm += c
    map.push(i)
  }
  const at = norm.indexOf(quote)
  if (at < 0) return null
  const locate = (abs) => {
    for (let i = nodes.length - 1; i >= 0; i--) {
      if (abs >= nodes[i].start) return { node: nodes[i].node, offset: abs - nodes[i].start }
    }
    return null
  }
  const s = locate(map[at])
  const e = locate(map[at + quote.length - 1] + 1)
  if (!s || !e) return null
  const range = document.createRange()
  range.setStart(s.node, s.offset)
  range.setEnd(e.node, e.offset)
  return range
}

function selectAnnotation(a) {
  nextTick(() => {
    if (!paperRef.value) return
    const range = findQuoteRange(paperRef.value, a.quote)
    if (!range) {
      activeAnnoId.value = ''
      clearAnnoHighlight()
      message.info('正文中未找到该划线原文')
      return
    }
    activeAnnoId.value = a.id
    annoOpen.value = false
    range.startContainer.parentElement?.scrollIntoView({ block: 'center', behavior: 'smooth' })
    // 老 Safari 没有 Custom Highlight API：只滚动，不画高亮
    if (!window.CSS?.highlights || typeof Highlight === 'undefined') return
    clearAnnoHighlight()
    window.CSS.highlights.set(HL, new Highlight(range))
    annoTimer = setTimeout(clearAnnoHighlight, 1600)
  })
}

function parseTags(raw) {
  if (!raw) return []
  return raw.split(',').map((t) => t.trim()).filter(Boolean)
}

// ---- 目录跳转 / 内链 / 图片 ----
function goHeading(id) {
  tocOpen.value = false
  nextTick(() => {
    const el = paperRef.value?.querySelector(`#${id}`)
    el?.scrollIntoView({ block: 'start', behavior: 'smooth' })
  })
}

function onPaperClick(e) {
  const link = e.target.closest?.('a[data-route]')
  if (link) {
    e.preventDefault()
    router.push(link.getAttribute('data-route'))
    return
  }
  if (e.target.tagName === 'IMG') {
    // 图被外链包着时点图就是点链接，别同时开灯箱
    if (e.target.closest('a')) return
    previewSrc.value = e.target.src
  }
}

// ---- 分享 / 下载 / PDF ----
const shareOpen = ref(false)
const shareUrl = ref('')
const sharing = ref(false)

async function share() {
  if (sharing.value || !doc.value) return
  sharing.value = true
  try {
    shareUrl.value = await ensureDocumentShareUrl(doc.value.id)
    shareOpen.value = true
  } catch (e) {
    console.error('Failed to share document', e)
    message.error('创建分享链接失败')
  } finally {
    sharing.value = false
  }
}

async function copyShare() {
  try {
    await writeClipboard(shareUrl.value)
    message.success('已复制到剪贴板')
    shareOpen.value = false
  } catch (e) {
    console.error('Failed to copy share url', e)
    message.error('复制失败，请手动选择链接')
  }
}

function downloadMd() {
  downloadMarkdown(doc.value?.content, doc.value?.title)
}

const menuOptions = computed(() => {
  const items = [{ label: '下载 .md', key: 'download' }, { label: '导出 PDF', key: 'pdf' }]
  if (isMobile.value) items.unshift({ label: '分享链接', key: 'share' })
  return items
})

function onMenu(key) {
  if (key === 'download') downloadMd()
  else if (key === 'pdf') exportPDF(doc.value?.content, doc.value?.title, 'markdown')
  else if (key === 'share') share()
}
</script>

<template>
  <div class="page">
    <PageTopBar :title="title">
      <template #right>
        <TopBarBtn v-if="showAnno && !wide && !isMobile" :label="annoLabel" @click="annoOpen = true" />
        <TopBarBtn v-if="doc && !isMobile" label="分享" @click="share" />
        <NPopover v-if="doc && !isMobile" trigger="click" placement="bottom-end">
          <template #trigger>
            <button class="icon-text" type="button" aria-label="字号">Aa</button>
          </template>
          <div class="font-panel">
            <button
              v-for="n in FONT_STEPS"
              :key="n"
              type="button"
              class="font-option"
              :class="{ active: n === scale }"
              @click="setScale(n)"
            >{{ n }}%</button>
          </div>
        </NPopover>
        <NDropdown v-if="doc" trigger="click" placement="bottom-end" :options="menuOptions" @select="onMenu">
          <button class="icon-text more" type="button" aria-label="更多操作">⋯</button>
        </NDropdown>
      </template>
    </PageTopBar>

    <div class="reader">
      <aside v-if="toc.length" class="toc-side">
        <div class="toc-title">目录</div>
        <nav>
          <button
            v-for="h in toc"
            :key="h.id"
            type="button"
            class="toc-link"
            :class="`lv${h.level}`"
            @click="goHeading(h.id)"
          >{{ h.text }}</button>
        </nav>
      </aside>

      <div class="scroll">
        <div v-if="loading" class="center"><NSpin size="small" /></div>
        <div v-else-if="failed" class="center">
          <p class="err">{{ failed }}</p>
          <NButton size="small" @click="load">重试</NButton>
        </div>
        <div v-else class="paper message-content" :style="{ '--doc-scale': scale / 100 }">
          <h1 class="doc-title">{{ title }}</h1>
          <div class="doc-meta">
            <span v-if="doc?.updated_at">更新于 {{ new Date(doc.updated_at).toLocaleString() }}</span>
            <span v-for="tag in tags" :key="tag" class="doc-tag">{{ tag }}</span>
          </div>
          <article ref="paperRef" class="doc-body" v-html="html" @click="onPaperClick"></article>
        </div>
      </div>

      <aside v-if="showAnno && wide" class="anno-side">
        <div class="toc-title">{{ annoTotal ? `批注 · ${annoTotal}` : '批注' }}</div>
        <AnnotationList
          :items="annotations"
          :total="annoTotal"
          :loading="annoLoading"
          :failed="annoFailed"
          :active-id="activeAnnoId"
          @select="selectAnnotation"
          @retry="loadAnnotations(renderSeq)"
        />
      </aside>
    </div>

    <div v-if="isMobile && !loading && !failed" class="mobile-bar">
      <button v-if="toc.length" type="button" class="bar-btn" @click="tocOpen = true">目录</button>
      <button v-if="showAnno" type="button" class="bar-btn" @click="annoOpen = true">{{ annoLabel }}</button>
      <NPopover trigger="click" placement="top">
        <template #trigger>
          <button type="button" class="bar-btn" aria-label="字号">Aa {{ scale }}%</button>
        </template>
        <div class="font-panel">
          <button
            v-for="n in FONT_STEPS"
            :key="n"
            type="button"
            class="font-option"
            :class="{ active: n === scale }"
            @click="setScale(n)"
          >{{ n }}%</button>
        </div>
      </NPopover>
    </div>

    <NDrawer v-model:show="tocOpen" placement="bottom" :height="'58vh'">
      <NDrawerContent title="目录" closable>
        <nav class="drawer-toc">
          <button
            v-for="h in toc"
            :key="h.id"
            type="button"
            class="toc-link"
            :class="`lv${h.level}`"
            @click="goHeading(h.id)"
          >{{ h.text }}</button>
        </nav>
      </NDrawerContent>
    </NDrawer>

    <NDrawer v-model:show="annoOpen" :placement="isMobile ? 'bottom' : 'right'" v-bind="annoDrawerSize">
      <NDrawerContent title="批注" closable>
        <AnnotationList
          :items="annotations"
          :total="annoTotal"
          :loading="annoLoading"
          :failed="annoFailed"
          :active-id="activeAnnoId"
          @select="selectAnnotation"
          @retry="loadAnnotations(renderSeq)"
        />
      </NDrawerContent>
    </NDrawer>

    <NModal v-model:show="shareOpen" preset="card" title="分享文档" style="width: min(480px, 92vw);">
      <div class="share-row">
        <NInput :value="shareUrl" readonly />
        <NButton type="primary" size="small" @click="copyShare">复制</NButton>
      </div>
      <div class="share-hint">链接长期有效</div>
    </NModal>

    <ImageLightbox :src="previewSrc" :visible="!!previewSrc" @close="previewSrc = ''" />
  </div>
</template>

<style scoped>
.page { height: 100%; display: flex; flex-direction: column; min-height: 0; }
.reader { flex: 1; min-height: 0; display: flex; }
.toc-side {
  width: 220px;
  flex: 0 0 220px;
  overflow-y: auto;
  padding: 18px 10px 28px 22px;
  border-right: 0.5px solid var(--color-border);
}
.toc-title {
  font-size: 11.5px;
  font-weight: 700;
  letter-spacing: 0.06em;
  color: var(--color-text-muted);
  padding: 0 8px 8px;
}
.toc-link {
  display: block;
  width: 100%;
  border: 0;
  background: transparent;
  text-align: left;
  padding: 5px 8px;
  border-radius: 8px;
  color: var(--color-text-secondary);
  font-size: 12.5px;
  line-height: 1.45;
  cursor: pointer;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.toc-link:hover { background: var(--color-primary-soft); color: var(--color-primary); }
.toc-link.lv2 { padding-left: 20px; font-size: 12px; }
.toc-link.lv3 { padding-left: 32px; font-size: 12px; color: var(--color-text-muted); }
.drawer-toc { padding-bottom: 16px; }

.anno-side {
  width: 320px;
  flex: 0 0 320px;
  overflow-y: auto;
  padding: 18px 14px 28px;
  border-left: 0.5px solid var(--color-border);
}

.scroll { flex: 1; min-width: 0; overflow-y: auto; padding: 6px 16px 40px; }
.paper {
  max-width: 728px;
  margin: 0 auto;
  padding: 28px 34px 40px;
  border-radius: 14px;
  background: var(--color-surface);
  border: 0.5px solid var(--color-border);
  box-shadow: var(--shadow-sm);
  font-size: calc(15px * var(--doc-scale, 1));
  line-height: 1.75;
  color: var(--color-text);
}
.doc-title {
  font-size: 1.7em;
  line-height: 1.3;
  margin: 0 0 8px;
  color: var(--color-text);
}
.doc-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-bottom: 22px;
  padding-bottom: 16px;
  border-bottom: 0.5px solid var(--color-border);
  font-size: 12px;
  color: var(--color-text-muted);
}
.doc-tag {
  padding: 2px 9px;
  border-radius: 999px;
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
.doc-body :deep(h1) { font-size: 1.6em; margin: 1.1em 0 0.4em; scroll-margin-top: 16px; }
.doc-body :deep(h2) { font-size: 1.25em; margin: 1.2em 0 0.4em; scroll-margin-top: 16px; }
.doc-body :deep(h3) { font-size: 1.08em; margin: 1em 0 0.35em; scroll-margin-top: 16px; }
.doc-body :deep(h4) { font-size: 1em; margin: 0.9em 0 0.3em; }
.doc-body :deep(p) { margin: 0 0 0.7em; }
.doc-body :deep(ul),
.doc-body :deep(ol) { margin: 0 0 0.7em; padding-left: 1.5em; }
.doc-body :deep(blockquote) {
  margin: 0 0 0.7em;
  padding: 2px 12px;
  border-left: 3px solid var(--color-primary-soft);
  color: var(--color-text-secondary);
}
.doc-body :deep(a) { color: var(--color-primary); text-decoration: none; }
.doc-body :deep(a:hover) { text-decoration: underline; }
.doc-body :deep(img) { max-width: 100%; height: auto; border-radius: 8px; cursor: zoom-in; }
.doc-body :deep(img.img-failed) { min-height: 40px; background: var(--color-tool); }
.doc-body :deep(.table-wrap) { overflow-x: auto; margin: 0 0 0.7em; }
.doc-body :deep(table) { border-collapse: collapse; min-width: 460px; }
.doc-body :deep(th),
.doc-body :deep(td) { border-bottom: 1px solid var(--color-border); padding: 7px 8px; text-align: left; }
.doc-body :deep(pre) { background: var(--color-input); padding: 12px; border-radius: var(--radius-md); overflow-x: auto; }
.doc-body :deep(code) { background: var(--color-tool); padding: 2px 6px; border-radius: 6px; font-size: 0.9em; }
.doc-body :deep(pre code) { background: none; padding: 0; }
.center { display: grid; place-items: center; gap: 10px; padding: 60px 0; }
.err { font-size: 12.5px; color: var(--color-text-secondary); margin: 0; }

.icon-text {
  border: 0.5px solid var(--color-border);
  background: rgba(255, 255, 255, 0.24);
  color: var(--color-text-secondary);
  border-radius: 999px;
  height: 28px;
  min-width: 32px;
  padding: 0 9px;
  font-size: 12.5px;
  cursor: pointer;
}
[data-theme="dark"] .icon-text { background: rgba(255, 255, 255, 0.04); }
.icon-text:hover { background: var(--color-primary-soft); color: var(--color-primary); }
.icon-text.more { font-size: 15px; line-height: 1; }
.font-panel { display: flex; gap: 6px; }
.font-option {
  border: 0.5px solid var(--color-border);
  background: transparent;
  color: var(--color-text-secondary);
  border-radius: 999px;
  padding: 4px 10px;
  font-size: 12px;
  cursor: pointer;
}
.font-option.active { background: var(--color-primary); border-color: var(--color-primary); color: #fff; }
[data-theme="dark"] .font-option.active { color: #10221B; }

.mobile-bar {
  position: absolute;
  left: 50%;
  bottom: max(14px, var(--safe-bottom));
  transform: translateX(-50%);
  width: max-content;
  max-width: calc(100% - 20px);
  display: flex;
  gap: 8px;
  padding: 6px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--color-surface-strong) 82%, transparent);
  border: 0.5px solid var(--color-border);
  box-shadow: var(--shadow-md);
  backdrop-filter: blur(14px);
  -webkit-backdrop-filter: blur(14px);
  z-index: 20;
}
.bar-btn {
  border: 0;
  background: transparent;
  color: var(--color-text);
  border-radius: 999px;
  padding: 7px 14px;
  font-size: 12.5px;
  white-space: nowrap;
  cursor: pointer;
}
.bar-btn:hover { background: var(--color-primary-soft); color: var(--color-primary); }

.share-row { display: flex; gap: 8px; }
.share-row :deep(.n-input) { flex: 1; }
.share-hint { margin-top: 10px; font-size: 12px; color: var(--color-text-muted); }

@media (max-width: 1099px) {
  .toc-side { display: none; }
  .scroll { padding: 4px 10px 88px; }
  .paper {
    padding: 20px 18px 30px;
    border-radius: 12px;
    font-size: calc(16px * var(--doc-scale, 1));
  }
}
</style>

<style>
/* ::highlight 里 var() 解析不出来，颜色写死为 token 实值（primary-soft 的实色近似） */
article.doc-body::highlight(doc-anno-flash) { background-color: #CFE8DA; }
[data-theme="dark"] article.doc-body::highlight(doc-anno-flash) { background-color: #2E5749; }
</style>
