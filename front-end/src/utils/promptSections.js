// system prompt 预览用：按顶层 `## ` 标题切段。
// 前缀缓存要求 prompt 里只有顶层二级标题（builder 只写 "## "），正文中的 `### ` 不算切点。
const TAG_RE = /<(\/?)([a-zA-Z_][\w.:-]*)([^<>]*?)(\/?)>/g

// depthDelta 统计一行里的 XML 标签净深度。正文里的 `<` 早已被 Go 侧转义成 &lt;，
// 所以能匹配到的都是真标签；Go 编码器会把文本里的换行转成 &#xA;，标签不会跨行。
function depthDelta(line) {
  let d = 0
  for (const m of line.matchAll(TAG_RE)) {
    if (m[1]) d--
    else if (!m[4]) d++
  }
  return d
}

export function splitPromptSections(text) {
  const out = []
  let cur = { title: '', lines: [] }
  // 段正文是 XML 时（稳定上下文里的 summaries/profile，正文由 LLM 生成，可能含行首 `## `），
  // 在 XML 未闭合期间不认 `## ` 为边界，闭合后恢复切分（后面的 `## 语音回答` 不能被吞）。
  let xmlDepth = 0
  let xmlTracked = false
  const canSplit = () => !xmlTracked || xmlDepth <= 0

  for (const line of (text || '').split('\n')) {
    if (line.startsWith('## ') && canSplit()) {
      out.push(cur)
      cur = { title: line.slice(3).trim(), lines: [] }
      xmlDepth = 0
      xmlTracked = false
      continue
    }
    if (!xmlTracked && cur.lines.every(l => !l.trim())) {
      xmlTracked = line.trimStart().startsWith('<')
    }
    cur.lines.push(line)
    if (xmlTracked) xmlDepth += depthDelta(line)
  }
  out.push(cur)
  return out
    .map(s => ({ title: s.title, body: s.lines.join('\n').trim() }))
    .filter(s => s.title || s.body)
}

// 只有「整段就是 XML」才交给 XML 美化渲染；混着 markdown 的段落照旧走 md。
export function isXmlSection(body) {
  return (body || '').startsWith('<')
}
