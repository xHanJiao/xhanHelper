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

## 四、修法建议（**未实施**，仅供下轮执行）

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
