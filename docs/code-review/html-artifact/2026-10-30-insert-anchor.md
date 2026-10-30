# 第五轮审查 · 真机回归：`insert` 锚在表格单元格上产出非法结构

**审查日期**：2026-10-30
**起因**：真机回归（「HTML 工具回归测试 · 10.30 上午」）发现 1 个真 bug
**审查范围**：`server/internal/util/htmlpatch/htmlpatch.go`（insert 校验与应用）、`server/internal/tool/html_artifact.go`（`patch` 文案 / `patchErrorMessage`）、`docs/design/html-artifact/fine-grained-edit.md` §4.5 / §八
**结论**：❌ **P1 真 bug，本轮只记录、未修**。`insert.after` 指向表格单元格时服务端不报错，把块级片段塞进 `<tr>`，`source_html` 变成非法结构；浏览器重解析时会把这段内容**挤出表格**（foster parenting），位置与样式静默跳走，而模型下一次拿到的 `outline` 看起来完全正常。
**关联**：`docs/code-review/html-artifact/2026-10-30-final.md` 的「✅ 可以合并」需要按本条修正 —— 那一轮把「表格族」当成了一类问题，只补了**片段侧**，漏掉了**锚点侧**。
**修复状态**：2026-10-30 已按 §四 实施 —— `htmlpatch.go` 新增 `ErrBadAnchor` + `tableContextTags`，insert 校验检查锚点父容器；`patchErrorMessage` 给出两条出路；`htmlpatch_test.go` / `html_artifact_test.go` 各补用例（含「失败不改页面」）。真机复测待跑。

---

## 一、复现路径

回归里给的最短复现（页面 `bf7571ba`）：

1. `html.get(view="outline")` → 拿到表格单元格 `t10`（`tag: td`，`hash: cdfe4588`）；
2. `html.patch({"insert":[{"after":"t10","hash":"cdfe4588","html":"<p class=\"lead\">…</p>"}]})`；
3. 服务端返回 `{"success":true,"inserted":1,"updated":["t10"]}` —— **成功**；
4. `source_html` 实际变成 `<tbody><tr><td>…</td><p class="lead">…</p><td>…</td></tr></tbody>`；
5. 紧接着的 `outline` 把那个 `<p>` 当成普通段落编号（t8）—— **看起来正常**；
6. 只有 `view="full"`（或右栏渲染）能看出问题：浏览器解析时把 `<p>` 挪到表格外，位置/样式全变。

也就是说：①不是报错，是「成功返回 + 静默产坏」；②outline 会把坏结构当正常块，掩盖问题。

## 二、根因

三处叠在一起，缺的是「锚点所在容器的内容模型」这一层校验：

| 环节 | 代码 | 现状 |
|---|---|---|
| insert 校验 | `htmlpatch.go:184-200` | 只校验锚点**存在 / hash 一致 / 没被删**，然后校验**片段本身** |
| 片段校验 | `htmlpatch.go:291-336`（`parseFragment` + `insertTags`） | 只回答「这个标签能否单独作为片段出现」，不回答「插进这个父容器合不合法」 |
| 应用 | `htmlpatch.go:211-219` | `parent.InsertBefore(n, next)`，`parent` = 锚点父节点；锚点是 `td` 时父节点就是 `tr` |
| 收尾 | `ApplyEdit` → `render` + `htmlsanitize` | 只做标签/属性/CSS 白名单，**不做内容模型校验** |

所以片段 `<p>` 过了白名单、锚点 hash 也对、写入也成功，非法结构就这么进了 `source_html`（还通过了乐观锁）。

**这条规则设计文档其实写过**：`fine-grained-edit.md:325`「别锚在 `td/th` 上（会插出 `<tr><td>x</td><p>…</p></tr>` 这种非法结构，浏览器重解析后位置会跳）」。但它只落在 `patch` 描述和 `get(outline)` 的 tip 里，属于**提示词约束**；而 §4.5 自己写的是「服务端校验是硬要求，不只依赖提示词」。本次回归就是这个反例。

## 三、为什么前四轮没抓到（复审方法问题，值得记账）

- 第二轮复审里的「表格标签插不进去」（`rejectTableParts` + `insertTags` 去掉表格族）修的是**片段侧**；相关用例的输入全是「表格片段」，没有一条输入是「表格锚点」。
- 锚点类用例集中在**存在性**：锚点不存在、hash 过期、锚点被删（`htmlpatch_test.go:339-365`）——没有一条校验**锚点位置**。
- 真机剧本（§七）里没有「往表格里加内容」这一步。

