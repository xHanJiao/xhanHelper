#if os(macOS)
import SwiftUI
import AppKit

extension Notification.Name {
    /// 恢复 / 导入 / 清除 / 删除改变了数据根或备份目录后广播，供展示列表的视图刷新。
    static let localBackupDidChange = Notification.Name("xhan_local_backup_did_change")
}

/// 停后端 → 跑文件操作 → 重启后端的公共编排（备份/恢复/清除三处共用）。
@MainActor
enum LocalDataOps {
    /// - Parameter op: 在无写入者时执行的文件操作。
    /// - Returns: 出错时的描述（nil = 成功）。
    static func withBackendStopped(_ backend: LocalBackendManager, _ op: () throws -> Void) async -> String? {
        await backend.stopAndWait()
        var failure: String?
        do { try op() } catch { failure = error.localizedDescription }
        await backend.restart()
        NotificationCenter.default.post(name: .localBackupDidChange, object: nil)
        return failure
    }
}

/// UI-11：本地数据备份与恢复。
///
/// 备份引擎在 Go（每小时检查、到期才跑）；本视图只负责设置、列表、立即备份、恢复。
/// 设置存 UserDefaults（`LocalBackupSettings` 的键），经子进程 env 注入 Go；改完需「保存并重启后端」。
struct LocalBackupSection: View {
    @ObservedObject private var backend = LocalBackendManager.shared

    @AppStorage(LocalBackupSettings.enabledKey) private var enabled = false
    @AppStorage(LocalBackupSettings.destKey) private var dest = ""
    @AppStorage(LocalBackupSettings.intervalDaysKey) private var intervalDays = 1
    @AppStorage(LocalBackupSettings.dailyKeepKey) private var dailyKeep = 3
    @AppStorage(LocalBackupSettings.weeklyKeepKey) private var weeklyKeep = 1

    @State private var files: [LocalBackupFile] = []
    @State private var safetyCopies: [LocalBackupSafety] = []
    @State private var dirty = false
    @State private var busy = false
    @State private var errorText: String?
    @State private var infoText: String?
    @State private var activeAlert: ActiveAlert?

    /// 需二次确认的动作，合并成单个 alert（同视图挂多个 .alert 会互相压制）。
    private enum ActiveAlert: Identifiable {
        case restoreArchive(LocalBackupFile)
        case deleteArchive(LocalBackupFile)
        case restoreSafety(LocalBackupSafety)
        case deleteSafety(LocalBackupSafety)

        var id: String {
            switch self {
            case .restoreArchive(let f): return "ra:\(f.id)"
            case .deleteArchive(let f): return "da:\(f.id)"
            case .restoreSafety(let s): return "rs:\(s.id)"
            case .deleteSafety(let s): return "ds:\(s.id)"
            }
        }
    }

    private var apiClient: APIClient? {
        guard let token = backend.token else { return nil }
        return APIClient(baseURL: backend.baseURL, token: token)
    }

