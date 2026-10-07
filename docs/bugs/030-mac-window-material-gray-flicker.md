# 030 · macOS：右侧开浏览器 + 中栏打字时，背景偶发矩形灰闪

## 问题表现

macOS 客户端，右侧打开浏览器（右栏 `WKWebView`）时，在左/中栏聊天输入框打字，
可见中栏透明背景区域冒出一块**矩形灰色**、转瞬即逝（闪动）。

- **偶现**，非必现——同一动作多打几次才可能看到一次。
- 只在浏览器开着时出现；浏览器不打开时基本看不到。
- 右侧网页本身、顶栏不受影响，只有中栏（无内容遮挡的透明区）显形。

## 根因

- 窗口是**非不透明**的：`App.swift` 的 `configureWindow` 里 `isOpaque=false`、
  `backgroundColor=.clear`（为固定照片底图服务）。
- 整窗背景原来压了一层 `Rectangle().fill(.regularMaterial)`（`GlassWindowModifier.swift`），
  即一个覆盖整个窗口的 `NSVisualEffectView`。
- 右侧浏览器是 `WKWebView`，**独立合成层**。两者共存时，每次按键都让窗口重绘/重合成，
  这个 effect view 在重采样背景的一帧里会**偶发先画成不透明灰色兜底**。
- 灰色只有在**没有不透明内容遮住的区域**可见——中栏聊天区正是这样一块透明矩形，
  于是看起来就是"左栏背景下一块矩形灰闪"。右侧被网页盖住、顶栏在上，故不显形。
- 打字直接改 `ChatViewModel.draftText`，`InputBar` 每键重跑、`TextEditor` 光标闪烁，
  正好是持续触发源；合成器行为 racy，故偶现。

## 最终方案

（2026-10-07）

1. `Shared/Views/GlassWindowModifier.swift`：整窗**去掉** `.regularMaterial`，只留
   `AppBackdrop()`（照片重度模糊 + 35% 遮罩，本身即不透明玻璃观感）。这正是设计文档
   `docs/design/mac-apps/window-background-photos.md` §七 R1 预留的 fallback，D10 随之反转。
2. ~~输入胶囊一并去掉 material~~ → **已回滚**。评审指出：胶囊不在本 bug 因果链上（灰闪来自整窗
   effect view，胶囊下方始终有消息列表，不会显形），且换成固定半透明色会破坏「降低透明度」适配、
   并让滚动文字透出（无材质模糊的鬼影）。故 `InputBar` 保持 `.regularMaterial` 不变。
3. **亮度补偿**：整窗 material 移除后整体变暗（原材质兼有"提亮"作用）。`AppBackdrop` 补一层
   中性白霜 `Color.white.opacity(frost)`：浅色 `0.40` / 深色 `0.08`（深色更小）。真机若偏亮/偏暗
   只改这两个数（`AppBackdrop.frostLight/frostDark`）。注意**不能**靠调 `veil` 代替——深色
   下 veil 是近黑色，调厚只会更暗。

观感若偏暗/偏艳，只调 `AppBackdrop.veil`（现 0.40）与 `frost`，不加新 hex。

## 验证

- `swiftc -typecheck`（macOS 26.3，`-default-isolation MainActor`）0 error。
- 真机：右侧开浏览器 + 中栏打字，偶发灰闪不再出现；整体观感可接受。
  **偶现**，以日常使用不复现为准，不靠当场二元复现判定。

## 已知上限（复发时的排查方向）

若日后仍偶发，残余候选（均 macOS 侧可达）：

- `AppBackdrop` 的 `.blur(radius: 60, opaque: true)`（CALayer filter，同类机制，可能瞬时兜底）。
  **注意**：它现在是唯一的不透明兜底，真动必须同时补上 `reduceTransparency` 之外的不透明路径，
  别只删 blur。
- 消息区 `MessageListView` 的底部渐隐 `.mask`。
- 其余盖在透明区上的 `.regularMaterial`：聊天底部浮层（`InputBar` 胶囊、`AskUserBar:99`、
  `SubagentPanel`）、卡片（`CardBackground:22/37`）、右栏激活 tab（`RightSidebarView:173/443`）、
  文档纸底（`DocumentListView:609`）。
- 注：`MessageListView:182/911` 的菜单/分享胶囊是**双平台编译**（非 iOS 专属），但属瞬时弹窗，
  不是复发灰闪的优先候选。

届时按"每次只动一层、用几天看是否复发"逐层二分，别一次全改。

## 踩坑提示

偶现问题不要用"改一下 → 打字看有没有闪"当场判定：短期不复现 ≠ 修好。判据应是日常使用中是否复发；
能顺手录屏/截图，可直接看矩形边界落在那层，省一轮往返。
