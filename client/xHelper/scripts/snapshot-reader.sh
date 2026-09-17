#!/bin/bash
# 在真引擎（WKWebView）里加载阅读版模板、合成选区 / 画划线并截图到输出目录。
#
# 用途：验证「真实渲染 / 选区长什么样」。选区的绘制是引擎行为，Blink 与 WebKit 差别很大，
# WebKit 会把跨段整块选中画成一整块大矩形 —— 所以 Chrome（check-reader-html.sh）测不了这类
# 问题，得看这里的图。模板从 Swift 源码现抠（reader-template.py），不会漂移。
#
# 用法：client/xHelper/scripts/snapshot-reader.sh [输出目录] [light|dark]
#       默认 $TMPDIR/reader-snapshot + light（外观必须显式定，跟随系统会截出说不清的图）
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
MARKED="$HERE/../xHelperMac/Resources/marked.min.js"
OUT_DIR="${1:-${TMPDIR:-/tmp}/reader-snapshot}"
MODE="${2:-light}"
TMP="${TMPDIR:-/tmp}"
HTML="$TMP/reader-snapshot.html"
BIN_DIR="$TMP/reader-snapshot-bin"
BIN="$BIN_DIR/run"

[ -f "$MARKED" ] || { echo "找不到 $MARKED"; exit 1; }

mkdir -p "$OUT_DIR"

# 测试正文：长短段落交替 + 小标题，用来观察跨段选中时整块区域被填充 / 贴字
MD="$TMP/reader-snapshot.md"
cat > "$MD" <<'MD'
## 一、结论先行

所以判断"这到底几个进程"，得看它是 process 还是 thread，端口数量会骗人。

插件的通信是四条 ZMQ 通道（进程之间传消息用的库）+ 几个共享对象；这套东西在《插件与 MCP》那篇里讲过。

短段落。

---

## 三、它们之间怎么说话

它同时用了四种"说话方式"，各管一段：

一个更长的段落，用来观察跨段选中时整块区域被填充的效果，还有段与段之间那些空白。

---

## 五、容器 li 里的一段

- 会话性质为测试，内容刻意无意义、不承载真实信息，不作为笔记/记忆记录
- 话题风格要求：煞有介事（严肃语气 + 空洞内核）
- 用户中途追加的风格要求：不够幽默、不够无厘头，需提高荒诞度
- 已产出素材共 4 条，均附逻辑点评：
  - 信息论角度：无意义对话信息熵更高，更难被压缩
  - 「所有男人必定都当过孙子，但只有一部分男人才能成为爷爷」

## 七、一张表

| 维度 | 取值 |
|---|---|
| 写入时机 | 请求期 |
| 存储位置 | 向量库 |
MD

python3 "$HERE/reader-template.py" "$MARKED" 'N.E.K.O 记忆系统切面调研' \
  '<span>调研</span><span>Phase-18</span>' "$MD" '[]' > "$HTML"

