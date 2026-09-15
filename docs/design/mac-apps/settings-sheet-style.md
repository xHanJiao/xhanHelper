# macOS 设置页子页面卡片风格规范

## 概述

macOS 设置页（`MacSettingsView`）中通过 `BrowseRow` 打开的子页面（sheet）统一使用「玻璃卡片分组」风格：每个场景一张卡，卡片头带 accent 图标、加粗标题与作用域说明，内容行简洁对齐。首个落地页面：快捷键速查（`ShortcutGuideView`）。

## 设计原则

1. **风格统一**：设置子页面共用同一套卡片 / 行 / 键帽样式，不各写各的
2. **分组明确**：一组一卡；标题说「是什么」，hint 说「什么时候生效」
3. **自适应深浅色**：只用 material 与 hierarchical style（`.primary` / `.quaternary` / `.tertiary`），不硬编码颜色（accent 走资产 `appAccent`）
4. **简洁克制**：不做 hover 高亮、计数徽章、立体投影、搜索框

## 卡片结构

```
┌──────────────────────────────────────────┐
│ ⌘ 通用                          全局生效   │   ← accent 图标 + 标题 + 灰色 hint
│ ─────────────────────────────────────────│
│ 新建对话                        [⌘][N]    │   ← 行：说明左，键帽列右对齐
│ 新建无痕会话                 [⌘][⇧][N]    │
│ ─────────────────────────────────────────│   ← Divider 细分隔线
│ 打开设置                        [⌘][,]    │
└──────────────────────────────────────────┘
         ↕ 卡片间距 16
┌──────────────────────────────────────────┐
│ 🌐 浏览器                      浏览器聚焦时 │
└──────────────────────────────────────────┘
```

## 组件

代码：`client/xHelper/xHelperMac/SettingsStyle.swift`

### SettingsCard

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `icon` | String | 是 | SF Symbol 名，accent 着色 |
| `title` | String | 是 | 分组标题 |
| `hint` | String? | 否 | 作用域说明（灰色小字） |
| `content` | @ViewBuilder | 是 | 内容行；行间分隔线由调用方用 `Divider().opacity(0.6).padding(.leading, 14)` 控制 |

### KeycapView

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `label` | String | 是 | 单个按键文案（如 `⌘`、`Esc`、`⏎`） |

## 样式规范

| 用途 | 取值 |
|------|------|
| 卡片背景 | `.regularMaterial`，圆角 12（`style: .continuous`） |
| 卡片描边 | `.primary.opacity(0.08)`，0.5pt |
| 标题图标 | `Color("appAccent")`，14pt semibold，固定宽 18 |
| 标题 | `.system(size: 13, weight: .semibold)` |
| 作用域 hint | `.system(size: 11)` + `.tertiary` |
| 内容行文本 | `.system(size: 13)` |
| 内容行内边距 | 水平 14 / 垂直 8 |
| 行分隔线 | `Divider().opacity(0.6)`，左缩进 14 |
| 键帽背景 | `.quaternary.opacity(0.5)`，圆角 6 |
| 键帽描边 | `.primary.opacity(0.1)`，0.5pt |
| 键帽字体 | `.system(size: 12, weight: .medium, design: .monospaced)`，minWidth 24 × minHeight 22 |
| 键帽间距 | 4 |
| 键帽列宽 | `minWidth: 110, alignment: .trailing` |
| 卡片间距 | 16 |
| 页面内边距 | 16 |
| 页面尺寸 | 由 `BrowseRow(sheetSize:)` 指定（快捷键页 560 × 640） |

按键数据约定：连击拆成多个 keycap（`["⌘", "⇧", "N"]`）；`"/"` 表示「或」，渲染为分隔符而非键帽（如 `↑ / ↓`）。

## Sheet 容器约定

子页面通过 `BrowseRow` 以 sheet 打开，`BrowseRow` 会挂一个 `.cancellationAction` 的「关闭」按钮。

- **尺寸由 `BrowseRow(sheetSize:)` 决定**，默认 500×400。内容更大时必须显式传 `sheetSize:`，否则内容会被 500×400 的窗口裁掉（顶部「通用」和右侧键帽看不到）。
- 页面内声明 `.navigationTitle(...)`（参考 `LogDiagnosticView`）给 sheet 一个标题。
- 内容要**填满窗口并自行滚动**：只写 `ScrollView { ... }`，不要再加 `minWidth/minHeight` 或 `idealWidth/idealHeight` —— 那会让内容溢出窗口而非撑大它。

```swift
// 设置页入口
BrowseRow("快捷键", icon: "keyboard",
          destination: ShortcutGuideView(),
          sheetSize: CGSize(width: 560, height: 640))

// 子页面：填满 + 滚动，不写 frame
ScrollView { ... }
    .navigationTitle("快捷键")
```

## 使用示例

```swift
ScrollView {
    VStack(spacing: 16) {
        SettingsCard(icon: "globe", title: "浏览器", hint: "浏览器聚焦时") {
            ForEach(Array(items.enumerated()), id: \.offset) { index, item in
                if index > 0 {
                    Divider().opacity(0.6).padding(.leading, 14)
                }
                HStack(spacing: 12) {
                    Text(item.desc).font(.system(size: 13))
                    Spacer(minLength: 24)
                    HStack(spacing: 4) {
                        ForEach(item.keys, id: \.self) { key in
                            KeycapView(label: key)
                        }
                    }
                    .frame(minWidth: 110, alignment: .trailing)
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 8)
            }
        }
    }
    .padding(16)
}
```

## 采用范围

- 已采用：快捷键速查 `ShortcutGuideView`
- 未来：设置页 `BrowseRow` 打开的子页面（sheet）一律沿用
- 暂不迁移：账单记录 / 已归档会话 / 诊断日志（保持现状，需要时再迁）
