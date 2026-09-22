import SwiftUI

/// fs 写工具 L3 确认条的 diff 预览卡片：
/// header（操作 + 路径 + +N -M）+ 可滚动逐行着色 diff（内联 max 240pt / 放大态铺满）。
/// 行渲染：3pt 左色条 + 行号 + 极淡整行底（不再满幅红绿色带）；底色走 surfaceInput token。
/// expanded=true 时用于放大浮层（字号加大、不限高），内联态 header 带放大角标提示可点。
struct DiffPreviewView: View {
    let diffText: String
    let additions: Int?
    let deletions: Int?
    let op: String       // "新建" | "覆盖" | "追加"（fs__write）
    let filePath: String
    var expanded: Bool = false
    /// 放大浮层用：卡内底部操作行（允许/拒绝）。内联态为 nil，按钮由 ChatView 放在卡外。
    var footer: AnyView? = nil

    private var lines: [DiffLineParser.Line] {
        DiffLineParser.parse(diffText)
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            hairline

            ScrollView(.vertical) {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                        DiffLineView(line: line, fontSize: expanded ? 13 : 11)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(maxHeight: expanded ? .infinity : 240)

            if let footer {
                hairline
                footer
            }
        }
        .background(Color("surfaceInput"), in: RoundedRectangle(cornerRadius: 12))
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(Color("assistantText").opacity(0.12)))
        .frame(maxWidth: .infinity, maxHeight: expanded ? .infinity : nil, alignment: .topLeading)
    }

    private var hairline: some View {
        Rectangle()
            .fill(Color("assistantText").opacity(0.1))
            .frame(height: 1)
    }

    private var header: some View {
        HStack(spacing: 6) {
            Image(systemName: op == "新建" ? "plus.square" : "square.and.pencil")
                .font(.caption)
                .foregroundColor(.orange)
            Text("\(op)文件")
                .font(expanded ? .callout.weight(.semibold) : .caption.weight(.semibold))
            Text(filePath)
                .font(expanded ? .callout : .caption)
                .foregroundColor(.secondary)
                .lineLimit(1)
                .truncationMode(.middle)
            Spacer(minLength: 8)
            if let a = additions, let d = deletions {
                statPill("+\(a)", color: Color("appAccent"))
                statPill("-\(d)", color: .red)
            }
            if !expanded {
                // 可点提示：点击卡片放大预览
                Image(systemName: "arrow.up.left.and.arrow.down.right")
                    .font(.caption2)
                    .foregroundColor(.secondary)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
    }

    private func statPill(_ text: String, color: Color) -> some View {
        Text(text)
            .font(.caption2.monospacedDigit())
            .foregroundColor(color)
            .padding(.horizontal, 6)
            .padding(.vertical, 1)
            .background(color.opacity(0.15), in: RoundedRectangle(cornerRadius: 6))
    }
}

/// 放大浮层内容（LightboxCenter 承载）：大窗口 diff + 允许/拒绝。
/// 点预览区 / Esc / 确认被解决（超时或别处操作）都会收起。
struct ExpandedDiffPreviewView: View {
    @ObservedObject var viewModel: ChatViewModel
    let conf: ToolConfirmation
    let onClose: () -> Void

    private var closeHint: String {
        #if os(macOS)
        "点预览区或按 Esc 收起"
        #else
        "点预览区收起"
        #endif
    }

    var body: some View {
        DiffPreviewView(
            diffText: conf.diffText ?? "",
            additions: conf.additions,
            deletions: conf.deletions,
            op: conf.displayName,
            filePath: conf.filePath ?? "",
            expanded: true,
            footer: AnyView(footer)
        )
        .contentShape(Rectangle())
        // 与内部 ScrollView 并存：拖拽滚动、单击收起，互不抢手势（按钮行在卡内、不在此手势下）
        .simultaneousGesture(TapGesture().onEnded { onClose() })
        #if os(macOS)
        .frame(minWidth: 700, minHeight: 500)
        #endif
        .padding(24)
        // 确认在别处被解决（超时自动拒绝 / 内联按钮）：浮层别留着失效内容
        .onChange(of: viewModel.pendingToolConfirmation?.id) { _, newID in
            if newID != conf.id { onClose() }
        }
    }

    private var footer: some View {
        HStack(spacing: 8) {
            Image(systemName: "shield.lefthalf.filled")
                .font(.caption)
                .foregroundColor(.orange)
            Text("60s 未处理自动拒绝 · \(closeHint)")
                .font(.caption2)
                .foregroundColor(.secondary)
            Spacer(minLength: 8)
            Button("允许") { resolve(allow: true) }
                .buttonStyle(.bordered)
                .tint(Color("appAccent"))
            Button("拒绝") { resolve(allow: false) }
                .buttonStyle(.bordered)
                .tint(.secondary)
        }
        .padding(.horizontal, 13)
        .padding(.vertical, 10)
    }

    private func resolve(allow: Bool) {
        viewModel.resolveToolConfirmation(allow: allow)
        onClose()
    }
}

/// 单行 diff：3pt 左色条（加绿/删红）+ 符号列 + 行号 + 极淡整行底；meta 行灰字无色条。
private struct DiffLineView: View {
    let line: DiffLineParser.Line
    var fontSize: CGFloat = 11

    private var accent: Color? {
        switch line.kind {
        case .added: return Color("appAccent")
        case .removed: return .red
        case .meta, .context: return nil
        }
    }

    private var glyph: String {
        switch line.kind {
        case .added: return "+"
        case .removed: return "-"
        case .meta, .context: return ""
        }
    }

    var body: some View {
        HStack(spacing: 0) {
            Rectangle()
                .fill(accent ?? .clear)
                .frame(width: 3)

            HStack(alignment: .firstTextBaseline, spacing: 7) {
                Text(glyph)
                    .font(.system(size: fontSize, design: .monospaced))
                    .foregroundColor(accent ?? .clear)
                    .frame(width: fontSize * 0.8)
                Text(line.number.map(String.init) ?? "")
                    .font(.system(size: fontSize, design: .monospaced))
                    .foregroundColor(.secondary)
                    .frame(width: fontSize * 2.4, alignment: .trailing)
                Text(line.text)
                    .font(.system(size: fontSize, design: .monospaced))
                    .foregroundColor(line.kind == .meta ? .secondary : Color("assistantText"))
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 2)
            .background((accent ?? .clear).opacity(0.06))
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}
