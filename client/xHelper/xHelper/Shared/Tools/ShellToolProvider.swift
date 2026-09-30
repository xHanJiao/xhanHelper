#if os(macOS)
import Foundation
import Darwin

/// macOS 本地 Shell 工具：`shell__run`。
///
/// 分级与确认在 `LocalToolRegistry` / `ChatViewModel+ClientTools` 层完成，本类只负责
/// **执行已获准的命令**：no-shell 管道执行器 + posix_spawn 进程组 + 超时看门狗 + 输出预算。
/// 设计见 docs/design/shell-tool/。
final class ShellToolProvider: @unchecked Sendable {
    /// 执行作用域：`.full` 完整 shell（namespace `shell`，仅子任务）；`.peek` 只读探查
    /// （namespace `shell_peek`，主对话可见）。两者共用执行内核，只换命令白名单。
    let scope: ShellScope

    init(scope: ShellScope) {
        self.scope = scope
    }

    var namespace: String {
        scope == .peek ? "shell_peek" : "shell"
    }

    /// flatName（`shell__run` / `shell_peek__run`）→ 作用域；非 shell 返回 nil。
    static func scope(forFlatName name: String) -> ShellScope? {
        if name.hasPrefix("shell_peek__") { return .peek }
        if name.hasPrefix("shell__") { return .full }
        return nil
    }

    /// capture 上限：达到即杀进程组并停止读取（内存有界）；即落盘上限。
    nonisolated static let captureMaxBytes = 2 * 1024 * 1024
    nonisolated static let maxLineChars = 2000
    nonisolated static let defaultTimeout: TimeInterval = 60
    nonisolated static let maxTimeout: TimeInterval = 100

    // MARK: - 分级 / 拦截（供 LocalToolRegistry 调用）

    /// 命令是否完全落在 Agent 工作区内（供风险分级）。
    /// 预留：当前只读一律 L2、写恒 L4，已无调用方；留作未来"写命令工作区内降档"用。
    static func isWorkspaceScoped(arguments: [String: Any]) -> Bool {
        guard let command = arguments["command"] as? String else { return false }
        let workdir = resolveWorkdir(arguments["workdir"] as? String)
        return ShellCommandParser.isScopedToAgentWorkspace(command: command, workdir: workdir)
    }

