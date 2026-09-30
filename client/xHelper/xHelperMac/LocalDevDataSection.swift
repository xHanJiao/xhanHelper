#if os(macOS)
import SwiftUI
import AppKit
import UniformTypeIdentifiers

/// dev（Debug 构建）专属的开发者数据管理。
///
/// - **导入备份**：选任意 `.tar.gz`（含来自 prod 的归档），解包进 **dev 自己的数据根**；
///   不触碰 prod 数据根（restore 只动 `LocalPaths.dataRoot`，并在旁边留安全副本）。
/// - **清除 dev 数据**：清空整个 dev 数据根，回到全新空库（后端重启自动建 `me`），清除前留一份安全副本。
///
/// 仅 `LocalPaths.isDev` 时挂进设置页；prod（Release）看不到、也不受影响。
struct LocalDevDataSection: View {
    @ObservedObject private var backend = LocalBackendManager.shared

    @State private var busy = false
    @State private var errorText: String?
    @State private var infoText: String?
    @State private var activeAlert: ActiveAlert?
    @State private var pendingImport: URL?

    /// 需二次确认的动作，合并成单个 alert（同视图挂多个 .alert 会互相压制）。
    private enum ActiveAlert: Identifiable {
        case confirmImport(URL)
        case clearData

        var id: String {
            switch self {
            case .confirmImport(let url): return "ci:\(url.path)"
            case .clearData: return "clear"
            }
        }
    }

    var body: some View {
        Section {
            Button {
                chooseImportFile()
            } label: {
                Label("从文件导入备份…", systemImage: "square.and.arrow.down")
            }
            .disabled(busy)
            .help("选任意备份包（含来自 prod 的），解包进 dev 数据根；会覆盖当前 dev 数据")

            Button(role: .destructive) {
                activeAlert = .clearData
            } label: {
                Label("清除 dev 数据", systemImage: "trash")
            }
            .disabled(busy)
            .help("清空整个 dev 数据根，回到全新空库；清除前自动留一份安全副本")

            if busy {
                HStack(spacing: 6) { ProgressView().controlSize(.small); Text("处理中…").font(.caption) }
            }
            if let errorText {
                Label(errorText, systemImage: "xmark.octagon")
                    .font(.caption)
                    .foregroundColor(.red)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let infoText {
                Label(infoText, systemImage: "checkmark.circle")
                    .font(.caption)
                    .foregroundColor(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        } header: {
            Text("开发者数据（dev）")
        } footer: {
            Text("仅 Debug（dev 沙盒）可见。导入会把备份解包进 dev 数据根并留安全副本，不会写入 prod；清除数据后界面缓存建议重启 app 刷新。")
        }
        .alert("确认操作", isPresented: Binding(
            get: { activeAlert != nil },
            set: { if !$0 { activeAlert = nil } }
        ), presenting: activeAlert) { alert in
            Button("取消", role: .cancel) { activeAlert = nil }
            switch alert {
            case .confirmImport:
                Button("导入", role: .destructive) {
                    activeAlert = nil
                    Task { await importArchive() }
                }
            case .clearData:
                Button("清除", role: .destructive) {
                    activeAlert = nil
                    Task { await clearData() }
                }
            }
        } message: { alert in
            Text(alertMessage(alert))
        }
    }

    private func alertMessage(_ alert: ActiveAlert) -> String {
        switch alert {
        case .confirmImport(let url):
            return "将用「\(url.lastPathComponent)」覆盖当前 dev 数据（可能是 prod 备份），并把当前数据另存为安全副本。恢复期间本地服务会重启，此操作不可撤销。"
        case .clearData:
            return "将清空整个 dev 数据根，回到全新空库，并把当前数据另存为安全副本。此操作不可撤销。"
        }
    }

    // MARK: - 动作

    private func chooseImportFile() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = false
        panel.canChooseFiles = true
        panel.allowsMultipleSelection = false
        panel.allowedContentTypes = [.gzip]
        panel.prompt = "选择"
        panel.message = "选择备份包（xhanHelper-*.tar.gz，含来自 prod 的）"
        guard panel.runModal() == .OK, let url = panel.url else { return }
        pendingImport = url
        activeAlert = .confirmImport(url)
    }

    private func importArchive() async {
        guard let url = pendingImport else { return }
        busy = true
        errorText = nil
        infoText = nil
        let failure = await LocalDataOps.withBackendStopped(backend) {
            try LocalBackup.restore(archive: url, dataRoot: LocalPaths.dataRoot)
        }
        if let failure {
            errorText = "导入失败（已尝试回滚）：\(failure)"
        } else {
            infoText = "导入完成。当前数据已另存为安全副本（见「备份与恢复」列表）。"
        }
        busy = false
        pendingImport = nil
    }

    private func clearData() async {
        busy = true
        errorText = nil
        infoText = nil
        let failure = await LocalDataOps.withBackendStopped(backend) {
            try LocalBackup.clear(dataRoot: LocalPaths.dataRoot)
        }
        if let failure {
            errorText = "清除失败：\(failure)"
        } else {
            infoText = "已清空 dev 数据（当前数据已另存为安全副本）。后端已重建空库；建议重启 app 刷新界面缓存。"
        }
        busy = false
    }
}

#Preview("开发者数据（dev）") {
    Form {
        LocalDevDataSection()
    }
    .formStyle(.grouped)
    .frame(width: 480, height: 360)
}
#endif
