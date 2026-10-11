# macOS 窗口底图：固定照片 + 玻璃

> 日期：2026-10-04
> 状态：**已实施**（客户端；真机验收待做）
> 关联：`docs/design/mac-two-column-layout/design.md`（顶栏/标题栏几何）、`AGENTS.md`「UI 预览与设计 token」

估算（开工）：1 面 / 4 缝（1 条需先建探针）/ 2–3 次真机往返 / 1 个待拍决定 · ~3–5h
估算（定稿）：8 面 / 2 缝（均须真机、材质/安全区行为无法离屏探针）/ 2–3 次真机往返 / 0 个待拍决定 · ~3h

## 一、背景与目标

现状下 macOS 主窗口是一层"透桌面"的毛玻璃：窗口透明，根视图 `.background(.regularMaterial)` 采样的是
**窗口背后的桌面/其他 app**（`GlassWindowModifier.swift:7-8` + `App.swift:501-508` 把 `NSWindow` 设透明）。所以
壁纸一换、背后 app 一换，整个界面观感就飘——这不是可调参数能解决的，是"采样源"错了。

目标：给 app 一个**自己的、固定的底层颜色**——用照片当底，半透明玻璃压在照片上。这样玻璃模糊的是我们
自己的照片，与桌面/其他 app 无关：

- **始终有一张底图**（不再提供"透桌面纯毛玻璃"）：用户没导入 → 内置默认图；导入过 → 用用户池。
- 底图是**重度模糊、只留色块**的一层；原先其上压全窗 `regularMaterial`，**2026-10-07 因灰闪移除**（见 §八/D10），现直接以底图当作玻璃。
- 支持"选一个目录 → 导入图片（一键压到 192px）到内部目录 → 随机轮换"，导入后与源目录无关。

### 决策（已拍）

| # | 决定 | 取值 |
|---|------|------|
| D1 | 底图来源优先级 | 用户池非空用用户池；为空用内置默认图 |
| D2 | 是否保留"无底图/透桌面" | **不保留**，永远有底图 |
| D3 | 默认图 | 用户提供 `DSCF1575.JPG`，**压到长边 192**（192×128 / 15KB）打进 app 资源 |
| D4 | 导入后与源目录关系 | 快照，导入后无关；不自动重扫；只扫顶层 |
| D5 | 内部命名 | `bg-<sha256(data) 前 8 位>.jpg`（内容哈希，天然去重、跨启动稳定）；不做 manifest |
| D6 | 多源目录 | 叠加进同一池；设置页可单张删除 |
| D7 | 轮换触发 | 单选：启动随机 / 切屏随机（切走再切回本 app）/ 定时 3·5·20min |
| D8 | 呈现强度 | 重度模糊、只留色块；模糊半径 **60**、`.blur(opaque: true)` |
| D9 | ⌘, 设置窗 | 只铺内容区，32pt 标题栏保持系统原生 |
| D10 | 全窗顶层 | ~~保留 `regularMaterial`~~ → **已移除**（2026-10-07：灰闪，见 §八）；玻璃语义归卡片层 |
| D11 | 文档阅读器底 | 正文给一层"纸"底（material/浅色），保可读；底图只透到四周 |

## 二、现状（代码事实）

- **窗口根**：`App.swift:536` 的 `WindowGroup`，content 套 `.glassWindow()`（`:546`）。
  `GlassWindowModifier.swift` = `content.ignoresSafeArea(edges:.top).background(AppBackdrop())`（整窗只挂底图，不压 material）。
- **窗口透明**：`App.swift:501-508` `configureWindow`：`isOpaque=false`、`backgroundColor=.clear`、
  `toolbar=nil`；由 `applicationWill/DidBecomeActive`（`:367-376`）对所有窗口调用。窗口样式
  `.windowStyle(.hiddenTitleBar)`（`App.swift:609`）。
- **标题栏/顶栏几何**：系统标题栏高 **32pt**（本机 macOS 26.3 实测 `NSWindow.frameRect`）；app 的 `TopBarView`
  高 **54pt** = `Layout.topBarHeight`（内容距窗顶 12 + 胶囊 36 + 下边距 6），`.ignoresSafeArea(edges:.top)` 从窗口顶 y=0 起算。
  详见 `../mac-card-top-wrap/design.md`（该值同时决定中栏卡片的**上溢量** `cardTopRise`）与 `mac-two-column-layout/design.md`。
