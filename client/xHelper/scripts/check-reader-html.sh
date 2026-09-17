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

ZZMDIMG0X
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
function ok(name, cond, extra) { if (!cond) fails.push(name + (extra ? ' [' + extra + ']' : '')); }
function last(type) { for (var i = window.__msgs.length - 1; i >= 0; i--) if (window.__msgs[i].type === type) return window.__msgs[i]; }
var c = document.getElementById('content');

ok('ready 已上报', !!last('ready'));
ok('markdown 渲染出标题', c.querySelectorAll('h2').length === 4, 'h2=' + c.querySelectorAll('h2').length);
ok('渲染出表格', !!c.querySelector('table tbody tr'));
ok('渲染出代码块', !!c.querySelector('pre code'));
ok('行内代码', !!c.querySelector('p code'));
ok('mermaid 占位被替换', c.querySelectorAll('.mermaid img').length === 0 ? false : c.querySelectorAll('.mermaid img.m-light').length === 1);
ok('mermaid 用的是 data URI', (c.querySelector('.mermaid img.m-light') || {}).src ? c.querySelector('.mermaid img.m-light').src.indexOf('data:image/png') === 0 : false);
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

// 选区自己画：selectionchange 是异步派发的，所以这块要等一拍
function writeResult() {
  var el = document.createElement('div');
  el.id = 'result';
  el.textContent = fails.length ? ('FAIL ' + fails.join(' | ')) : 'PASS';
  document.body.appendChild(el);
}
try {
  var rng = document.createRange();
  rng.selectNodeContents(c.querySelector('p'));
  var sel = window.getSelection();
  sel.removeAllRanges();
  sel.addRange(rng);
  setTimeout(function () {
    try {
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
      writeResult();
    } catch (e) {
      fails.push('选区断言异常: ' + (e && e.message ? e.message : e));
      writeResult();
    }
  }, 0);
} catch (e) {
  fails.push('选区构造异常: ' + (e && e.message ? e.message : e));
  writeResult();
}
</script>
"""
html = html.replace('</body>', tests + '</body>', 1)
open(p, 'w', encoding='utf-8').write(html)
print('html 已生成: ' + p)
PY

DOM="$("$CHROME" --headless=new --disable-gpu --virtual-time-budget=3000 --dump-dom "file://$OUT" 2>/dev/null \
  | python3 -c 'import sys,re; m=re.search(r"<div id=\"result\">(.*?)</div>", sys.stdin.read(), re.S); print(m.group(1) if m else "FAIL 没有拿到断言结果")')"

echo "结果: $DOM"
case "$DOM" in PASS) exit 0 ;; *) exit 1 ;; esac
