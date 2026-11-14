#if os(macOS)
import CryptoKit
import Foundation

/// EPUB 归档层：路径归一 + 系统 `unzip` + SHA256。
///
/// 不自己写 zip 解析（`LocalBackup` 也是调系统工具）。唯一自己做的事是**解包前**校验
/// 条目名：`unzip` 会照做 `../` 与绝对路径，恶意/损坏的包能写到 `Books/<id>/` 外面去。
nonisolated enum EpubArchive {

    // MARK: - 路径归一

    /// 把 EPUB 里的 href 归一成 `content/` 内的相对路径，逃出根目录或为空 → nil。
    ///
    /// - `baseDir`：href 引用方所在目录（相对 `content/`，如 `OEBPS/Text`；根目录传 `""`）。
    /// - 处理 `#fragment` / `?query` / 百分号编码 / 反斜杠（坏包）/ `..`。
    static func normalize(_ href: String, baseDir: String) -> String? {
        var raw = href.trimmingCharacters(in: .whitespacesAndNewlines)
        if let cut = raw.firstIndex(where: { $0 == "#" || $0 == "?" }) {
            raw = String(raw[raw.startIndex..<cut])
        }
        raw = raw.replacingOccurrences(of: "\\", with: "/")
        raw = raw.removingPercentEncoding ?? raw
        guard !raw.isEmpty else { return nil }

        var segments: [String] = []
        let absolute = raw.hasPrefix("/")
        let base = absolute ? "" : baseDir
        for part in (base + "/" + raw).split(separator: "/", omittingEmptySubsequences: false) {
            switch part {
            case "", ".":
                continue
            case "..":
                // 已经到根还要往上 = 逃出 content/，直接拒绝
                guard !segments.isEmpty else { return nil }
                segments.removeLast()
            default:
                segments.append(String(part))
            }
        }
        guard !segments.isEmpty else { return nil }
        return segments.joined(separator: "/")
    }

    /// href 里的锚点（`ch1.xhtml#s2` → `s2`）；没有或只有 `#` 返回 nil。
    static func fragment(of href: String) -> String? {
        guard let hash = href.firstIndex(of: "#") else { return nil }
        let raw = href[href.index(after: hash)...].trimmingCharacters(in: .whitespacesAndNewlines)
        guard !raw.isEmpty else { return nil }
        return raw.removingPercentEncoding ?? raw
    }

    /// 目录部分（归一后的相对路径），根目录返回 `""`。
    static func dirName(of relativePath: String) -> String {
        let dir = (relativePath as NSString).deletingLastPathComponent
        return dir == "." ? "" : dir
    }

    // MARK: - 解包

    /// 解包到 `dest`（已存在的同名文件覆盖）。条目名先过安全校验。
    static func unzip(_ archive: URL, into dest: URL) throws {
        let listing: String
        do {
            listing = try run("/usr/bin/unzip", ["-Z1", archive.path])
        } catch {
            throw BookImportError.invalidFile("无法读取压缩包：\(error.localizedDescription)")
        }
        for line in listing.split(separator: "\n") {
            let name = String(line).trimmingCharacters(in: .whitespacesAndNewlines)
            if name.isEmpty || name.hasPrefix("/") || name.split(separator: "/").contains("..") {
                throw BookImportError.invalidFile("压缩包含不安全路径：\(name)")
            }
        }
        do {
            try FileManager.default.createDirectory(at: dest, withIntermediateDirectories: true)
            _ = try run("/usr/bin/unzip", ["-o", "-q", "-d", dest.path, archive.path])
        } catch let error as BookImportError {
            throw error
        } catch {
            throw BookImportError.writeFailed("解包失败：\(error.localizedDescription)")
        }
    }

    // MARK: - 解包后大小

    /// 解包后的总字节数（防 zip bomb：5MB 的包能解出几十 GB）。
    /// 解析不出来时返回 nil —— 调用方按「未知」继续，还有源文件 200MB 上限兜底。
    static func totalUncompressedBytes(_ archive: URL) -> Int? {
        guard let listing = try? run("/usr/bin/unzip", ["-l", archive.path]) else { return nil }
        guard let regex = try? NSRegularExpression(
            pattern: #"^\s*(\d+)\s+\d{2}-\d{2}-\d{4}\s+\d{2}:\d{2}\s+\S"#,
            options: [.anchorsMatchLines]
        ) else { return nil }
        let ns = listing as NSString
        var total = 0
        var matched = false
        var overflowed = false
        regex.enumerateMatches(in: listing, range: NSRange(location: 0, length: ns.length)) { match, _, _ in
            guard !overflowed, let match, match.numberOfRanges > 1,
                  let value = Int(ns.substring(with: match.range(at: 1))) else { return }
            matched = true
            // 大小来自 zip 中央目录，可被构造（zip64 填两个超大值）→ 溢出会 trap，直接封顶
            let (sum, overflow) = total.addingReportingOverflow(value)
            if overflow {
                overflowed = true
                return
            }
            total = sum
        }
        if overflowed { return Int.max }
        return matched ? total : nil
    }

    // MARK: - SHA256

    /// 分块算 SHA256（书可能 200MB，不整文件读进内存）。
    static func sha256Hex(_ url: URL) throws -> String {
        let handle: FileHandle
        do {
            handle = try FileHandle(forReadingFrom: url)
        } catch {
            throw BookImportError.invalidFile("无法打开文件：\(error.localizedDescription)")
        }
        defer {
            do { try handle.close() } catch {
                NSLog("[EpubArchive] 关闭文件句柄失败 \(url.path)：\(error)")
            }
        }
        var hasher = SHA256()
        while true {
            let chunk: Data?
            do { chunk = try handle.read(upToCount: 1 << 20) } catch {
                throw BookImportError.invalidFile("读取文件失败：\(error.localizedDescription)")
            }
            guard let chunk, !chunk.isEmpty else { break }
            hasher.update(data: chunk)
        }
        return hasher.finalize().map { String(format: "%02x", $0) }.joined()
    }

    // MARK: - 外部进程

    /// 顺序读 stdout → stderr（与 `LocalBackup.runProcess` 同款）。失败时把 stderr 带出去。
    private static func run(_ path: String, _ args: [String]) throws -> String {
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
        guard p.terminationStatus == 0 else {
            let stderr = String(decoding: errData, as: UTF8.self)
                .trimmingCharacters(in: .whitespacesAndNewlines)
            throw BookImportError.invalidFile("\(path) 退出码 \(p.terminationStatus)：\(stderr)")
        }
        return String(decoding: outData, as: UTF8.self)
    }
}
#endif
