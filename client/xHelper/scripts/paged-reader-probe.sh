#!/bin/bash
# 横向翻页（CSS 多栏）可行性探针。真 WKWebView 里跑六条断言，不截图。
#
# 验的是 `docs/design/epub-reader/design.md §5.7` 那套方案在 WebKit 的实际行为：
#   1 多栏是否真的生成（scrollWidth > clientWidth）
#   2 跨栏划线：正文文本不变、mark 文本 == 命中区间（沿用文档阅读器的硬约束）
#   3 跨栏选区：选中文本与拍平文本逐字一致
#   4 查找命中后能滚到正确栏
#   5 字号变化后能回到同一句（按字符偏移恢复）
#   6 竖长图不会把整栏撑破（max-height 约束是否生效）
#
# 页面用**真实的阅读器模板**（reader-template.py 从 Swift 源码现抠），只额外注入分栏 CSS
# 与探针脚本 —— 排版/字号/划线样式都与线上一致，探针结论才作数。
#
# 用法：client/xHelper/scripts/paged-reader-probe.sh
# 退出码：0 六条全过 / 1 有断言失败 / 2 环境或加载问题
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
MARKED="$HERE/../xHelperMac/Resources/marked.min.js"
TMP="${TMPDIR:-/tmp}"
HTML="$TMP/paged-reader-probe.html"
BIN_DIR="$TMP/paged-reader-probe-bin"
BIN="$BIN_DIR/run"

[ -f "$MARKED" ] || { echo "找不到 $MARKED"; exit 1; }
mkdir -p "$BIN_DIR"

# 正文：40 句带编号的长段落（必然横跨 3 栏）+ 两处查找关键词（第二处在末栏之后）
MD="$TMP/paged-reader-probe.md"
python3 - "$MD" <<'PY'
import sys
sentence = "第{}句：这一段用来把正文撑到三栏以上，内容本身没有意义，但每一句都带编号，确保任意跨栏切片在全文中唯一。"
long_para = "分栏探针长句：" + "".join(sentence.format(i) for i in range(1, 41))
md = f"""## 分栏探针

{long_para}

分栏探针关键词 第一次出现，用来验证查找后的横向滚动；它不该把读者带到相邻的那一栏。

第二段内容够长才能让后面出现更多栏，所以再说一遍分栏探针关键词，放在靠后的位置，确保命中点在首栏之外。
"""
open(sys.argv[1], "w", encoding="utf-8").write(md)
PY

python3 "$HERE/reader-template.py" "$MARKED" '分栏探针' '<span>探针</span>' "$MD" '[]' > "$HTML"

