#!/bin/bash
# 自查 DocumentReaderView 的 HTML/JS（正文渲染 / 查找 / 目录跳转）。
#
# 为什么要有这个脚本：阅读器的模板与 JS 是嵌在 Swift 源码里的一整块字符串，
# `swiftc -typecheck` 只能证明它是个合法字符串，证明不了里面的 JS 能跑。
# 这里把模板原样抠出来，灌进 headless Chrome 跑一遍，断言真实行为 ——
# 抠的是源文件本身，所以模板改了这里跟着变，不存在副本漂移。
#
# 用法：client/xHelper/scripts/check-reader-html.sh
#
# 注意：模板的**抠取**在 reader-template.py 里（两个自查脚本共用，避免副本漂移）；
# 但选区的**绘制**是引擎行为、Chrome 与 WKWebView 差别很大，别拿这个脚本判选区长短，
# 那类问题用 snapshot-reader.sh 在真引擎里看。
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
MARKED="$HERE/../xHelperMac/Resources/marked.min.js"
OUT="${TMPDIR:-/tmp}/reader-check.html"
CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

[ -f "$MARKED" ] || { echo "找不到 $MARKED"; exit 1; }
[ -x "$CHROME" ] || { echo "找不到 Chrome：$CHROME"; exit 1; }

# 正文：故意放可数的词 + 同名标题 + 代码块里的命中 + 一个 mermaid 占位
MD="${TMPDIR:-/tmp}/reader-check.md"
cat > "$MD" <<'MD'
## 一、结论先行

记忆系统的第一处提及，用来数命中。

## 二、对照

记忆系统的第二处，还有 `行内代码` 和**粗体**。

| 维度 | A | B |
|---|---|---|
| 写入时机 | 请求期 | 对话后 |

| 字段 | 说明 |
| --- | --- |
| 内容类型 | 允许的类型标签 |
| 内容类型 | 库 新闻 |

```python
# 记忆系统 第三处（在代码块里，也必须能搜到）
x = 1
```

## 小结

第一个小结段落。

### 2.1 子节

小节正文。

## 小结

第二个小结段落，同名标题的序号要靠 occurrence 区分。

- 清单第一项，用来测跨列表项
- 清单第二项，后半截

路径在 `~/xhanCodes` 下面，用来测行内代码里的波浪号（服务端会把 `~` 投影掉）。

宽度&nbsp;标记，用来测 NBSP 空白也要折成空格。

方括号： [内容] 与 [ ]

行尾两空格硬换行  
这是下一行

ZZMDIMG0X

![配图](/images/abc.png)
MD

PNG="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8AAAwAB/gHrKAAAAABJRU5ErkJggg=="
python3 "$HERE/reader-template.py" "$MARKED" 'N.E.K.O 记忆系统切面调研' \
  '<span>调研</span><span>Phase-18</span>' "$MD" "[{\"light\":\"$PNG\",\"dark\":\"$PNG\"}]" 20.06 > "$OUT"

python3 - "$OUT" <<'PY'
import sys
p = sys.argv[1]
html = open(p, encoding='utf-8').read()

# 拦截 JS→Swift 的 postMessage，把回话收进 __msgs；并记录 scrollIntoView 命中的元素
probe = """<script>
window.__msgs = [];
window.webkit = { messageHandlers: { reader: { postMessage: function (m) { window.__msgs.push(m); } } } };
window.__scrolled = [];
Element.prototype.scrollIntoView = function () { window.__scrolled.push(this); };
</script>
"""
html = html.replace('<meta charset="utf-8">', '<meta charset="utf-8">\n' + probe, 1)

