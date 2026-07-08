import SwiftUI

struct LogDiagnosticView: View {
    @StateObject private var logBuffer = LogBuffer.shared
    @State private var selectedTab = 0
    @State private var historyFiles: [URL] = []
    @State private var expandedFile: URL?
    @State private var showExportSheet = false

    private let tabs = ["当前会话", "历史日志"]

    var body: some View {
        VStack(spacing: 0) {
            Picker("", selection: $selectedTab) {
                ForEach(tabs.indices, id: \.self) { i in
                    Text(tabs[i]).tag(i)
                }
            }
            .pickerStyle(.segmented)
            .padding()

            Group {
                if selectedTab == 0 {
                    sessionList
                } else {
                    historyList
                }
            }
        }
        .navigationTitle("诊断日志")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button {
                    showExportSheet = true
                } label: {
                    Image(systemName: "square.and.arrow.up")
                }
            }
        }
        .onAppear {
            historyFiles = LogBuffer.shared.historyFiles()
        }
        #if os(iOS)
        .sheet(isPresented: $showExportSheet) {
            if let text = logsText() {
                ActivityView(activityItems: [text])
            }
        }
        #endif
    }

    private var sessionList: some View {
        List {
            ForEach(Array(logBuffer.entries.reversed())) { entry in
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 6) {
                        Text(entry.level.icon).font(.caption)
                        Text(entry.formattedTime)
                            .font(.caption.monospaced())
                            .foregroundColor(.secondary)
                        Text("\(entry.shortFile):\(entry.line)")
                            .font(.caption)
                            .foregroundColor(.secondary)
                    }
                    Text(entry.message)
                        .font(.subheadline)
                        .foregroundColor(color(for: entry.level))
                }
                .padding(.vertical, 2)
            }
            .onDelete { indexSet in
                for offset in indexSet.sorted(by: >) {
                    LogBuffer.shared.removeEntry(at: offset)
                }
            }
        }
    }

    private var historyList: some View {
        List {
            ForEach(historyFiles, id: \.self) { file in
                Section {
                    let _ = expandedFile == file
                    Text(LogBuffer.shared.readHistoryFile(file))
                        .font(.caption.monospaced())
                        .textSelection(.enabled)
                } header: {
                    HStack {
                        Text(file.lastPathComponent)
                        Spacer()
                        if let attrs = try? FileManager.default.attributesOfItem(atPath: file.path),
                           let date = attrs[.modificationDate] as? Date {
                            Text(formatFileDate(date))
                                .font(.caption)
                                .foregroundColor(.secondary)
                        }
                    }
                }
            }
        }
    }

    private func color(for level: LogLevel) -> Color {
        switch level {
        case .debug:   return .gray
        case .info:    return .primary
        case .warning: return .orange
        case .error:   return .red
        }
    }

    private func logsText() -> String? {
        let lines = logBuffer.entries.map { entry in
            "[\(entry.level.rawValue.uppercased())] \(entry.formattedTime) \(entry.shortFile):\(entry.line) \(entry.message)"
        }
        guard !lines.isEmpty else { return nil }
        return lines.joined(separator: "\n")
    }

    private func formatFileDate(_ date: Date) -> String {
        let f = DateFormatter()
        f.dateFormat = "MM-dd HH:mm"
        return f.string(from: date)
    }
}

#if os(iOS)
struct ActivityView: UIViewControllerRepresentable {
    let activityItems: [Any]

    func makeUIViewController(context: Context) -> UIActivityViewController {
        UIActivityViewController(activityItems: activityItems, applicationActivities: nil)
    }

    func updateUIViewController(_ uiViewController: UIActivityViewController, context: Context) {}
}
#endif