    /// 风险等级（供 `LocalToolRegistry`）：
    /// - `.full`：`.l4`（写/执行）→ 4；`.allow`（只读）→ 2（免确认，不再分工作区内外）。
    /// - `.peek`：只会是 `.allow` → 2；`.deny` 已在 `blockedReason` 前置拒绝。
    static func shellLevel(arguments: [String: Any], scope: ShellScope) -> Int {
        guard let command = (arguments["command"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines),
              !command.isEmpty else { return 4 }
        let workdir = resolveWorkdir(arguments["workdir"] as? String)
        switch ShellCommandParser.evaluate(command: command, workdir: workdir, scope: scope).risk {
        case .l4: return 4
        case .allow: return 2
        case .deny: return 4 // 正常已被 blockedReason 前置拒绝，兜底
        }
    }

    /// 硬阻断原因：命令缺失 / workdir 不存在或未授权 / 语法不支持 / 未命中白名单。
    /// 返回 nil 表示可以继续（allow 或 l4）。
    static func blockedReason(arguments: [String: Any], scope: ShellScope) -> String? {
        guard let command = (arguments["command"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines),
              !command.isEmpty else {
            return "缺少 command 参数"
        }
        let workdir = resolveWorkdir(arguments["workdir"] as? String)
        var isDir: ObjCBool = false
        guard FileManager.default.fileExists(atPath: workdir, isDirectory: &isDir), isDir.boolValue else {
            return "工作目录不存在或不是目录：\(workdir)"
        }
        guard DirectoryAuthorizationManager.shared.isAuthorized(workdir) else {
            let dirs = DirectoryAuthorizationManager.shared.permanentDirs
            let hint = dirs.isEmpty
                ? "当前没有已授权目录，请先在「设置 → 文件访问授权」添加。"
                : "当前已授权目录：\(dirs.joined(separator: "、"))。请用其中之一作为 workdir。"
            return "工作目录未授权：\(workdir)。\(hint)"
        }
        return ShellCommandParser.evaluate(command: command, workdir: workdir, scope: scope).denyReason
    }

    static func resolveWorkdir(_ raw: String?) -> String {
        let base = raw?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        if base.isEmpty {
            // 默认落 Agent 工作区（App 自有、隐式授权、可读写）
            AgentWorkspace.ensureExists()
            return AgentWorkspace.rootPath
        }
        return FsPath.resolve(base)
    }

    // MARK: - 执行入口

    private func perform(arguments: [String: Any], context: ExecuteContext) async -> LocalToolResult {
        guard let command = (arguments["command"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines),
              !command.isEmpty else {
            return Self.failure("缺少 command 参数")
        }
        if let reason = Self.blockedReason(arguments: arguments, scope: scope) {
            return Self.failure(reason)
        }
        let workdir = Self.resolveWorkdir(arguments["workdir"] as? String)
        let evaluation = ShellCommandParser.evaluate(command: command, workdir: workdir, scope: scope)
        guard !evaluation.stages.isEmpty else {
            return Self.failure(evaluation.denyReason ?? "命令解析失败")
        }

        var timeout = Self.clampTimeout((arguments["timeout_secs"] as? NSNumber)?.doubleValue)
        if let serverDeadline = context.deadlineSecs {
            timeout = min(timeout, serverDeadline)
        }
        let budget = Self.budgetCap(context.budgetBytes)

        return await run(stages: evaluation.stages, workdir: workdir, timeout: timeout, budget: budget)
    }

    private func run(stages: [ShellStage], workdir: String, timeout: TimeInterval, budget: Int) async -> LocalToolResult {
        let jobID = UUID()
        await ShellConcurrencyGate.shared.acquire()
        // 阻塞执行放 detached，且只调用 nonisolated static（主线程不参与读写，避免彩球）。
        let result = await Task.detached(priority: .userInitiated) {
            ShellToolProvider.executeStages(jobID: jobID, stages: stages, workdir: workdir, timeout: timeout, budget: budget)
        }.value
        await ShellConcurrencyGate.shared.release()
        return result
    }

    nonisolated private static func executeStages(jobID: UUID, stages: [ShellStage], workdir: String, timeout: TimeInterval, budget: Int) -> LocalToolResult {
        defer { ShellProcessRegistry.shared.finish(jobID) }

        // 分组：pipe 相连的相邻 stage 构成一个 concurrent pipeline；&& / || / ; 分段串行。
        var pipelines: [[ShellStage]] = []
        var current: [ShellStage] = []
        for stage in stages {
            if stage.connector == .pipe, !current.isEmpty {
                current.append(stage)
            } else {
                if !current.isEmpty { pipelines.append(current) }
                current = [stage]
            }
        }
        if !current.isEmpty { pipelines.append(current) }

        let deadline = Date().addingTimeInterval(timeout)
        var combined = Data()
        var lastExit = 0
        var timedOut = false
        var capped = false
        var executedAny = false
        var stopNote: String?

        for pipeline in pipelines {
            if ShellProcessRegistry.shared.isCancelled(jobID) {
                stopNote = "命令已被用户停止"
                break
            }
            switch pipeline[0].connector {
            case .and where executedAny && lastExit != 0:
                continue
            case .or where executedAny && lastExit == 0:
                continue
            default:
                break
            }
            let remaining = deadline.timeIntervalSinceNow
            if remaining <= 0 {
                timedOut = true
                break
            }
            let outcome = runPipeline(jobID: jobID, pipeline: pipeline, workdir: workdir, timeout: remaining)
            combined.append(outcome.data)
            if combined.count >= Self.captureMaxBytes {
                // 全局 2MB 硬上限：多段（;）累积也封顶，避免内存/落盘无界
                combined = Data(combined.prefix(Self.captureMaxBytes))
                capped = true
            }
            if let err = outcome.startError {
                stopNote = err
                break
            }
            lastExit = outcome.exitCode
            if outcome.timedOut { timedOut = true }
            if outcome.capped { capped = true }
            executedAny = true
            if timedOut { break }
        }

        return buildResult(captured: combined, exitCode: lastExit, timedOut: timedOut, capped: capped, note: stopNote, budget: budget)
    }

    nonisolated private struct PipelineOutcome {
        var data = Data()
        var exitCode = 0
        var timedOut = false
        var capped = false
        var startError: String?
    }

    nonisolated private static func runPipeline(jobID: UUID, pipeline: [ShellStage], workdir: String, timeout: TimeInterval) -> PipelineOutcome {
        var outcome = PipelineOutcome()
        let n = pipeline.count

        var execPaths: [String] = []
        for stage in pipeline {
            guard let path = ShellCommandParser.resolveExecutable(stage.argv[0], workdir: workdir) else {
                outcome.startError = "命令不可执行：\(stage.argv[0])"
                return outcome
            }
            execPaths.append(path)
        }

        var pipes: [[Int32]] = []
        for _ in 0..<max(0, n - 1) {
            var fds: [Int32] = [0, 0]
            guard pipe(&fds) == 0 else {
                for p in pipes { close(p[0]); close(p[1]) }
                outcome.startError = "创建管道失败"
                return outcome
            }
            pipes.append(fds)
        }
        var capture: [Int32] = [0, 0]
        guard pipe(&capture) == 0 else {
            for p in pipes { close(p[0]); close(p[1]) }
            outcome.startError = "创建管道失败"
            return outcome
        }
        let captureRead = capture[0]
        let captureWrite = capture[1]
        let devNull = open("/dev/null", O_RDONLY)

        var pids: [pid_t] = []
        for i in 0..<n {
            let stdoutFD: Int32 = (i < n - 1) ? pipes[i][1] : captureWrite
            let stdinFD: Int32 = (i == 0) ? devNull : pipes[i - 1][0]
            var pid: pid_t = 0
            let ok = spawn(
                exec: execPaths[i],
                argv: pipeline[i].argv,
                workdir: workdir,
                stdin: stdinFD,
                stdout: stdoutFD,
                stderr: captureWrite,
                pid: &pid
            )
            if !ok { break }
            pids.append(pid)
        }
        close(devNull)
        close(captureWrite)
        for p in pipes { close(p[0]); close(p[1]) }

        guard pids.count == n else {
            for pid in pids { kill(-pid, SIGKILL) }
            for pid in pids {
                var status: Int32 = 0
                while waitpid(pid, &status, 0) < 0 && errno == EINTR {}
            }
            close(captureRead)
            outcome.startError = "启动进程失败：\(pipeline[pids.count].argv[0])"
            return outcome
        }

        ShellProcessRegistry.shared.register(jobID, pids: pids)
        defer { ShellProcessRegistry.shared.unregister(jobID) }

        // 看门狗：到点整组 SIGTERM，宽限 2s 后 SIGKILL。
        let timedOutFlag = ShellAtomicFlag()
        let watchdog = DispatchSource.makeTimerSource(queue: DispatchQueue.global(qos: .utility))
        watchdog.schedule(deadline: .now() + timeout)
        watchdog.setEventHandler {
            timedOutFlag.set()
            Self.killGroups(pids, SIGTERM)
            DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + 2) {
                Self.killGroups(pids, SIGKILL)
            }
        }
        watchdog.resume()

        var data = Data()
        var capped = false
        let bufferSize = 65536
        var buffer = [UInt8](repeating: 0, count: bufferSize)
        while true {
            let bytesRead = read(captureRead, &buffer, bufferSize)
            if bytesRead <= 0 { break }
            data.append(contentsOf: buffer[0..<bytesRead])
            if data.count >= Self.captureMaxBytes {
                capped = true
                data = Data(data.prefix(Self.captureMaxBytes)) // 精确到 2MB，之后一律丢弃
                Self.killGroups(pids, SIGKILL)
                break
            }
        }
        watchdog.cancel()
        close(captureRead)

        var lastStatus: Int32 = 0
        for (idx, pid) in pids.enumerated() {
            var status: Int32 = 0
            while waitpid(pid, &status, 0) < 0 && errno == EINTR {}
            if idx == pids.count - 1 { lastStatus = status }
        }

        outcome.data = data
        outcome.timedOut = timedOutFlag.isSet
        outcome.capped = capped
        outcome.exitCode = Self.exitCode(from: lastStatus)
        return outcome
    }

    // MARK: - posix_spawn

    nonisolated private static func spawn(
        exec: String,
        argv: [String],
        workdir: String,
        stdin: Int32,
        stdout: Int32,
        stderr: Int32,
        pid: inout pid_t
    ) -> Bool {
        var fileActions: posix_spawn_file_actions_t?
        posix_spawn_file_actions_init(&fileActions)
        defer { posix_spawn_file_actions_destroy(&fileActions) }
        posix_spawn_file_actions_addchdir(&fileActions, workdir)
        posix_spawn_file_actions_adddup2(&fileActions, stdin, 0)
        posix_spawn_file_actions_adddup2(&fileActions, stdout, 1)
        posix_spawn_file_actions_adddup2(&fileActions, stderr, 2)

        var attr: posix_spawnattr_t?
        posix_spawnattr_init(&attr)
        defer { posix_spawnattr_destroy(&attr) }
        // SETPGROUP + pgroup=0 → 子进程自成进程组（pgid == pid），便于整组 kill；
        // CLOEXEC_DEFAULT → 只保留显式 dup2 的 fd，避免父进程管道 fd 泄漏导致 EOF 不到达。
        let flags: Int16 = Int16(POSIX_SPAWN_SETPGROUP | POSIX_SPAWN_CLOEXEC_DEFAULT)
        posix_spawnattr_setflags(&attr, flags)
        posix_spawnattr_setpgroup(&attr, 0)

        let envList = Self.childEnvironment()
        var cargv: [UnsafeMutablePointer<CChar>?] = argv.map { strdup($0) }
        cargv.append(nil)
        var cenv: [UnsafeMutablePointer<CChar>?] = envList.map { strdup($0) }
        cenv.append(nil)
        defer {
            for p in cargv where p != nil { free(p) }
            for p in cenv where p != nil { free(p) }
        }

        return posix_spawn(&pid, exec, &fileActions, &attr, &cargv, &cenv) == 0
    }

    /// 子进程环境白名单：不继承 App 环境（API key 从根上不进入子进程），固定 PATH，
    /// 并叠加 git/pager/editor 加固（防 pager/editor 交互挂起、外部 diff 执行）。
    nonisolated static func childEnvironment() -> [String] {
        let home = NSHomeDirectory()
        let user = NSUserName()
        return [
            "PATH=\(ShellCommandParser.trustedPath)",
            "HOME=\(home)",
            "USER=\(user)",
            "LOGNAME=\(user)",
            "SHELL=/bin/zsh",
            "TMPDIR=\(NSTemporaryDirectory())",
            "TERM=dumb",
            "LANG=en_US.UTF-8",
            "GIT_PAGER=cat",
            "PAGER=cat",
            "GIT_EDITOR=true",
            "GIT_TERMINAL_PROMPT=0",
            "GIT_CONFIG_NOSYSTEM=1",
            "GIT_ATTR_NOSYSTEM=1",
            "GIT_EXTERNAL_DIFF=",
        ]
    }

    nonisolated private static func killGroups(_ pids: [pid_t], _ signal: Int32) {
        for pid in pids where pid > 0 { kill(-pid, signal) }
    }

    nonisolated private static func exitCode(from status: Int32) -> Int {
        // Darwin 的 WIFEXITED/WEXITSTATUS 是函数式宏，Swift 不可见，按 sys/wait.h 手工解码。
        let wstatus = status & 0x7F
        if wstatus == 0 { return Int((status >> 8) & 0xFF) }   // 正常退出
        if wstatus != 0x7F { return 128 + Int(wstatus) }       // 被信号终止
        return -1                                               // 停止/其他
    }

    // MARK: - 结果构造

    nonisolated private static func failure(_ message: String) -> LocalToolResult {
        LocalToolResult(serverResult: nil, serverError: message, clientAction: nil)
    }

    nonisolated static func clampTimeout(_ requested: Double?) -> TimeInterval {
        guard let v = requested, v.isFinite else { return defaultTimeout }
        return min(max(v, 1), maxTimeout)
    }

    nonisolated static func budgetCap(_ budgetBytes: Int?) -> Int {
        let budget = budgetBytes ?? 50 * 1024
        return min(max(budget - 256, 1024), 50 * 1024)
    }

    nonisolated static func buildResult(captured: Data, exitCode: Int, timedOut: Bool, capped: Bool, note: String?, budget: Int) -> LocalToolResult {
        let decoded = String(data: captured, encoding: .utf8)
        let invalidEncoding = (decoded == nil)
        let raw = decoded ?? String(decoding: captured, as: UTF8.self)
        // display 只用于"注入上下文"（归一 \r、剥 ANSI、剔控制字符、超长行截断并标注）；
        // 落盘用 raw（原样捕获），保证 output_path 不因清洗/截行丢内容。
        let (display, longLineTruncated) = Self.normalize(raw)

        var obj: [String: Any] = ["exit_code": exitCode]
        if timedOut { obj["timed_out"] = true }

        var notes: [String] = []
        if let note { notes.append(note) }
        if invalidEncoding { notes.append("输出含非 UTF-8 字节，已按替换字符展示。") }
        if longLineTruncated { notes.append("部分超长行（>\(maxLineChars) 字符）在 output 里已截断并标注；完整原文见 output_path（若已落盘）。") }

        let limitMB = Self.captureMaxBytes / (1024 * 1024)
        let truncated = capped || raw.utf8.count > budget
        if truncated {
            obj["output"] = Self.tailUTF8(OutputSanitizer.sanitize(display), maxBytes: max(0, budget - 512))
            obj["truncated"] = true
            if capped {
                // 命中采集硬上限：超出的字节已被丢弃，落盘文件也不完整——必须如实告知
                obj["capture_truncated"] = true
                obj["capture_limit_bytes"] = Self.captureMaxBytes
            }
            if let url = ToolOutputStore.shared.save(raw) {
                obj["output_path"] = url.path
                obj["stored_bytes"] = raw.utf8.count
                if capped {
                    notes.append("⚠️ 命令输出超过 \(limitMB)MB 采集上限，超出的字节**已被丢弃且无法恢复**；output_path 里只有已捕获的 \(raw.utf8.count) 字节，**不是完整输出**，output 字段也只是这段已捕获内容的尾部。要完整内容只能从命令侧收窄（示例：| head -50、| tail -100、--max-count=20、按范围分页），不要重复执行同一命令。")
                } else {
                    notes.append("输出较大，已**完整**存入 output_path（\(raw.utf8.count) 字节）；output 字段只是尾部预览。output_path 只能由 fs__ 工具读取（shell 无权限），用 fs__grep 定位或 fs__read(output_path, ...) 分段读完整内容。")
                }
            } else if capped {
                notes.append("⚠️ 命令输出超过 \(limitMB)MB 采集上限，超出部分**已被丢弃且无法恢复**；且落盘失败，仅有上面的尾部预览。请从命令侧收窄输出。")
            }
        } else {
            obj["output"] = OutputSanitizer.sanitize(display)
        }
        if timedOut {
            notes.append("命令超时被终止，以上为超时前已产生的输出。")
        }
        if !notes.isEmpty {
            obj["note"] = notes.joined(separator: "\n")
        }

        guard let data = try? JSONSerialization.data(withJSONObject: obj) else {
            return Self.failure("结果序列化失败")
        }
        return LocalToolResult(serverResult: data, serverError: nil, clientAction: nil)
    }

    // MARK: - 输出清洗

    /// 归一 `\r`、剥离 ANSI、剔除控制字符、超长行截断（并标记损失）。返回 (展示文本, 是否有超长行被截断)。
    nonisolated static func normalize(_ s: String) -> (text: String, longLineTruncated: Bool) {
        var text = s
        if text.contains("\r") {
            text = text.replacingOccurrences(of: "\r\n", with: "\n")
            text = text.replacingOccurrences(of: "\r", with: "\n")
        }
        text = stripANSI(text)
        text = String(text.unicodeScalars.filter { scalar in
            scalar == "\n" || scalar == "\t" || (scalar.value >= 0x20 && scalar.value != 0x7F)
        })
        let (display, longLineTruncated) = truncateLongLines(text, maxChars: maxLineChars)
        return (display, longLineTruncated)
    }

    nonisolated static func stripANSI(_ s: String) -> String {
        guard let re = try? NSRegularExpression(pattern: "\u{1B}\\[[0-9;?]*[ -/]*[@-~]") else { return s }
        return re.stringByReplacingMatches(in: s, options: [], range: NSRange(s.startIndex..., in: s), withTemplate: "")
    }

    /// 超长行截断：保留前 maxChars 字符并追加损失标记。返回 (文本, 是否截断过)。
    nonisolated static func truncateLongLines(_ s: String, maxChars: Int) -> (text: String, truncated: Bool) {
        if !s.contains("\n"), s.count <= maxChars { return (s, false) }
        var truncated = false
        let lines: [String] = s.split(separator: "\n", omittingEmptySubsequences: false).map { line in
            if line.count > maxChars {
                truncated = true
                return String(line.prefix(maxChars)) + "…[+\(line.count - maxChars) chars truncated]"
            }
            return String(line)
        }
        return (truncated ? lines.joined(separator: "\n") : s, truncated)
    }

    /// 按 UTF-8 字节安全取尾部（shell 的错误/汇总在末尾，对齐 opencode）。
    nonisolated static func tailUTF8(_ s: String, maxBytes: Int) -> String {
        if s.utf8.count <= maxBytes { return s }
        var bytes = 0
        var chars: [Character] = []
        for ch in s.reversed() {
            let n = String(ch).utf8.count
            if bytes + n > maxBytes { break }
            bytes += n
            chars.append(ch)
        }
        return "…[前部输出已截断]…\n" + String(chars.reversed())
    }
}

extension ShellToolProvider: LocalToolProvider {

    /// peek 组的目录级描述（静态，字节稳定）：主循环在启用前就能从工具目录看到该组的定位与边界。
    var groupDescription: String? {
        scope == .peek
            ? "本地只读探查（Mac 客户端）。仅支持 ls/stat/file/wc/du -sh/git status 等低耗命令；"
                + "扫描、读取内容、完整 git 历史等命令仅子任务可用，需要时派 file_explore。已授权目录见函数描述。"
            : nil
    }

    func toolSchemas() -> [LocalToolSchema] {
        switch scope {
        case .full: return fullSchemas()
        case .peek: return peekSchemas()
        }
    }

    private func fullSchemas() -> [LocalToolSchema] {
        [
            LocalToolSchema(
                name: "run",
                description: """
                在用户 Mac 上执行一条终端命令，用于了解项目/环境现状（git status、ls、grep），
                以及做只读文本处理（sort/uniq/cut/jq）、文件增删改（rm/mv/cp/mkdir/touch）、git 写操作（add/commit/checkout）。

                用法：
                - **按风险分级执行**：只读命令免确认；写/删/移动、git 写操作等为红色强制确认（工作区内也需确认）。用户拒绝则不执行。
                - 只读命令：ls/rg/grep/head/sort/uniq/cut/tr/nl/column/jq 等，以及 git 只读（status/log/show/diff/branch -l/…）。
                - 受限命令：find（禁 -exec/-delete 等）、sed（禁 -i/-f 与 e/w/r）、awk（禁 -f 与 system/getline/输出重定向）。
                - git 只做只读与明确写（add/commit/checkout/switch/restore/reset/clean/merge/rebase/cherry-pick/revert/stash/tag/branch/remote）。
                - **不支持**：解释器/构建链（node/python3/go/npm/make…）、perl/xargs、网络（curl/wget/ssh/scp、git push/fetch/pull）、
                  系统类（sudo/open/osascript/kill…）。读/写文件优先用 fs__read / fs__write / fs__edit；联网用浏览器。
                - 支持管道与串联：|  &&  ||  ;
                - 不支持：变量展开（$VAR）、通配符（* ? ~）、重定向（> <）、cd、后台（&）。
                  需要切换目录用 workdir 参数；需要找文件可以用 fs__glob。
                - 工作目录（workdir）默认是 Agent 工作区；命令里引用的文件路径同样必须在可访问目录内，否则会被拒绝。

                \(DirectoryAuthorizationManager.accessDescriptionBlock())
                - 输出**硬上限 2MB**：命令产生超过 2MB 输出时，超出的字节会被**丢弃且无法恢复**。
                  输出可能很大时务必在源头收窄：| head -50、| tail -100、git log --max-count=20 --oneline、按范围分页。
                - 注入上下文约 20KB：超过的部分自动落盘并返回 output_path（output 字段只是尾部预览）。
                  output_path 只能由 fs__ 工具读取（shell 无权限）：用 fs__grep 定位或 fs__read(output_path, ...) 续读落盘内容。
                - 返回 JSON：output（合并 stdout+stderr；截断时为尾部预览）、exit_code、timed_out；
                  截断时含 truncated / stored_bytes / output_path。**若 capture_truncated=true，说明命中 2MB 采集上限、
                  超出部分已丢失，output_path 里也不是完整输出**，此时必须改用更小的命令重新获取。
                """,
                inputSchema: [
                    "type": "object",
                    "properties": [
                        "command": [
                            "type": "string",
                            "description": "要执行的命令，如 git status 或 ls -la | head -20",
                        ],
                        "workdir": [
                            "type": "string",
                            "description": "工作目录绝对路径（可选，默认 Agent 工作区，隐式授权）",
                        ],
                        "timeout_secs": [
                            "type": "integer",
                            "description": "超时秒数（可选，默认 60，上限 100）",
                        ],
                    ],
                    "required": ["command"],
                ],
                isReadOnly: false
            ),
        ]
    }

    private func peekSchemas() -> [LocalToolSchema] {
        [
            LocalToolSchema(
                name: "run",
                description: """
                在用户 Mac 上做**只读探查**：看环境、看文件/目录元信息、看 git 状态。**不读文件正文、不扫盘、不改任何东西**。

                允许的命令：
                - 环境：pwd / whoami / hostname / date / uname / sw_vers / arch / which / basename / dirname / realpath
                - 文件·目录元信息：ls（禁 -R）/ stat / file / wc / du（仅 -sh 汇总）
                - git 状态：git status / git rev-parse / git branch --show-current / git log -n N（N≤\(ShellCommandParser.peekGitLogMaxCount)）

                说明：
                - 支持管道与串联（| && || ;）；不支持变量展开、通配符、重定向、cd、后台。
                - 工作目录默认 Agent 工作区；命令里的文件路径必须在可访问目录内，敏感文件（密钥/凭据）会被硬拦。
                - **扫描/读取内容/完整历史等命令不在主对话开放**（如 rg/grep/find/tree/sort/head/tail/cat、
                  git log 不带 -n、git show、git diff）。需要这些时，请调用 task 工具派 file_explore 子任务执行。

                \(DirectoryAuthorizationManager.accessDescriptionBlock())
                - 返回 JSON：output、exit_code、timed_out；输出过大时截断并可能落盘（output_path 由 fs__ 工具读取）。
                """,
                inputSchema: [
                    "type": "object",
                    "properties": [
                        "command": [
                            "type": "string",
                            "description": "要执行的只读探查命令，如 ls -la、git status、du -sh",
                        ],
                        "workdir": [
                            "type": "string",
                            "description": "工作目录绝对路径（可选，默认 Agent 工作区，隐式授权）",
                        ],
                        "timeout_secs": [
                            "type": "integer",
                            "description": "超时秒数（可选，默认 60，上限 100）",
                        ],
                    ],
                    "required": ["command"],
                ],
                isReadOnly: true
            ),
        ]
    }

    func execute(tool: String, arguments: [String: Any], context: ExecuteContext) async throws -> LocalToolResult {
        guard tool == "run" else {
            return LocalToolResult(serverResult: nil, serverError: "未知函数: \(tool)", clientAction: nil)
        }
        return await perform(arguments: arguments, context: context)
    }
}
#endif
