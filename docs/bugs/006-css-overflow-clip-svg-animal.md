# Bug #006 — CSS overflow:hidden + fixed height 导致 SVG 像素动效不可见

> 日期: 2026-05-25 | 严重程度: P2 | 耗时: ~1h

---

## 问题表现

流式对话等待动画，猫和螃蟹正常显示，海豚完全不显示。猫的耳朵在页面上也消失了（但 preview HTML 里有）。

## 根因链条

这是一个三层叠加的 CSS 问题：

### 第 1 层：`overflow:hidden` + 固定容器高度裁剪了 SVG 内容

[MessageBubble.vue](front-end/src/components/MessageBubble.vue#L130-L139) 中 `.bubble-cat` 容器：

```css
.bubble-cat {
  width: 34px;
  height: 24px;       /* ← 固定高度，按猫 SVG 设计 */
  overflow: hidden;    /* ← 超出部分直接裁掉 */
}
.bubble-cat :deep(svg) {
  transform: scale(0.4);
  transform-origin: top left;
}
```

容器 34×24px 是按猫 SVG（84×60，scale 0.4 后正好 33.6×24）精确设计的。海豚 SVG 原为 84×84，scale 0.4 后 33.6×33.6，超出容器 9.6px。

但这还不够解释“完全不显示”——猫也有部分内容被裁（耳朵），为何猫至少还能看到身体？

**关键在于 viewBox 坐标分布**：猫的内容（头部）集中在 viewBox 顶部（y=-1~3），落在容器可见区（顶部 24px）内。海豚的内容（身体 y=5~10、水面 y=8~14）全部在 viewBox 下半部分，完全处于裁剪线以下，**一片空白**。

```
猫 viewBox "0 -1 14 10":  头部 y=0~3  → 在可见区 ✓
海豚 viewBox "0 0 14 14": 身体 y=5~10 → 全部被裁 ✗
```

**教训**：当多个 SVG 共享同一个 `overflow:hidden` 容器时，必须检查每个 SVG 的内容在 viewBox 中的位置是否落在可见区内。不能假设“能看到的那个”代表了所有情况。

### 第 2 层：`steps(1)` 使关键帧动画不生效

海豚有 3 种身体形态（body-b 水下、body-a 蓄力、body-am 空中）和 3 种尾鳍，通过 visibility 关键帧切换。原 CSS 用 `steps(1)`：

```css
.body-b { animation: body-b-vis 2.5s steps(1) infinite; }
@keyframes body-b-vis {
  0%, 100% { visibility: visible; }
  17%, 64% { visibility: hidden; }
}
```

`steps(1)` 把动画分成 1 个等长区间，**只使用 0% 和 100% 两个边界关键帧**，中间的关键帧（17%、64%）被跳过。body-b 的 0% 和 100% 都是 `visible`，所以永远可见；body-a/body-am 的 0% 和 100% 都是 `hidden`，所以永远不可见。形态切换完全失效。

**修复**：去掉 `steps(1)`，用默认 `linear`。由于 `visibility` 是离散属性，值在关键帧边界瞬间切换：

```css
.body-b { animation: body-b-vis 2.5s linear infinite; }
@keyframes body-b-vis {
  0%, 64%, 100% { visibility: visible; }
  17% { visibility: hidden; }
}
```

**教训**：`steps(n)` 只访问 0% 和 100% 关键帧，中间关键帧形同虚设。做逐帧动画时，要么不用 `steps()`（依赖离散属性的自然切换），要么确保关键帧数量和 step 数量匹配。

### 第 3 层：Vue scoped CSS 与父组件 `:deep()` 的优先级平局

猫的动画加在 SVG 根元素上（`.animal-cat svg`），动画中的 `transform` 值会覆盖父组件 `:deep(svg)` 设置的 `transform: scale(0.4)`——因为 **CSS Animation 的优先级高于普通 author 声明**，猫以 100% 尺寸显示。

海豚的动画加在子元素 `<g class="dolphin">` 上，SVG 根元素没有动画覆盖，`scale(0.4)` 始终生效，海豚只有猫的 40% 大小。

直接在子组件加 `.animal-dolphin svg { transform: none; }` 试图覆盖，但因为 Vue scoped CSS 编译后：
- 父组件：`.bubble-cat[data-v-parent] svg` → 特异性 0,2,1
- 子组件：`.animal-dolphin svg[data-v-child]` → 特异性 0,2,1

**特异性平局**，而父组件 CSS 加载更晚（import 链中 MessageBubble 是父组件，其样式在 WaitingAnimation 之后注入），父组件胜出。必须用 `!important` 打破平局：

```css
.animal-dolphin svg { transform: none !important; }
```

**教训**：Vue 父子组件之间通过 `:deep()` 穿透的样式 vs 子组件自身 scoped 样式，特异性可能平局。加载顺序通常对父组件有利。遇到覆盖失败时，检查编译后的选择器特异性，必要时用 `!important`。

## 最终修复

| 层 | 修复 |
|----|------|
| overflow 裁剪 | 去掉 `overflow:hidden` 和固定 `height`，让动画自然占位 |
| viewBox 偏移 | 海豚 viewBox 改为 `"0 5 14 9"`，内容上移 + 纵向压缩 |
| steps(1) | 改为 `linear`，简化关键帧为只保留切换边界 |
| 尺寸差异 | `transform: none !important` 覆盖父组件 scale(0.4) |
| 跳跃出界 | `translateY(-12px)` → `-6px`，`rotate(-12deg)` → `-6deg` |

## 涉及文件

- [MessageBubble.vue](front-end/src/components/MessageBubble.vue) — 移除 `.bubble-cat` 的 `overflow:hidden` 和 `height:24px`
- [WaitingAnimation.vue](front-end/src/components/WaitingAnimation.vue) — viewBox、动画、尺寸修复
- [dolphin-preview.html](front-end/src/components/dolphin-preview.html) — 同步预览文件
