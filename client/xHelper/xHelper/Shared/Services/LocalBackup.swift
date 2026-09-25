import Foundation

/// 本地数据备份的客户端侧：设置读写、归档列表、恢复。
///
/// 备份引擎在 Go（`internal/backup`）：`VACUUM INTO` 一致性快照 + 数据根整体 tar.gz。
/// 这里只负责：
/// - 设置（存 UserDefaults，经 `LocalBackendDiagnostics.childEnvironment` 以 `XHAN_BACKUP_*` 注入 Go）；
/// - 列归档（直接读目标目录，不走 API）；
/// - 恢复（停后端后由 Swift 调系统 `/usr/bin/tar` 解包）。
enum LocalBackupSettings {

    static let enabledKey = "xhan_backup_enabled"
    static let destKey = "xhan_backup_dir"
    static let intervalDaysKey = "xhan_backup_interval_days"
    static let dailyKeepKey = "xhan_backup_daily_keep"
    static let weeklyKeepKey = "xhan_backup_weekly_keep"

    struct Values {
        var enabled: Bool
        var dest: String
        var intervalDays: Int
        var dailyKeep: Int
        var weeklyKeep: Int
    }

    static func load() -> Values {
        let d = UserDefaults.standard
        func intValue(_ key: String, _ fallback: Int) -> Int {
            d.object(forKey: key) as? Int ?? fallback
        }
        return Values(
            enabled: d.bool(forKey: enabledKey),
            dest: d.string(forKey: destKey) ?? "",
            intervalDays: intValue(intervalDaysKey, 1),
            dailyKeep: intValue(dailyKeepKey, 3),
            weeklyKeep: intValue(weeklyKeepKey, 1)
        )
    }
}

/// 一份备份归档（客户端视角，用于列表展示）。
struct LocalBackupFile: Identifiable, Hashable {
    let name: String
    let url: URL
    let bytes: Int64
    let date: Date

    var id: String { url.path }
}

/// 一份「安全副本」：恢复前把原数据根整体改名保存下来的目录。
struct LocalBackupSafety: Identifiable, Hashable {
    let url: URL
    let bytes: Int64
    let date: Date

    var name: String { url.lastPathComponent }
    var id: String { url.path }
}

enum LocalBackup {
    static let archivePrefixBase = "xhanHelper"

    static var env: String { LocalPaths.env }

    static func archivePrefix(env: String = LocalBackup.env) -> String {
        "\(archivePrefixBase)-\(env)-"
    }

