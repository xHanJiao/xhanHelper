#if os(macOS)
import Foundation

/// Shell 命令的解析与白名单判定（纯逻辑，不做任何执行）。
///
/// 安全模型（见 docs/design/shell-tool/design.md 四、七）：命令字符串**永不交给 shell**。
/// 本文件只做 quote-aware 切分 / tokenize / 分级，任何未命中白名单或含不支持语法的输入都直接拒绝。
/// 因为没有解释器，`$( )` / 反引号 / `;` 注入都会被当普通字面量，不构成绕过。
nonisolated enum ShellRisk: Sendable {
    case allow
    case l4
    case deny(String)
}

/// 执行作用域：`full` = 完整 shell（仅子任务）；`peek` = 只读探查（主对话可用，见 shell_peek）。
/// 两者共用切分/执行/预算，只有命令白名单与形式守卫不同。
nonisolated enum ShellScope: Sendable {
    case full
    case peek
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

    /// allow：只读 / 无副作用，自动执行（免确认）。
    static let allowCommands: Set<String> = [
        "ls", "pwd", "head", "tail", "wc", "file", "stat", "which", "type",
        "du", "df", "tree", "basename", "dirname", "realpath", "md5", "shasum",
        "cmp", "diff", "echo", "grep", "rg",
        "date", "whoami", "hostname", "uname", "id", "sw_vers", "arch",
        // v2 只读文本处理（写口由 allowFormBlockedReason 单独堵）
        "sort", "uniq", "cut", "tr", "nl", "column", "jq",
    ]

    /// l4 命令集：写 / 执行类，恒 L4（红色确认、必经确认，**工作区内也 L4**）。
    /// 危险藏在参数里的命令（find/sed/awk）另有形式守卫（`l4FormBlockedReason`）。
    static let l4Commands: Set<String> = [
        "find", "sed", "awk",
        "rm", "mv", "cp", "mkdir", "rmdir", "touch", "ln",
        "chmod", "chown", "tee", "truncate",
    ]

    /// l4 中"只读语义"的命令：路径参数按读校验；其余 l4 按写校验（目标不存在查父目录）。
    static let l4ReadOnly: Set<String> = ["find", "sed", "awk"]

    /// l4 命令需要硬拒的参数形式（命令执行 / 任意写口）。
    static let l4DeniedFlags: [String: Set<String>] = [
        "find": ["-exec", "-execdir", "-ok", "-okdir", "-delete", "-fls", "-fprint", "-fprint0", "-fprintf"],
        "sed": ["-i", "--in-place", "-f", "--file"],
        "awk": ["-f", "--file"],
    ]

    static let gitReadOnly: Set<String> = [
        "status", "log", "show", "diff", "blame", "rev-parse", "ls-files", "shortlog", "describe",
    ]

    /// git 只支持"了解仓库在干啥"（只读）；写操作不交给 agent（用 git 写请走人工终端）。
    static let gitArgLevel: Set<String> = ["branch", "remote", "tag", "stash"]

    /// git 写子命令（v2）：明确的写操作走 L4 确认（不再一律拒绝）。
    static let gitWrite: Set<String> = [
        "add", "commit", "checkout", "switch", "restore", "reset",
        "clean", "merge", "rebase", "cherry-pick", "revert",
    ]

    // MARK: - peek 白名单（主对话可见的只读探查，输出天然有界、不读文件正文）

    /// peek 允许的命令：看环境 / 文件·目录元信息 / git 状态，均不返回文件内容、不扫盘。
    /// 刻意不含：rg/grep/find/tree/du(裸)/sort/uniq/cut/tr/nl/column/jq/diff/md5/shasum/head/tail。
    /// **这是独立于 `allowCommands` 的更窄子集**：给 full 白名单加只读命令时不必同步到此（peek 刻意更保守）。
    /// 只列固定 PATH 里有真实二进制的命令（`type` 是 shell 内建、无二进制，故不收，用 `which`）。
    static let peekCommands: Set<String> = [
        "pwd", "whoami", "hostname", "date", "uname", "sw_vers", "arch",
        "which", "basename", "dirname", "realpath",
        "ls", "stat", "file", "wc", "du",
    ]

    /// peek 里 `git log` 的条数上限（超过即拒，避免完整历史灌进主上下文）。
    static let peekGitLogMaxCount = 20

    // MARK: - 入口

    /// 解析 + 分级一条命令。`workdir` 用于解析相对路径参数（敏感文件拦截）与相对 argv[0]。
    /// `scope` 决定白名单：`.full` 完整 shell，`.peek` 只读探查（主对话）。
    static func evaluate(command: String, workdir: String, scope: ShellScope = .full) -> ShellEvaluation {
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
            switch classify(stage.argv, workdir: workdir, scope: scope) {
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
    /// **预留**：只读命令现恒 L2、写命令恒 L4，暂无调用方；留作未来"写命令工作区内降档"用。
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

    static func classify(_ argv: [String], workdir: String, scope: ShellScope = .full) -> ShellRisk {
        guard let raw = argv.first, !raw.isEmpty else {
            return .deny("空命令")
        }
        guard let resolved = resolveExecutable(raw, workdir: workdir) else {
            return scope == .peek
                ? .deny(peekUnavailableReason(raw))
                : .deny("命令未在白名单内或不可执行：\(raw)")
        }
        let base = (resolved as NSString).lastPathComponent

        if scope == .peek {
            return classifyPeek(base: base, argv: argv, workdir: workdir)
        }

        // 版本查询：命令本身在白名单内，且参数仅 --version / -V → allow（不做 `* --version` 通配）
        if argv.count == 2, argv[1] == "--version" || argv[1] == "-V", isWhitelistedName(base) {
            return .allow
        }

        if base == "git" {
            return classifyGit(Array(argv.dropFirst()))
        }
        if allowCommands.contains(base) {
            if let reason = allowFormBlockedReason(base: base, argv: argv) {
                return .deny(reason)
            }
            if let reason = pathBlockedReason(argv: argv, workdir: workdir) {
                return .deny(reason)
            }
            return .allow
        }
        if l4Commands.contains(base) {
            if let reason = l4FormBlockedReason(base: base, argv: argv) {
                return .deny(reason)
            }
            let forWrite = !l4ReadOnly.contains(base)
            if let reason = pathBlockedReason(argv: argv, workdir: workdir, forWrite: forWrite) {
                return .deny(reason)
            }
            return .l4
        }
        return .deny("命令未在白名单内：\(base)")
    }

    // MARK: - peek 分级（只读探查，主对话可见）

    /// peek 只读探查分级：命中 peek 白名单（含小心的形式守卫）→ allow（免确认）；其余 → 拒绝，
    /// 并在错误信息里引导模型派 file_explore 子任务执行重命令。
    static func classifyPeek(base: String, argv: [String], workdir: String) -> ShellRisk {
        if base == "git" {
            return classifyGitPeek(Array(argv.dropFirst()))
        }
        guard peekCommands.contains(base) else {
            return .deny(peekUnavailableReason(base))
        }
        if argv.count == 2, argv[1] == "--version" || argv[1] == "-V" {
            return .allow
        }
        if let reason = peekFormBlockedReason(base: base, argv: argv) {
            return .deny(reason)
        }
        if let reason = pathBlockedReason(argv: argv, workdir: workdir) {
            return .deny(reason)
        }
        return .allow
    }

    /// peek 形式守卫：`ls` 禁递归；`du` 仅 `-s`/`--summarize` 汇总（逐目录列出输出不可控）。
    private static func peekFormBlockedReason(base: String, argv: [String]) -> String? {
        let args = Array(argv.dropFirst())
        if base == "ls" {
            if args.contains("--recursive") {
                return "ls 递归列目录（-R）输出不可控，不在主对话开放；如需请派 file_explore 子任务"
            }
            for a in args where a.hasPrefix("-") && !a.hasPrefix("--") {
                if a.dropFirst().contains("R") {
                    return "ls 递归列目录（-R）输出不可控，不在主对话开放；如需请派 file_explore 子任务"
                }
            }
        }
        if base == "du" {
            let summarize = args.contains("-s") || args.contains("--summarize")
                || args.contains { $0.hasPrefix("-") && !$0.hasPrefix("--") && $0.dropFirst().contains("s") }
            if !summarize {
                return "du 仅支持 -s/--summarize 汇总形式（逐目录列出输出不可控，请派 file_explore 子任务）"
            }
        }
        return nil
    }

    /// peek 下的 git：仅放 `status` / `rev-parse` / `branch --show-current` / `log -n N(N≤20)`。
    private static func classifyGitPeek(_ args: [String]) -> ShellRisk {
        guard let sub = args.first, !sub.isEmpty else {
            return .deny(peekUnavailableReason("git"))
        }
        let rest = Array(args.dropFirst())
        switch sub {
        case "status", "rev-parse":
            return .allow
        case "branch":
            if rest == ["--show-current"] { return .allow }
            return .deny("git branch 在主对话仅支持 --show-current；其余请派 file_explore 子任务")
        case "log":
            if let n = gitLogCount(rest), (1...peekGitLogMaxCount).contains(n) { return .allow }
            return .deny("git log 在主对话必须带 -n/--max-count 且 1...\(peekGitLogMaxCount)；完整历史请派 file_explore 子任务")
        default:
            return .deny(peekUnavailableReason("git \(sub)"))
        }
    }

    /// 解析 `git log` 的条数：`-n N` / `-nN` / `--max-count=N`；缺失或非法返回 nil。
    /// **取最后一个 count**（git 多个 max-count 时后者胜出，只看首个会被 `-n 20 --max-count=99999` 绕过）；
    /// 负值/非数字一律返回 nil（`-n -1` 被 git 当无限制，必须拒）。
    private static func gitLogCount(_ args: [String]) -> Int? {
        var last: Int?
        var i = 0
        while i < args.count {
            let a = args[i]
            if a == "-n" || a == "--max-count" {
                guard i + 1 < args.count, let n = Int(args[i + 1]) else { return nil }
                last = n
                i += 2
                continue
            }
            if a.hasPrefix("--max-count=") {
                guard let n = Int(a.dropFirst("--max-count=".count)) else { return nil }
                last = n
            } else if a.hasPrefix("-n"), a.count > 2 {
                guard let n = Int(a.dropFirst(2)) else { return nil }
                last = n
            }
            i += 1
        }
        return last
    }

    /// peek 未命中白名单时的拒绝信息（顺带引导模型派子任务）。
    private static func peekUnavailableReason(_ command: String) -> String {
        "「\(command)」不在主对话开放（可能扫盘/读取大量内容）。如需执行请派 file_explore 子任务；"
            + "主对话只支持探查类只读命令（ls/stat/file/wc/du -sh/git status 等）。"
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

    // MARK: - 参数形式守卫

    /// allow 命令的写口拦截。当前只有 `sort -o/--output` 能写文件。
    private static func allowFormBlockedReason(base: String, argv: [String]) -> String? {
        if base == "sort" {
            for a in argv.dropFirst() {
                if a == "--output" || a.hasPrefix("--output=") { return "sort -o/--output 会写文件，已拒绝" }
                if a.hasPrefix("-"), !a.hasPrefix("--"), a.dropFirst().contains("o") {
                    return "sort -o/--output 会写文件，已拒绝"
                }
            }
        }
        return nil
    }

    /// l4 命令的形式守卫：硬拒参数在禁止表里，或 sed/awk 脚本含执行/写口。
    private static func l4FormBlockedReason(base: String, argv: [String]) -> String? {
        let args = Array(argv.dropFirst())
        if let denied = l4DeniedFlags[base], let hit = args.first(where: { denied.contains($0) }) {
            return "命令参数被拒绝（\(base) \(hit)）"
        }
        if base == "sed" {
            for a in args {
                if a == "-i" || a.hasPrefix("-i.") || a == "--in-place" || a.hasPrefix("--in-place=") {
                    return "sed 就地修改（-i）会写文件，已拒绝"
                }
                if a == "--file" || a.hasPrefix("--file=") { return "sed -f 从文件读脚本，已拒绝" }
            }
            if let r = sedScriptReason(sedScripts(args)) { return r }
        }
        if base == "awk", let r = awkScriptReason(awkScripts(args)) {
            return r
        }
        return nil
    }

    /// 取出 sed 的脚本文本：所有 `-e` 值；无 `-e` 时取第一个位置参数。
    private static func sedScripts(_ args: [String]) -> [String] {
        var scripts: [String] = []
        var sawDashE = false
        var tookPositional = false
        var i = 0
        while i < args.count {
            let a = args[i]
            if a == "-e" || a == "--expression" {
                if i + 1 < args.count { scripts.append(args[i + 1]); sawDashE = true }
                i += 2
                continue
            }
            if a.hasPrefix("--expression=") {
                scripts.append(String(a.dropFirst("--expression=".count))); sawDashE = true
            } else if a.hasPrefix("-e"), a.count > 2 {
                scripts.append(String(a.dropFirst(2))); sawDashE = true
            } else if !a.hasPrefix("-"), !sawDashE, !tookPositional {
                scripts.append(a); tookPositional = true
            }
            i += 1
        }
        return scripts
    }

    /// 取出 awk 的程序文本：第一个位置参数（`-F`/`-v` 的值先跳过）。
    private static func awkScripts(_ args: [String]) -> [String] {
        var i = 0
        while i < args.count {
            let a = args[i]
            if a == "-F" || a == "-v" || a == "--assign" || a == "--field-separator" {
                i += 2
                continue
            }
            if a.hasPrefix("-F") || a.hasPrefix("-v") || a.hasPrefix("--assign=") || a.hasPrefix("--field-separator=") {
                i += 1
                continue
            }
            if a.hasPrefix("-") { i += 1; continue }
            return [a]
        }
        return []
    }

    /// sed 脚本守卫。
    /// ponytail: 子串 / 正则级 best-effort，不是解析器——已知有漏报上限；要更严得上 sed 语法解析。
    /// 因 sed 恒 L4，确认条会显示人话解释兜底。
    private static func sedScriptReason(_ scripts: [String]) -> String? {
        for s in scripts {
            // 命令位的 e / w / r / W / R（可带地址前缀）
            if s.range(of: #"(^|[;{}\n])[ \t]*(?:[0-9]+(?:,[0-9]*)?|\$)?[ \t]*[ewrWR](?:[ \t;}]|$|[0-9])"#,
                       options: .regularExpression) != nil {
                return "sed 脚本含执行/读写命令（e/w/r），已拒绝"
            }
            // s/// 的 e / w 标志
            if s.range(of: #"s([^\\\nA-Za-z0-9])(?:\\.|[^\\])*?\1(?:\\.|[^\\])*?\1[0-9gipm]*[ew]"#,
                       options: .regularExpression) != nil {
                return "sed 替换命令带 e/w 标志（执行/写文件），已拒绝"
            }
        }
        return nil
    }

    /// awk 程序守卫。
    /// ponytail: 子串 / 正则级 best-effort，不是解析器——已知有漏报上限；要更严得上 awk 语法解析。
    private static func awkScriptReason(_ scripts: [String]) -> String? {
        for s in scripts {
            if s.contains("system") || s.contains("getline") || s.contains("ENVIRON") || s.contains("close(") {
                return "awk 程序含 system/getline/ENVIRON，已拒绝"
            }
            if s.range(of: #"printf?[^;\n}]*[>|]"#, options: .regularExpression) != nil {
                return "awk 程序含输出重定向/管道，已拒绝"
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
        if gitWrite.contains(sub) {
            return .l4
        }
        if gitArgLevel.contains(sub) {
            return classifyGitArgLevel(sub, rest)
        }
        return .deny("git 仅支持只读或明确写子命令：\(sub)")
    }

    private static func classifyGitArgLevel(_ sub: String, _ args: [String]) -> ShellRisk {
        switch sub {
        case "branch":
            if gitBranchIsWrite(args) { return .l4 }
            let allowed: Set<String> = [
                "-a", "--all", "-r", "--remotes", "-v", "-vv", "--verbose", "-l", "--list",
                "--contains", "--merged", "--no-merged", "--points-at",
                "-h", "--help", "--color", "--no-color", "--column", "--no-column",
                "--show-current", "-i", "--ignore-case",
            ]
            let prefixed = ["--sort=", "--format=", "--contains=", "--points-at=", "--merged=", "--no-merged=", "--column="]
            return readOnlyFlags(args, allowed: allowed, prefixed: prefixed)

        case "tag":
            if gitTagIsWrite(args) { return .l4 }
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
            if let first = args.first,
               ["add", "remove", "rm", "rename", "set-url", "set-branches"].contains(first) {
                return .l4
            }
            if args.isEmpty { return .allow }
            if args.allSatisfy({ ["-v", "--verbose"].contains($0) }) { return .allow }
            if let first = args.first, ["show", "get-url"].contains(first) { return .allow }
            return .deny("git remote 仅支持只读查看（-v/show/get-url）或写操作（add/remove/set-url）")

        case "stash":
            guard let first = args.first else {
                return .deny("git stash 需显式子命令（list/show/push/pop/apply/drop）")
            }
            if first == "list" || first == "show" { return .allow }
            if ["push", "pop", "apply", "drop", "clear", "save", "branch"].contains(first) { return .l4 }
            return .deny("git stash 仅支持只读 list/show 或写操作 push/pop/apply/drop")

        default:
            return .deny("git 仅支持只读子命令：\(sub)")
        }
    }

    /// git branch 的写形式：写 flag 或位置参数（创建/重命名目标）。
    private static func gitBranchIsWrite(_ args: [String]) -> Bool {
        let writeFlags: Set<String> = [
            "-d", "-D", "-m", "-M", "-c", "-C", "-f", "-t", "-u",
            "--delete", "--move", "--copy", "--force", "--track",
            "--set-upstream-to", "--unset-upstream", "--edit-description",
        ]
        return args.contains { writeFlags.contains($0) || $0.hasPrefix("--set-upstream-to=") } || hasPositional(args)
    }

    /// git tag 的写形式：创建/删除/签名 flag，或位置参数（tag 名）。
    private static func gitTagIsWrite(_ args: [String]) -> Bool {
        let writeFlags: Set<String> = [
            "-d", "--delete", "-a", "--annotate", "-s", "--sign",
            "-m", "--message", "-f", "--force", "-e", "--edit",
        ]
        return args.contains { writeFlags.contains($0) || $0.hasPrefix("--message=") } || hasPositional(args)
    }

    /// 是否存在位置参数（非 flag）——区分 git 的读/写形式。
    private static func hasPositional(_ args: [String]) -> Bool {
        args.contains { !$0.hasPrefix("-") }
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

    /// 命令参数里凡是"像路径"的值：
    /// 1) realpath 后过 `SensitiveFileBlocker`（挡 `head ~/.ssh/id_rsa` 这类私钥泄露）；
    /// 2) 确实存在的路径必须 `isAuthorized`（已授权目录 + Agent 工作区隐式读写根），否则拒绝。
    ///    —— 这里用严格 `isAuthorized`，不用 `isReadAuthorized`：`tool_output/` 溢出落盘目录
    ///    只对 fs 工具开只读例外（fs 读回时会脱敏），shell 不得读取。
    /// 3) `forWrite=true`（写/删除类命令）：目标不存在时校验**父目录** `isAuthorized`（覆盖
    ///    `mkdir` / `touch` / `cp`/`mv` 目标等尚未存在的路径）。
    /// 只对"存在"的路径做授权目录校验，避免把含 `/` 的正则（如 `grep '/api/v1' .`）当路径误杀。
    static func pathBlockedReason(argv: [String], workdir: String, forWrite: Bool = false) -> String? {
        let recursive = !forWrite && isRecursiveInvocation(argv)
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
            if FileManager.default.fileExists(atPath: resolved) {
                if !DirectoryAuthorizationManager.shared.isAuthorized(resolved) {
                    return "路径不在已授权目录内：\(resolved)（如需访问请先在「设置 → 文件访问授权」添加该目录）"
                }
            } else if forWrite {
                let parent = (resolved as NSString).deletingLastPathComponent
                if !DirectoryAuthorizationManager.shared.isAuthorized(parent) {
                    return "写入目标所在目录未授权：\(parent)（目标：\(resolved)）"
                }
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
