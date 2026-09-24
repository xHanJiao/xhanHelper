#if os(macOS)
@preconcurrency import Foundation
import Combine
import Darwin

/// 本地后端启动器：像 `make local` 一样把 app 内嵌的 Go 二进制拉起来，退出 app 时停掉。
/// 只在 macOS 生效（iOS 仍连云端）。「内嵌」= 打包进 app，不是把 Go 编进 Swift。
///
/// 安全底线：Go 侧只监听 127.0.0.1（`conf/config.local.yaml` 的 `host`），本地 HTTP 绝不出网。
@MainActor
final class LocalBackendManager: ObservableObject {

    enum State: Equatable {
        case idle
        case starting
        case ready
        case failed(String)
    }

    static let shared = LocalBackendManager()

    @Published private(set) var state: State = .idle

    /// 本地后端端口（prod）。dev 端口 18924 留待第 4 期。
    private let port = 18923

    /// 本地访问令牌：app 生成、经 env 传给 Go；后端只比对不存储（重装换新 token 照样连）。
    /// 2B-3 起改存 Keychain（生物保护）；当前先用 UserDefaults。
    let token: String

    private let tokenKey = "xhan_local_token"
    private let logDir: URL
    private let logFile: URL
    private var process: Process?
    private var logHandle: FileHandle?
    private var tail: [String] = []
    private let tailLimit = 40
    private var stopping = false
    private var healthTask: Task<Void, Never>?

    var baseURL: String { "http://127.0.0.1:\(port)" }
    var logFileURL: URL { logFile }

    /// 本地后端是否接管寻址：显式设了 xhan_server_url 就让他自己连
    /// （逃生口；设置页入口当前仅 Debug 可达，Release 随 2B-3 / 2B-8）。
    static var isActive: Bool {
        (UserDefaults.standard.string(forKey: "xhan_server_url") ?? "").isEmpty
    }