# 断言脚本：全部通过则把 PASS 写进 #result
tests = """<script>
var fails = [];
try {
var okCount = 0;
function ok(name, cond, extra) { okCount++; if (!cond) fails.push(name + (extra ? ' [' + extra + ']' : '')); }
function last(type) { for (var i = window.__msgs.length - 1; i >= 0; i--) if (window.__msgs[i].type === type) return window.__msgs[i]; }
var c = document.getElementById('content');

ok('ready 已上报', !!last('ready'));
ok('markdown 渲染出标题', c.querySelectorAll('h2').length === 4, 'h2=' + c.querySelectorAll('h2').length);
ok('渲染出表格', !!c.querySelector('table tbody tr'));
ok('渲染出代码块', !!c.querySelector('pre code'));
ok('行内代码', !!c.querySelector('p code'));
ok('mermaid 占位被替换', c.querySelectorAll('.mermaid img').length === 0 ? false : c.querySelectorAll('.mermaid img.m-light').length === 1);
ok('mermaid 用的是 data URI', (c.querySelector('.mermaid img.m-light') || {}).src ? c.querySelector('.mermaid img.m-light').src.indexOf('data:image/png') === 0 : false);

// 点图 → 回传 image（交给原生大图浮层）。mermaid 是 data URI，文档配图是绝对 URL。
c.querySelector('.mermaid img.m-light').dispatchEvent(new MouseEvent('click', { bubbles: true }));
var mimg = last('image');
ok('点 mermaid 图回传 image（data URI）',
   !!mimg && typeof mimg.src === 'string' && mimg.src.indexOf('data:image/png') === 0,
   mimg ? String(mimg.src).slice(0, 24) : 'no msg');
var pimg = c.querySelector('#content p img');
ok('渲染出正文配图', !!pimg);
pimg.dispatchEvent(new MouseEvent('click', { bubbles: true }));
var pmsg = last('image');
ok('点正文配图回传 image（绝对 URL）', !!pmsg && /\/images\/abc\.png$/.test(pmsg.src),
   pmsg ? pmsg.src : 'no msg');
ok('标题/标签在外面', document.querySelector('.doc-title').textContent.indexOf('N.E.K.O') === 0 && document.querySelectorAll('.doc-tags span').length === 2);
ok('底色透明', getComputedStyle(document.body).backgroundColor === 'rgba(0, 0, 0, 0)');
ok('字号变量生效', getComputedStyle(document.documentElement).getPropertyValue('--fs').trim() === '20.06px');

// 查找：正文 3 处「记忆系统」（含代码块里的那一处）
reader.find('记忆系统');
var r = last('findResult');
ok('find 计数=3', r && r.count === 3 && r.index === 0, JSON.stringify(r));
ok('CSS.highlights 收下了 find', window.CSS && CSS.highlights.has('find') && CSS.highlights.get('find').size === 3);
ok('当前项高亮', CSS.highlights.get('find-cur').size === 1);

reader.findNext();
ok('findNext → 1', last('findResult').index === 1);
reader.findNext();
ok('findNext → 2', last('findResult').index === 2);
reader.findNext();
ok('findNext 回绕 → 0', last('findResult').index === 0);
reader.findPrev();
ok('findPrev 回绕 → 2', last('findResult').index === 2);

reader.find('zzz不存在zzz');
ok('无命中', last('findResult').count === 0 && last('findResult').index === -1);

reader.clearFind();
ok('clearFind 清掉高亮', !CSS.highlights.has('find'));

// 目录跳转：同名「小结」靠 occurrence 区分（DOM 里是第 0 个和第 1 个）
var jies = [].slice.call(c.querySelectorAll('h2')).filter(function (h) { return h.textContent.trim() === '小结'; });
ok('两处同名小结', jies.length === 2, 'len=' + jies.length);
window.__scrolled = [];
reader.scrollToHeading(2, '小结', 1);
ok('occurrence=1 命中第二个', window.__scrolled.length === 1 && window.__scrolled[0] === jies[1]);
window.__scrolled = [];
reader.scrollToHeading(2, '小结', 0);
ok('occurrence=0 命中第一个', window.__scrolled.length === 1 && window.__scrolled[0] === jies[0]);
window.__scrolled = [];
reader.scrollToHeading(3, '2.1 子节', 0);
ok('按级别匹配 h3', window.__scrolled.length === 1 && window.__scrolled[0].tagName === 'H3');

reader.setFontSize(24);
ok('setFontSize 改的是 --fs', getComputedStyle(document.documentElement).getPropertyValue('--fs').trim() === '24px');

} catch (e) {
  fails.push('抛出异常: ' + (e && e.message ? e.message : e));
}

// ---- 异步部分：选区 / 工具条 / 浮层 / 划线批注，逐拍推进 ----
function writeResult() {
  var el = document.createElement('div');
  el.id = 'result';
  el.textContent = fails.length ? ('FAIL ' + fails.join(' | ')) : 'PASS';
  document.body.appendChild(el);
  var st = document.createElement('div');
  st.id = 'stats';
  st.textContent = okCount + ' 条断言';
  document.body.appendChild(st);
}
function mk(id, quote, note, prefix, suffix, orphaned) {
  return { id: id, quote: quote, note: note || '', prefix: prefix || '', suffix: suffix || '',
           orphaned: !!orphaned, created_at: '2026-09-17T21:04:00+08:00' };
}
function put(list) { reader.setAnnotations(JSON.stringify(list)); }

function selectionChecks() {
  var rng = document.createRange();
  rng.selectNodeContents(c.querySelector('p'));
  var sel = window.getSelection();
  sel.removeAllRanges();
  sel.addRange(rng);
  // 选区由引擎自己画（自绘的两个毛病见 CSS 里那段注释），所以这里只钉两件事：
  // 我们别再自注册 sel 高亮；原生选区本身可用（复制粘贴不受影响）
  ok('选区不再自绘（引擎画）', !CSS.highlights.has('sel'));
  ok('原生选区可用（String(sel) 有内容）', String(sel).length > 0);
  // ::selection 的 background 必须是 transparent：铺了底 WebKit 就会画跨块大矩形
  var selRules = [];
  function collect(rules) {
    for (var i = 0; i < rules.length; i++) {
      var r = rules[i];
      if (r.selectorText && r.selectorText.indexOf('::selection') >= 0) selRules.push(r);
      if (r.cssRules) collect(r.cssRules);
    }
  }
  collect(document.styleSheets[0].cssRules);
  ok('有 ::selection 规则', selRules.length >= 2, '找到 ' + selRules.length);
  var bgBad = selRules.filter(function (r) { return r.style.backgroundColor !== 'transparent'; });
  ok('::selection 不铺底色', bgBad.length === 0, bgBad.length + ' 条铺了');
  var varBad = selRules.filter(function (r) { return r.style.color.indexOf('var(') >= 0; });
  ok('::selection 的 color 没用 var()（WebKit 解析不出来）', varBad.length === 0,
     varBad.length + ' 条用了 var()');
  sel.removeAllRanges();
}

/* 造一个真实选区 + 派发 mouseup（工具条的入口），等一拍再断言 */
function armSelectionAndMouseUp() {
  var p = c.querySelector('p');                       // 第一段：记忆系统的第一处提及…
  var rng = document.createRange();
  rng.setStart(p.firstChild, 0);
  rng.setEnd(p.firstChild, 10);                       // 「记忆系统的第一处提及」(10 字)
  var sel = window.getSelection();
  sel.removeAllRanges();
  sel.addRange(rng);
  p.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }));
}

function toolbarChecks() {
  ok('mouseup 浮出工具条', document.getElementById('toolbar').classList.contains('open'));
  ok('工具条 3 个动作', document.querySelectorAll('#toolbar button[data-act]').length === 3);

  document.querySelector('#toolbar button[data-act="note"]').click();
  var editor = document.getElementById('note-editor');
  var neSave = document.getElementById('ne-save');
  var neText = document.getElementById('ne-text');
  ok('点「批注」出输入浮层', editor.classList.contains('open'));
  ok('quote 预览已填', document.getElementById('ne-quote').textContent.indexOf('记忆系统的第一处提及') === 0);
  ok('空批注文案=保存为纯划线', neSave.textContent === '保存为纯划线');

  // Esc 不再关浮层（误触会丢正在写的批注）：派发到 textarea 上，浮层必须还在
  neText.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
  ok('Esc 不关闭批注浮层', editor.classList.contains('open'));

  neText.value = '这是批注';
  neText.dispatchEvent(new Event('input'));
  ok('有文字文案=保存批注', neSave.textContent === '保存批注');

  neSave.click();
  var msg = last('create');
  ok('保存发出 create（quote/前缀/后缀/note 齐全）',
     !!msg && msg.quote === '记忆系统的第一处提及' && msg.note === '这是批注'
       && msg.prefix.length > 0 && msg.suffix.length > 0, JSON.stringify(msg));
  ok('保存后浮层收起、选区清空',
     !editor.classList.contains('open') && window.getSelection().isCollapsed);
  ok('create 没有原地包 mark（等 Swift 回推）', c.querySelectorAll('mark.hl').length === 0);
}

function annotationChecks() {
  var text0 = c.textContent;                 // 画任何线之前的正文文本
  var blocks0 = c.querySelectorAll('#content p, #content h2, #content h3, #content li, #content td').length;

  function norm(s) { return String(s || '').replace(/[*_`~|\[\]]/g, '').replace(/\s+/g, ''); }
  function markText(id) {
    var out = '';
    [].slice.call(c.querySelectorAll('mark.hl[data-id="' + id + '"]')).forEach(function (m) { out += m.textContent; });
    return out;
  }
  /* 每画一条都钉三件事：正文文本一字不少、块结构没被搬动、mark 里的字就是命中的那段 */
  function draw(list, expectMarks, id, expectQuote) {
    put(list);
    ok('画线不改正文文本' + (id ? '（' + id + '）' : ''),
       c.textContent === text0, '画前 ' + text0.length + ' 字 → 画后 ' + c.textContent.length + ' 字');
    ok('画线不搬块结构' + (id ? '（' + id + '）' : ''),
       c.querySelectorAll('#content p, #content h2, #content h3, #content li, #content td').length === blocks0
         && c.querySelectorAll('mark p, mark div, mark h1, mark h2, mark h3, mark ul, mark ol, mark li, mark table, mark pre, mark blockquote, h2 h2, h2 ul, h2 li, h2 p').length === 0);
    if (expectMarks !== null && expectMarks !== undefined) {
      ok('mark 段数 = ' + expectMarks + (id ? '（' + id + '）' : ''),
         c.querySelectorAll('mark.hl[data-id="' + id + '"]').length === expectMarks,
         '实际 ' + c.querySelectorAll('mark.hl[data-id="' + id + '"]').length);
    }
    if (id) {
      ok('mark 里就是命中的字（' + id + '）', norm(markText(id)) === norm(expectQuote),
         'mark[' + norm(markText(id)).slice(0, 40) + '] quote[' + norm(expectQuote).slice(0, 40) + ']');
    }
  }

  // 1) 段中（单文本节点）
  draw([mk('a1', '第一个小结段落')], 1, 'a1', '第一个小结段落');

  // 2) 整段
  draw([mk('a2', '第一个小结段落。')], 1, 'a2', '第一个小结段落。');

  // 3) 跨块（p → h3）：一个 id 多个 mark
  draw([mk('a3', '第一个小结段落。 2.1 子节')], 2, 'a3', '第一个小结段落。2.1 子节');

  // 4) 标题打头跨块 —— 真机踩过的形状：起点在 h2 里，块级克隆曾被插进标题
  draw([mk('a4', '一、结论先行 记忆系统的第一处提及')], 2, 'a4', '一、结论先行记忆系统的第一处提及');
  ok('标题仍在外层', c.querySelector('#content > h2').textContent === '一、结论先行');
  ok('段落仍是 h2 的兄弟', c.querySelectorAll('#content > p')[0].textContent.indexOf('记忆系统的第一处提及') === 0);

  // 5) 跨列表项
  draw([mk('a5', '清单第一项，用来测跨列表项 清单第二项')], 2, 'a5', '清单第一项，用来测跨列表项清单第二项');

  // 6) 跨表格行（thead/tbody 不在 BLOCK_TAGS 里，正是以前最容易被塞进 mark 的形状）
  draw([mk('a6', '写入时机 请求期 对话后')], 3, 'a6', '写入时机请求期对话后');

  // 7) 行内标记内（<strong> / <code>）与代码块内
  draw([mk('a7', '粗体')], 1, 'a7', '粗体');
  draw([mk('a8', '记忆系统 第三处')], 1, 'a8', '记忆系统第三处');

  // 7b) DOM 里有字面量行内标记字符（`~/xhanCodes` 渲染后仍带 `~`）。
  //     服务端存的 quote 已投影（`~` 没了），客户端 haystack 也必须剥掉同样的标记，
  //     否则整条线静默画不出 —— 真机踩过：「划了几条之后再也批注不上」。
  //     跨了「文本 + <code> + 文本」三个节点，所以是 3 段 mark。
  draw([mk('tilde', '路径在 /xhanCodes 下面')], 3, 'tilde', '路径在 /xhanCodes 下面');

  // 7c) NBSP（&nbsp; 渲染成 \u00a0）也要折成空格：空白判定走 /\s/，
  //     硬编码白名单会漏掉 NBSP → haystack 保留 NBSP、needle 是空格 → 静默画不出。
  draw([mk('nbsp', '宽度 标记，用来测 NBSP 空白也要折成空格。')], 1, 'nbsp',
       '宽度 标记，用来测 NBSP 空白也要折成空格。');

  // 7d) 字面方括号：不能当 Markdown 链接标记剥掉（DOM 里是真字符、客户端 fold 也保留）。
  //     剥掉会让 `[内容]` 划不满、只选 `[ ]` 时 quote 变空 → 静默画不出 / 无从创建。
  draw([mk('brk', '方括号： [内容] 与 [ ]')], 1, 'brk', '方括号： [内容] 与 [ ]');
  // 覆盖必须到尾部 `]`：旧实现把方括号当标记剥掉，下划线只画到「与」就停了
  ok('mark 覆盖到尾部方括号', markText('brk') === '方括号： [内容] 与 [ ]', 'markText=' + markText('brk'));
  draw([mk('brksp', '[ ]')], 1, 'brksp', '[ ]');

  // 7e) 硬换行（行尾两空格 → <br>）：服务端投影是一个空格，客户端 flatten 必须在 <br> 处补分隔符，
  //     否则跨硬换行的选区少一个空格、永远锚不上。跨了 <br> 两侧两个文本节点 → 2 段 mark。
  draw([mk('br', '行尾两空格硬换行 这是下一行')], 2, 'br', '行尾两空格硬换行这是下一行');

  // 8) 有 note 挂气泡，且贴在最后一段 mark 的同一块里
  draw([mk('a9', '第一个小结段落。 2.1 子节', '这是批注')], 2, 'a9', '第一个小结段落。2.1 子节');
  var bub = c.querySelectorAll('span.ann-bubble[data-for="a9"]');
  var lastMark = c.querySelectorAll('mark.hl[data-id="a9"]')[1];
  ok('有 note 挂标记', bub.length === 1);
  ok('标记贴在最后一段 mark 的同一块里', !!bub[0] && bub[0].parentElement === lastMark.parentElement);
  // 标记是「线色小圆点」，不是图标：没有 svg、没有绿底、尺寸就是个点
  ok('批注标记是小圆点不是图标', !!bub[0] && !bub[0].querySelector('svg') && bub[0].offsetWidth <= 8 && bub[0].offsetHeight <= 8);
  draw([mk('a10', '第一个小结段落')], 1, 'a10', '第一个小结段落');
  ok('纯划线没标记', c.querySelectorAll('span.ann-bubble').length === 0);

  // 9) 多处命中：靠前后文消歧挑对那一处
  draw([mk('d1', '记忆系统', '', '二、对照', '的第二处')], 1, 'd1', '记忆系统');
  var dm = c.querySelector('mark.hl[data-id="d1"]');
  ok('多命中按前后文挑对位置', !!dm && dm.closest('p').textContent.indexOf('第二处') >= 0);
  draw([mk('d2', '记忆系统', '', 'zzz', 'zzz')], 0);
  ok('前后文对不上就不猜（不画）', c.querySelectorAll('mark.hl').length === 0);

  // 9b) 重复文字 + 边界是空白（表格相邻单元格）：prefix/suffix 过 fold() 被 trim 掉紧贴 quote 的分隔空白，
  //     旧实现拿 trim 过的上下文去比未 trim 的 haystack，边界一个「空格 vs 中文」就让得分恒 0，
  //     重复 quote 消歧全 0 < need → 静默不画。真机：一篇文档里两个「内容类型」字段（任务元数据表 / 投递记录表）。
  draw([mk('dup', '内容类型', '', '字段 说明', '允许的类型标签')], 1, 'dup', '内容类型');
  var dupCells = [].slice.call(c.querySelectorAll('td')).filter(function (td) { return td.textContent.trim() === '内容类型'; });
  var dupMark = c.querySelector('mark.hl[data-id="dup"]');
  ok('重复文字靠上下文挑对第一处（不是第二行）',
     dupCells.length === 2 && !!dupMark && dupCells[0].contains(dupMark),
     dupCells.length !== 2 ? 'found ' + dupCells.length + ' dup cells'
                           : (dupMark ? (dupCells[0].contains(dupMark) ? 'first' : 'other row') : 'no mark'));

  // 10) 失活的不画
  draw([mk('o1', '记忆系统', '', '', '', true)], 0);
  ok('失活的正文里不画', c.querySelectorAll('mark.hl').length === 0);

  // 11) 重叠 = 嵌套 mark（外层先画，内层后画）
  put([mk('p1', '记忆系统的第一处', ''), mk('p2', '第一处提及', '')]);
  ok('重叠划线 = 嵌套 mark', c.querySelectorAll('mark.hl mark.hl').length >= 1
       && c.querySelectorAll('mark.hl[data-id="p1"] mark.hl[data-id="p2"], mark.hl[data-id="p2"] mark.hl[data-id="p1"]').length >= 1);
  ok('重叠也不改正文文本', c.textContent === text0);

  // 12) 悬停交叠处 → popover 同时列出两条，并打「重叠划线」标签
  put([mk('p1', '记忆系统的第一处', '外批注'), mk('p2', '第一处提及', '内批注')]);
  var nr = c.querySelector('mark.hl[data-id="p2"]').getBoundingClientRect();
  c.dispatchEvent(new MouseEvent('mousemove', { bubbles: true,
    clientX: nr.left + 1, clientY: nr.top + nr.height / 2 }));
  var pv = document.getElementById('popover');
  ok('交叠处 hover 列出全部两条 + 重叠标签',
     pv.classList.contains('open') && pv.textContent.indexOf('外批注') >= 0
       && pv.textContent.indexOf('内批注') >= 0 && pv.textContent.indexOf('重叠划线') >= 0);

  // 13) 左键点划线 → 定位请求（发 focus），不再弹菜单
  c.querySelector('mark.hl[data-id="p1"]').dispatchEvent(new MouseEvent('click', { bubbles: true }));
  ok('左键点划线发 focus（带 id）', !!last('focus') && last('focus').id === 'p1', JSON.stringify(last('focus')));
  ok('左键不再开菜单', !document.getElementById('ctxmenu').classList.contains('open'));

  // 右键（contextmenu）打开菜单：编辑 / 复制 / 删除
  function openCtx(id) {
    var el = c.querySelector('mark.hl[data-id="' + id + '"]');
    var r = el.getBoundingClientRect();
    el.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true,
      clientX: r.left + 1, clientY: r.top + 4 }));
  }

  // 13b) 菜单开着时 hover 同一条划线，不得再弹 hover 浮层（两个浮层会叠在一起）
  openCtx('p1');
  ok('右键划线出菜单', document.getElementById('ctxmenu').classList.contains('open'));
  var p1r = c.querySelector('mark.hl[data-id="p1"]').getBoundingClientRect();
  pv.classList.remove('open');
  c.dispatchEvent(new MouseEvent('mousemove', { bubbles: true,
    clientX: p1r.left + 1, clientY: p1r.top + p1r.height / 2 }));
  ok('菜单开着时 hover 不再出浮层', !pv.classList.contains('open'));
  // 关掉菜单，别影响后续用例
  document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
  ok('点外部关掉菜单', !document.getElementById('ctxmenu').classList.contains('open'));

  // 14) hover 有 note 的划线 → popover 列出批注内容
  put([mk('h1', '记忆系统的第一处', '悬停看看')]);
  var hr = c.querySelector('mark.hl[data-id="h1"]').getBoundingClientRect();
  c.dispatchEvent(new MouseEvent('mousemove', { bubbles: true,
    clientX: hr.left + 2, clientY: hr.top + hr.height / 2 }));
  ok('hover 出 popover（带 note）',
     document.getElementById('popover').classList.contains('open')
       && document.getElementById('popover').textContent.indexOf('悬停看看') >= 0);

  // 15) 抽屉要用：滚到划线 + 闪烁；找不到不抛异常
  window.__scrolled = [];
  ok('scrollToAnnotation 命中并闪烁',
     reader.scrollToAnnotation('h1') === true
       && c.querySelector('mark.hl[data-id="h1"]').classList.contains('flash')
       && window.__scrolled.length >= 1);
  ok('scrollToAnnotation 未命中返回 false', reader.scrollToAnnotation('不存在') === false);

  // 16) 菜单动作回传（右键打开菜单）
  openCtx('h1');
  document.querySelector('#ctxmenu button[data-cact="copy"]').click();
  ok('菜单「复制原文」回传 copy', !!last('copy') && last('copy').text.indexOf('记忆系统的第一处') >= 0);
  openCtx('h1');
  document.querySelector('#ctxmenu button[data-cact="del"]').click();
  ok('菜单「删除」回传 delete', !!last('delete') && last('delete').id === 'h1');

  // 17) 编辑批注：带上原 note、保存发 update
  put([mk('e1', '记忆系统的第一处', '旧批注')]);
  openCtx('e1');
  document.querySelector('#ctxmenu button[data-cact="edit"]').click();
  ok('点「编辑批注」带出原 note',
     document.getElementById('note-editor').classList.contains('open')
       && document.getElementById('ne-text').value === '旧批注');
  document.getElementById('ne-text').value = '新批注';
  document.getElementById('ne-save').click();
  var up = last('update');
  ok('保存发 update', !!up && up.id === 'e1' && up.note === '新批注', JSON.stringify(up));

  // 18) 反复重画（抽屉/增删改都会走这条）不能把结构画坏
  for (var i = 0; i < 3; i++) {
    draw([mk('r1', '一、结论先行 记忆系统的第一处提及', '批注'), mk('r2', '清单第二项')], null, null, null);
  }
  ok('反复重画后正文文本依旧', c.textContent === text0);
  ok('反复重画后块结构依旧',
     c.querySelectorAll('#content p, #content h2, #content h3, #content li, #content td').length === blocks0);

  // 19) 划线样式：文字底部波浪线，不铺底色
  //     （曾经是底色高亮；mark 的 UA 默认底色是黄的，改成划线后必须显式清掉）
  put([mk('s1', '记忆系统的第一处', '')]);
  var sm = c.querySelector('mark.hl[data-id="s1"]');
  var cs = getComputedStyle(sm);
  ok('划线是下划线 + 波浪', cs.textDecorationLine.indexOf('underline') >= 0 && cs.textDecorationStyle === 'wavy');
  ok('划线不铺底色', cs.backgroundColor === 'rgba(0, 0, 0, 0)', '实际 ' + cs.backgroundColor);
  var probe = document.createElement('div');
  probe.style.cssText = 'display:none;color:var(--hl)';
  document.body.appendChild(probe);
  var hlColor = getComputedStyle(probe).color;
  document.body.removeChild(probe);
  ok('线色用的是 --hl（不是 currentColor）', cs.textDecorationColor === hlColor,
     '线 ' + cs.textDecorationColor + ' vs --hl ' + hlColor);
  var rootCs = getComputedStyle(document.documentElement);
  var hlBase = rootCs.getPropertyValue('--hl').trim(), hlHov = rootCs.getPropertyValue('--hl-hover').trim();
  ok('hover 线色比常态更实', hlBase && hlHov && hlBase !== hlHov, hlBase + ' → ' + hlHov);
  ok('popover 里没有「点击编辑」文案', document.getElementById('popover').textContent.indexOf('点击编辑') < 0);

  // 19b) 连线终点取「正文列那一行」而不是划线片段本身：x/w = #content 左右缘、y/h = 划线首行片段。
  //      这样短划线（结束在行中间）的连线也落在右侧留白里，不横穿该行后半段文字。
  var ar = reader.annotationRect('s1');
  var colBox = c.getBoundingClientRect();
  var fr = sm.getClientRects()[0];
  ok('annotationRect 的 x/w 是正文列（不是划线片段）',
     ar && Math.abs(ar.x - colBox.left) < 0.5 && Math.abs(ar.w - colBox.width) < 0.5,
     JSON.stringify(ar) + ' vs col.left=' + colBox.left + ' col.width=' + colBox.width);
  ok('annotationRect 的 y/h 仍是划线首行片段',
     ar && Math.abs(ar.y - fr.top) < 0.5 && Math.abs(ar.h - fr.height) < 0.5,
     ar ? ('y ' + ar.y + ' vs ' + fr.top) : 'null');
  ok('annotationRect 未命中返回 null', reader.annotationRect('不存在') === null);

  // 20) 长批注（P5 把 AI 长回答存进来之后的常态）：浮层自己封顶内滚。
  //     以前没有 max-height：fixed 浮层超出视口那截永远拿不到，滚轮还会冒泡去滚正文
  var longNote = '';
  for (var li = 0; li < 40; li++) {
    longNote += '第' + li + '行：一段很长的批注内容，用来把浮层撑高。https://example.com/a/' + li + '\\n';
  }
  put([mk('l1', '记忆系统的第一处', longNote)]);
  var lr = c.querySelector('mark.hl[data-id="l1"]').getBoundingClientRect();
  c.dispatchEvent(new MouseEvent('mousemove', { bubbles: true,
    clientX: lr.left + 2, clientY: lr.top + lr.height / 2 }));
  var lp = document.getElementById('popover');
  var lcs = getComputedStyle(lp);
  ok('长批注浮层封顶在视口内', lp.classList.contains('open') && lp.offsetHeight <= window.innerHeight,
     lp.offsetHeight + ' vs ' + window.innerHeight);
  ok('浮层长内容自己内滚', lp.scrollHeight > lp.clientHeight, lp.scrollHeight + ' vs ' + lp.clientHeight);
  ok('浮层 overflow-y: auto', lcs.overflowY === 'auto', lcs.overflowY);
  ok('浮层不把滚动串给正文', lcs.overscrollBehavior.indexOf('contain') >= 0, lcs.overscrollBehavior);
  ok('长 URL 不横向溢出', lp.scrollWidth <= lp.clientWidth + 1, lp.scrollWidth + ' vs ' + lp.clientWidth);
  // 卡片里不要滚动条：滚动条会把内容挤压 15px 左右（headless Chrome 是非覆盖式的，正好量得出来）
  ok('卡片里没有滚动条', lp.offsetWidth - lp.clientWidth <= 2 + 1,
     'offset ' + lp.offsetWidth + ' client ' + lp.clientWidth);
  ok('卡片隐藏滚动条（scrollbar-width: none）', lcs.scrollbarWidth === 'none', lcs.scrollbarWidth);
  // 内滚时浏览器会派发 scroll（capture 阶段的 window 监听连后代滚动也收得到）：
  // 不能把它当成「页面滚动」把浮层关掉 —— 真机症状是「一滚浮层就消失，滚轮落到正文上」
  lp.scrollTop = 200;
  lp.dispatchEvent(new Event('scroll'));
  ok('浮层内滚不关卡片', lp.classList.contains('open') && lp.scrollTop === 200,
     'open=' + lp.classList.contains('open') + ' scrollTop=' + lp.scrollTop);
  // 页面滚动仍然要关（P3 的既有语义不变）
  document.dispatchEvent(new Event('scroll'));
  ok('页面滚动仍然关掉浮层', !lp.classList.contains('open'));
  // hidePopover 复位了 hoverKey：光标还在正文里，再 hover 同一处也必须能再开
  c.querySelector('mark.hl[data-id="l1"]').dispatchEvent(new MouseEvent('mousemove', {
    bubbles: true, clientX: lr.left + 2, clientY: lr.top + lr.height / 2 }));
  ok('关掉后再 hover 同一处还能开', lp.classList.contains('open'));

  c.dispatchEvent(new MouseEvent('mouseleave', { bubbles: true }));
  put([]);
}

setTimeout(function () {
  try {
    selectionChecks();
    armSelectionAndMouseUp();
    setTimeout(function () {
      try { toolbarChecks(); } catch (e) { fails.push('工具条断言异常: ' + (e && e.message ? e.message : e)); }
      setTimeout(function () {
        try { annotationChecks(); } catch (e) { fails.push('划线断言异常: ' + (e && e.message ? e.message : e)); }
        writeResult();
      }, 0);
    }, 0);
  } catch (e) {
    fails.push('选区断言异常: ' + (e && e.message ? e.message : e));
    writeResult();
  }
}, 0);
</script>
"""
html = html.replace('</body>', tests + '</body>', 1)
open(p, 'w', encoding='utf-8').write(html)
print('html 已生成: ' + p)
PY

read -r DOM STATS <<<"$("$CHROME" --headless=new --disable-gpu --virtual-time-budget=3000 --dump-dom "file://$OUT" 2>/dev/null \
  | python3 -c '
import sys, re
h = sys.stdin.read()
def pick(i):
    m = re.search(r"<div id=\"" + i + r"\">(.*?)</div>", h, re.S)
    return m.group(1) if m else ("FAIL 没有拿到断言结果" if i == "result" else "")
print(pick("result"), pick("stats"))')"

echo "结果: ${DOM}${STATS:+（${STATS}）}"
case "$DOM" in PASS) exit 0 ;; *) exit 1 ;; esac
