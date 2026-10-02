# 重复文字划不上（表格同名字段）—— 消歧打分两侧边界不对称（一边 trim 一边没 trim）

## 问题表现

文档里某段文字**在正文出现多次**时，针对它的划线 / 批注画不出：抽屉「全部」里记录正常、不在「已失活」、点击 toast「这条划线在当前正文里定位不到」。

复现文档：`主动消息 · 技术实现文档 v0.1`（prod 库 `e4581d4d-1f28-44fb-95e2-cc9979f3e37b`）里的划线 `cd556560-…`（quote `内容类型`）。该词在正文出现 3 次：

1. 任务元数据表 `| 内容类型 | 允许的类型标签，如「库」「新闻」 |`
2. 投递记录表 `| 内容类型 | 库 / 新闻 |`
3. 第 5 节正文「判定口径按内容类型分叉…」

同一个文档里的另外两条划线（`源列表`、`消息标题，也是会话标题`）都画得出——它们只在正文出现一次，**根本不走消歧**。所以症状是「有的画得出、有的画不出」，取决于 quote 是否重复，不是随机复现。

## 根因：`locate()` 的消歧打分两侧边界不对称

`DocumentReaderView.swift` 的 `locate()` 在命中有多处时，用 `prefix` / `suffix` 打分消歧：

```js
var pre = fold(prefix);   // fold() 末尾有 .trim()
var suf = fold(suffix);
...
var score = 2 * commonTail(pre, hay.slice(0, h))
          + 2 * commonHead(suf, hay.slice(h + needle.length));
if (bestScore < need) return null;   // need = (pre.length + suf.length) / 2
```

- `prefix` / `suffix` 过 `fold()` 时被 `.trim()`，**紧贴 quote 的那侧分隔空白被吃掉**（表格里相邻单元格在投影里就是一个空格）。
- 但用来比对的 haystack `hay.slice(...)` **没 trim**，边界处仍是那个空格。

于是 `commonTail` 比到最后一个字符就撞上「空格 vs 中文」→ 立即返回 0；`commonHead` 同理。**只要 quote 两侧是空白分隔，得分恒为 0**，`0 < need` → `locate` 返回 `null` → `redrawAnnotations()` 走「客户端找不到就不画」，正文里彻底没有它。服务端仍判 `orphaned=0`，所以抽屉里一切正常。

用真实数据验证（node 复刻 `fold`/`locate`）：

```
现状：3 处命中，commonTail/commonHead 全 0，总分全 0，need≈43 → 不画
修复：正确那处 score=180（need=45），错误两处 4 / 0 → 挑对
```

## 修复

`locate()` 里给打分用的 haystack 上下文做与 `prefix` / `suffix` **对称的边界 trim**，`need` 与其余逻辑不动：

```js
var score = 2 * commonTail(pre, hay.slice(0, h).replace(/\s+$/, ''))
          + 2 * commonHead(suf, hay.slice(h + needle.length).replace(/^\s+/, ''));
```

服务端锚定本来就正确，只改客户端。存量坏记录无需重挂：客户端每次全量重推都会重新 `locate`，修完即自愈。

## 钉子

`client/xHelper/scripts/check-reader-html.sh` 新增用例 9b：测试正文加一张表，两行的首列都是 `内容类型`（相邻单元格即空白边界），用 `prefix='字段 说明'` / `suffix='允许的类型标签'` 消歧，断言画出 **1 段 mark**。落在哪一处用**语义判定**：收集所有文本恰为 `内容类型` 的 `<td>`（应为 2 个），断言 mark 落在**第一个** td 内（`dupCells[0].contains(dupMark)`），不依赖相邻单元格写了什么。

**变异验证**：把 `.replace(/\s+$/, '')` / `.replace(/^\s+/, '')` 去掉（还原旧写法）→ 9b 立刻变红（`mark 段数 = 1 [实际 0]`）；恢复后 166 条断言全绿。

## 留给以后的

- **消歧阈值 `need = (pre.length + suf.length) / 2` 偏保守**：长上下文里若只有一侧对得上，仍可能被卡掉。当前修复只解决「两侧都该匹配却因边界 trim 归零」这一类，真正的近似匹配（错别字、重排）没做。
- 已在 `docs/bugs/019`/`021` 记过的「客户端投影必须与服务端 `project()` 对齐」不变量，与本次无关（本次是打分口径的 trim 不对称，不是投影）。