一句话：校验清单是按**字段**列的，不是按**非法组合**列的。锚点 × 片段的组合里，「表格」这一格始终没人碰。

## 四、修法建议（已按此实施，见 §七）

最小改动只落在 `htmlpatch`，不引入内容模型推导：

```go
// tableContextTags — 子节点只能是表格结构的容器。在它们内部插块级片段会产出
// <tr><p> 这类非法结构；浏览器重解析时把内容挤出表格，位置/样式静默跳走。
var tableContextTags = map[string]bool{
    "table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true,
}
```

在 `htmlpatch.go:184-200` 的 insert 校验段加一条：`a.node.Parent` 命中 `tableContextTags` → 返回新哨兵 `ErrBadAnchor`（文案要告诉模型两条出路：加段落锚在**整张 table 之后**；加行/加格只能 `view="full"` 整页重写）。
`tool/html_artifact.go:591 patchErrorMessage` 加一条 `errors.Is(err, htmlpatch.ErrBadAnchor) → err.Error()`，与 `ErrBadFragment` 同档。

**为什么只拦表格族，不顺手管 `ul/ol`、`p`：**

- 表格族是唯一会被 HTML5 **foster parenting** 搬走的上下文 → 位置静默错位、class 挂错，是「看起来成功实际坏掉」。
- `<ul>` 里塞 `<p>`、`<p>` 里塞 `<p>`：浏览器按文档顺序产出（`<p>` 会隐式闭合外层），位置与样式仍然对，只是不规范；为它们做内容模型推导不值得。
- 推荐路径不受影响：锚在「整张 table 之前的块 / 之后的块」上时，父容器是 div/section/body，照常可以插。

**可达性核对**：表格内部能被编号的只有 `td / th / caption`（`textTags`，`htmlpatch.go:34-42`），`tr/thead/tbody/tfoot` 拿不到 nid —— map 里那几项是防御性条目，不影响正常路径。

**建议的测试**（表驱动 `TestInsertRejectsTableAnchor`）：

1. 锚 `td` + `<p>` → `ErrBadAnchor`，且源码**未变**（校验必须先于任何节点修改）；
2. 锚 `th` + `<img>` → `ErrBadAnchor`；
3. 锚 `caption` → `ErrBadAnchor`；
4. 回归保护：锚在 table 前一个块上插整张 `<table>` 仍成功（`TestInsertWholeTable` 已有，确认没被误伤）。

## 五、观察项（不改，记账）

1. **`remove` 删单个 `td` 仍允许**：会得到参差表格，但结构合法、位置不跳；没有证据说它是问题，先不动。
2. **锚在 `p` 里的 `img` 后面**会产出 `<p><img><p>…</p></p>`：浏览器隐式闭合外层 `p`，位置/样式仍正确，按无害处理。
3. 回归里未测完的两项（div 中无 nid 的文字走整页重写、页脚之前的末尾锚定）不属于本条，留给下一轮。

## 六、影响面与未覆盖

- 只影响「锚点在表格内部」的 `insert`；`ops` / `remove` / `style` 不受影响。
- 已落库的非法页面不会被自动修（读取侧不做结构重写），只能用 `view="full"` + `create` 重建。
- 本轮只做审查：没有真机验证修复效果，也没有跑「模型被拒后能否自纠」这一步（修完需要 1 次真机往返）。

---

## 七、修复后复审（同日 · `211ed258` / `430212d0` / `48914598`）

§四 的建议已按原样落地（`ErrBadAnchor` + `tableContextTags` + 两条出路文案 + 用例），同一天还跟了两笔表情——`430212d0`（落点抬到语义容器之后 / `updated` 只报 ops / 页脚文案）与 `48914598`（图注按语义标记识别 / 无 `<style>` 可新建 / 12 张按图片 id 去重）。

表格锚点这条**修复成立**：`td/th/caption` 三种锚点被拒、校验先于任何节点修改、锚在表格外插整张 `table` 不受影响，用例都对得上。`updated` 语义、`get(style)` 空样式返回、图片去重也都与文档一致。

复审这三笔提交，新发现 **2 条 P2 + 5 条 P3**。两个 P2 都是真机探针跑出来的（临时用例跑完即删），不是读代码猜的。

### P2-1 · 图注里的图会被连带删掉（数据丢失）

`contentOf`（`htmlpatch.go:453-476`）遇到 `captionLike` 节点**跳过整个子树**，所以 `<figcaption>` / `.cap` 里的 `<img>` / `<video>` 不算 `media`。删掉同一 figure 的主图后 `residue` 判为残渣 → 整个 figure（含图注里的那张图**和**图注文字）被删。