- **内容列**：`ContentView.swift` → `macOSContentArea` 是一张**完整卡片**：`RoundedRectangle(12)` 描边（`assistantText` 12%）
  + 同形**白霜提亮底**（`Color.white` `cardFillOpacity`=6%，与 `AppBackdrop.frost` 同族；**不要用 `assistantText` 压暗**，真机偏暗沉）
  + **投影**（`CardElevationShadow`：只在卡片外投、浅色黑 / 深色白），三条都 `.padding(.top, -cardTopRise)` 向上溢出，
  把红绿灯 + 会话下拉包进卡片；上边停在窗口内 6pt、四条边都看得见。右栏**不画边缘**（内容直接坐底图）。
  顶栏**无底色**。详见 `../mac-card-top-wrap/design.md`。
- **暖色渐变的归属**：`GlassBackground.swift`（LinearGradient + 光晕）**当前无 macOS 分支**，仅被 iOS
  `ChatView`、`SettingsView` 及 **macOS `LocalSettingsViews.swift:131`（解锁页）/`:176`（首次配置页）** 使用。
  macOS 主聊天窗走 `AppBackdrop`（固定照片底图；原先整窗 `.regularMaterial` 已于 2026-10-07 移除，见 §八）。
- **图片压缩现成件**：`ImageCompressor`（`Shared/Services/ImageCompressor.swift`）——`shrink`（长边 2048 / >2MB）、
  `thumbnail(_:maxPixel:)`（`:82`，ImageIO 降采样、含 EXIF 转正）、`encode`（private）。
- **数据根**：`LocalPaths.dataRoot`（`LocalPaths.swift:56`），dev/prod 隔离；`LocalPaths.allDataRoots`（`:61`）护栏用。
- **目录选择现成件**：`OpenDirectoryPanel`（`App.swift:1357`，已 `canChooseDirectories`）。
- **设置页**：`MacSettingsView`（`App.swift:813`）「外观」段在 `:839`；已有 `.sheet` 子页先例（`:941/:979`）。
- **targets**：`project.pbxproj` 用 `PBXFileSystemSynchronizedRootGroup`——mac target 组 = `xHelper/Shared` +
  `xHelperMac`；iOS target = `xHelper`。故 `xHelper/Assets.xcassets` **只进 iOS**，mac 资源只能放
  `xHelperMac/Assets.xcassets`；新增 `.swift` 不必碰 pbxproj。

## 三、上次的坑（必读红线）

做暖色渐变时出现过"色块盖住主内容区 + 标题栏只盖了一半"。根因：**底图挂在内容列内部**——`ChatView` 里
`Color("chatBackground").ignoresSafeArea()`（commit `9108de91`）；它只顶进中间列的宽度，标题栏左右两段仍透明。
该处代码现已不存在。

> **红线**：底图必须是**整窗单层**，只挂在 WindowGroup 根（§4.1），**任何列 / ChatView / 列表都不许再自铺底层色**。

## 四、详细设计

### 4.1 窗口层（唯一不变量）

`GlassWindowModifier` 整窗只挂 `AppBackdrop`（照片在最底，不透明）。**不压整窗 `regularMaterial`**（原 D10 方案，2026-10-07 因灰闪移除，见 §八）：

```swift
content
  .ignoresSafeArea(edges: .top)
  .background {
      AppBackdrop()                        // 照片（默认 or 用户池），重模糊 + 遮罩，不透明
  }                                        // AppBackdrop 内部已 ignoresSafeArea（含 32pt 标题栏，无横缝）
```

- `AppBackdrop` 永不为空（§4.2），照片层不透明 → 桌面/其他 app 被彻底遮住。
- `GlassBackground` 的 macOS 分支改为复用 `AppBackdrop`（供解锁页/首次配置页）；iOS 保留暖色渐变。
- `App.swift:611` 的 `Settings {}` Scene 加 `.background(AppBackdrop())`，页面 Form 加
  `.scrollContentBackground(.hidden)`；**32pt 标题栏保持系统原生**（D9）。
- **D9 的前提修复**：`configureWindow` 现在遍历 `NSApp.windows` 全集（`App.swift:501-508`）会把设置窗也弄透明。
  改为**只处理主窗口**：新增 `AppDelegate.primaryWindow`（在 `LocalWindowReader` 里记下主窗口），
  `configureWindow` 首行 `guard window === Self.primaryWindow else { return }`。**保持 `isOpaque=false`**（别改 true，
  会打死透明 `NSPanel`）。
- **无障碍**：`AppBackdrop` 里读 `\.accessibilityReduceTransparency`，为 true 时不画照片（交由不透明材质兜底）。

