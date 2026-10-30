import Foundation

/// 灵阅正文的本地缓存（docs/design/html-artifact §9.8）。
///
/// 键为 artifact id，`<id>.html` 存正文、`<id>.etag` 存服务端 ETag。重复打开时带
/// `If-None-Match` 走 304，不用每次重下整页（带图页面能到几 MB）。
/// 系统清 Caches 后只是退回一次完整下载，不影响正确性。
///
/// 这个文件不依赖 SwiftUI / LocalPaths / LogBuffer：目录、上限、日志都由调用方注入，
/// 以便在 SwiftPM 里直接测（`Tests/ArtifactHTMLCacheCoreTests`）。app 侧单例见
/// `ArtifactHTMLCache+Shared.swift`。
final class ArtifactHTMLCache {
    struct Entry {
        let html: String
        let etag: String?
    }

    private let dir: URL
    private let maxBytes: Int64
    private let log: (String) -> Void

    init(dir: URL, maxBytes: Int64 = 512 << 20, log: @escaping (String) -> Void = { _ in }) {
        self.dir = dir
        self.maxBytes = maxBytes
        self.log = log
    }

    func load(id: String) -> Entry? {
        guard let htmlURL = fileURL(id, ext: "html") else { return nil }
        guard FileManager.default.fileExists(atPath: htmlURL.path) else { return nil }
        do {
            let html = try String(contentsOf: htmlURL, encoding: .utf8)
            return Entry(html: html, etag: readETag(id))
        } catch {
            log("ArtifactHTMLCache.load id=\(id): \(error)")
            return nil
        }
    }

    /// 写入失败向上抛：缓存失败不该影响本次展示，由调用方记日志后继续。
    func store(id: String, html: String, etag: String?) throws {
        guard let htmlURL = fileURL(id, ext: "html"),
              let etagURL = fileURL(id, ext: "etag") else {
            throw NSError(domain: "ArtifactHTMLCache", code: 400,
                userInfo: [NSLocalizedDescriptionKey: "非法 artifact id"])
        }
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try html.write(to: htmlURL, atomically: true, encoding: .utf8)
        if let etag, !etag.isEmpty {
            try etag.write(to: etagURL, atomically: true, encoding: .utf8)
        } else if FileManager.default.fileExists(atPath: etagURL.path) {
            try FileManager.default.removeItem(at: etagURL)
        }
        pruneIfNeeded()
    }

    /// 304 命中 → 刷新 mtime，让淘汰更接近 LRU。否则常看的页反而因为「从没重写过」先被清掉。
    func touch(id: String) {
        let now = Date()
        for ext in ["html", "etag"] {
            guard let url = fileURL(id, ext: ext),
                  FileManager.default.fileExists(atPath: url.path) else { continue }
            do {
                try FileManager.default.setAttributes([.modificationDate: now], ofItemAtPath: url.path)
            } catch {
                log("ArtifactHTMLCache.touch \(url.lastPathComponent): \(error)")
            }
        }
    }

    /// 产物删除 / 404 时清掉，别让已删的页面还能从缓存里被翻出来。
    func remove(ids: [String]) {
        for id in ids {
            for ext in ["html", "etag"] {
                guard let url = fileURL(id, ext: ext),
                      FileManager.default.fileExists(atPath: url.path) else { continue }
                do {
                    try FileManager.default.removeItem(at: url)
                } catch {
                    log("ArtifactHTMLCache.remove \(url.lastPathComponent): \(error)")
                }
            }
        }
    }

    private func readETag(_ id: String) -> String? {
        guard let url = fileURL(id, ext: "etag"),
              FileManager.default.fileExists(atPath: url.path) else { return nil }
        do {
            let etag = try String(contentsOf: url, encoding: .utf8)
                .trimmingCharacters(in: .whitespacesAndNewlines)
            return etag.isEmpty ? nil : etag
        } catch {
            // 标签坏了不影响正文；下次走完整 200 重写
            log("ArtifactHTMLCache.etag id=\(id): \(error)")
            return nil
        }
    }

    /// id 来自服务端/卡片数据，最终拼进文件名：只接受简单标识符，挡路径穿越。
    private func fileURL(_ id: String, ext: String) -> URL? {
        guard !id.isEmpty, id != ".", id != "..",
              !id.contains("/"), !id.contains("\\") else { return nil }
        return dir.appendingPathComponent("\(id).\(ext)", isDirectory: false)
    }

    private func pruneIfNeeded() {
        let fm = FileManager.default
        let keys: Set<URLResourceKey> = [.fileSizeKey, .contentModificationDateKey]
        let files: [URL]
        do {
            files = try fm.contentsOfDirectory(at: dir, includingPropertiesForKeys: Array(keys))
        } catch {
            log("ArtifactHTMLCache.prune list: \(error)")
            return
        }

        var entries: [(url: URL, size: Int64, date: Date)] = []
        var total: Int64 = 0
        for file in files {
            do {
                let values = try file.resourceValues(forKeys: keys)
                let size = Int64(values.fileSize ?? 0)
                entries.append((file, size, values.contentModificationDate ?? .distantPast))
                total += size
            } catch {
                log("ArtifactHTMLCache.prune stat \(file.lastPathComponent): \(error)")
            }
        }
        guard total > maxBytes else { return }
        for entry in entries.sorted(by: { $0.date < $1.date }) {
            guard total > maxBytes else { break }
            do {
                try fm.removeItem(at: entry.url)
                total -= entry.size
            } catch {
                log("ArtifactHTMLCache.prune remove \(entry.url.lastPathComponent): \(error)")
            }
        }
    }
}
