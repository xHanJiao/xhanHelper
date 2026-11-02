import MarkdownIt from 'markdown-it'

// 全站统一的聊天/文档 Markdown 渲染配置。
// html: false —— 模型输出不直接当 HTML 执行；换行交给段落，避免把列表撑开。
const md = new MarkdownIt({ html: false, breaks: false, linkify: true })

// 内链 xhanhelper://document/<id> 走应用内路由（点击由容器代理，见 MessageBubble 的 handleBubbleClick）；
// 外链一律新窗口 + noopener。
md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
  const token = tokens[idx]
  const href = token.attrGet('href') || ''
  const internal = href.match(/^xhanhelper:\/\/document\/(.+)$/)
  // 站内路径也走路由（模型两种写法都会出现：xhanhelper://document/<id> 或 /documents/<id>）
  const localPath = href.match(/^\/(documents|artifacts|chat)(\/|$)/)
  if (internal) {
    token.attrSet('data-route', `/documents/${internal[1]}`)
    token.attrSet('href', '#')
  } else if (localPath) {
    token.attrSet('data-route', href)
  } else if (/^https?:/i.test(href)) {
    token.attrSet('target', '_blank')
    token.attrSet('rel', 'noopener noreferrer')
  }
  return self.renderToken(tokens, idx, options)
}

// 表格太宽时横滑，不要把整条消息撑破（窄屏必踩）
md.renderer.rules.table_open = () => '<div class="table-wrap"><table>'
md.renderer.rules.table_close = () => '</table></div>'

// 代码块右上角复制按钮。v-html 里挂不了事件，点击由 App.vue 的全局代理处理。
// 导出 PDF 时传 env.noCopy，避免把按钮印进文档。
md.renderer.rules.fence = (tokens, idx, options, env) => {
  const token = tokens[idx]
  const langName = (token.info || '').trim().split(/\s+/)[0]
  const lang = langName ? ` class="language-${md.utils.escapeHtml(langName)}"` : ''
  const code = `<pre><code${lang}>${md.utils.escapeHtml(token.content)}</code></pre>`
  if (env?.noCopy) return code
  return `<div class="code-block"><button type="button" class="code-copy" data-code-copy>复制</button>${code}</div>`
}

export default md
