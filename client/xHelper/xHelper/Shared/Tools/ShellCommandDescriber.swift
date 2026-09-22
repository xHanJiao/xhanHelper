#if os(macOS)
import Foundation

/// 把已解析的 shell 命令翻成中文解释（确认条展示用）。
/// 动机：用户不读 shell 命令，甚至命令常常很长；确认条需要一句「人话」让他判断**意图**，
/// 而不是让他审原始命令。本类只做展示翻译，不做任何校验（校验在 `ShellCommandParser`）。
/// 纯逻辑、无副作用，可单测。
nonisolated enum ShellCommandDescriber {

    /// 多段命令：逐段翻译后用「|」连接（管道/串联不细分，够用即可）。
    static func describe(stages: [ShellStage]) -> String {
        guard !stages.isEmpty else { return "(空命令)" }
        return stages.map { describeStage($0.argv) }.joined(separator: "  |  ")
    }

    private static func describeStage(_ argv: [String]) -> String {
        guard let raw = argv.first, !raw.isEmpty else { return "(空)" }
        let base = (raw as NSString).lastPathComponent
        let args = Array(argv.dropFirst())
        switch base {
        case "rm": return rm(args)
        case "mv": return "移动：\(arrow(args))"
        case "cp": return "复制：\(arrow(args))"
        case "mkdir": return "创建目录：\(pos(args))"
        case "rmdir": return "删除目录：\(pos(args))"
        case "touch": return "创建/更新时间戳：\(pos(args))"
        case "ln": return "创建链接：\(args.joined(separator: " "))"
        case "chmod": return "修改权限：\(args.joined(separator: " "))"
        case "chown": return "修改属主：\(args.joined(separator: " "))"
        case "tee": return "写入文件：\(pos(args))"
        case "truncate": return "截断文件：\(args.joined(separator: " "))"
        case "find": return "查找文件：\(args.joined(separator: " "))"
        case "sed": return "文本处理（sed）：\(args.joined(separator: " "))"
        case "awk": return "文本处理（awk）：\(args.joined(separator: " "))"
        case "git": return git(args)
        case "sort", "uniq", "cut", "tr", "nl", "column", "jq":
            return "只读文本处理（\(base)）：\(args.joined(separator: " "))"
        default:
            let tail = args.isEmpty ? "" : " " + args.joined(separator: " ")
            return "执行：\(base)\(tail)"
        }
    }

    private static func rm(_ args: [String]) -> String {
        let recursive = args.contains {
            $0 == "--recursive" || ($0.hasPrefix("-") && !$0.hasPrefix("--") && ($0.contains("r") || $0.contains("R")))
        }
        let force = args.contains {
            $0 == "--force" || ($0.hasPrefix("-") && !$0.hasPrefix("--") && $0.contains("f"))
        }
        let kind: String
        if recursive && force { kind = "递归强制删除" }
        else if recursive { kind = "递归删除" }
        else if force { kind = "强制删除" }
        else { kind = "删除" }
        return "\(kind)：\(pos(args))（不可恢复）"
    }

    private static func arrow(_ args: [String]) -> String {
        let ps = positionals(args)
        guard ps.count >= 2 else { return ps.joined(separator: "、") }
        let dst = ps.last!
        let src = ps.dropLast().joined(separator: "、")
        return "\(src) → \(dst)"
    }

    private static func git(_ args: [String]) -> String {
        guard let sub = args.first else { return "git" }
        let rest = Array(args.dropFirst())
        switch sub {
        case "add": return "git 暂存：\(pos(rest))"
        case "commit": return "创建 git 提交：\(messageValue(rest) ?? pos(rest))"
        case "checkout": return "git 切换/检出：\(pos(rest))"
        case "switch": return "git 切换分支：\(pos(rest))"
        case "restore": return "git 恢复文件：\(pos(rest))"
        case "reset": return "git 重置（可能丢弃改动）：\(rest.joined(separator: " "))"
        case "clean": return "git 清理未跟踪文件：\(rest.joined(separator: " "))"
        case "merge": return "git 合并：\(pos(rest))"
        case "rebase": return "git 变基：\(rest.joined(separator: " "))"
        case "cherry-pick": return "git 拣选提交：\(pos(rest))"
        case "revert": return "git 撤回提交：\(pos(rest))"
        case "stash": return "git stash \(rest.first ?? "")".trimmingCharacters(in: .whitespaces)
        case "tag": return "git 标签：\(rest.joined(separator: " "))"
        case "branch": return "git 分支：\(rest.joined(separator: " "))"
        case "remote": return "git 远程：\(rest.joined(separator: " "))"
        default: return "git \(args.joined(separator: " "))"
        }
    }

    private static func messageValue(_ args: [String]) -> String? {
        for (i, a) in args.enumerated() {
            if a == "-m" || a == "--message" { return i + 1 < args.count ? args[i + 1] : nil }
            if a.hasPrefix("--message=") { return String(a.dropFirst("--message=".count)) }
        }
        return nil
    }

    private static func pos(_ args: [String]) -> String {
        let ps = positionals(args)
        return ps.isEmpty ? "(无)" : ps.joined(separator: "、")
    }

    /// 位置参数（非 `-`/`--` 开头）。不做 value-flag 感知：仅用于展示，误差无害。
    private static func positionals(_ args: [String]) -> [String] {
        args.filter { !$0.hasPrefix("-") }
    }
}
#endif
