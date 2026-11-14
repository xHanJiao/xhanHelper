#!/bin/bash
# 书阅读器（BOOK 模式）模板自查：真 WKWebView 里跑九条断言（两种宽度各跑一遍），不截图。
#
# 验的是设计文档 `docs/design/epub-reader/design.md` §5.5 / §5.7 / §六 在 WebKit 的真实行为：
#   1 章节注入：正文出现、块结构正确
#   2 章末「下一章」按钮存在，且不进拍平文本（否则进度偏移被它污染）
#   3 进度往返：position() → 滚走 → restorePosition() 回到同一句
#   4 划线：mark 文本 == quote、正文一个字都不变
#   5 改字号后同一句不漂（按字符偏移回位）
#   6 翻页模式：多栏生成、turnPage 步长 == 栏宽 + 栏距
#   7 翻页模式下划线仍画得出（跨栏那段也得画）
#   8 页宽设得比可用宽度大时，栏宽跟着缩、仍能翻页（右栏面板常比 728 窄）
#   9 目录锚点：collectAnchors 给出偏移、scrollToAnchor 落在同一句
#
# 页面用**真实模板**（reader-template.py --book 现抠），排版/划线样式与线上同一份。
#
# 用法：client/xHelper/scripts/book-reader-probe.sh
# 退出码：0 全过 / 1 有断言失败 / 2 环境或加载问题
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
MARKED="$HERE/../xHelperMac/Resources/marked.min.js"
TMP="${TMPDIR:-/tmp}"
HTML="$TMP/book-reader-probe.html"
BIN_DIR="$TMP/book-reader-probe-bin"
BIN="$BIN_DIR/run"

mkdir -p "$BIN_DIR"
EMPTY_MD="$TMP/book-reader-probe-empty.md"
: > "$EMPTY_MD"

python3 "$HERE/reader-template.py" --book "$MARKED" '书' '' "$EMPTY_MD" '[]' > "$HTML"

