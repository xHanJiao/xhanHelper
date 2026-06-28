import MarkdownIt from 'markdown-it'

const md = new MarkdownIt({
  html: false,
  breaks: true,
  linkify: true
})

/**
 * Build a complete HTML document optimized for PDF printing.
 * The print-specific styles ensure rendered PDF has:
 *  - A4-compatible page layout
 *  - Serif body text (reads better on paper) with Chinese fallback
 *  - Clean tables, code blocks, blockquotes
 *  - Proper page-break handling
 */
function buildPrintHTML(title, bodyHTML) {
  return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>${escapeHTML(title)}</title>
<style>
  /* ── Reset & Base ── */
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

  body {
    font-family: "Noto Serif SC", "Source Han Serif SC", "Songti SC", "SimSun", Georgia, "Times New Roman", serif;
    font-size: 12pt;
    line-height: 1.8;
    color: #1a1a1a;
    max-width: 720px;
    margin: 0 auto;
    padding: 48px 40px;
    background: #fff;
  }

  /* ── Title ── */
  .print-title {
    font-size: 22pt;
    font-weight: 700;
    letter-spacing: 0.02em;
    margin-bottom: 8px;
    color: #111;
    line-height: 1.35;
  }

  .print-meta {
    font-size: 9pt;
    color: #888;
    margin-bottom: 32px;
    padding-bottom: 16px;
    border-bottom: 1px solid #e0e0e0;
  }

  /* ── Headings ── */
  h1 { font-size: 1.5em; margin: 1.2em 0 0.5em; font-weight: 700; color: #111; }
  h2 { font-size: 1.25em; margin: 1em 0 0.4em; font-weight: 700; color: #222; }
  h3 { font-size: 1.1em; margin: 0.8em 0 0.35em; font-weight: 600; color: #333; }
  h4, h5, h6 { font-size: 1em; margin: 0.7em 0 0.3em; font-weight: 600; color: #444; }

  /* ── Paragraph ── */
  p { margin: 0.45em 0; }

  /* ── Lists ── */
  ul, ol { padding-left: 1.6em; margin: 0.45em 0; }
  li { margin: 0.15em 0; }

  /* ── Inline Code ── */
  code {
    font-family: "SF Mono", "Cascadia Code", "Menlo", "Monaco", "Courier New", monospace;
    font-size: 0.88em;
    background: #f5f5f5;
    padding: 1px 5px;
    border-radius: 3px;
    border: 1px solid #e8e8e8;
  }

  /* ── Code Blocks ── */
  pre {
    background: #f8f8f8;
    border: 1px solid #e2e2e2;
    border-radius: 6px;
    padding: 14px 16px;
    margin: 0.6em 0;
    overflow-x: auto;
    font-size: 9.5pt;
    line-height: 1.55;
  }
  pre code {
    background: none;
    padding: 0;
    border: none;
    font-size: inherit;
  }

  /* ── Blockquote ── */
  blockquote {
    margin: 0.6em 0;
    padding: 6px 0 6px 14px;
    border-left: 3px solid #6366f1;
    color: #555;
    font-style: italic;
  }

  /* ── Tables ── */
  table {
    border-collapse: collapse;
    width: 100%;
    margin: 0.6em 0;
    font-size: 10pt;
  }
  th, td {
    border: 1px solid #d0d0d0;
    padding: 6px 10px;
    text-align: left;
  }
  th {
    background: #f2f2f2;
    font-weight: 600;
  }

  /* ── Horizontal Rule ── */
  hr {
    border: none;
    border-top: 1px solid #e0e0e0;
    margin: 1.2em 0;
  }

  /* ── Links ── */
  a { color: #4f46e5; text-decoration: underline; }

  /* ── Images ── */
  img { max-width: 100%; height: auto; }

  /* ── Print-specific ── */
  @page {
    size: A4;
    margin: 18mm 16mm 20mm 16mm;
  }

  @media print {
    body {
      padding: 0;
      max-width: none;
    }
    pre, blockquote, img, table {
      page-break-inside: avoid;
    }
    h1, h2, h3, h4, h5, h6 {
      page-break-after: avoid;
    }
    .print-meta {
      border-bottom-color: #ccc;
    }
  }
</style>
</head>
<body>
<h1 class="print-title">${escapeHTML(title)}</h1>
<div class="print-meta">${new Date().toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' })}</div>

${bodyHTML}
</body>
</html>`
}

function escapeHTML(str) {
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

function plainTextToHTML(text) {
  // Escape HTML, then wrap paragraphs separated by double newlines
  const escaped = escapeHTML(text)
  const paragraphs = escaped.split(/\n\n+/)
  return paragraphs
    .map(p => {
      const trimmed = p.trim()
      if (!trimmed) return ''
      return '<p>' + trimmed.replace(/\n/g, '<br>') + '</p>'
    })
    .join('\n')
}

/**
 * Open a clean print window and trigger the browser print dialog.
 * Falls back to a hidden iframe if popups are blocked.
 */
function openAndPrint(html) {
  const printWindow = window.open('', '_blank', 'width=860,height=640')

  if (printWindow) {
    printWindow.document.write(html)
    printWindow.document.close()
    // document.write is synchronous — DOM is ready, no need to wait for 'load'.
    // SetTimeout(0) defers print() past the current call stack so the browser
    // has a chance to complete layout before the print dialog blocks it.
    setTimeout(() => {
      printWindow.addEventListener('afterprint', () => {
        printWindow.close()
      }, { once: true })
      printWindow.print()
    }, 0)
    return
  }

  // Fallback: popup blocked — use hidden iframe
  const iframe = document.createElement('iframe')
  iframe.style.cssText = 'position:fixed;top:0;left:0;width:0;height:0;border:none;visibility:hidden;'
  document.body.appendChild(iframe)

  const iframeDoc = iframe.contentDocument || iframe.contentWindow.document
  iframeDoc.write(html)
  iframeDoc.close()

  iframe.contentWindow.addEventListener('afterprint', () => {
    document.body.removeChild(iframe)
  }, { once: true })

  setTimeout(() => {
    iframe.contentWindow.print()
  }, 0)
}

/**
 * Composable: export markdown or plain-text content as PDF via browser print.
 *
 * Usage:
 *   const { exportPDF } = useExportPDF()
 *   exportPDF(markdownContent, 'My Document')
 *
 * The function renders markdown to HTML, wraps it in a print-optimized page,
 * and opens the browser's print dialog (where the user can choose "Save as PDF").
 *
 * @returns {{ exportPDF: (content: string, title?: string, contentType?: 'markdown'|'text') => void }}
 */
export function useExportPDF() {
  function exportPDF(content, title = '', contentType = 'markdown') {
    const bodyHTML = contentType === 'markdown'
      ? md.render(content || '')
      : plainTextToHTML(content || '')
    const titleStr = title || '未命名文档'
    const html = buildPrintHTML(titleStr, bodyHTML)
    openAndPrint(html)
  }

  return { exportPDF }
}