### 4.2 底图来源与优先级

```
用户池非空  →  在用户池里按轮换模式选一张
用户池为空  →  内置默认图 bgDefault（打进 app 资源）
```

无 clear/透桌面分支（D2）。「清空用户池」= 回到默认图。默认图不参与轮换、不可删。

### 4.3 默认图资产

- 源：`~/Downloads/DSCF1575.JPG`（FUJIFILM X-M5，6240×4160 / 16.1MB）。
- 已用 `sips --resampleHeightWidthMax 192 --setProperty format jpeg --setProperty formatOptions 82` 压成
  **192×128 / 15KB**，落 `xHelperMac/Assets.xcassets/bgDefault.imageset/`（`Contents.json` 单张 `universal` 1x）。
  只提交成品，不提交 16MB 原图。
- 代码用 `Image("bgDefault")` / `NSImage(named: "bgDefault")`（**读包内资源**——预览/沙箱进程读不到
  `~/Downloads`（TCC 保护），实测踩过）。

### 4.4 用户导入 / 存储 / 命名

- 内部目录 `LocalPaths.dataRoot/backgrounds/`（dev/prod 隔离）。
- 命名 `bg-<sha256(data) 前 8 位>.jpg`（用 CryptoKit，**不要用 `hashValue`**——每进程随机种子，跨启动不稳定）。
- 导入流程（选目录后，**后台线程**，可取消）：
  1. 护栏：源目录不得落在 `LocalPaths.allDataRoots` 任一内（`hasPrefix(root+"/")`，抄
     `LocalBackupSection.swift:267-270`），并 `resolvingSymlinksInPath` 后再判（挡软链指进 `backgrounds/`）。
  2. 枚举源目录**顶层**图片（扩展名白名单 jpg/jpeg/png/heic/webp/gif，只扫顶层，D4）。
  3. 数量上限（如 300）+ 跳过计数；逐张读入 → `ImageCompressor.downscaledJPEG(data, maxPixel: 192)` → 写内部目录；
     目标文件已存在（同 hash）则跳过。
  4. 之后源目录增删改一概不影响（快照）。
- **不做 manifest**：池子直接枚举目录；删除=删文件；清空=删光。
- 备注：`backgrounds/` 在数据根内 → 随 Go 侧整根备份走；每张 ~10KB，备份几乎不涨。

### 4.5 轮换（`BackgroundPhotoStore`，单选）

- 模式（`UserDefaults` key `LocalPaths.scoped("xhan_bg_rotation")`）：`启动随机 | 切屏随机 | 定时(3/5/20min)`。
- 触发：启动（init 时选一张）；切屏（`AppDelegate.applicationDidBecomeActive` 通知 store）；定时（`Timer`，
  间隔取所选值）。均"换一张与当前不同"；会话内稳定。
- 注意：`applicationDidBecomeActive` 也会在 `⌘,` 关设置、解锁时触发（切屏模式下会顺带换图，可接受）。
- 「换一张」手动按钮。池空（用默认图）时不轮换。

### 4.6 设置页「外观」新增（`MacSettingsView`）

- 「外观」段加一行：「背景图片」+ 当前张数 +「管理…」按钮。
- 点开走 **`.sheet` 子页**（缩略图网格塞 Form row 会挤；先例 `App.swift:941/:979`），内容：
  - 默认图预览（标注"默认"）。
  - 用户池缩略图网格，每张带删除（文件小到 192，**直接显示、无需 NSCache**）。
  - 「选择目录…」（复用 `OpenDirectoryPanel`）、「清空」（回默认，二次确认）。
  - 轮换方式 Picker、「换一张」。

### 4.7 渲染与性能

- 池子只存**文件清单**，不预解码整池（每张只有 192px，解码本就极便宜）。
- `AppBackdrop`：`Image(...)` → `scaledToFill` → `.blur(radius: 60, opaque: true)`（**`opaque: true` 必修**：
  否则重模糊四边渐隐、透出底层桌面）。按 `colorScheme` 叠暖色遮罩：浅色 `chatBackground`、深色 `chatBackgroundDeep`。
- 遮罩已实现：`veil = 0.40`，浅色 `Color("chatBackground")`、深色 `Color("chatBackgroundDeep")`（真机调出的值）。
  等价 `color-mix(in srgb, <chatBackground|chatBackgroundDeep> 40%, transparent)`；真机若偏了只改 `AppBackdrop.veil`。