python3 - "$HTML" <<'PY'
import sys
p = sys.argv[1]
hook = r"""<script nonce="testnonce">
(function () {
  var content = document.getElementById('content');
  var rows = [];
  function add(name, ok, detail) { rows.push({ name: name, ok: !!ok, detail: String(detail) }); }

  var paragraphs = [];
  for (var i = 1; i <= 30; i++) {
    paragraphs.push('<p id="p' + i + '">第' + i + '段：这一段用来检验书阅读器的章节渲染、进度偏移与划线，句子带编号以便唯一命中。</p>');
  }
  var chapterHTML = '<h2>第一章</h2>' + paragraphs.join('') +
    '<img id="fig" src="fig.png" width="120" height="90">';

  function charRect(node, off) {
    var len = node.nodeType === 3 ? node.nodeValue.length : 0;
    if (!len) return null;
    var i = Math.min(Math.max(off, 0), len - 1);
    var r = document.createRange();
    r.setStart(node, i); r.setEnd(node, i + 1);
    var rs = r.getClientRects();
    return rs.length ? rs[0] : r.getBoundingClientRect();
  }

  window.__bookProbe = function () {
    reader.setChapter(chapterHTML, 'file:///tmp/', true);

    /* ---------- 1 章节注入 ---------- */
    var ps = content.querySelectorAll('p');
    var text = content.textContent;
    add('1 章节注入', ps.length === 30 && text.indexOf('第1段') >= 0 && text.indexOf('第30段') >= 0,
        '段落=' + ps.length + ' 首段' + (text.indexOf('第1段') >= 0 ? '在' : '不在') +
        ' 末段' + (text.indexOf('第30段') >= 0 ? '在' : '不在'));

    /* ---------- 2b 书排版类必须挂上（否则整套字号/行距/页宽 CSS 是死代码） ---------- */
    add('2b html.book 生效', document.documentElement.classList.contains('book'),
        'class="' + document.documentElement.className + '" / measure=' +
        getComputedStyle(document.documentElement).getPropertyValue('--measure'));

    /* ---------- 2 章末按钮不进拍平文本 ----------
       判据：拿「下一章」当 quote 去划线。拍平文本里若收了这个按钮，locate 就会命中并在按钮里
       包出 mark；收了它就等于给进度偏移与划线锚点都掺了正文之外的字符。 */
    var nextBtn = content.querySelector('.chapter-next');
    reader.setAnnotations(JSON.stringify([
      { id: 'btn', quote: '下一章', prefix: '', suffix: '', note: '', orphaned: false }
    ]));
    var btnMarks = content.querySelectorAll('mark.hl[data-id="btn"]').length;
    add('2 章末按钮不进拍平文本', !!nextBtn && btnMarks === 0,
        '按钮' + (nextBtn ? '存在' : '缺失') + ' / 按钮被当成正文划线=' + btnMarks + ' 处');
    reader.setAnnotations('[]');

    /* ---------- 3 进度往返 ---------- */
    window.scrollTo(0, 1400);
    void content.offsetHeight;
    var off0 = reader.position();
    window.scrollTo(0, 0);
    void content.offsetHeight;
    var scrolledBack = window.scrollY;
    reader.restorePosition(off0);
    void content.offsetHeight;
    var off1 = reader.position();
    var scrollAfter = window.scrollY;
    /* 纵向模式的落点是「行」：恢复后取样到的是那一行的行首字，与原点最多差一行。
       真正要求的是**收敛**（再存再恢复不再漂），所以第二轮必须完全相等。 */
    reader.restorePosition(off1);
    void content.offsetHeight;
    var off2 = reader.position();
    add('3 进度往返', off0 > 100 && scrolledBack === 0 && scrollAfter > 200 &&
        Math.abs(off1 - off0) <= 60 && off2 === off1,
        'offset ' + off0 + ' → 回顶部 → 恢复后 ' + off1 + '（差 ' + (off1 - off0) +
        '）→ 再往返 ' + off2 + '/ scrollY 0 → ' + Math.round(scrollAfter));

    /* ---------- 4 划线 ---------- */
    var allText = content.textContent;
    var quote = '第5段：这一段用来检验书阅读器的章节渲染';
    reader.setAnnotations(JSON.stringify([
      { id: 'a1', quote: quote, prefix: '', suffix: '', note: '', orphaned: false }
    ]));
    var marks = [].slice.call(content.querySelectorAll('mark.hl[data-id="a1"]'));
    var markText = marks.map(function (m) { return m.textContent; }).join('');
    add('4 划线', marks.length > 0 && markText === quote && content.textContent === allText,
        'marks=' + marks.length + ' mark文本' + (markText === quote ? '==quote' : '!=quote!!') +
        ' 正文' + (content.textContent === allText ? '不变' : '被改!!'));

    /* ---------- 5 改字号后同一句不漂 ---------- */
    reader.restorePosition(off0);
    void content.offsetHeight;
    var before = reader.position();
    var h0 = content.scrollHeight;
    reader.setTypo({ fs: 26 });
    void content.offsetHeight;
    var after = reader.position();
    var h1 = content.scrollHeight;
    add('5 改字号回同一句', Math.abs(after - before) <= 60 && h1 !== h0,
        'offset ' + before + ' → ' + after + '（差 ' + (after - before) + '）/ 高度 ' + h0 + ' → ' + h1);

    /* ---------- 6 翻页模式 ---------- */
    reader.setReadingMode('paged');
    void content.offsetHeight;
    var box = content.getBoundingClientRect();
    var span = box.width + 64;
    var pages = content.scrollWidth / span;
    var sl0 = content.scrollLeft;
    var turned = reader.turnPage(1);
    void content.offsetHeight;
    var sl1 = content.scrollLeft;
    reader.turnPage(-1);
    void content.offsetHeight;
    var sl2 = content.scrollLeft;
    add('6 翻页模式', pages >= 2 && turned && Math.abs((sl1 - sl0) - span) <= 2 && Math.abs(sl2 - sl0) <= 2,
        '≈' + pages.toFixed(2) + ' 页 / scrollLeft ' + Math.round(sl0) + ' → ' + Math.round(sl1) +
        '（步长 ' + Math.round(span) + '）→ 翻回来 ' + Math.round(sl2));

    /* ---------- 7 翻页模式下划线跨栏仍画得出 ---------- */
    var t2 = content.textContent;
    var quote2 = '第12段：这一段用来检验书阅读器的章节渲染、进度偏移与划线';
    reader.setAnnotations(JSON.stringify([
      { id: 'a2', quote: quote2, prefix: '', suffix: '', note: '', orphaned: false }
    ]));
    var marks2 = [].slice.call(content.querySelectorAll('mark.hl[data-id="a2"]'));
    var markText2 = marks2.map(function (m) { return m.textContent; }).join('');
    add('7 翻页模式划线', marks2.length > 0 && markText2 === quote2 && content.textContent === t2,
        'marks=' + marks2.length + ' mark文本' + (markText2 === quote2 ? '==quote' : '!=quote!!') +
        ' 正文' + (content.textContent === t2 ? '不变' : '被改!!') +
        ' / 文本里有该句=' + (content.textContent.indexOf('第12段') >= 0));

    /* ---------- 8 页宽大于可用宽度（右栏面板常比 728 窄） ---------- */
    reader.setTypo({ measure: 2000 });
    void content.offsetHeight;
    var w2 = content.clientWidth;
    var span2 = w2 + 64;
    var pages2 = content.scrollWidth / span2;
    content.scrollLeft = 0;
    var turned2 = reader.turnPage(1);
    void content.offsetHeight;
    add('8 页宽超过可用宽度', w2 <= window.innerWidth + 2 && pages2 >= 2 && turned2,
        '栏宽 ' + Math.round(w2) + ' ≤ 视口 ' + window.innerWidth + ' / ≈' + pages2.toFixed(2) +
        ' 页 / 翻页' + (turned2 ? '生效' : '失效!!'));

    /* ---------- 9 目录锚点：换算偏移 + 跳过去落在同一句 ---------- */
    reader.setReadingMode('scroll');
    void content.offsetHeight;
    var anchors = [];
    try { anchors = JSON.parse(reader.collectAnchors(JSON.stringify(['p10', 'p20']))); } catch (e) { anchors = []; }
    var a10 = null, a20 = null;
    anchors.forEach(function (a) { if (a.id === 'p10') a10 = a; if (a.id === 'p20') a20 = a; });
    reader.scrollToAnchor('p20');
    void content.offsetHeight;
    var after = reader.position();
    add('9 目录锚点跳转', !!a10 && !!a20 && a10.offset < a20.offset && Math.abs(after - a20.offset) <= 40,
        '锚点偏移 p10=' + (a10 ? a10.offset : '-') + ' p20=' + (a20 ? a20.offset : '-') +
        ' / 跳转后取样 ' + after + '（差 ' + (a20 ? after - a20.offset : '-') + '）');

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
echo "—— 书阅读器九条断言 ——"
PROBE_FN=__bookProbe "$BIN" "$HTML"
echo "—— 窄面板（360pt，模拟右栏）——"
PROBE_FN=__bookProbe PROBE_WIDTH=360 "$BIN" "$HTML"