python3 - "$HTML" <<'PY'
import sys
p = sys.argv[1]
hook = r"""<script nonce="testnonce">
(function () {
  var BLOCK = { P:1, DIV:1, H1:1, H2:1, H3:1, H4:1, LI:1, BLOCKQUOTE:1, PRE:1, TD:1, TH:1 };

  function fold(s) { return String(s).replace(/[\s\u00a0]+/g, ' ').trim(); }

  function charRect(node, off) {
    var len = node.nodeType === 3 ? node.nodeValue.length : 0;
    if (!len) return null;
    var i = Math.min(Math.max(off, 0), len - 1);
    var r = document.createRange();
    r.setStart(node, i); r.setEnd(node, i + 1);
    var rs = r.getClientRects();
    return rs.length ? rs[0] : r.getBoundingClientRect();
  }

  window.__pagedProbe = function () {
    var rows = [];
    function add(name, ok, detail) { rows.push({ name: name, ok: !!ok, detail: String(detail) }); }

    var content = document.getElementById('content');
    var html = document.documentElement;

    function blockOf(node) {
      var el = node.nodeType === 1 ? node : node.parentNode;
      while (el && el !== content) {
        if (el.nodeType === 1 && BLOCK[el.tagName]) return el;
        el = el.parentNode;
      }
      return content;
    }
    function flatMap() {
      var walker = document.createTreeWalker(content, NodeFilter.SHOW_TEXT, {
        acceptNode: function (n) {
          return n.parentNode && n.parentNode.closest && n.parentNode.closest('.blocked-img')
            ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT;
        }
      });
      var nodes = [], text = '', lastBlock = null, n;
      while ((n = walker.nextNode())) {
        var b = blockOf(n);
        if (lastBlock && b !== lastBlock) text += '\n';
        nodes.push({ node: n, start: text.length });
        text += n.nodeValue;
        lastBlock = b;
      }
      return { nodes: nodes, text: text };
    }
    function rangeAt(flat, start, end) {
      var range = document.createRange(), gotStart = false, gotEnd = false;
      for (var i = 0; i < flat.nodes.length; i++) {
        var it = flat.nodes[i], len = it.node.nodeValue.length;
        if (!gotStart && start >= it.start && start <= it.start + len) {
          range.setStart(it.node, start - it.start); gotStart = true;
        }
        if (!gotEnd && end >= it.start && end <= it.start + len) {
          range.setEnd(it.node, end - it.start); gotEnd = true;
        }
        if (gotStart && gotEnd) break;
      }
      return (gotStart && gotEnd) ? range : null;
    }
    function offsetOf(flat, node, off) {
      for (var i = 0; i < flat.nodes.length; i++) {
        if (flat.nodes[i].node === node) return flat.nodes[i].start + off;
      }
      return -1;
    }
    function rectLefts(rects) {
      var lefts = [];
      [].forEach.call(rects, function (r) { if (r.width > 0) lefts.push(r.left); });
      return lefts;
    }
    function spread(lefts) {
      return lefts.length ? Math.max.apply(null, lefts) - Math.min.apply(null, lefts) : 0;
    }

    /* ---------- 模板自带的翻页 CSS（不再内联覆盖，测的就是线上那套几何） ---------- */
    var style = document.createElement('style');
    style.textContent =
      'html.paged, html.paged body { overflow: hidden; height: 100%; }' +
      'html.paged .page { height: 100vh; padding-bottom: 36px; }';
    document.head.appendChild(style);

    html.classList.add('paged');
    /* 页宽设置（--measure）决定一栏宽度：显式设成 728，避免探针页没有 `html.book`
       时变量缺失、整条 max-width 失效（那样测出来的就是一栏 = 视口，漏掉限宽那条） */
    reader.setTypo({ measure: 728 });
    void content.offsetHeight;

    /* 采样：视口里撒点，取每个点下面的字符偏移。一屏一栏时，第 N 页与第 N+1 页
       覆盖的偏移必然前后不重叠；若一屏塞得下两栏，翻一页只挪了半屏，两页就会重叠。 */
    function sampleOffsets() {
      var b = content.getBoundingClientRect();
      var flat = flatMap(), out = [];
      for (var gy = 0; gy < 6; gy++) {
        for (var gx = 0; gx < 4; gx++) {
          var r = document.caretRangeFromPoint(
            b.left + 8 + (b.width - 16) * gx / 3,
            b.top + 8 + (b.height - 16) * gy / 5);
          if (!r || !content.contains(r.startContainer)) continue;
          var off = offsetOf(flat, r.startContainer, r.startOffset);
          if (off >= 0) out.push(off);
        }
      }
      return out;
    }

    content.scrollLeft = 0;
    var page0 = sampleOffsets();
    var turnOK = reader.turnPage(1);
    reader.snapPage();
    var page1 = sampleOffsets();
    content.scrollLeft = 0;
    void content.offsetHeight;

    var max0 = page0.length ? Math.max.apply(null, page0) : -1;
    var min1 = page1.length ? Math.min.apply(null, page1) : -1;
    var tmplW = content.clientWidth;
    var expectW = Math.min(728, window.innerWidth - 56);   /* 一栏 = 页宽，窄了就跟着缩 */
    add('0 一屏一栏（模板 CSS）',
        page0.length >= 8 && page1.length >= 8 && turnOK && max0 < min1 &&
        Math.abs(tmplW - expectW) <= 1,
        'clientWidth=' + tmplW + '（期望 ' + expectW + '）scrollWidth=' + content.scrollWidth +
        ' / 第1页取样 ' + page0.length + ' 个（偏移 ≤' + max0 + '）' +
        ' / 第2页 ' + page1.length + ' 个（偏移 ≥' + min1 + '）' +
        (max0 < min1 ? ' 不重叠' : ' 重叠!! 一屏不止一栏'));

    var avail = Math.max(260, window.innerHeight - content.getBoundingClientRect().top - 36);
    content.style.height = avail + 'px';
    content.scrollLeft = 0;
    void content.offsetHeight;

    var box = content.getBoundingClientRect();
    var pageSpan = box.width + 64;
    var pages = content.scrollWidth / pageSpan;
    add('1 多栏生成', content.scrollWidth > box.width + 64 && pages >= 2,
        'scrollWidth=' + content.scrollWidth + ' clientWidth=' + content.clientWidth +
        ' 估算页数=' + pages.toFixed(2) + ' 列高=' + Math.round(avail));

    /* ---------- 2 跨栏划线 ---------- */
    /* 取「栏边界附近」的一段：先找到首栏右侧中点处的字符偏移，再以它为中心截 120 字。
       这样 quote 必然横跨两栏，且带编号的句子保证全文唯一（locate 不会命中别处）。 */
    /* 滚到第二栏，栏首那个字就是边界偏移（比按坐标撒点稳） */
    content.scrollLeft = box.width + 64;
    void content.offsetHeight;
    var f0 = flatMap();
    var edgeCaret = document.caretRangeFromPoint(box.left + 6, box.top + 6);
    var edgeOff = (edgeCaret && content.contains(edgeCaret.startContainer))
      ? offsetOf(f0, edgeCaret.startContainer, edgeCaret.startOffset) : -1;
    content.scrollLeft = 0;
    void content.offsetHeight;

    var quote = '', qStart = -1;
    for (var s0 = Math.max(0, edgeOff - 60); edgeOff >= 0 && s0 <= edgeOff + 60; s0 += 10) {
      var cand = f0.text.slice(s0, s0 + 120);
      if (cand.length === 120 && cand.indexOf('\n') === -1) { qStart = s0; quote = cand; break; }
    }
    var before = content.textContent;
    if (quote) {
      reader.setAnnotations(JSON.stringify([
        { id: 'p1', quote: quote, prefix: '', suffix: '', note: '', orphaned: false }
      ]));
    }
    var marks = [].slice.call(content.querySelectorAll('mark.hl[data-id="p1"]'));
    var markText = marks.map(function (m) { return m.textContent; }).join('');
    var after = content.textContent;
    var markLefts = [];
    marks.forEach(function (m) {
      var r = document.createRange();
      r.selectNodeContents(m);
      markLefts = markLefts.concat(rectLefts(r.getClientRects()));
    });
    var markSpan = spread(markLefts);
    add('2 跨栏划线', !!quote && after === before && markText === quote &&
        marks.length > 0 && markSpan > box.width,
        'marks=' + marks.length + ' 文本' + (after === before ? '不变' : '被改!!') +
        ' mark文本' + (markText === quote ? '==quote' : '!=quote!!') +
        ' 边界偏移=' + edgeOff + ' 矩形横跨=' + Math.round(markSpan) + 'px（栏宽 ' + Math.round(box.width) + '）');

    /* ---------- 3 跨栏选区 ---------- */
    var selRange = quote ? rangeAt(flatMap(), qStart, qStart + quote.length) : null;
    var selText = '', selSpan = 0;
    if (selRange) {
      var s = window.getSelection();
      s.removeAllRanges();
      s.addRange(selRange);
      selText = String(s);
      selSpan = spread(rectLefts(selRange.getClientRects()));
      s.removeAllRanges();
    }
    add('3 跨栏选区', !!selText && fold(selText) === fold(quote) && selSpan > box.width,
        '选中 ' + selText.length + ' 字 / quote ' + quote.length + ' 字 / 折空白后' +
        (fold(selText) === fold(quote) ? '一致' : '不一致!!') +
        ' 矩形横跨=' + Math.round(selSpan) + 'px');

    /* ---------- 4 查找后滚到命中栏 ---------- */
    content.scrollLeft = 0;
    void content.offsetHeight;
    var token = '分栏探针关键词';
    var f2 = flatMap();
    var hitAt = f2.text.lastIndexOf(token);
    var hitRange = hitAt >= 0 ? rangeAt(f2, hitAt, hitAt + token.length) : null;
    var insideBefore = false, insideAfter = false, moved = false;
    if (hitRange) {
      var pr = content.getBoundingClientRect();
      var r0 = hitRange.getBoundingClientRect();
      insideBefore = r0.left >= pr.left - 1 && r0.right <= pr.right + 1;
      if (!insideBefore) {
        content.scrollLeft += (r0.left - pr.left) - 40;
        var r1 = hitRange.getBoundingClientRect();
        var pr2 = content.getBoundingClientRect();
        insideAfter = r1.left >= pr2.left - 1 && r1.right <= pr2.right + 1;
        moved = content.scrollLeft > 0;
      }
    }
    add('4 查找滚到命中栏', !!hitRange && !insideBefore && insideAfter && moved,
        '命中初始在视口内=' + insideBefore + ' / 滚动 scrollLeft=' + Math.round(content.scrollLeft) +
        ' / 滚动后在视口内=' + insideAfter);

    /* ---------- 5 字号变化后回到同一句 ---------- */
    /* 先滚到第二栏，再记「视口左上角那个字」的偏移 —— 这才是真实恢复路径 */
    content.scrollLeft = box.width + 64;
    void content.offsetHeight;
    var prA = content.getBoundingClientRect();
    var caret = document.caretRangeFromPoint(prA.left + 6, prA.top + 6);
    var f3 = flatMap();
    var off0 = (caret && content.contains(caret.startContainer))
      ? offsetOf(f3, caret.startContainer, caret.startOffset) : -1;

    reader.setFontSize(26);
    void content.offsetHeight;

    var f4 = flatMap();
    var charRange = off0 >= 0 ? rangeAt(f4, off0, off0 + 1) : null;
    var prB = content.getBoundingClientRect();
    var target = charRange ? charRange.getBoundingClientRect() : null;
    if (target) content.scrollLeft += (target.left - prB.left) - 8;
    var final = charRange ? charRange.getBoundingClientRect() : null;
    var prC = content.getBoundingClientRect();
    var lineH = 26 * 1.78;
    /* 翻页模式只能把「该字所在的那一栏」摆到视口里，字在栏内的纵向位置由重排决定 ——
       所以判据是「同一偏移的字落在当前栏内」，不是「贴到栏顶」 */
    var okX = final && final.left >= prC.left - 2 && final.left <= prC.left + 80;
    var okY = final && final.top >= prC.top - 2 && final.top <= prC.bottom - lineH;
    add('5 字号变化回到同一句', off0 >= 0 && !!final && okX && okY,
        'offset=' + off0 + ' 恢复后 x=' + (final ? Math.round(final.left - prC.left) : '-') +
        ' y=' + (final ? Math.round(final.top - prC.top) : '-') + '（列高 ' + Math.round(prC.height) + '）');

    reader.setFontSize(20.06);
    content.scrollLeft = 0;
    void content.offsetHeight;

    /* ---------- 6 竖长图不撑破栏 ---------- */
    var canvas = document.createElement('canvas');
    canvas.width = 1200; canvas.height = 1800;
    var g = canvas.getContext('2d');
    g.fillStyle = '#cde3ea'; g.fillRect(0, 0, 1200, 1800);
    g.fillStyle = '#33544e'; g.font = '160px -apple-system'; g.fillText('FIG', 120, 420);

    var wrap = document.createElement('div');
    var img = new Image();
    img.setAttribute('width', '1200');
    img.setAttribute('height', '1800');
    img.src = canvas.toDataURL('image/png');
    wrap.appendChild(img);
    content.appendChild(wrap);

    img.style.maxHeight = 'none';
    img.style.maxWidth = '100%';
    void content.offsetHeight;
    var raw = img.getBoundingClientRect();
    var rawRatio = raw.height ? raw.width / raw.height : 0;
    var colH = content.clientHeight;

    /* 竖长图在分栏里不会被「撑破」—— overflow-y:hidden 会把超出栏高的部分**静默裁掉**
       （实测不约束时 rect 被压到列高、宽高比也坏了）。真正的判据是：
       加上 max-height 后，整张图按原比例装得进一栏。 */
    /* 约束 = max-width/max-height + object-fit: contain（图按原比例装进盒子，盒子不出栏） */
    img.style.objectFit = 'contain';
    img.style.maxHeight = '100%';
    void content.offsetHeight;
    var pctH = Math.round(img.getBoundingClientRect().height);

    img.style.maxHeight = (avail - 8) + 'px';
    void content.offsetHeight;
    var fixed = img.getBoundingClientRect();

    add('6 大图不撑破栏',
        fixed.height <= colH && content.scrollHeight <= content.clientHeight + 2,
        '不加约束 ' + Math.round(raw.width) + '×' + Math.round(raw.height) +
        '（横跨整条滚动区、超出栏高 → 被裁） / max-height:100% → 高 ' + pctH +
        ' / max-height:' + (avail - 8) + 'px + object-fit:contain → ' +
        Math.round(fixed.width) + '×' + Math.round(fixed.height) + ' / 列高 ' + colH);

    /* ---------- 7 翻页：有动画 + 停在整页 ---------- */
    /* 起点先故意放在半页：turnPage 必须落到整页边界，不能带着半页偏移累加 */
    var span = pageSpan;
    var maxL = Math.max(0, content.scrollWidth - content.clientWidth);
    function onPage() {
      var x = content.scrollLeft;
      if (Math.abs(x - maxL) < 1) return true;   /* 最后一页贴在内容尾部 */
      return Math.abs(x / span - Math.round(x / span)) < 0.001;
    }
    content.scrollLeft = 0;
    reader.snapPage();
    reader.turnPage(1);
    var animated = content.scrollLeft < span * 0.5;   /* 没瞬间跳过去 → 真的有补间 */
    reader.snapPage();                                 /* 动画途中吸附 = 落到目标页 */
    var nextOK = onPage() && content.scrollLeft === span;
    content.scrollLeft = span + Math.round(span / 2);
    reader.turnPage(1);
    reader.snapPage();
    var halfOK = onPage();
    reader.turnPage(-1);
    reader.snapPage();
    var backOK = onPage();
    add('7 翻页动画 + 停在整页', animated && nextOK && halfOK && backOK,
        '有动画=' + animated + ' / 从 0 → ' + Math.round(content.scrollLeft) + ' 整页=' + nextOK +
        ' / 从半页翻后整页=' + halfOK + ' / 再退回整页=' + backOK +
        '（步长 ' + span + '，末页 ' + maxL + '）');

    return JSON.stringify({ rows: rows });
  };
})();
</script>
"""
s = open(p, encoding='utf-8').read()
open(p, 'w', encoding='utf-8').write(s.replace('</body>', hook + '</body>', 1))
PY

if [ ! -x "$BIN" ] || [ "$HERE/paged-reader-probe.swift" -nt "$BIN" ]; then
  echo "编译 paged-reader-probe.swift ..."
  rm -f "$BIN"
  xcrun swiftc -O -o "$BIN" "$HERE/paged-reader-probe.swift" -framework WebKit -framework AppKit
fi

echo "页面：$HTML"
echo "—— 六条断言 ——"
"$BIN" "$HTML"