- **中性白霜 `frost`**（2026-10-07 补）：整窗 `.regularMaterial` 移除后（§八），原材质的"提亮"随之丢失、整体变暗。
  在遮罩之上再叠一层 `Color.white.opacity(frost)` 补偿：浅色 `0.40`、深色 `0.08`（深色更小，因深色材质只提到浅灰）。
  真机若偏亮/偏暗只改这两个数（`AppBackdrop.frostLight/frostDark`）。
- **D11 文档"纸"底**：右栏文档阅读器 `DocumentReaderView` 是全透明 WKWebView（`:409/:438`），底图会透到正文后。
  给其宿主一层 material（或浅色"纸"底），保证正文可读；底图只透到四周。
  （注：`AnnotationDrawer.swift:738/:840` 的实色 `chatBackground` 在 preview 壳里，不是生产路径。）

## 五、改动文件清单

| 文件 | 动作 |
|------|------|
| `xHelperMac/Assets.xcassets/bgDefault.imageset/` | 新增（已完成：192 默认图 + Contents.json） |
| `Shared/Services/BackgroundPhotoStore.swift` | 新增（`#if os(macOS)`：池枚举 / 导入 / 删除 / 清空 / 轮换 / 当前图） |
| `Shared/Views/AppBackdrop.swift` | 新增（`#if os(macOS)`：默认图或当前图 → 色块层） |
| `Shared/Services/ImageCompressor.swift` | 改（暴露 `downscaledJPEG(data, maxPixel:)`） |
| `Shared/Views/GlassWindowModifier.swift` | 改（整窗只挂 `AppBackdrop`；双层中的 material 已于 2026-10-07 移除，见 §八） |
| `Shared/Views/GlassBackground.swift` | 改（mac 走 AppBackdrop；iOS 留渐变） |
| `Shared/Views/DocumentListView.swift` | 改（`DocumentFullView` mac 加 `.regularMaterial` 纸底，D11） |
| `Shared/Views/GlassWindowBackground.swift` | **删**（零引用的旧"透桌面"实现，正是要替换掉的反例） |
| `xHelperMac/App.swift` | 改（Settings 底图 + 排除 Settings 窗 + 切屏钩子 + 外观入口 + 主窗即配置） |
| `xHelperMac/BackgroundPhotoSettingsView.swift` | 新增（设置子页，`SettingsCard` 分组） |
| `xHelperMac/LocalSettingsViews.swift` | 改（`LocalWindowReader` 改 `viewDidMoveToWindow`，`primaryWindow` 首启时序可靠） |
| `xHelperMac/WindowBasePreview.swift` | 改（调参预览） |
| `client/xHelper/Package.swift` + `Tests/ImageCompressorCoreTests/` | 新增（`downscaledJPEG` 回归钉：长边 ≤192 / JPEG / 确定性） |
| `docs/design/mac-apps/window-background-photos.md` | 本文件 |
| 项目 `AGENTS.md` | 更新「UI 预览与设计 token」窗口底/表面语言段 |

> 两个新文件放 `Shared/` 同层（`GlassWindowModifier` 在 `Shared`，避免 Shared→Mac 反向依赖）；
> 新增后补 `/tmp/shared.txt` 两行。

## 六、真机验收清单

1. **标题栏满覆盖**：主窗 0–54pt 那条带（含系统标题栏 32pt）照片一条线到底，左/中/右**无竖缝**、上下**无横缝**。
2. **背景观感**：底图重模糊 + 遮罩后即玻璃观感；不透明，桌面/其他 app 不透。
3. 空池 → 默认图；导入 → 用户池；清空 → 回默认图。
4. 三种轮换模式各自生效（启动 / **切走再切回**（含"关设置窗回主窗"）/ 定时）。
5. ⌘, 设置窗：内容区有底图、**标题栏系统原生**、缩略图与单张删除可用。
6. 解锁页 / 首次配置页底图正常。
7. resize 时底图填满圆角、不露边。
8. 恢复备份后底图还在。
9. ~~全窗"照片 + `regularMaterial`"版对比纯遮罩版（D10 已选保留材质，留档）~~（D10 已改为移除材质）。

## 七、风险与 fallback

- **R1（最大）**：~~若 `.regularMaterial` 仍去采样桌面而非窗内照片~~ → **2026-10-07 触发并落地**：
  整窗 `.regularMaterial` 已移除（非不透明窗 + WKWebView 共存下逐键重合成偶发灰闪），直接靠照片自身的重模糊当玻璃（见 §八）。
