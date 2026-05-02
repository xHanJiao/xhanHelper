import SwiftUI
import Grape

struct PlanGraphView: View {
    let nodes: [PlanNodeInfo]
    let edges: [(Int, Int)]

    @State private var graphStates = ForceDirectedGraphState()

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            headerView

            if nodes.isEmpty {
                loadingView
            } else {
                graphView
            }
        }
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 12).stroke(Color.orange, lineWidth: 1))
        .padding(.horizontal)
    }

    var headerView: some View {
        HStack {
            Image(systemName: "square.grid.2x2")
                .font(.caption).foregroundColor(.orange)
            Text("执行计划").font(.caption).fontWeight(.medium)
            Spacer()
        }
    }

    var loadingView: some View {
        HStack {
            ProgressView().scaleEffect(0.5)
            Text("准备中...").font(.caption).foregroundColor(.secondary)
        }
    }

    var graphView: some View {
        ForceDirectedGraph(states: graphStates) {
            Series(nodes.indices) { i in
                NodeMark(id: i)
                    .foregroundStyle(self.nodeColor(nodes[i].status))
                    .symbolSize(radius: 14)
            }
            Series(edges.indices) { i in
                LinkMark(from: edges[i].0, to: edges[i].1)
                    .stroke(.gray.opacity(0.3))
            }
        } force: {
            .link()
            .center()
            .manyBody()
        }
        .frame(height: 140)
    }

    func nodeColor(_ s: String) -> Color {
        switch s {
        case "done":    return .green
        case "running": return .blue
        case "failed":  return .red
        default:        return .gray
        }
    }
}
