#!/bin/bash
# 书阅读器（BOOK 模式）模板自查：真 WKWebView 里跑全部断言（两种宽度各跑一遍），不截图。
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
#  10 到底再翻 = 自动换节（前后都认），换节途中不重复举手
#  10b 小数栏宽（640.5）、10c 末页差 3px 也算到头
#  11 滚动模式到底再滚一下才换节，一次拖动只换一次
#  12 方向键：两种模式 × 四个键 + ⌥⌘ 都认
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
    /* 模板的 post() 先找 `window.webkit.messageHandlers.reader`（没有原生 handler 时
       `window.webkit` 不存在，post 静默丢弃）。探针把它换成收集器，才能验「到底再翻
       有没有举手换节」——这一步必须在任何会 post 的动作之前做。 */
    window.__posts = [];
    window.webkit = { messageHandlers: { reader: { postMessage: function (m) {
      (window.__posts = window.__posts || []).push(m);
    } } } };
    function posts(type) {
      return window.__posts.filter(function (p) { return p && p.type === type; }).length;
    }
    function wait(ms) { var t = Date.now(); while (Date.now() - t < ms) { /* 手势判定靠时间间隔 */ } }
    function wheel(dy) {
      document.dispatchEvent(new WheelEvent('wheel', { deltaY: dy, bubbles: true, cancelable: true }));
    }
    function press(key, mods) {
      var init = { key: key, bubbles: true, cancelable: true };
      if (mods) { for (var k in mods) { init[k] = mods[k]; } }
      document.dispatchEvent(new KeyboardEvent('keydown', init));
    }

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
    reader.snapPage();   /* 翻页有动画，位置在 220ms 后才落定；吸附 = 直接落到目标页 */
    void content.offsetHeight;
    var sl1 = content.scrollLeft;
    reader.turnPage(-1);
    reader.snapPage();
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
    reader.snapPage();
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

    /* ---------- 10 到底再翻 = 自动换节（前后都认，小数栏宽也要认） ---------- */
    /* 走真机那条路：从第一页一页一页翻到末页，再翻一下必须换节。
       栏宽带小数时 `scrollWidth - clientWidth`（都取整）和真实末页差零点几像素，
       「x === before」这种一字不差的判断会永远不成立 —— 这里直接把 640.5 摆出来测。 */
    function advanceChecks(name, measure) {
      reader.setTypo({ measure: measure });
      reader.setChapter(chapterHTML, 'file:///tmp/', true, true);
      reader.setReadingMode('paged');
      void content.offsetHeight;
      content.scrollLeft = 0;
      reader.snapPage();
      var pages = 0;
      while (pages < 100) {
        window.__posts = [];
        reader.turnPage(1);
        if (posts('nextSection') === 1) break;
        reader.snapPage();
        void content.offsetHeight;
        pages++;
      }
      var reachedEnd = posts('nextSection') === 1;
      var endPos = content.scrollLeft, sw = content.scrollWidth, cw = content.clientWidth;
      var maxCalc = Math.max(0, sw - cw);
      /* 换节途中（新正文还没到）再翻：不能再举手，否则一次连滚会连跳两节 */
      window.__posts = [];
      reader.turnPage(1);
      var locked = posts('nextSection') === 0;
      /* 反向：第一页再往前翻 → 上一节末页（新 setChapter 把锁放掉） */
      reader.setChapter(chapterHTML, 'file:///tmp/', true, true);
      reader.setReadingMode('paged');
      void content.offsetHeight;
      content.scrollLeft = 0;
      reader.snapPage();
      window.__posts = [];
      var back = reader.turnPage(-1);
      var reachedStart = posts('prevSection') === 1;
      /* 反向的落点：超大偏移必须夹到最后一个字 → 停在**末页**（不是首屏） */
      reader.restorePosition(999999);
      void content.offsetHeight;
      var endX = Math.max(0, content.scrollWidth - content.clientWidth);
      var landedEnd = Math.abs(content.scrollLeft - endX) < 2;
      var w = content.getBoundingClientRect().width;
      add(name, reachedEnd && locked && back && reachedStart && landedEnd,
          '栏宽 ' + (Math.round(w * 10) / 10) + ' / 翻 ' + pages + ' 页到末页（末页 ' +
          Math.round(endPos) + '，算出来的 max ' + maxCalc + '，差 ' + Math.round(maxCalc - endPos) + 'px）' +
          ' / 末页再翻=' + (reachedEnd ? '换节' : '没换!!') +
          ' / 换节途中不重复=' + locked +
          ' / 第一页往前=' + (reachedStart ? '换节' : '没换!!') +
          ' / 上一节落末页=' + (landedEnd ? Math.round(content.scrollLeft) : '否!!'));
    }
    advanceChecks('10 到底再翻 = 换节', 728);
    advanceChecks('10b 小数栏宽（640.5）也换节', 640.5);
    /* 真机上「→ 在末页死活不换节」的形态：位置离算出来的末页差几像素。
       这条钉的是判据 ——「这一下挪不动接近一整页」就算到头，不是「一字不差等于 max」。 */
    (function nudgeChecks() {
      reader.setTypo({ measure: 728 });
      reader.setChapter(chapterHTML, 'file:///tmp/', true, true);
      reader.setReadingMode('paged');
      void content.offsetHeight;
      var maxNudge = Math.max(0, content.scrollWidth - content.clientWidth);
      content.scrollLeft = Math.max(0, maxNudge - 3);
      window.__posts = [];
      reader.turnPage(1);
      var ok = posts('nextSection') === 1;
      add('10c 末页差 3px 也算到头', ok,
          '位置 ' + content.scrollLeft + '（末页 ' + maxNudge + '）再翻 → ' +
          (ok ? '换节' : '只挪 3px，不换!!'));
    })();
    reader.setTypo({ measure: 728 });

    /* ---------- 11 滚动模式：到底再滚一下才换节，一次拖动只换一次 ---------- */
    reader.setChapter(chapterHTML, 'file:///tmp/', true, false);
    reader.setReadingMode('scroll');
    void content.offsetHeight;
    window.scrollTo(0, 0);
    void document.documentElement.offsetHeight;
    window.__posts = [];
    wheel(120); wheel(120); wheel(120); wheel(120); wheel(120);   /* 没到底：一下都不换 */
    var midPosts = posts('nextSection');
    wait(200);                                                    /* 停手 → 下一串算新手势 */
    window.scrollTo(0, document.documentElement.scrollHeight);
    void document.documentElement.offsetHeight;
    window.__posts = [];
    wheel(120); wheel(120); wheel(120); wheel(120); wheel(120);   /* 到底连滚 5 下：只换一次 */
    var bottomPosts = posts('nextSection');
    wait(400);                                                    /* 冷却过了，但换节锁还在 */
    wheel(120);
    var lockedPosts = posts('nextSection');
    add('11 滚动模式到底再滚 = 换节',
        midPosts === 0 && bottomPosts === 1 && lockedPosts === 1,
        '没到底不换=' + (midPosts === 0) + ' / 到底连滚 5 下换 ' + bottomPosts + ' 次' +
        ' / 换节途中累计 ' + lockedPosts + ' 次');

    /* ---------- 12 方向键：两种模式 × 四个键 + ⌥⌘ ---------- */
    function toPagedEnd() {
      reader.setTypo({ measure: 728 });
      reader.setChapter(chapterHTML, 'file:///tmp/', true, true);
      reader.setReadingMode('paged');
      void content.offsetHeight;
      content.scrollLeft = content.scrollWidth;   /* 顶到真实末页 */
      reader.snapPage();
    }
    function toPagedStart() {
      reader.setChapter(chapterHTML, 'file:///tmp/', true, true);
      reader.setReadingMode('paged');
      void content.offsetHeight;
      content.scrollLeft = 0;
      reader.snapPage();
    }
    function toScrollBottom() {
      reader.setChapter(chapterHTML, 'file:///tmp/', true, true);
      reader.setReadingMode('scroll');
      void content.offsetHeight;
      window.scrollTo(0, document.documentElement.scrollHeight);
      void document.documentElement.offsetHeight;
    }
    var keyFails = [];
    function checkKey(label, setup, key, mods, expect) {
      setup();
      var sel = window.getSelection();
      if (sel) sel.removeAllRanges();
      window.__posts = [];
      press(key, mods);
      var got = window.__posts.filter(function (p) {
        return p && (p.type === 'nextSection' || p.type === 'prevSection' || p.type === 'sectionJump');
      });
      var ok = got.length === 1 && got[0].type === expect;
      if (!ok) {
        keyFails.push(label + '→' + (got.length
          ? got.map(function (p) { return p.type + (p.delta ? '(' + p.delta + ')' : ''); }).join('+')
          : '没反应'));
      }
      return ok;
    }
    checkKey('→ 末页', toPagedEnd, 'ArrowRight', null, 'nextSection');
    checkKey('↓ 末页', toPagedEnd, 'ArrowDown', null, 'nextSection');
    checkKey('← 首页', toPagedStart, 'ArrowLeft', null, 'prevSection');
    checkKey('↑ 首页', toPagedStart, 'ArrowUp', null, 'prevSection');
    checkKey('滚动 ↓ 到底', toScrollBottom, 'ArrowDown', null, 'nextSection');
    checkKey('滚动 → 到底', toScrollBottom, 'ArrowRight', null, 'nextSection');
    checkKey('⌥⌘→', toPagedStart, 'ArrowRight', { metaKey: true, altKey: true }, 'sectionJump');
    checkKey('⌥⌘←', toPagedEnd, 'ArrowLeft', { metaKey: true, altKey: true }, 'sectionJump');
    add('12 方向键（两种模式 × 四个键 + ⌥⌘）', keyFails.length === 0,
        keyFails.length ? '不通过：' + keyFails.join(' / ') : '八个组合全对');

    /* ---------- 13-19 批 16：脚注 / 链接 / 内联图 / srcset ---------- */
    var IMG1 = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8AAAwAB/gHrKAAAAABJRU5ErkJggg==';
    var noteHTML =
      '<h2>注释测试</h2>' +
      '<p id="np1">正文一<sup><a epub:type="noteref" href="#fn-1"><img src="' + IMG1 + '" alt="注"/></a></sup>后面还有字。</p>' +
      '<aside epub:type="footnote" id="fn-1"><ol class="duokan-footnote-content"><li class="duokan-footnote-item">这是第一条注释，应该出现在卡片里。</li></ol></aside>' +
      '<p id="np2">正文二<sup><a epub:type="noteref" href="#fn-2">2</a></sup>。</p>' +
      '<aside epub:type="footnote" id="fn-2"><p>第二条注释，带 <strong>加粗</strong>。</p></aside>' +
      '<p id="np3">跨文件<sup><a epub:type="noteref" href="notes.xhtml#en-1">3</a></sup>。</p>' +
      '<p id="np4">普通锚点<sup><a href="#np5">4</a></sup>。</p>' +
      '<p id="np5">普通段落，不应弹注释。</p>' +
      '<p id="np6">外链 <a href="https://example.com/a?b=1#c">example</a>；书内 <a href="ch2.xhtml#s2">ch2</a>；页内 <a href="#np5">锚点</a>。</p>' +
      '<p id="np7">行内图 <img id="inline-img" src="' + IMG1 + '"/> 后面还有字。</p>' +
      '<p id="np8"><img id="block-img" src="' + IMG1 + '"/></p>' +
      '<figure><img id="fig-img" src="' + IMG1 + '"/><figcaption>图注</figcaption></figure>' +
      '<picture><source id="src-source" srcset="../Images/a.png 1x, ../Images/b.png 2x"/><img src="' + IMG1 + '"/></picture>' +
      '<audio src="demo.mp3">音频</audio>' +
      '<p id="np9"><ruby>東京<rt>とうきょう</rt></ruby></p>' +
      '<p id="np10">role 注标：<span role="doc-noteref"><a href="#fn-3">注</a></span>。</p>' +
      '<aside role="doc-footnote" id="fn-3"><p>用 ARIA role 写的注释。</p></aside>' +
      '<div role="doc-footnote" id="fn-4"><p>div 容器的注释，也应藏起来。</p></div>' +
      '<p id="np11">尾注引用<sup><a epub:type="noteref" href="#en-same">6</a></sup>。</p>' +
      '<section epub:type="endnotes"><ol><li id="en-same"><p>同文件尾注列表。</p></li></ol></section>' +
      '<section epub:type="footnotes"><ol><li id="en-free"><p>没人指的尾注区块，应该还能读。</p></li></ol></section>';

    function lastPost(type) {
      var list = window.__posts.filter(function (p) { return p && p.type === type; });
      return list.length ? list[list.length - 1] : null;
    }
    function clickEl(el) {
      el.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    }

    reader.setTypo({ measure: 728 });
    reader.setReadingMode('scroll');
    reader.setChapter(noteHTML, 'file:///tmp/', true, true);
    void content.offsetHeight;

    var fnRefs = content.querySelectorAll('a.fn-ref');
    var note1 = document.getElementById('fn-1');
    var note2 = document.getElementById('fn-2');
    var note3 = document.getElementById('fn-3');
    var note4 = document.getElementById('fn-4');
    var hiddenOK = [note1, note2, note3, note4].every(function (el) {
      return getComputedStyle(el).display === 'none';
    });
    reader.find('这是第一条注释');
    var noteFind = lastPost('findResult');
    reader.clearFind();
    var findOK = !!noteFind && noteFind.count === 0;
    add('13 脚注识别与隐藏',
        fnRefs.length === 5 && hiddenOK && findOK,
        '注标=' + fnRefs.length + '（期望 5）/ 各容器隐藏=' + hiddenOK + ' / 注释文字 find=' + (noteFind ? noteFind.count : '-'));

    /* 点注标：卡片出现；不能落进 IMG → 大图浮层 */
    var noteIcon = content.querySelector('#np1 img');
    window.__posts = [];
    clickEl(noteIcon);
    var pop = document.getElementById('note-pop');
    var popOpen = pop.classList.contains('open');
    var popText = pop.querySelector('.note-body').textContent;
    var noLightbox = posts('image') === 0;
    add('14 注释卡片',
        popOpen && popText.indexOf('第一条注释') >= 0 && noLightbox,
        '打开=' + popOpen + ' / 内容=' + (popText.indexOf('第一条注释') >= 0) + ' / 未弹大图=' + noLightbox);
    add('15 注释卡片可关闭', (function () {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
      var closedByEsc = !pop.classList.contains('open');
      clickEl(noteIcon);
      window.dispatchEvent(new Event('scroll'));
      var closedByScroll = !pop.classList.contains('open');
      clickEl(noteIcon);
      document.getElementById('note-scrim').click();
      var closedByScrim = !pop.classList.contains('open');
      return closedByEsc && closedByScroll && closedByScrim;
    })(), 'Esc / 滚动 / 遮罩 都能收');

    /* 跨文件尾注：目标不在 DOM → 发 noteFile */
    window.__posts = [];
    clickEl(content.querySelector('#np3 a.fn-ref'));
    var nf = lastPost('noteFile');
    add('16 跨文件注释发 noteFile',
        !!nf && (nf.href || '').indexOf('notes.xhtml') >= 0 && nf.frag === 'en-1',
        nf ? ('href=' + nf.href + ' frag=' + nf.frag) : '没发');

    /* 链接分发：外链 / 书内 / 普通页内锚点 */
    var extA = content.querySelector('a[data-external]');
    var bookA = content.querySelector('a[data-book-href]:not(.fn-ref)');
    window.__posts = [];
    clickEl(extA);
    var extPost = lastPost('openExternal');
    window.__posts = [];
    clickEl(bookA);
    var bookPost = lastPost('bookLink');
    window.__posts = [];
    clickEl(content.querySelector('#np4 a[href^="#"]'));
    var plainOK = posts('bookLink') === 0 && posts('openExternal') === 0 && !pop.classList.contains('open');
    add('17 链接分发',
        !!extPost && (extPost.href || '').indexOf('https://example.com') === 0 &&
        !!bookPost && (bookPost.href || '').indexOf('ch2.xhtml') >= 0 && plainOK,
        '外链=' + (extPost ? 'openExternal' : '没发') +
        ' / 书内=' + (bookPost ? 'bookLink' : '没发') +
        ' / 无类型 sup=' + (plainOK ? '按锚点' : '误弹/误跳'));

    /* 内联图 vs 独占图（含注标图标 1em） */
    var inlineImg = document.getElementById('inline-img');
    var blockImg = document.getElementById('block-img');
    var figImg = document.getElementById('fig-img');
    var iconStyle = getComputedStyle(noteIcon);
    add('18 内联图 / 独占图',
        getComputedStyle(inlineImg).display === 'inline' &&
        getComputedStyle(blockImg).display === 'block' &&
        getComputedStyle(figImg).display === 'block' &&
        iconStyle.display === 'inline' && parseFloat(iconStyle.width) < 30,
        '行内=' + getComputedStyle(inlineImg).display +
        ' / 独占=' + getComputedStyle(blockImg).display +
        ' / figure=' + getComputedStyle(figImg).display +
        ' / 注标=' + iconStyle.display + ' ' + iconStyle.width);

    /* srcset 重写 + 音视频移除 */
    var srcset = document.getElementById('src-source').getAttribute('srcset') || '';
    add('19 srcset / 音视频',
        srcset.indexOf('file:') >= 0 && srcset.indexOf('../') < 0 && !content.querySelector('audio'),
        'srcset=' + srcset + ' / audio=' + (content.querySelector('audio') ? '还在' : '已移除'));

    /* ---------- 20-22 真机撞到的三类漏判 / 样式 ---------- */
    /* role 写在包注标的外层 <span> 上（ARIA 写法）：必须认出注标、aside 必须藏 */
    var roleRef = content.querySelector('#np10 a');
    window.__posts = [];
    clickEl(roleRef);
    var rolePopText = pop.querySelector('.note-body').textContent;
    add('20 role 注标（role 在外层 span）',
        roleRef.classList.contains('fn-ref') && pop.classList.contains('open') &&
        rolePopText.indexOf('用 ARIA role 写的注释') >= 0,
        '注标=' + roleRef.classList.contains('fn-ref') +
        ' / 弹卡片=' + pop.classList.contains('open') +
        ' / 内容=' + rolePopText.slice(0, 12));
    document.getElementById('note-scrim').click();

    /* 注释容器不止 ASIDE：div[role=doc-footnote] 与同文件 endnotes 列表都要藏 */
    var endnotes = document.getElementById('en-same').closest('section');
    var freeNotes = document.getElementById('en-free').closest('section');
    add('21 注释容器按语义藏',
        getComputedStyle(note4).display === 'none' &&
        getComputedStyle(endnotes).display === 'none' &&
        getComputedStyle(freeNotes).display !== 'none',
        'div 容器=' + getComputedStyle(note4).display +
        ' / 被指到的尾注区块=' + getComputedStyle(endnotes).display +
        ' / 没人指的尾注区块=' + getComputedStyle(freeNotes).display + '（应可见）');

    /* 链接必须看得出来：书内 / 页内 / 外链统一绿字点线 + pointer；注标不掺和 */
    var bodyColor = getComputedStyle(content).color;
    var extStyle = getComputedStyle(extA);
    var bookStyle = getComputedStyle(bookA);
    var anchorA = content.querySelector('#np6 a[href^="#"]');
    var noteRefA = content.querySelector('#np1 a');
    add('22 链接样式',
        extStyle.color !== bodyColor && bookStyle.color !== bodyColor &&
        extStyle.cursor === 'pointer' && bookStyle.cursor === 'pointer' &&
        bookStyle.borderBottomStyle === 'dotted' &&
        anchorA.classList.contains('book-link') &&
        !noteRefA.classList.contains('book-link'),
        '正文色=' + bodyColor + ' / 书内=' + bookStyle.color + ' ' + bookStyle.cursor +
        ' / 页内带类=' + anchorA.classList.contains('book-link') +
        ' / 注标不带类=' + !noteRefA.classList.contains('book-link'));

    /* ---------- 23-24 宽表 ---------- */
    /* 前面垫几段正文：一章只有一张表时撑不满一页，翻页步长恒为 0，测不出「表格不撑破分栏」 */
    var tableHTML = '';
    for (var fp = 0; fp < 4; fp++) {
      tableHTML += '<p>' + new Array(200).join('填充正文，用来把这一章撑过一页。') + '</p>';
    }
    tableHTML += '<h2>宽表</h2><table><tr>';
    for (var tc = 1; tc <= 10; tc++) tableHTML += '<th>列 ' + tc + ' 标题</th>';
    tableHTML += '</tr>';
    for (var tr = 1; tr <= 4; tr++) {
      tableHTML += '<tr>';
      for (var tc2 = 1; tc2 <= 10; tc2++) {
        /* 用不可断行的长 token：中文可以在任意字间断行，自动布局会缩回栏宽，测不出溢出 */
        tableHTML += '<td>cell-' + tc2 + '-' + new Array(24).join('x') + '</td>';
      }
      tableHTML += '</tr>';
    }
    tableHTML += '</table>';
    reader.setTypo({ measure: 728 });
    reader.setChapter(tableHTML, 'file:///tmp/', true, true);
    reader.setReadingMode('paged');
    void content.offsetHeight;
    var twrap = content.querySelector('.table-scroll');
    var span23 = content.clientWidth + 64;
    var overflow = !!twrap && twrap.scrollWidth > twrap.clientWidth + 1;
    var startX = content.scrollLeft;
    reader.turnPage(1);
    reader.snapPage();
    var moved = content.scrollLeft - startX;
    add('23 宽表横滚',
        overflow && Math.abs(moved - span23) <= 2,
        '窄栏内溢出=' + (twrap ? (twrap.scrollWidth + '>' + twrap.clientWidth) : '没有容器') +
        ' / 翻一页步长=' + Math.round(moved) + '（期望 ' + Math.round(span23) + '）');

    /* 翻页模式下滚轮先给宽表：没到头不翻页，到头才翻 */
    (function tableWheel() {
      var wrapEl = content.querySelector('.table-scroll');
      var cellEl = wrapEl ? wrapEl.querySelector('td') : null;
      if (!wrapEl || !cellEl) { add('24 滚轮让位给宽表', false, '没有表格容器'); return; }
      reader.snapPage();
      var beforePage = content.scrollLeft;
      wrapEl.scrollLeft = 0;
      var ev1 = new WheelEvent('wheel', { deltaY: 120, bubbles: true, cancelable: true });
      cellEl.dispatchEvent(ev1);
      var letGo = !ev1.defaultPrevented;
      var pageStill = content.scrollLeft === beforePage;
      wrapEl.scrollLeft = wrapEl.scrollWidth - wrapEl.clientWidth;
      var ev2 = new WheelEvent('wheel', { deltaY: 120, bubbles: true, cancelable: true });
      cellEl.dispatchEvent(ev2);
      var tookOver = ev2.defaultPrevented;
      reader.snapPage();
      add('24 滚轮让位给宽表', letGo && pageStill && tookOver,
          '没到头不抢=' + letGo + ' / 页没动=' + pageStill + ' / 到头后接管=' + tookOver);
    })();

    /* 25 书签摘录：snippet(off, len) 从拍平文本取一段，空白折叠、不带块分隔符 */
    (function bookmarkSnippet() {
      reader.setChapter('<p id="bm1">第一段书签文本，用来验证摘录。</p><p>第二段不要选中。</p>',
                        'file:///tmp/', true, true);
      void content.offsetHeight;
      var head = reader.snippet(0, 4);
      var cross = reader.snippet(15, 6);   /* 起点落在块间分隔符上 */
      add('25 书签摘录',
          head === '第一段书' && cross === '第二段不要',
          '开头=' + JSON.stringify(head) + ' / 跨段=' + JSON.stringify(cross));
    })();

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
echo "—— 书阅读器全部断言 ——"
PROBE_FN=__bookProbe "$BIN" "$HTML"
echo "—— 窄面板（360pt，模拟右栏）——"
PROBE_FN=__bookProbe PROBE_WIDTH=360 "$BIN" "$HTML"
