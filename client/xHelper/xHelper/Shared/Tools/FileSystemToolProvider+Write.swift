#if os(macOS)
import Foundation

// MARK: - Write 预览

struct FSWritePreview {
    let filePath: String
    /// "新建" | "覆盖" | "追加"
    let op: String
    let diffText: String
    let additions: Int?
    let deletions: Int?
    let degraded: Bool
}

// MARK: - Write 实现

extension FileSystemToolProvider {

    // MARK: 预览（L3 确认前计算 diff，不落盘）

    /// 依据参数预演写入并生成 diff 文本，供确认条展示。
    /// 参数非法/文件不可读时返回 nil（执行时会给出明确错误）。
    static func previewWrite(arguments: [String: Any]) -> FSWritePreview? {
        guard let rawPath = arguments["path"] as? String, !rawPath.isEmpty,
              let content = arguments["content"] as? String else {
            return nil
        }
        let path = resolvePath(rawPath)
        let mode = (arguments["mode"] as? String) ?? "overwrite"

        let fm = FileManager.default
        let exists = fm.fileExists(atPath: path)

        if exists, SensitiveFileBlocker.check(path) != nil {
            return nil
        }

        let oldText = exists ? (readFullText(path) ?? "") : ""
        let newText: String
        if mode == "append" {
            var base = oldText
            if !base.isEmpty, !base.hasSuffix("\n") {
                base += "\n"
            }
            newText = base + content
        } else {
            newText = content
        }

        let result = DiffRenderer.render(oldText: oldText, newText: newText, filePath: path)
        let op = exists ? (mode == "append" ? "追加" : "覆盖") : "新建"
        return FSWritePreview(
            filePath: path,
            op: op,
            diffText: result.text,
            additions: result.additions,
            deletions: result.deletions,
            degraded: result.degraded
        )
    }

    // MARK: 执行

    func handleWrite(arguments: [String: Any], context: ExecuteContext) async throws -> LocalToolResult {
        guard let rawPath = arguments["path"] as? String, !rawPath.isEmpty else {
            return LocalToolResult(serverResult: nil, serverError: "缺少 path 参数", clientAction: nil)
        }
        guard let content = arguments["content"] as? String else {
            return LocalToolResult(serverResult: nil, serverError: "缺少 content 参数", clientAction: nil)
        }
        let mode = (arguments["mode"] as? String) ?? "overwrite"
        guard mode == "overwrite" || mode == "append" else {
            return LocalToolResult(serverResult: nil, serverError: "mode 只支持 overwrite / append", clientAction: nil)
        }

        let path = FileSystemToolProvider.resolvePath(rawPath)

        if let blocked = SensitiveFileBlocker.check(path) {
            return LocalToolResult(serverResult: nil, serverError: blocked, clientAction: nil)
        }
        if !DirectoryAuthorizationManager.shared.isAuthorized(path) {
            return LocalToolResult(serverResult: nil, serverError: "路径不在已授权目录内（或 symlink 指向授权目录外）: \(path)", clientAction: nil)
        }

        let fm = FileManager.default
        var isDir: ObjCBool = false
        let exists = fm.fileExists(atPath: path, isDirectory: &isDir)
        if exists && isDir.boolValue {
            return LocalToolResult(serverResult: nil, serverError: "路径是目录，不是文件: \(path)", clientAction: nil)
        }

        // 写前必读：覆盖/追加已有文件需本会话 read 过（防盲写/覆盖未知改动）
        if exists, !hasRead(path) {
            let hint = mode == "append"
                ? "请先用 read 读取该文件后再追加"
                : "请先用 read 读取该文件后再覆盖写入"
            return LocalToolResult(serverResult: nil, serverError: "\(hint): \(path)", clientAction: nil)
        }

        // 组装最终字节（保留原 BOM）
        var data: Data
        if exists {
            let original = (try? Data(contentsOf: URL(fileURLWithPath: path))) ?? Data()
            let body = Data(content.utf8)
            if mode == "append" {
                var base = original
                if !base.isEmpty, base.last != UInt8(ascii: "\n"), !content.isEmpty {
                    base.append(UInt8(ascii: "\n"))
                }
                data = base + body
            } else {
                if original.count >= 3, original[0] == 0xEF, original[1] == 0xBB, original[2] == 0xBF {
                    data = Data([0xEF, 0xBB, 0xBF]) + body
                } else {
                    data = body
                }
            }
        } else {
            data = Data(content.utf8)
        }

        // 原子写：同目录 temp + 权限保留 + replace/move
        let target = URL(fileURLWithPath: path)
        let tmp = URL(fileURLWithPath: path + ".\(UUID().uuidString).tmp")
        do {
            try data.write(to: tmp, options: .withoutOverwriting)
            if let perms = (try? fm.attributesOfItem(atPath: path))?[.posixPermissions] {
                try fm.setAttributes([.posixPermissions: perms], ofItemAtPath: tmp.path)
            }
            if exists {
                _ = try fm.replaceItemAt(target, withItemAt: tmp)
            } else {
                try fm.moveItem(at: tmp, to: target)
            }
        } catch {
            try? fm.removeItem(at: tmp)
            return LocalToolResult(serverResult: nil, serverError: "写入失败: \(error.localizedDescription)", clientAction: nil)
        }

        markRead(path) // 更新会话读记录（后续 edit 不再报未读）

        let verb = mode == "append" ? "Appended" : "Wrote"
        let label = mode == "append" ? "追加" : "写入"
        let detail = exists ? "" : " (新建)"
        return LocalToolResult(
            serverResult: "\(verb) \(data.count) bytes to \(path)\(detail) — \(label)成功".data(using: .utf8),
            serverError: nil,
            clientAction: nil
        )
    }

    // MARK: 辅助

    /// 全量读取文件文本（按检测编码解码，剥 BOM）。失败返回 nil。
    static func readFullText(_ path: String) -> String? {
        guard let fh = FileHandle(forReadingAtPath: path) else { return nil }
        defer { try? fh.close() }
        let probe = (try? fh.read(upToCount: 64 * 1024)) ?? Data()
        let encoding = FileSystemToolProvider.detectEncoding(probe)
        guard let data = try? Data(contentsOf: URL(fileURLWithPath: path)) else { return nil }

        func stripped(_ text: String?) -> String? {
            guard var t = text else { return nil }
            if t.hasPrefix("\u{FEFF}") { t.removeFirst() }
            return t
        }

        switch encoding {
        case .utf8:
            return stripped(String(data: data, encoding: .utf8))
        case .nonUTF8:
            return FileSystemToolProvider.decodeNonUTF8(data)?.text
        case .utf16LE:
            return stripped(String(data: data, encoding: .utf16LittleEndian))
        case .utf16BE:
            return stripped(String(data: data, encoding: .utf16BigEndian))
        }
    }
}
#endif