    var body: some View {
        Section {
            Toggle("自动备份", isOn: $enabled)

            LabeledContent("备份位置") {
                HStack(spacing: 6) {
                    Text(dest.isEmpty ? "未选择" : dest)
                        .font(.system(.caption, design: .monospaced))
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .foregroundColor(dest.isEmpty ? .secondary : .primary)
                    Button("选择…") { chooseDirectory() }
                    if !dest.isEmpty {
                        Button("打开") { NSWorkspace.shared.open(URL(fileURLWithPath: dest)) }
                            .buttonStyle(.borderless)
                    }
                }
            }

            LabeledContent("间隔（天）") {
                Stepper("\(intervalDays)", value: $intervalDays, in: 1...30)
            }
            LabeledContent("保留最近") {
                Stepper("\(dailyKeep) 次", value: $dailyKeep, in: 1...30)
            }
            LabeledContent("额外保留") {
                Stepper("\(weeklyKeep) 份（≥7 天前）", value: $weeklyKeep, in: 1...10)
            }

            HStack(spacing: 10) {
                Button {
                    Task { await backupNow() }
                } label: {
                    if busy {
                        HStack(spacing: 6) { ProgressView().controlSize(.small); Text("处理中…") }
                    } else {
                        Text("立即备份")
                    }
                }
                .disabled(busy || dirty || !enabled || dest.isEmpty)
                .help(!enabled ? "请先开启自动备份" : (dirty ? "请先「保存并重启后端」使设置生效" : ""))

                Button("保存并重启后端") { Task { await saveAndRestart() } }
                    .disabled(!dirty || (enabled && dest.isEmpty))
                    .help(enabled && dest.isEmpty ? "开启自动备份前请先选择备份位置" : "")

                Button("刷新列表") { reload() }
                    .buttonStyle(.borderless)
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

            if files.isEmpty {
                Text(enabled ? "暂无备份（到点或点「立即备份」生成）" : "开启后才会生成备份")
                    .font(.caption)
                    .foregroundColor(.secondary)
            } else {
                ForEach(files) { file in
                    // 非本环境归档（如 dev 看到 prod）只许「导入」，绝不给「删除」。
                    let foreign = file.env != LocalBackup.env
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            HStack(spacing: 4) {
                                Text(file.name)
                                    .font(.system(.caption, design: .monospaced))
                                    .lineLimit(1)
                                    .truncationMode(.middle)
                                if let env = file.env, foreign {
                                    Text(env)
                                        .font(.caption2)
                                        .foregroundColor(.orange)
                                }
                            }
                            Text("\(byteText(file.bytes)) · \(dateText(file.date))")
                                .font(.caption2)
                                .foregroundColor(.secondary)
                        }
                        Spacer()
                        Button(foreign ? "导入到 dev" : "恢复", role: .destructive) { activeAlert = .restoreArchive(file) }
                            .buttonStyle(.borderless)
                            .help(foreign ? "解包进 dev 数据根，不改动「\(file.env ?? "?")」环境" : "")
                        if !foreign {
                            Button("删除", role: .destructive) { activeAlert = .deleteArchive(file) }
                                .buttonStyle(.borderless)
                        }
                    }
                    .disabled(busy)
                }
            }

            if !safetyCopies.isEmpty {
                Text("安全副本（恢复前自动保存的原数据，留最近 \(LocalBackup.safetyKeep) 份）")
                    .font(.caption2)
                    .foregroundColor(.secondary)
                ForEach(safetyCopies) { safety in
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(safety.name)
                                .font(.system(.caption, design: .monospaced))
                                .lineLimit(1)
                                .truncationMode(.middle)
                            Text("\(byteText(safety.bytes)) · \(dateText(safety.date))")
                                .font(.caption2)
                                .foregroundColor(.secondary)
                        }
                        Spacer()
                        Button("恢复", role: .destructive) { activeAlert = .restoreSafety(safety) }
                            .buttonStyle(.borderless)
                        Button("删除", role: .destructive) { activeAlert = .deleteSafety(safety) }
                            .buttonStyle(.borderless)
                    }
                    .disabled(busy)
                }
            }
        } header: {
            Text("备份与恢复")
        } footer: {
            Text("每 N 天自动把整个数据根打包到所选目录（可为 iCloud Drive），保留最近几次 + 额外一份 ≥7 天前的。只在 app 运行时备份；睡眠/退出后下次启动补跑。改设置后点「保存并重启后端」生效。")
        }
        .onAppear { reload() }
        .onReceive(NotificationCenter.default.publisher(for: .localBackupDidChange)) { _ in reload() }
        .onChange(of: enabled) { _, _ in dirty = true }
        .onChange(of: dest) { _, _ in dirty = true }
        .onChange(of: intervalDays) { _, _ in dirty = true }
        .onChange(of: dailyKeep) { _, _ in dirty = true }
        .onChange(of: weeklyKeep) { _, _ in dirty = true }
        .alert("确认操作", isPresented: Binding(
            get: { activeAlert != nil },
            set: { if !$0 { activeAlert = nil } }
        ), presenting: activeAlert) { alert in
            Button("取消", role: .cancel) { activeAlert = nil }
            switch alert {
            case .restoreArchive(let file):
                Button("恢复", role: .destructive) {
                    activeAlert = nil
                    Task { await restoreArchive(file) }
                }
            case .deleteArchive(let file):
                Button("删除", role: .destructive) {
                    activeAlert = nil
                    deleteArchive(file)
                }
            case .restoreSafety(let safety):
                Button("恢复", role: .destructive) {
                    activeAlert = nil
                    Task { await restoreSafety(safety) }
                }
            case .deleteSafety(let safety):
                Button("删除", role: .destructive) {
                    activeAlert = nil
                    deleteSafety(safety)
                }
            }
        } message: { alert in
            Text(alertMessage(alert))
        }
    }

    private func alertMessage(_ alert: ActiveAlert) -> String {
        switch alert {
        case .restoreArchive(let f):
            return "将用「\(f.name)」覆盖当前数据，并把当前数据另存为安全副本。恢复期间本地服务会重启，此操作不可撤销。"
        case .deleteArchive(let f):
            return "删除备份「\(f.name)」？此操作不可撤销。"
        case .restoreSafety(let s):
            return "将把数据恢复到「\(s.name)」保存的状态，并把当前数据另存为新的安全副本。恢复期间本地服务会重启，此操作不可撤销。"
        case .deleteSafety(let s):
            return "删除安全副本「\(s.name)」？删除后无法再回退到该状态。"
        }
    }

    // MARK: - 动作

    private func reload() {
        // dev 下不过滤环境，能列出 prod 归档；prod 仍只看本环境。
        files = LocalBackup.list(dest: dest, allEnvs: LocalPaths.isDev)
        safetyCopies = LocalBackup.listSafety(dataRoot: LocalPaths.dataRoot)
    }

    private func chooseDirectory() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "选择"
        panel.message = "选择备份存放目录（可放入 iCloud Drive）"
        guard panel.runModal() == .OK, let url = panel.url else { return }
        // 两个环境的数据根都不许选（避免把本环境归档写进另一个环境的目录）。
        for root in LocalPaths.allDataRoots {
            if url.path == root.path || url.path.hasPrefix(root.path + "/") {
                errorText = "备份位置不能选在数据根（\(root.path)）内"
                return
            }
        }
        dest = url.path
        errorText = nil
    }

    private func backupNow() async {
        guard let api = apiClient else {
            errorText = "本地后端未就绪，稍后重试"
            return
        }
        busy = true
        errorText = nil
        infoText = nil
        do {
            let archive = try await api.triggerLocalBackup()
            infoText = "已备份：\(archive.name)"
            reload()
        } catch {
            errorText = "备份失败：\(error.localizedDescription)"
        }
        busy = false
    }

    private func saveAndRestart() async {
        errorText = nil
        infoText = nil
        dirty = false
        await backend.restart()
        reload()
    }

    private func restoreArchive(_ file: LocalBackupFile) async {
        busy = true
        errorText = nil
        infoText = nil
        let failure = await LocalDataOps.withBackendStopped(backend) {
            try LocalBackup.restore(archive: file.url, dataRoot: LocalPaths.dataRoot)
        }
        if let failure {
            errorText = "恢复失败（已尝试回滚）：\(failure)"
        } else {
            infoText = "恢复完成。当前数据已另存为安全副本（见下方列表），确认数据正常后可删除。"
        }
        busy = false
        reload()
    }

    private func restoreSafety(_ safety: LocalBackupSafety) async {
        busy = true
        errorText = nil
        infoText = nil
        let failure = await LocalDataOps.withBackendStopped(backend) {
            try LocalBackup.restoreFromSafety(safety: safety.url, dataRoot: LocalPaths.dataRoot)
        }
        if let failure {
            errorText = "从安全副本恢复失败（已尝试回滚）：\(failure)"
        } else {
            infoText = "已从安全副本恢复。当前数据已另存为新的安全副本（见下方列表）。"
        }
        busy = false
        reload()
    }

    private func deleteArchive(_ file: LocalBackupFile) {
        do {
            try LocalBackup.delete(file)
            infoText = "已删除备份：\(file.name)"
            errorText = nil
            reload()
        } catch {
            errorText = "删除备份失败：\(error.localizedDescription)"
        }
    }

    private func deleteSafety(_ safety: LocalBackupSafety) {
        do {
            try LocalBackup.deleteSafety(safety)
            infoText = "已删除安全副本：\(safety.name)"
            errorText = nil
            reload()
        } catch {
            errorText = "删除安全副本失败：\(error.localizedDescription)"
        }
    }

    // MARK: - 格式化

    private func byteText(_ bytes: Int64) -> String {
        let f = ByteCountFormatter()
        f.countStyle = .file
        return f.string(fromByteCount: bytes)
    }

    private func dateText(_ date: Date) -> String {
        let f = DateFormatter()
        f.dateFormat = "yyyy-MM-dd HH:mm"
        return f.string(from: date)
    }
}

#Preview("UI-11 备份与恢复") {
    Form {
        LocalBackupSection()
    }
    .formStyle(.grouped)
    .frame(width: 480, height: 620)
}
#endif
