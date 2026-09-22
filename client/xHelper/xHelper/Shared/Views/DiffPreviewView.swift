import SwiftUI

/// fs 写工具 L3 确认条的 diff 预览卡片：
/// header（操作 + 路径 + +N -M）+ 可滚动逐行着色 diff（内联 max 240pt / 放大态铺满）。
/// 对齐 opencode 审批体验：unified diff、加绿删红、不截断靠滚动。
/// expanded=true 时用于放大浮层（字号加大、不限高），内联态 header 带放大角标提示可点。
struct DiffPreviewView: View {
    let diffText: String
    let additions: Int?
    let deletions: Int?
    let op: String       // "新建" | "覆盖" | "追加"（fs__write）
    let filePath: String
    var expanded: Bool = false

    private var lines: [String] {
        // 过滤 ---/+++ 文件头（卡片 header 已显示路径，避免被 +/- 前缀误着色成红绿）
        diffText.components(separatedBy: "\n")
            .filter { !$0.isEmpty && !$0.hasPrefix("--- ") && !$0.hasPrefix("+++ ") }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
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
                Spacer()
                if let a = additions, let d = deletions {
                    Text("+\(a) -\(d)")
                        .font(.caption2.monospacedDigit())
                        .foregroundColor(.secondary)
                }
                if !expanded {
                    // 可点提示：点击卡片放大预览
                    Image(systemName: "arrow.up.left.and.arrow.down.right")
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }
            }

            ScrollView(.vertical) {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                        DiffLineView(line: line, fontSize: expanded ? 13 : 11)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(maxHeight: expanded ? .infinity : 240)
            .background(Color.primary.opacity(0.04), in: RoundedRectangle(cornerRadius: 6))
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.secondary.opacity(0.25)))
        }
        .frame(maxWidth: .infinity, maxHeight: expanded ? .infinity : nil, alignment: .topLeading)
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
        "点击预览区或按 Esc 收起"
        #else
        "点击预览区收起"
        #endif
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            // 头部 + diff：点击收起区。按钮行不在其中，避免 simultaneousGesture 抢走按钮点击
            VStack(alignment: .leading, spacing: 10) {
                HStack(spacing: 8) {
                    Image(systemName: conf.displayName == "新建" ? "plus.square" : "square.and.pencil")
                        .font(.callout)
                        .foregroundColor(.orange)
                    Text("\(conf.displayName)文件")
                        .font(.headline)
                    Text(conf.filePath ?? "")
                        .font(.callout)
                        .foregroundColor(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Spacer()
                    if let a = conf.additions, let d = conf.deletions {
                        Text("+\(a) -\(d)")
                            .font(.caption.monospacedDigit())
                            .foregroundColor(.secondary)
                    }
                    Text(closeHint)
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }

                DiffPreviewView(
                    diffText: conf.diffText ?? "",
                    additions: conf.additions,
                    deletions: conf.deletions,
                    op: conf.displayName,
                    filePath: conf.filePath ?? "",
                    expanded: true
                )
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .contentShape(Rectangle())
            // 与内部 ScrollView 并存：拖拽滚动、单击收起，互不抢手势
            .simultaneousGesture(TapGesture().onEnded { onClose() })

            HStack(spacing: 8) {
                Image(systemName: "shield.lefthalf.filled")
                    .font(.caption)
                    .foregroundColor(.orange)
                Text("60s 未处理自动拒绝")
                    .font(.caption2)
                    .foregroundColor(.secondary)
                Spacer()
                Button("允许") { resolve(allow: true) }
                    .buttonStyle(.bordered)
                    .tint(.orange)
                Button("拒绝") { resolve(allow: false) }
                    .buttonStyle(.bordered)
                    .tint(.orange)
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        #if os(macOS)
        .frame(minWidth: 700, minHeight: 500)
        #endif
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(Color.secondary.opacity(0.25)))
        .padding(24)
        #if os(macOS)
        .onExitCommand { onClose() }
        #endif
        // 确认在别处被解决（超时自动拒绝 / 内联按钮）：浮层别留着失效内容
        .onChange(of: viewModel.pendingToolConfirmation?.id) { _, newID in
            if newID != conf.id { onClose() }
        }
    }

    private func resolve(allow: Bool) {
        viewModel.resolveToolConfirmation(allow: allow)
        onClose()
    }
}

/// 单行 diff 渲染：按前缀着色（+ 绿底 / - 红底 / @@ 灰 / 上下文普通）
private struct DiffLineView: View {
    let line: String
    var fontSize: CGFloat = 11

    private enum Kind {
        case added, removed, meta, context
    }

    private var kind: Kind {
        // 顺序关键：@@/---/+++ 头先判，避免 "+"/"-" 前缀误着色
        if line.hasPrefix("@") || line.hasPrefix("---") || line.hasPrefix("+++") { return .meta }
        if line.hasPrefix("+") { return .added }
        if line.hasPrefix("-") { return .removed }
        return .context
    }

    private var bg: Color {
        switch kind {
        case .added: return .green.opacity(0.22)
        case .removed: return .red.opacity(0.22)
        case .meta, .context: return .clear
        }
    }

    private var fg: Color {
        switch kind {
        case .meta: return .secondary
        case .context: return .primary
        case .added, .removed: return .primary
        }
    }

    var body: some View {
        Text(line)
            .font(.system(size: fontSize, design: .monospaced))
            .foregroundColor(fg)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 6)
            .padding(.vertical, 1)
            .background(bg)
    }
}
