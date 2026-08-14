import SwiftUI

/// fs 写工具 L3 确认条的 diff 预览卡片：
/// header（操作 + 路径 + +N -M）+ 可滚动逐行着色 diff（max 240pt）。
/// 对齐 opencode 审批体验：unified diff、加绿删红、不截断靠滚动。
struct DiffPreviewView: View {
    let diffText: String
    let additions: Int?
    let deletions: Int?
    let op: String       // "新建" | "覆盖" | "追加"（fs__write）
    let filePath: String

    private var lines: [String] {
        diffText.components(separatedBy: "\n").filter { !$0.isEmpty }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Image(systemName: op == "新建" ? "plus.square" : "square.and.pencil")
                    .font(.caption)
                    .foregroundColor(.orange)
                Text("\(op)文件")
                    .font(.caption.weight(.semibold))
                Text(filePath)
                    .font(.caption)
                    .foregroundColor(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer()
                if let a = additions, let d = deletions {
                    Text("+\(a) -\(d)")
                        .font(.caption2.monospacedDigit())
                        .foregroundColor(.secondary)
                }
            }

            ScrollView(.vertical) {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                        DiffLineView(line: line)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(maxHeight: 240)
            .background(Color.primary.opacity(0.04), in: RoundedRectangle(cornerRadius: 6))
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.secondary.opacity(0.25)))
        }
    }
}

/// 单行 diff 渲染：按前缀着色（+ 绿底 / - 红底 / @@ 灰 / 上下文普通）
private struct DiffLineView: View {
    let line: String

    private enum Kind {
        case added, removed, meta, context
    }

    private var kind: Kind {
        if line.hasPrefix("+") { return .added }
        if line.hasPrefix("-") { return .removed }
        if line.hasPrefix("@") || line.hasPrefix("---") || line.hasPrefix("+++") { return .meta }
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
            .font(.system(size: 11, design: .monospaced))
            .foregroundColor(fg)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 6)
            .padding(.vertical, 1)
            .background(bg)
    }
}
