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
        /// 等 Touch ID 解锁（app 层门：过指纹后才读/建 token 并拉起后端）。
        case locked
        case starting
        case ready
        case failed(String)
    }

    static let shared = LocalBackendManager()

    @Published private(set) var state: State = .idle

    /// 密钥是否已配置（缓存；避免在 SwiftUI body 里每次都读 Keychain）。
    /// 绑定 Keychain + 环境变量回退，`start()` / `restart()` 时刷新。
    @Published private(set) var isConfigured: Bool

    /// 本地后端端口：Debug=dev(18924) / Release=prod(18923)，见 `LocalPaths`。
    private let port = LocalPaths.backendPort

    /// 本地访问令牌：app 生成、经 env 传给 Go；后端只比对不存储（重装换新 token 照样连）。
    /// 由 `LocalAuthService` 从 Keychain 读出/生成，解锁前为 nil。
    private(set) var token: String?

    private let logDir: URL
    private let logFile: URL
    private var process: Process?
    private var logHandle: FileHandle?
    private var tail: [String] = []
    private let tailLimit = 40
    private var stopping = false
    /// 是否有 resolveTokenAndSpawn 在途（防并发双起进程）。
    private var isResolving = false
    private var healthTask: Task<Void, Never>?

    var baseURL: String { "http://127.0.0.1:\(port)" }
    var logFileURL: URL { logFile }

    /// 本地后端是否接管寻址：显式设了 xhan_server_url 就让他自己连（逃生口）。
    static var isActive: Bool {
        (UserDefaults.standard.string(forKey: "xhan_server_url") ?? "").isEmpty
    }

    private init() {
        // 防漂移自检：数据根名在三处各有一份（`LocalPaths` / `ToolsDataRoot` / `LocalToolResultStore`），
        // 它们是同一个决定的三个副本，必须在启动时对齐（SwiftPM 跨 target 不能共享同一文件）。
        assert(
            LocalPaths.dataRootName == ToolsDataRoot.name && LocalPaths.dataRootName == LocalToolResultStore.dataRootName,
            "数据根名不一致：LocalPaths=\(LocalPaths.dataRootName) ToolsDataRoot=\(ToolsDataRoot.name) LocalToolResultStore=\(LocalToolResultStore.dataRootName)"
        )
        self.isConfigured = LocalSecretsStore.isConfigured
        self.logDir = LocalPaths.backendLogDir
        self.logFile = LocalPaths.backendLogDir.appendingPathComponent("backend.log")
    }

    // MARK: - 生命周期

    /// 启动后端。缺密钥 / 二进制缺失 / 端口占用等都在 `state` 里以人话报出。
    /// Touch ID 开启时先进入 `.locked`，由 `unlock()`（UI-10）拿 token 后再真正拉起进程。
    func start() {
        guard state == .idle else { return }
        isConfigured = LocalSecretsStore.isConfigured
        guard Self.isActive else {
            LogBuffer.shared.info("local backend 未启用（已设 xhan_server_url 逃生口）")
            return
        }
        guard isConfigured else {
            // 未配置密钥：不起进程，交给 UI-1 首次配置。
            LogBuffer.shared.info("local backend 未启动：尚未配置密钥")
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
        guard FileManager.default.fileExists(atPath: confDir.appendingPathComponent(LocalPaths.confFileName).path) else {
            state = .failed("app 内未找到后端配置目录（\(confDir.path)）。")
            return
        }
        let secrets = LocalSecretsStore.load()
        let missing = LocalBackendDiagnostics.missingSecrets(secrets)
        guard missing.isEmpty else {
            state = .failed("未配置密钥：\(missing.joined(separator: "、"))。")
            return
        }

        // Touch ID 开启时只进入锁定态**等着**——不在这里启动进程。
        // 否则它会和用户点「解锁」触发的 unlock() 并发各起一个进程（双指纹、抢同一个端口）。
        if LocalAuthService.shared.isActive {
            state = .locked
            return
        }
        state = .starting
        Task { await resolveTokenAndSpawn(binary: binary, confDir: confDir, secrets: secrets) }
    }

    /// UI-10 解锁入口（`.locked` 态下点「解锁」）。
    /// 先过指纹（app 层门），过了才读/建 token 并拉起后端；取消 → 留在锁定态可重试。
    func unlock() {
        guard state == .locked else { return }
        state = .starting
        Task {
            guard await LocalAuthService.shared.authenticate(reason: "解锁 xhanHelper 本地服务") else {
                state = .locked
                return
            }
            guard let resources = Bundle.main.resourceURL else {
                state = .failed("找不到 app 资源目录，无法启动本地后端。")
                return
            }
            await resolveTokenAndSpawn(
                binary: resources.appendingPathComponent("xhan-helper"),
                confDir: resources.appendingPathComponent("conf"),
                secrets: LocalSecretsStore.load()
            )
        }
    }

    /// 失败后重试：先停掉残留进程再重来。
    func restart() async {
        await stopAndWait()
        state = .idle
        start()
    }

    /// 逃生口：Touch ID 凭据读不出来时，关掉开关 + 删掉旧凭据 + 重启后端（走无指纹路径）。
    func disableTouchIDAndRetry() async {
        LocalAuthService.shared.disableAndResetToken()
        await restart()
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

    /// 读/建 token → 拉起子进程（指纹门在调用方 `unlock()` 已过）。
    /// - 读/写失败 → `.failed`（带逃生口）；成功 → 起进程。
    /// 重入保护：任何路径并发进入都只放行一次（防双进程）。
    private func resolveTokenAndSpawn(binary: URL, confDir: URL, secrets: LocalSecrets) async {
        guard !isResolving else { return }
        isResolving = true
        defer { isResolving = false }

        let resolved: String
        switch LocalAuthService.shared.loadOrCreateToken() {
        case .failed(let reason):
            state = .failed(reason + "\n可在设置页用「关闭 Touch ID 并重置凭据」后重试。")
            return
        case .ok(let t):
            resolved = t
        }
        token = resolved

        do {
            try FileManager.default.createDirectory(at: logDir, withIntermediateDirectories: true)
        } catch {
            // 日志目录建不出来不致命：内存 tail 仍可用于报错（有意降级）。
            LogBuffer.shared.warning("建后端日志目录失败：\(error)")
        }

        let proc = Process()
        proc.executableURL = binary
        proc.environment = LocalBackendDiagnostics.childEnvironment(
            base: ProcessInfo.processInfo.environment,
            confDir: confDir.path,
            appEnv: LocalPaths.appEnv,
            token: resolved,
            secrets: secrets
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

    private struct HealthResponse: Decodable { let status: String }
}
#endif
