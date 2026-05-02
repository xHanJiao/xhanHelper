import SwiftUI

struct PlanNodeInfo: Identifiable {
    let id: String
    let label: String
    let status: String // pending | running | done | failed
    let duration: String?
}

struct PlanProgressView: View {
    let nodes: [PlanNodeInfo]

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Image(systemName: "square.grid.2x2")
                    .font(.caption).foregroundColor(.orange)
                Text("执行计划").font(.caption).fontWeight(.medium)
                Spacer()
            }

            ForEach(nodes) { node in
                HStack(spacing: 6) {
                    statusIcon(node.status)
                    Text(node.label).font(.caption2)
                    Spacer()
                    if let d = node.duration {
                        Text(d).font(.caption2).foregroundColor(.secondary)
                    }
                }
            }
        }
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 12).stroke(Color.orange, lineWidth: 1))
        .padding(.horizontal)
    }

    @ViewBuilder
    func statusIcon(_ s: String) -> some View {
        switch s {
        case "done":
            Image(systemName: "checkmark.circle.fill").font(.caption).foregroundColor(.green)
        case "running":
            ProgressView().scaleEffect(0.4).frame(width: 12, height: 12)
        case "failed":
            Image(systemName: "xmark.circle.fill").font(.caption).foregroundColor(.red)
        default:
            Circle().stroke(Color.gray, lineWidth: 1).frame(width: 10, height: 10)
        }
    }
}
