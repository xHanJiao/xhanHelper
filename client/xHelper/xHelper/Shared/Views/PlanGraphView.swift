import SwiftUI

struct PlanGraphView: View {
    let nodes: [PlanNodeInfo]
    let edges: [(Int, Int)]

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Image(systemName: "square.grid.2x2")
                    .font(.caption).foregroundColor(.orange)
                Text("执行计划").font(.caption).fontWeight(.medium)
                Spacer()
            }
            ScrollView(.horizontal) {
                HStack(spacing: 8) {
                    ForEach(Array(nodes.enumerated()), id: \.offset) { i, node in
                        HStack(spacing: 2) {
                            if i > 0 { Text("→").font(.caption2).foregroundColor(.secondary) }
                            VStack(spacing: 2) {
                                Circle()
                                    .fill(color(node.status))
                                    .frame(width: 14, height: 14)
                                Text(node.label)
                                    .font(.system(size: 8))
                                    .lineLimit(1)
                                    .frame(width: 60)
                            }
                        }
                    }
                }
            }
        }
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 12).stroke(Color.orange, lineWidth: 1))
        .padding(.horizontal)
    }

    func color(_ s: String) -> Color {
        switch s {
        case "done":    return .green
        case "running": return .blue
        case "failed":  return .red
        default:        return .gray
        }
    }
}
