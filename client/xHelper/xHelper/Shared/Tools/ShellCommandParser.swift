#if os(macOS)
import Foundation

/// Shell 命令的解析与白名单判定（纯逻辑，不做任何执行）。
///
/// 安全模型（见 docs/design/mac-apps/shell-tool.md 四、七）：命令字符串**永不交给 shell**。
/// 本文件只做 quote-aware 切分 / tokenize / 分级，任何未命中白名单或含不支持语法的输入都直接拒绝。
/// 因为没有解释器，`$( )` / 反引号 / `;` 注入都会被当普通字面量，不构成绕过。
nonisolated enum ShellRisk: Sendable {
    case allow
    case l4
    case deny(String)
}

nonisolated enum ShellConnector: Sendable {
    case first
    case pipe
    case and
    case or
    case seq
}

struct ShellStage: Sendable {
    let argv: [String]
    let connector: ShellConnector
}

struct ShellParseFailure: Error, Sendable {
    let message: String
}

struct ShellEvaluation: Sendable {
    let stages: [ShellStage]
    let risk: ShellRisk

    var denyReason: String? {
        if case .deny(let r) = risk { return r }
        return nil
    }
}

nonisolated enum ShellCommandParser {

    /// 固定 PATH：不走用户真实 PATH——既不读 `.zshrc`（无副作用、无 rc 注入），
    /// 也不把用户可写目录纳入信任（防"先写个同名 git 再让 shell 执行"的提权）。
    static let trustedPathDirs = ["/usr/bin", "/bin", "/usr/sbin", "/sbin", "/usr/local/bin", "/opt/homebrew/bin"]
    static let trustedPath = trustedPathDirs.joined(separator: ":")

    // MARK: - 白名单

    /// allow：只读 / 无副作用，自动执行。
    static let allowCommands: Set<String> = [
        "ls", "pwd", "head", "tail", "wc", "file", "stat", "which", "type",
        "du", "df", "tree", "basename", "dirname", "realpath", "md5", "shasum",
        "cmp", "diff", "echo", "grep", "rg",
        "date", "whoami", "hostname", "uname", "id", "sw_vers", "arch",
    ]

    /// l4 命令集：**空**。shell 的风险等级由 `LocalToolRegistry` 定：默认 L4（人工确认、信任模式不可跳），
    /// 但命令**完全落在 Agent 工作区内**时降为 L3（默认仍确认、信任模式可跳，见 `isScopedToAgentWorkspace`）。
    /// 这里只需区分"白名单只读（可执行）"与"未命中（拒绝）"。文件写请用 fs__write/fs__edit；联网用浏览器。
    static let l4Commands: Set<String> = []

    static let gitReadOnly: Set<String> = [
        "status", "log", "show", "diff", "blame", "rev-parse", "ls-files", "shortlog", "describe",
    ]

    /// git 只支持"了解仓库在干啥"（只读）；写操作不交给 agent（用 git 写请走人工终端）。
    static let gitArgLevel: Set<String> = ["branch", "remote", "tag", "stash"]

    // MARK: - 入口

    /// 解析 + 分级一条命令。`workdir` 用于解析相对路径参数（敏感文件拦截）与相对 argv[0]。
    static func evaluate(command: String, workdir: String) -> ShellEvaluation {
        let trimmed = command.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            return ShellEvaluation(stages: [], risk: .deny("空命令"))
        }
        let stages: [ShellStage]
        do {
            stages = try split(trimmed)
        } catch let failure as ShellParseFailure {
            return ShellEvaluation(stages: [], risk: .deny(failure.message))
        } catch {
            return ShellEvaluation(stages: [], risk: .deny("命令解析失败"))
        }

        var risk: ShellRisk = .allow
        for stage in stages {
            switch classify(stage.argv, workdir: workdir) {
            case .deny(let reason):
                return ShellEvaluation(stages: stages, risk: .deny(reason))
            case .l4:
                risk = .l4
            case .allow:
                break
            }
        }
        return ShellEvaluation(stages: stages, risk: risk)
    }

    /// 命令是否**完全落在 Agent 工作区内**（workdir + 所有实际存在的显式路径参数都在 workspace 内）。
    /// 供 `LocalToolRegistry` 定级：true → L3（默认确认、信任模式可跳），false → L4（强制确认）。
    /// 与 `pathBlockedReason` 同一套路径提取（`pathCandidate`），只统计"存在"的路径，
    /// 避免把 `grep '/api/v1' .` 这类正则误判为越界。
    static func isScopedToAgentWorkspace(command: String, workdir: String) -> Bool {
        guard DirectoryAuthorizationManager.shared.isWithinAgentWorkspace(workdir) else { return false }
        let trimmed = command.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, let stages = try? split(trimmed) else { return false }
        for stage in stages {
            for arg in stage.argv.dropFirst() {
                guard let path = pathCandidate(arg, workdir: workdir) else { continue }
                let resolved = FsPath.resolve(path)
                if FileManager.default.fileExists(atPath: resolved),
                   !DirectoryAuthorizationManager.shared.isWithinAgentWorkspace(resolved) {
                    return false
                }
            }
        }
        return true
    }

    // MARK: - 切分 / tokenize

    private enum Word {
        case token(String)
        case op(ShellConnector)
    }

    /// quote-aware 切分顶层 `|` / `&&` / `||` / `;` / 换行；拒绝不支持的重定向与后台。
    static func split(_ s: String) throws -> [ShellStage] {
        let chars = Array(s)
        var words: [Word] = []
        var token = ""
        var hasToken = false
        var quote: Character?
        var i = 0

        func flushToken() {
            if hasToken {
                words.append(.token(token))
                token = ""
                hasToken = false
            }
        }

        while i < chars.count {
            let c = chars[i]
            if let q = quote {
                if c == q {
                    quote = nil
                    i += 1
                    continue
                }
                if c == "\\", q == "\"", i + 1 < chars.count {
                    token.append(chars[i + 1])
                    hasToken = true
                    i += 2
                    continue
                }
                token.append(c)
                hasToken = true
                i += 1
                continue
            }

            switch c {
            case "'", "\"":
                quote = c
                hasToken = true
                i += 1
            case "\\":
                if i + 1 < chars.count {
                    token.append(chars[i + 1])
                    hasToken = true
                    i += 2
                } else {
                    i += 1
                }
            case " ", "\t":
                flushToken()
                i += 1
            case "\n", "\r":
                flushToken()
                words.append(.op(.seq))
                i += 1
            case ";":
                flushToken()
                words.append(.op(.seq))
                i += 1
            case "&":
                if i + 1 < chars.count, chars[i + 1] == "&" {
                    flushToken()
                    words.append(.op(.and))
                    i += 2
                } else {
                    throw ShellParseFailure(message: "不支持后台执行（&）")
                }
            case "|":
                if i + 1 < chars.count, chars[i + 1] == "|" {
                    flushToken()
                    words.append(.op(.or))
                    i += 2
                } else {
                    flushToken()
                    words.append(.op(.pipe))
                    i += 1
                }
            case ">", "<":
                throw ShellParseFailure(message: "不支持重定向（\(c)）")
            default:
                token.append(c)
                hasToken = true
                i += 1
            }
        }

        if quote != nil {
            throw ShellParseFailure(message: "引号未闭合")
        }
        flushToken()

        guard !words.isEmpty else {
            throw ShellParseFailure(message: "空命令")
        }

        var stages: [ShellStage] = []
        var current: [String] = []
        var connector: ShellConnector = .first
        for w in words {
            switch w {
            case .token(let t):
                current.append(t)
            case .op(let op):
                guard !current.isEmpty else {
                    throw ShellParseFailure(message: "命令语法错误：操作符附近缺少命令")
                }
                stages.append(ShellStage(argv: current, connector: connector))
                current = []
                connector = op
            }
        }
        guard !current.isEmpty else {
            throw ShellParseFailure(message: "命令以操作符结尾")
        }
        stages.append(ShellStage(argv: current, connector: connector))
        return stages
    }

    // MARK: - 分级

    static func classify(_ argv: [String], workdir: String) -> ShellRisk {
        guard let raw = argv.first, !raw.isEmpty else {
            return .deny("空命令")
        }
        guard let resolved = resolveExecutable(raw, workdir: workdir) else {
            return .deny("命令未在白名单内或不可执行：\(raw)")
        }
        let base = (resolved as NSString).lastPathComponent

        // 版本查询：命令本身在白名单内，且参数仅 --version / -V → allow（不做 `* --version` 通配）
        if argv.count == 2, argv[1] == "--version" || argv[1] == "-V", isWhitelistedName(base) {
            return .allow
        }

        if base == "git" {
            return classifyGit(Array(argv.dropFirst()))
        }
        if allowCommands.contains(base) {
            if let reason = pathBlockedReason(argv: argv, workdir: workdir) {
                return .deny(reason)
            }
            return .allow
        }
        if l4Commands.contains(base) {
            return .l4
        }
        return .deny("命令未在白名单内：\(base)")
    }

    static func isWhitelistedName(_ name: String) -> Bool {
        name == "git" || allowCommands.contains(name) || l4Commands.contains(name)
    }

    static func resolveExecutable(_ name: String, workdir: String) -> String? {
        let fm = FileManager.default
        if name.contains("/") {
            let abs: String
            if name.hasPrefix("/") {
                abs = (name as NSString).standardizingPath
            } else {
                abs = ((workdir as NSString).appendingPathComponent(name) as NSString).standardizingPath
            }
            let dir = (abs as NSString).deletingLastPathComponent
            guard trustedPathDirs.contains(dir), fm.isExecutableFile(atPath: abs) else { return nil }
            return abs
        }
        for dir in trustedPathDirs {
            let candidate = (dir as NSString).appendingPathComponent(name)
            if fm.isExecutableFile(atPath: candidate) {
                return candidate
            }
        }
        return nil
    }

    // MARK: - git

    private static func classifyGit(_ args: [String]) -> ShellRisk {
        guard let sub = args.first, !sub.isEmpty else {
            return .deny("git 需要子命令")
        }
        let rest = Array(args.dropFirst())
        // --no-index 让 git diff 绕过仓库、读取任意文件（含 ~/.ssh 私钥）→ 硬拒
        if sub == "diff", rest.contains("--no-index") {
            return .deny("git diff --no-index 可读取任意文件，已拒绝")
        }
        if gitReadOnly.contains(sub) {
            return .allow
        }
        if gitArgLevel.contains(sub) {
            return classifyGitArgLevel(sub, rest)
        }
        return .deny("git 仅支持只读子命令，不代操作 git：\(sub)")
    }

    private static func classifyGitArgLevel(_ sub: String, _ args: [String]) -> ShellRisk {
        switch sub {
        case "branch":
            let allowed: Set<String> = [
                "-a", "--all", "-r", "--remotes", "-v", "-vv", "--verbose", "-l", "--list",
                "--contains", "--merged", "--no-merged", "--points-at",
                "-h", "--help", "--color", "--no-color", "--column", "--no-column",
                "--show-current", "-i", "--ignore-case",
            ]
            let prefixed = ["--sort=", "--format=", "--contains=", "--points-at=", "--merged=", "--no-merged=", "--column="]
            return readOnlyFlags(args, allowed: allowed, prefixed: prefixed)

        case "tag":
            let allowed: Set<String> = [
                "-l", "--list", "-n", "--contains", "--points-at", "--merged", "--no-merged",
                "-h", "--help", "--color", "--no-color", "-i", "--ignore-case", "--omit-empty",
            ]
            let prefixed = ["--sort=", "--format=", "--contains=", "--points-at=", "--merged=", "--no-merged="]
            for a in args {
                if allowed.contains(a) || prefixed.contains(where: { a.hasPrefix($0) }) { continue }
                if a.hasPrefix("-n"), Int(a.dropFirst(2)) != nil { continue }
                if a.hasPrefix("-"), Int(a.dropFirst()) != nil { continue }
                return .deny("git tag 仅支持只读列举：\(a)")
            }
            return .allow

        case "remote":
            if args.isEmpty { return .allow }
            if args.allSatisfy({ ["-v", "--verbose"].contains($0) }) { return .allow }
            if let first = args.first, ["show", "get-url"].contains(first) { return .allow }
            return .deny("git remote 仅支持只读查看（-v/show/get-url）")

        case "stash":
            guard let first = args.first else { return .deny("git stash 需显式只读子命令（list/show）") }
            return (first == "list" || first == "show") ? .allow : .deny("git stash 仅支持只读 list/show")

        default:
            return .deny("git 仅支持只读子命令：\(sub)")
        }
    }

    private static func readOnlyFlags(_ args: [String], allowed: Set<String>, prefixed: [String]) -> ShellRisk {
        for a in args {
            if allowed.contains(a) { continue }
            if prefixed.contains(where: { a.hasPrefix($0) }) { continue }
            return .deny("git 参数看起来是写操作，已拒绝：\(a)")
        }
        return .allow
    }

    // MARK: - 路径拦截（敏感文件 + 授权目录）

    /// allow 档命令（head/tail/grep/rg/...）的参数里凡是"像路径"的值：
    /// 1) realpath 后过 `SensitiveFileBlocker`（挡 `head ~/.ssh/id_rsa` 这类私钥泄露）；
    /// 2) 确实存在的路径必须 `isAuthorized`（已授权目录 + Agent 工作区隐式读写根），否则拒绝。
    ///    —— 这里用严格 `isAuthorized`，不用 `isReadAuthorized`：`tool_output/` 溢出落盘目录
    ///    只对 fs 工具开只读例外（fs 读回时会脱敏），shell 不得读取。
    /// 只对"存在"的路径做授权目录校验，避免把含 `/` 的正则（如 `grep '/api/v1' .`）当路径误杀。
    static func pathBlockedReason(argv: [String], workdir: String) -> String? {
        let recursive = isRecursiveInvocation(argv)
        var sawPathArg = false
        for arg in argv.dropFirst() {
            guard let path = pathCandidate(arg, workdir: workdir) else { continue }
            sawPathArg = true
            let resolved = FsPath.resolve(path)
            if let reason = SensitiveFileBlocker.check(resolved) {
                return reason
            }
            // 递归命令的根目录若是高危路径的祖先（如 `grep -r x ~`），逐参数 check 拦不住被递归读到的子文件
            if recursive, SensitiveFileBlocker.isAncestorOfHardPath(resolved) {
                return "递归命令的根目录包含高危路径，已拒绝：\(resolved)"
            }
            if FileManager.default.fileExists(atPath: resolved),
               !DirectoryAuthorizationManager.shared.isAuthorized(resolved) {
                return "路径不在已授权目录内：\(resolved)（如需访问请先在「设置 → 文件访问授权」添加该目录）"
            }
        }
        // 递归命令且无显式路径参数 → 根为 workdir
        if recursive, !sawPathArg {
            let root = FsPath.resolve(workdir)
            if SensitiveFileBlocker.isAncestorOfHardPath(root) {
                return "递归命令的根目录包含高危路径，已拒绝：\(root)"
            }
        }
        return nil
    }

    /// 是否递归读取：`rg` 默认递归；`grep` 命中 `-r` / `-R` / `--recursive`。
    private static func isRecursiveInvocation(_ argv: [String]) -> Bool {
        guard let cmd = argv.first else { return false }
        switch (cmd as NSString).lastPathComponent.lowercased() {
        case "rg":
            return true
        case "grep":
            for arg in argv.dropFirst() {
                if arg == "--recursive" { return true }
                if arg.hasPrefix("--") { continue }
                if arg.hasPrefix("-"), arg.contains("r") || arg.contains("R") { return true }
            }
            return false
        default:
            return false
        }
    }

    private static func pathCandidate(_ arg: String, workdir: String) -> String? {
        guard !arg.isEmpty else { return nil }
        // --flag=/path 形式（先于 flag 前缀判断）
        if arg.hasPrefix("--"), let eq = arg.firstIndex(of: "=") {
            let value = String(arg[arg.index(after: eq)...])
            return value.isEmpty ? nil : pathCandidate(value, workdir: workdir)
        }
        guard !arg.hasPrefix("-") else { return nil }
        if arg.hasPrefix("/") {
            return arg
        }
        if arg.hasPrefix("~") {
            return (arg as NSString).expandingTildeInPath
        }
        // 相对路径一律相对 workdir 解析（不能用进程 cwd）。显式 `.`/`./x`/`../x` 直接返回（存在性由上层判），
        // 裸名只在 workdir 下确实存在时才当路径，避免把 grep 正则/普通参数误判。
        let resolved = FsPath.resolve((workdir as NSString).appendingPathComponent(arg))
        if arg.hasPrefix("./") || arg.hasPrefix("../") || arg == "." || arg == ".." {
            return resolved
        }
        return FileManager.default.fileExists(atPath: resolved) ? resolved : nil
    }
}
#endif