    private init() {
        self.token = Self.loadOrCreateToken(key: tokenKey)
        let logs = FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask).first!
            .appendingPathComponent("Logs/xhanHelper", isDirectory: true)
        self.logDir = logs
        self.logFile = logs.appendingPathComponent("backend.log")
    }

    // MARK: - 生命周期

    /// 启动后端。缺密钥 / 二进制缺失 / 端口占用等都在 `state` 里以人话报出。
    func start() {
        guard state == .idle else { return }
        guard Self.isActive else {
            LogBuffer.shared.info("local backend 未启用（已设 xhan_server_url 逃生口）")
            return
        }
        guard let resources = Bundle.main.resourceURL else {
            state = .failed("找不到 app 资源目录，无法启动本地后端。")
            return
        }
        let binary = resources.appendingPathComponent("xhan-helper")
        let confDir = resources.appendingPathComponent("conf")
        guard FileManager.default.isExecutableFile(atPath: binary.path) else {
            state = .failed("app 内未找到后端二进制（\(binary.path)）。\n请确认 Xcode 的「Bundle Backend」构建阶段已成功执行。")
            return
        }
        guard FileManager.default.fileExists(atPath: confDir.appendingPathComponent("config.local.yaml").path) else {
            state = .failed("app 内未找到后端配置目录（\(confDir.path)）。")
            return
        }

        let appEnv = ProcessInfo.processInfo.environment
        let missing = LocalBackendDiagnostics.missingEnv(appEnv)
        guard missing.isEmpty else {
            state = .failed("未配置密钥：\(missing.joined(separator: "、"))。\n请在 Xcode scheme 的 Run → Environment Variables 里填写后重试。")
            return
        }

        do {
            try FileManager.default.createDirectory(at: logDir, withIntermediateDirectories: true)
        } catch {
            // 日志目录建不出来不致命：内存 tail 仍可用于报错（有意降级）。
            LogBuffer.shared.warning("建后端日志目录失败：\(error)")
        }

        let proc = Process()
        proc.executableURL = binary
        proc.environment = LocalBackendDiagnostics.childEnvironment(
            base: appEnv, confDir: confDir.path, token: token
        )
        proc.standardInput = FileHandle.nullDevice

        tail.removeAll()
        logHandle = Self.openLogHandle(at: logFile)

        let pipe = Pipe()
        proc.standardOutput = pipe
        proc.standardError = pipe
        pipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard !data.isEmpty else { return }
            Task { @MainActor [weak self] in self?.ingestLog(data) }
        }
        proc.terminationHandler = { [weak self] p in
            let code = p.terminationStatus
            Task { @MainActor [weak self] in self?.handleExit(proc: p, code: code) }
        }

        do {
            try proc.run()
        } catch {
            state = .failed("启动后端进程失败：\(error.localizedDescription)")
            return
        }
        process = proc
        stopping = false
        state = .starting
        LogBuffer.shared.info("local backend 已启动 pid=\(proc.processIdentifier) port=\(port)")
        pollHealth()
    }

    /// 失败后重试：先停掉残留进程再重来。
    func restart() async {
        await stopAndWait()
        state = .idle
        start()
    }

    /// 发 SIGTERM（不阻塞主线程）。
    func stop() {
        healthTask?.cancel()
        healthTask = nil
        guard let proc = process, proc.isRunning else {
            process = nil
            return
        }
        stopping = true
        LogBuffer.shared.info("停止 local backend pid=\(proc.processIdentifier)")
        proc.terminate()
    }

    /// 停止并最多等 2 秒，超时 SIGKILL（app 退出路径用，保证无残留）。
    func stopAndWait() async {
        stop()
        guard let proc = process else { return }
        for _ in 0..<20 {
            if !proc.isRunning { break }
            try? await Task.sleep(nanoseconds: 100_000_000)
        }
        if proc.isRunning {
            kill(proc.processIdentifier, SIGKILL)
        }
        process = nil
    }

    /// 退出时同步停（`applicationWillTerminate` 无法 await）。
    func stopBlocking(timeout: TimeInterval = 2) {
        stop()
        guard let proc = process else { return }
        let deadline = Date().addingTimeInterval(timeout)
        while proc.isRunning && Date() < deadline {
            usleep(50_000)
        }
        if proc.isRunning {
            kill(proc.processIdentifier, SIGKILL)
        }
        process = nil
    }

    // MARK: - 内部

    private func ingestLog(_ data: Data) {
        logHandle?.write(data)
        tail.append(contentsOf: String(decoding: data, as: UTF8.self)
            .split(separator: "\n", omittingEmptySubsequences: true)
            .map(String.init))
        if tail.count > tailLimit {
            tail.removeFirst(tail.count - tailLimit)
        }
    }

    private func handleExit(proc: Process, code: Int32) {
        // 旧进程（restart 后）的迟到回调：忽略，别把新进程的状态冲掉。
        guard proc === process else { return }
        healthTask?.cancel()
        healthTask = nil
        process = nil
        guard !stopping else {
            state = .idle
            return
        }
        var reason = "后端进程意外退出（退出码 \(code)）。"
        if let hint = LocalBackendDiagnostics.hint(logTail: tail.joined(separator: "\n")) {
            reason += "\n\(hint)"
        }
        let tailText = tail.suffix(12).joined(separator: "\n")
        if !tailText.isEmpty {
            reason += "\n\n最近日志：\n\(tailText)"
        }
        state = .failed(reason)
    }

    private func pollHealth() {
        healthTask?.cancel()
        healthTask = Task { [weak self] in
            let deadline = Date().addingTimeInterval(60)
            while !Task.isCancelled {
                if let status = await self?.fetchHealth() {
                    if status == "ready" {
                        self?.state = .ready
                        LogBuffer.shared.info("local backend ready")
                        return
                    }
                    // "starting"：HTTP 已就绪、后台预热中，继续等。
                }
                if Date() > deadline {
                    let tailText = (self?.tail.suffix(12).joined(separator: "\n")) ?? ""
                    self?.state = .failed("后端启动超时（60 秒内未就绪）。\n\n最近日志：\n\(tailText)")
                    return
                }
                try? await Task.sleep(nanoseconds: 500_000_000)
            }
        }
    }

    /// 轮询健康。连不上 = 还没起来（预期状态），返回 nil 继续等，不算错误。
    private func fetchHealth() async -> String? {
        guard let url = URL(string: "\(baseURL)/api/health") else { return nil }
        var req = URLRequest(url: url)
        req.timeoutInterval = 2
        do {
            let (data, resp) = try await URLSession.shared.data(for: req)
            guard (resp as? HTTPURLResponse)?.statusCode == 200 else { return nil }
            return try JSONDecoder().decode(HealthResponse.self, from: data).status
        } catch {
            return nil
        }
    }

    /// 打开后端日志用于追加：保留上一次运行的内容（失败重试时崩溃证据不能丢）；
    /// 超过上限才截断，避免无限增长。
    /// ponytail: 单文件 + 大小上限够用；要按天滚动再加。
    private static func openLogHandle(at url: URL) -> FileHandle? {
        let maxBytes = 5 * 1024 * 1024
        let fm = FileManager.default
        let existingSize = (try? fm.attributesOfItem(atPath: url.path)[.size]) as? UInt64
        if existingSize == nil || (existingSize ?? 0) > maxBytes {
            if !fm.createFile(atPath: url.path, contents: nil) {
                LogBuffer.shared.warning("创建/截断后端日志失败：\(url.path)")
            }
        }
        do {
            let handle = try FileHandle(forWritingTo: url)
            try handle.seekToEnd()
            let stamp = ISO8601DateFormatter().string(from: Date())
            handle.write(Data("\n===== backend start @ \(stamp) =====\n".utf8))
            return handle
        } catch {
            LogBuffer.shared.warning("打开后端日志失败（\(url.path)）：\(error)")
            return nil
        }
    }

    private static func loadOrCreateToken(key: String) -> String {
        if let existing = UserDefaults.standard.string(forKey: key), !existing.isEmpty {
            return existing
        }
        let token = (UUID().uuidString + UUID().uuidString).replacingOccurrences(of: "-", with: "")
        UserDefaults.standard.set(token, forKey: key)
        return token
    }

    private struct HealthResponse: Decodable { let status: String }
}
#endif