- **R2**：若根层 `ignoresSafeArea` 实测够不到窗口最顶 → 在 `configureWindow` 补
  `window.titlebarAppearsTransparent = true` + `styleMask.insert(.fullSizeContentView)`。
- **R4**：轮换重模糊掉帧 → 缩略图尺寸再调小或对底图做缓存。

## 八、后续变更

- 2026-10-04：底图资产定为 192px（原 2048 方案废弃，省内存/备份）；顶层保留 `regularMaterial`（D10）；
  文档正文加"纸"底（D11）；模糊半径 60 + `.blur(opaque: true)`；短 hash 用 SHA256；`configureWindow`
  排除 Settings 窗。影响文件见 §五。
- 2026-10-04（review 收口）：
  - 补 **明暗遮罩**（之只落在预览里、没进生产）——`AppBackdrop` 加 `veil 0.35` + colorScheme 取色。
  - **空图兜底**：`currentImage` 为空/零尺寸时画不透明纯色，绝不"空图 → 透桌面"；`bgDefault` 取不到时打日志。
  - **`cancelImport` 真取消**：存 detached 句柄（原来只取消外层等待，`Task.isCancelled` 永远读不到）。
  - **错误不再静默**：`refreshPool` 区分"目录不存在"（正常空池）与"读失败"（保原池 + 打日志）；`remove`/`clear` 失败打日志。
  - **首启时序**：`LocalWindowReader` 改 `viewDidMoveToWindow()`（原 `makeNSView`+async 不保证 `view.window` 非 nil），
    首启窗口尺寸 / 透明机身 / 底图不再偶发不生效；`configureWindow` 只处理主窗口。
  - 删零引用 `GlassWindowBackground.swift`；设置子页改用 `SettingsCard` 分组；池内 <2 张禁用「换一张」。
  - 补 `ImageCompressorCore` 测试（长边 ≤192 / JPEG / 确定性）。
  - **未决**：`grouped Form + scrollContentBackground(.hidden)` 是否真让照片透出（设置窗）需真机验（§六-5）。
- 2026-10-07：**移除整窗 `regularMaterial`（D10 反转）**。现象：右侧开浏览器时，在中栏输入文字，
  中栏透明背景偶发一块矩形灰闪（非必现）。根因：窗口非不透明（`configureWindow`）且右侧挂了
  `WKWebView`（独立合成层），逐键重合成时整窗 `NSVisualEffectView` 偶发画成不透明灰色兜底，
  只在无内容遮挡的中栏透明区显形。方案：`GlassWindowModifier` 只留 `AppBackdrop`。
  （输入栏胶囊的 `.regularMaterial` 一度一并替换，评审发现它不在因果链上、且破坏「降低透明度」
  适配 + 材质模糊（透字鬼影），已回滚。）详见 `docs/bugs/030`。
  影响文件：`Shared/Views/GlassWindowModifier.swift`、`AGENTS.md`。
- 2026-10-07（续）：material 移除后整体观感偏暗（原材质兼有"提亮"作用），`AppBackdrop` 补一层
  中性白霜 `Color.white.opacity(frost)` 补偿（浅 0.40 / 深 0.08，见 §4.7）。影响文件：
  `Shared/Views/AppBackdrop.swift`、`xHelperMac/WindowBasePreview.swift`（加白霜滑杆）、`AGENTS.md`。
- 2026-10-09：**修 resize/常态下窗口最顶一条（系统标题栏 32pt）露桌面**。根因：上一条把
  `GlassWindowModifier` 里兜住标题栏的外层 `.ignoresSafeArea()`（连同 material）一起删了，
  只留 `AppBackdrop`，而它 body 里是 `ZStack{…}.ignoresSafeArea().clipped()` —— `.ignoresSafeArea()`
  扩进安全区的部分被其后的 `.clipped()` 裁回，标题栏那条没被覆盖；窗口非不透明 → 直接透桌面。
  方案（本次只做 A）：把 `.clipped()` 从 ZStack 外层收到 `photoLayer` 内部、给照片层显式
  `.frame(maxWidth:.infinity,maxHeight:.infinity)`，`.ignoresSafeArea()` 留在 body 最外层不再跟裁剪；
  ZStack 最底补一层不透明 `Color("chatBackground")` 兜底（照片掉帧/裁边也只露底色，不露桌面）。
  未做 B（`configureWindow` 窗口级不透明兜底）。影响文件：`Shared/Views/AppBackdrop.swift`。