# 合成选区 + 探针的入口（脚本合成的选区与手动拖拽的绘制结果相同）
python3 - "$HTML" <<'PY'
import sys
p = sys.argv[1]
hook = """<script>
window.__step = function (name) {
  if (name === 'collapse') { window.getSelection().removeAllRanges(); return 'collapsed'; }
  if (name === 'marks') { return window.__marks('容器 li 里的一段'); }
  if (name === 'marks-table') { return window.__marks('写入时机'); }
  if (name === 'marks-overlap') { return window.__marks('各管一段'); }
  if (name === 'marks-longnote') { return window.__marksLong(); }
  if (name === 'marks-longnote-scroll') { return window.__marksLongScroll(); }
  var r = window.__applySel(name);
  /* 选区在视口外就滚过去（只对选中、不折叠：折叠那张要在同一视口比残留像素） */
  var s = window.getSelection();
  if (s.rangeCount) {
    var rect = s.getRangeAt(0).getBoundingClientRect();
    window.scrollTo(0, window.scrollY + rect.top - 220);
  }
  return r;
};
/* 划线批注（用假数据，别把真实文档内容放进仓库）：
   跨块 / 带批注（出气泡）/ 落在 li 里 / 标题打头跨块 / **标题 + 紧跟列表**（真机丢字那个形状）/ 跨表格行。
   锚点文字决定滚到哪一段，保证那一类缺陷落在视口里 */
window.__marks = function (anchorText) {
  window.getSelection().removeAllRanges();
  /* 先滚到目标位置再画：画完之后再滚，截图会带上「滚走那块」的陈旧像素
     （DOM 探针显示那里根本没有背景，纯粹是渲染层的缓存） */
  var anchor = null;
  [].slice.call(document.querySelectorAll('#content p, #content h2, #content td')).forEach(function (el) {
    if (!anchor && el.textContent.indexOf(anchorText) >= 0) anchor = el;
  });
  if (anchor) window.scrollTo(0, window.scrollY + anchor.getBoundingClientRect().top - 60);
  /* 真机上「划完字少了」就是在这儿丢的：正文文本必须一字不差 */
  var before = document.getElementById('content').textContent;
  reader.setAnnotations(JSON.stringify([
    { id: 'k1', quote: '这套东西在《插件与 MCP》那篇里讲过。 短段落。', note: '', prefix: '', suffix: '', orphaned: false },
    { id: 'k2', quote: '各管一段', note: '这句是重点，回头写进总结。', prefix: '', suffix: '', orphaned: false },
    { id: 'k3', quote: '已产出素材共 4 条', note: '', prefix: '', suffix: '', orphaned: false },
    { id: 'k4', quote: '三、它们之间怎么说话 它同时用了四种', note: '', prefix: '', suffix: '', orphaned: false },
    { id: 'k5', quote: '写入时机 请求期', note: '', prefix: '', suffix: '', orphaned: false },
    { id: 'k6', quote: '五、容器 li 里的一段 会话性质为测试', note: '', prefix: '', suffix: '', orphaned: false },
    /* k7 与 k2 重叠：两条拟合成同一处的波浪线（真机上应该只看见一条，hover 才提示重叠） */
    { id: 'k7', quote: '管一段', note: '', prefix: '', suffix: '', orphaned: false }
  ]));
  var byId = [];
  ['k1', 'k2', 'k3', 'k4', 'k5', 'k6', 'k7'].forEach(function (id) {
    byId.push(id + '=' + document.querySelectorAll('mark.hl[data-id="' + id + '"]').length);
  });
  var after = document.getElementById('content').textContent;
  return 'marks=' + document.querySelectorAll('mark.hl').length
       + ' bubbles=' + document.querySelectorAll('span.ann-bubble').length
       + ' textLen=' + before.length + (before === after ? '=不变' : '→' + after.length + '!!丢字')
       + ' ' + byId.join(' ');
};
/* 长批注（P5 把 AI 的长回答存成批注之后的常态）：hover 浮层必须自己封顶内滚 ——
   它是 position: fixed，没有 max-height 时高出视口的那截永远拿不到，滚轮还会跑去滚正文。
   故意把划线放在视口下方 70% 处，逼出「上/下翻转 + 封顶」的最坏摆放 */
window.__marksLong = function () {
  window.getSelection().removeAllRanges();
  var target = null;
  [].slice.call(document.querySelectorAll('#content p')).forEach(function (el) {
    if (!target && el.textContent.indexOf('各管一段') >= 0) target = el;
  });
  if (!target) return 'no-target';
  window.scrollTo(0, window.scrollY + target.getBoundingClientRect().top - window.innerHeight * 0.7);
  var note = '';
  for (var i = 0; i < 60; i++) {
    note += '第 ' + i + ' 行：这是把 AI 的长回答存成批注之后的样子，用来把 hover 浮层撑高。'
          + 'https://example.com/a/' + i + '\\n';
  }
  reader.setAnnotations(JSON.stringify([
    { id: 'L1', quote: '各管一段', note: note, prefix: '', suffix: '', orphaned: false }
  ]));
  var m = document.querySelector('mark.hl[data-id="L1"]');
  if (!m) return 'no-mark';
  var content = document.getElementById('content');
  function hover() {
    var r = m.getBoundingClientRect();
    content.dispatchEvent(new MouseEvent('mousemove', {
      bubbles: true, clientX: r.left + 2, clientY: r.top + r.height / 2
    }));
  }
  hover();
  /* 定时补 hover：窗口一出现，真实光标（在屏幕上某处、不在划线上）会产生 mousemove，
     把浮层按时收掉 —— 只 hover 一次的话，700ms 后截图时它已经关了，图里空无一物。
     定时器 id 留着：下一个 job（浮层内滚）要先停掉它，否则它会把「被滚掉关了的浮层」重新开起来，
     内滚那一步就测不出来了 */
  window.__hoverTimer = window.setInterval(hover, 120);
  var pv = document.getElementById('popover');
  if (!pv || !pv.classList.contains('open')) return 'popover-not-open';
  var cs = getComputedStyle(pv);
  return 'popover 高 ' + pv.offsetHeight + ' / 可滚 ' + pv.scrollHeight + ' / client ' + pv.clientHeight
       + ' / 视口 ' + window.innerHeight + ' overflowY=' + cs.overflowY
       + ' overscroll=' + cs.overscrollBehavior
       + (pv.offsetHeight <= window.innerHeight ? ' 封顶OK' : ' 超出视口!!');
};
/* 浮层内滚：长批注的核心动作，也是真机踩过的坑 —— capture 阶段的 window scroll 监听
   连后代元素的滚动都收得到，以前它会把浮层一起关掉（用户看到的是「一滚浮层就没了，滚轮落到正文」）。
   这里接着上一个 job 的浮层，停掉补 hover 的定时器（否则它会把关掉的浮层重新开起来，什么都测不到），
   滚 200px，延迟 600ms（浏览器派发 scroll 是异步的）采一次真实状态，采完再补一次 hover 供截图 */
window.__marksLongScroll = function () {
  var pv = document.getElementById('popover');
  if (!pv || !pv.classList.contains('open')) return 'popover-not-open';
  if (window.__hoverTimer) { window.clearInterval(window.__hoverTimer); window.__hoverTimer = null; }
  var m = document.querySelector('mark.hl[data-id="L1"]');
  var content = document.getElementById('content');
  pv.scrollTop = 200;
  window.setTimeout(function () {
    window.__innerScrollState = (pv.classList.contains('open') ? 'open' : 'closed')
      + ' scrollTop=' + pv.scrollTop + ' scrollHeight=' + pv.scrollHeight
      + ' clientHeight=' + pv.clientHeight;
    if (!m) return;
    var r = m.getBoundingClientRect();
    content.dispatchEvent(new MouseEvent('mousemove', {
      bubbles: true, clientX: r.left + 2, clientY: r.top + r.height / 2
    }));
  }, 600);
  return 'set scrollTop → ' + pv.scrollTop + '（600ms 后采真实状态）';
};
window.__applySel = function (kind) {
  var ps = document.querySelectorAll('#content p'), hs = document.querySelectorAll('#content h2');
  if (!ps.length || !hs.length) return 'no-content';
  var r = document.createRange();
  if (kind === 'blocks') { r.setStart(ps[0], 0); r.setEnd(hs[1].childNodes[0], hs[1].childNodes[0].length); }
  else if (kind === 'partial') { r.setStart(ps[0].childNodes[0], 2); r.setEnd(ps[0].childNodes[0], 12); }
  else if (kind === 'nested') {
    /* 容器 li（自己带一段文本 + 子列表）整段选中：查「容器 li 自己的文本」会不会被漏掉 */
    var outer = document.querySelector('#content > ul');
    if (!outer || outer.children.length < 4) return 'no-list';
    var li = outer.children[3], sub = li.querySelector('ul li');
    r.setStart(li.childNodes[0], 0);
    r.setEnd(sub.childNodes[0], sub.childNodes[0].length);
  }
  else { r.setStart(ps[0].childNodes[0], 2); r.setEnd(ps[0].childNodes[0], ps[0].childNodes[0].length); }
  var s = window.getSelection(); s.removeAllRanges(); s.addRange(r);
};
/* 探针：选区本身 + 自绘高亮的簿记 + 选区覆盖到的每个文本节点有几个矩形。
   「簿记已清但像素还在」= 引擎没刷新高亮层；「覆盖了但矩形数 = 0」= 引擎算不出那个节点的矩形 */
window.__probe = function () {
  var s = window.getSelection();
  var o = { collapsed: s.isCollapsed, ranges: s.rangeCount, selLen: String(s).length,
            hasSel: !!(window.CSS && CSS.highlights.has('sel')),
            size: (window.CSS && CSS.highlights.has('sel')) ? CSS.highlights.get('sel').size : 0,
            cover: [],
            popover: (function () {
              var pv = document.getElementById('popover');
              if (!pv) return '-';
              var r = pv.getBoundingClientRect();
              return (pv.classList.contains('open') ? 'open' : 'closed')
                   + ' rect=' + Math.round(r.left) + ',' + Math.round(r.top) + ' '
                   + Math.round(r.width) + 'x' + Math.round(r.height)
                   + ' scrollTop=' + Math.round(pv.scrollTop)
                   + ' 卡内滚动条口=' + (pv.offsetWidth - pv.clientWidth)
                   + ' scrollbarWidth=' + getComputedStyle(pv).scrollbarWidth
                   + ' innerScroll=' + (window.__innerScrollState || '-');
            })() };
  if (!s.rangeCount) return JSON.stringify(o);
  var sel = s.getRangeAt(0);
  var walker = document.createTreeWalker(document.getElementById('content'), NodeFilter.SHOW_TEXT, null);
  var n, i = 0;
  while ((n = walker.nextNode())) {
    if (sel.intersectsNode(n)) {
      var sub = document.createRange();
      sub.setStart(n, n === sel.startContainer ? sel.startOffset : 0);
      sub.setEnd(n, n === sel.endContainer ? sel.endOffset : n.length);
      var t = n.nodeValue || '';
      o.cover.push(i + ':' + sub.getClientRects().length + 'x' +
                   (t.length > 16 ? t.slice(0, 16) + '…' : t));
    }
    i++;
  }
  return JSON.stringify(o);
};
</script>
"""
s = open(p, encoding='utf-8').read()
open(p, 'w', encoding='utf-8').write(s.replace('</body>', hook + '</body>', 1))
PY

mkdir -p "$OUT_DIR"

if [ ! -x "$BIN" ] || [ "$HERE/snapshot-reader.swift" -nt "$BIN" ]; then
  echo "编译 snapshot-reader.swift ..."
  [ -f "$BIN_DIR" ] && rm -f "$BIN_DIR"
  mkdir -p "$BIN_DIR"
  xcrun swiftc -O -o "$BIN" "$HERE/snapshot-reader.swift" -framework WebKit -framework AppKit
fi

"$BIN" "$HTML" "$OUT_DIR" "$MODE"
echo "截图目录: ${OUT_DIR}（外观: ${MODE}）"