探针结果：

```html
<!-- 改前 -->
<figure><img src="image:a"><figcaption>图注 <img src="image:icon"></figcaption></figure>
<!-- remove img1 之后 -->
<body><p>正文</p></body>      <!-- 图注与 icon 一起没了 -->
```

这正是第二轮 P1（「删一张图连带删掉同 figure 的另一张」）的同类问题在新判据下重现。触发条件窄（图注里放图），后果是静默删掉用户还要的元素。

修法二选一：① `captionLike` 分支仍然递归，只忽略**文字**、保留 `media` 信号；② `residue` 补一条「子树里还有 `media` 就不算残渣」。① 更直接。

测试缺口：现有用例只覆盖「图注是纯文本」（`TestRemoveImageKeepsPlainTextDiv` / `TestPruneLeavesNoBlankLine`），没有「图注里带图」。

### P2-2 · `figure` 的抬升把「给照片加说明」也抬出了 figure（需拍板）

`tightContainers` 按**父容器**判断（`htmlpatch.go:76-85`、`:219-223`），`figure` 的所有直接子块（`img`、`p`）都会被抬到 `</figure>` 之后。探针：

```html
<figure class="fig"><img src="image:a"><figcaption>图注</figcaption></figure>
insert after=img1 "<p class='cap'>新说明</p>"
→ …</figure><p class="cap">新说明</p>…        <!-- 落在 figure 外，拿不到 .fig 的样式 -->
```

`figure` 的内容模型本来就允许 flow content（只有 `figcaption` 必须首/尾），所以「照片下面加一句说明」这种最常用的编辑现在只能落到 figure 外；页面里 `figure` 带内部卡片样式时观感会变。

若要保留「锚在图注上要抬出去」的修复，建议按**锚点类型**细分，而不是按父容器一刀切：

- `ul` / `ol` / `dl` / `picture` 维持现状（只能装特定子元素，抬出去是对的）；
- `figure` 只在锚点是 `figcaption` 时抬；锚在 `img` 上照旧插在 figure 内。

这条要真机看一页「figure + 内部样式」的页面才能定，属于待拍板。

### P3

1. **`insertPlan.after` 成了死字段**（`htmlpatch.go:196`）：`tails` 改按 `target` 键之后没人读它，删掉即可。
2. **`ErrNoStyle` 已不可达**：`Style()` 现在无 style 返回 `("", "", nil)`；`ReplaceStyle` 只在 `findHead == nil` 时返它，而 `html.Parse` 总会补 `<head>`。于是 `getOutline`（`tool/html_artifact.go:346`）的 `!errors.Is(err, htmlpatch.ErrNoStyle)` 条件、`patchErrorMessage`（`:608`）的「页面缺少 `<head>`」分支都是死支。要么删，要么把哨兵改名为 `ErrNoHead` 让语义对上。
3. **「就落在页脚之后」有歧义**：`TestInsertAfterFooterLandsAfterFooter` 钉住的是「落在 `<footer>` **内部**、最后一块之后」（`页脚</p><p>新增</p></footer>`），描述写成「页脚之后」会被读成 `</footer>` 之后。行为可接受，建议文案改成「锚在页脚的最后一块上，新内容落在页脚内末尾」。
4. **表格是页面最后一个块时无路可走**：`table` 没有 nid、不能当锚点，而 hint 只给了「锚在 table 之后的块上」。这种页面只能整页重写；建议在 `get(outline)` 的 tip 里承认这条限制，别让模型反复试。
5. **`updated` 在幂等重放里仍列 nid**：`newSrc == src` 走 `patchResult(..., changedNow=false)` 时 `inserted/removed` 清零、`updated` 仍是 ops 清单，语义上什么都没更新（影响很小，模型只看 `success`）。

**修复状态（同日 · `32a21f71`）**：P2-1 图注里的媒体改为递归保留；P2-2 按锚点类型细分（只有锚在 `figcaption`/`caption` 上才抬，锚在照片上留在 figure 内）；P3 五条全收（死字段、`ErrNoHead`、页脚文案、表格末块提示、幂等 `updated`）。真机复测仍待跑。

### 本轮未做

- 没有跑真机（P2-2 需要一页带 figure 样式的页面才能拍板）；探针用例已删除，仓库无不相关改动。
- 工具结果历史回放（`5770bc45`）的复审结论另见 `docs/code-review/tool-result-history/2026-10-30.md`。