    static let stampFormatter: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "yyyyMMdd-HHmmss"
        f.locale = Locale(identifier: "en_US_POSIX")
        return f
    }()

    /// 从 `xhanHelper-<env>-YYYYMMDD-HHMMSS.tar.gz` 解析时间；失败返回 nil。
    static func parseDate(name: String) -> Date? {
        guard name.hasPrefix(archivePrefixBase + "-"), name.hasSuffix(".tar.gz") else { return nil }
        let base = name.replacingOccurrences(of: ".tar.gz", with: "")
        let parts = base.split(separator: "-")
        guard parts.count >= 3 else { return nil }
        let stamp = "\(parts[parts.count - 2])-\(parts[parts.count - 1])"
        return stampFormatter.date(from: stamp)
    }

    /// 读目标目录里本环境的归档，按时间倒序。
    static func list(dest: String) -> [LocalBackupFile] {
        guard !dest.isEmpty else { return [] }
        let fm = FileManager.default
        guard let names = try? fm.contentsOfDirectory(atPath: dest) else { return [] }
        let prefix = archivePrefix()
        var out: [LocalBackupFile] = []
        for name in names where name.hasPrefix(prefix) && name.hasSuffix(".tar.gz") {
            let url = URL(fileURLWithPath: dest).appendingPathComponent(name)
            let attrs = try? fm.attributesOfItem(atPath: url.path)
            let size = (attrs?[.size] as? NSNumber)?.int64Value ?? 0
            let mtime = (attrs?[.modificationDate] as? Date) ?? .distantPast
            out.append(LocalBackupFile(
                name: name,
                url: url,
                bytes: size,
                date: parseDate(name: name) ?? mtime
            ))
        }
        return out.sorted { $0.date > $1.date }
    }

    // MARK: - 安全副本

    /// 自动保留的安全副本份数（每次恢复产生一份整根目录）。
    static let safetyKeep = 3

    static func safetyPrefix(dataRoot: URL) -> String {
        "\(dataRoot.lastPathComponent).pre-restore-"
    }

    /// 扫数据根的父目录，发现本数据根的安全副本，按时间倒序。
    /// 从磁盘发现而非内存记录，app 重启后仍在。
    static func listSafety(dataRoot: URL) -> [LocalBackupSafety] {
        let fm = FileManager.default
        let parent = dataRoot.deletingLastPathComponent()
        let prefix = safetyPrefix(dataRoot: dataRoot)
        guard let names = try? fm.contentsOfDirectory(atPath: parent.path) else { return [] }
        var out: [LocalBackupSafety] = []
        for name in names where name.hasPrefix(prefix) {
            let url = parent.appendingPathComponent(name)
            var isDir: ObjCBool = false
            guard fm.fileExists(atPath: url.path, isDirectory: &isDir), isDir.boolValue else { continue }
            let stamp = String(name.dropFirst(prefix.count))
            let mtime = (try? fm.attributesOfItem(atPath: url.path))?[.modificationDate] as? Date
            out.append(LocalBackupSafety(
                url: url,
                bytes: directorySize(url),
                date: stampFormatter.date(from: stamp) ?? mtime ?? .distantPast
            ))
        }
        return out.sorted { $0.date > $1.date }
    }

    static func delete(_ file: LocalBackupFile) throws {
        try FileManager.default.removeItem(at: file.url)
    }

    static func deleteSafety(_ safety: LocalBackupSafety) throws {
        try FileManager.default.removeItem(at: safety.url)
    }

    /// 只保留最新 `keep` 份安全副本，其余删除（降级：失败只记日志，不打断恢复）。
    static func pruneSafety(dataRoot: URL, keep: Int = safetyKeep) {
        let all = listSafety(dataRoot: dataRoot)
        guard all.count > keep else { return }
        for old in all.dropFirst(keep) {
            do {
                try FileManager.default.removeItem(at: old.url)
            } catch {
                print("[LocalBackup] prune safety failed: \(old.url.path): \(error)")
            }
        }
    }

    /// 目录内所有普通文件大小之和（best-effort，失败计 0）。
    static func directorySize(_ url: URL) -> Int64 {
        let fm = FileManager.default
        guard let e = fm.enumerator(
            at: url,
            includingPropertiesForKeys: [.fileSizeKey, .isRegularFileKey],
            options: []
        ) else { return 0 }
        var total: Int64 = 0
        for case let u as URL in e {
            let v = try? u.resourceValues(forKeys: [.fileSizeKey, .isRegularFileKey])
            if v?.isRegularFile == true { total += Int64(v?.fileSize ?? 0) }
        }
        return total
    }
}

#if os(macOS)

enum LocalBackupRestoreError: LocalizedError {
    case archiveMissingDB
    case tarFailed(String)

    var errorDescription: String? {
        switch self {
        case .archiveMissingDB:
            return "所选备份里没有数据库文件（xhan_helper.db），拒绝恢复。"
        case .tarFailed(let detail):
            return "解包失败：\(detail)"
        }
    }
}

extension LocalBackup {

