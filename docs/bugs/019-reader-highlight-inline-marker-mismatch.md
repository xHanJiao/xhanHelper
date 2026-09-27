# 划线批注了几条之后再也批不上 —— 客户端投影只折空白、不剥行内标记

## 问题表现

在文档里连着划线 / 批注，**前几条正常，划到某一句之后，再划的线或批注都不出现**（不是画完又消失）。抽屉「全部」里那条记录正常存在、不在「已失活」里，点它会 toast「这条划线在当前正文里定位不到」。

复现文档：`Agent 调研人话版 · Today AI`（`0251e63b-c893-4466-a145-781985f53987`）。库里 5 条批注，前 3 条画得出，第 4、5 条画不出；坏掉的两条恰好都选到了含 `` `~/xhanCodes` `` 的那一句。

## 先排除的怀疑

- **不是 orphan**：库里的记录 `orphaned=0`、`quote_rewritten=0`，服务端认为锚得好好的。
- **不是服务端定位错**：第 4/5 条的服务端 `start_offset`/`end_offset` 与源文对得上。
- **不是「数量多了才坏」**：第 1～3 条与第 4/5 条的唯一差别是选区里**有没有行内标记字符**。

## 根因：客户端 haystack 的投影比服务端少剥了行内标记

设计文档 §4.3 的不变量：服务端 `project()`（Go）与客户端 `fold()`（JS）对同一份正文必须给出**同一条纯文本流**。两侧都丢 `mdInlineMarkers = * _ ` ~ | [ ]`。

但画线时，客户端拿 **`fold(quote)`** 当 needle，却在 **`normalized()`** 出来的 haystack 上搜 —— 而 `normalized()`（`DocumentReaderView.swift`）**只折叠空白，没剥行内标记**。

服务端存库时把 quote 投影过了（`~` 被丢），所以库里是 `…第一句话是"/xhanCodes 下的…`；而 DOM 里 marked 渲染的 `` `~/xhanCodes` `` 仍带 `~`，haystack 是 `…第一句话是"~/xhanCodes 下的…`。差的正是那个 `~`：

```
现状（只折空白）  haystack.indexOf(needle) = -1     → locate() 返回 null → 静默不画
修复（补剥标记）  haystack.indexOf(needle) = 命中    → wrapSpan 正常包 mark
```

`locate()` 返回 null 时 `redrawAnnotations()` 走「客户端找不到就不画：不报错，也不动服务端状态」那条路，所以正文里彻底没有它，抽屉里却看着一切正常。

旧的 `check-reader-html.sh` 用例里 quote 都不含 `*_`~|[]`，所以一直没被咬到。

## 修复

`DocumentReaderView.swift` 的 `normalized()`：字符循环里跳过行内标记字符、且**不写进 `toFlat` 映射**，让 haystack 与服务端 `project()` 落在同一投影空间。

```js
var MD_MARKER_CHARS = MD_MARKERS.source.slice(1, -1).replace(/\\/g, '');  // 从正则派生，避免第二份字面量漂移
var WS_CHAR = /\s/;                                                        // 空白判定对齐 unicode.IsSpace / fold() 的 \s
...
if (WS_CHAR.test(ch)) { ...折成单空格... continue; }
if (MD_MARKER_CHARS.indexOf(ch) >= 0) continue;   // 丢行内标记，不占投影下标
```

空白判定顺手从硬编码白名单 `' ' \n \t \r \u3000` 换成 `/\s/`：NBSP（`\u00a0`，粘贴文本极常见）、`\u2028/\u2029` 等与服务端 `unicode.IsSpace`、`fold()` 的 `\s` 对齐，否则同类「投影两边不一致」还有一发作（015 结尾列过这笔账）。

`prefix`/`suffix` 已经走 `fold()`，脏数据只需清一次。

## 钉子

`client/xHelper/scripts/check-reader-html.sh` 新增两条用例：

- **7b 行内标记**：正文里放 `` `~/xhanCodes` ``，用**已投影**的 quote `路径在 /xhanCodes 下面` 去 `draw()`，断言画出 3 段 mark（跨「文本 + `<code>` + 文本」三个节点）。
- **7c NBSP**：正文里放 `&nbsp;`（渲染成 `\u00a0`），quote 用折成空格的 `宽度 标记…`，断言画出 1 段 mark。

**变异验证**：

- 注释掉 `normalized()` 里剥标记那行 → 7b 立刻变红（`mark 段数 [实际 0]`）。
- 把空白判定改回硬编码白名单 → 7c 立刻变红（`mark 段数 [实际 0]`）。
- 两处都恢复后全绿（165 条断言）。

## 顺带：抽屉右键删除

坏掉的批注在正文里点不到，也就没有删除入口（正常行只有「点击跳转」，失活行才有删除按钮）。给 `AnnotationDrawer` 的两种行都挂 `.contextMenu`：正常行可直接删除，失活行额外给「重新定位」。删除走既有的 `reader.deleteAnnotation(id:)`，服务端为准、全量重推。这样「服务端说正常但客户端定位不到」的批注也有救。

## 留给以后的

- **跨语言投影不变量没有测试守着**（follow-up，不阻塞）：`project()`（Go）与 `fold()`/`normalized()`（JS）是两份手写实现，靠注释与 `docs/bugs/015`/`019` 两份记录维持一致 —— 已经两次栽在这条不变量上。建议加一份共享 golden fixture（同一份 markdown + 期望投影文本），Go 单测与 `check-reader-html.sh` 各读一遍。
- **残余不对齐**：服务端 `project()` 还会剥行首块标记（`# > - + *`、`N. `）。DOM 里只有 `<pre>` 代码块内部会出现字面量行首标记，而客户端在 `normalized()` 里已把 `\n` 折成空格、丢了「行首」信息 —— 若选区恰从代码块某行的 `#`/`-` 起手，仍可能对不上。属更窄的边角，本次不动。
- **链接目标 `(url)`**：服务端投影保留、DOM 只有锚文本，仍是旧账，非本次回归。