    /// 恢复：把当前数据根整体改名成安全副本，再解包归档到原数据根；失败自动回滚。
    /// 调用前必须已停掉后端（无写入者）。返回安全副本路径（成功后由 UI 提示是否删除）。
    @discardableResult
    static func restore(archive: URL, dataRoot: URL) throws -> URL {
        let fm = FileManager.default
        guard archiveContainsDB(archive) else { throw LocalBackupRestoreError.archiveMissingDB }

        let parent = dataRoot.deletingLastPathComponent()
        let safety = parent.appendingPathComponent(
            "\(dataRoot.lastPathComponent).pre-restore-\(stampFormatter.string(from: Date()))"
        )

        let hadRoot = fm.fileExists(atPath: dataRoot.path)
        if hadRoot {
            try fm.moveItem(at: dataRoot, to: safety)
        }
        do {
            try fm.createDirectory(at: dataRoot, withIntermediateDirectories: true)
            let res = try runProcess("/usr/bin/tar", ["-xzf", archive.path, "-C", dataRoot.path])
            guard res.status == 0 else {
                throw LocalBackupRestoreError.tarFailed(res.stderr.isEmpty ? "退出码 \(res.status)" : res.stderr)
            }
            guard fm.fileExists(atPath: dataRoot.appendingPathComponent("xhan_helper.db").path) else {
                throw LocalBackupRestoreError.archiveMissingDB
            }
        } catch {
            try? fm.removeItem(at: dataRoot)
            if hadRoot, fm.fileExists(atPath: safety.path) {
                try? fm.moveItem(at: safety, to: dataRoot)
            }
            throw error
        }
        pruneSafety(dataRoot: dataRoot)
        return safety
    }

    /// 从安全副本恢复：把当前数据根改名成新安全副本，再把选中的副本改名到数据根；失败回滚。
    /// 调用前必须已停掉后端。返回新安全副本路径（本次被换下的当前数据）。
    @discardableResult
    static func restoreFromSafety(safety: URL, dataRoot: URL) throws -> URL {
        let fm = FileManager.default
        guard fm.fileExists(atPath: safety.appendingPathComponent("xhan_helper.db").path) else {
            throw LocalBackupRestoreError.archiveMissingDB
        }

        let parent = dataRoot.deletingLastPathComponent()
        let backup = parent.appendingPathComponent(
            "\(dataRoot.lastPathComponent).pre-restore-\(stampFormatter.string(from: Date()))"
        )

        let hadRoot = fm.fileExists(atPath: dataRoot.path)
        if hadRoot {
            try fm.moveItem(at: dataRoot, to: backup)
        }
        do {
            try fm.moveItem(at: safety, to: dataRoot)
        } catch {
            if hadRoot, fm.fileExists(atPath: backup.path) {
                try? fm.moveItem(at: backup, to: dataRoot)
            }
            throw error
        }
        pruneSafety(dataRoot: dataRoot)
        return backup
    }

    /// 归档里是否含顶层 `xhan_helper.db`（解包前校验，拒绝坏归档）。
    static func archiveContainsDB(_ archive: URL) -> Bool {
        guard let res = try? runProcess("/usr/bin/tar", ["-tzf", archive.path]), res.status == 0 else {
            return false
        }
        return res.stdout.split(separator: "\n").contains { $0.trimmingCharacters(in: .whitespaces) == "xhan_helper.db" }
    }

    /// 运行外部进程并捕获输出。
    /// ponytail: 顺序读 stdout→stderr；tar 的 stderr 极小，正常不会撑满管道造成死锁。
    static func runProcess(_ path: String, _ args: [String]) throws -> (status: Int32, stdout: String, stderr: String) {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: path)
        p.arguments = args
        let outPipe = Pipe()
        let errPipe = Pipe()
        p.standardOutput = outPipe
        p.standardError = errPipe
        try p.run()
        let outData = outPipe.fileHandleForReading.readDataToEndOfFile()
        let errData = errPipe.fileHandleForReading.readDataToEndOfFile()
        p.waitUntilExit()
        return (
            p.terminationStatus,
            String(decoding: outData, as: UTF8.self),
            String(decoding: errData, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        )
    }
}

#endif
