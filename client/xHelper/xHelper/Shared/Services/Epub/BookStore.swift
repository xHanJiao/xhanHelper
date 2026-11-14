#if os(macOS)
import Foundation

/// `Books/` 的读写。一本一个目录，`meta.json` 是唯一索引（不建索引文件，避免两份数据不同步）。
nonisolated struct BookStore {
    let root: URL

    init(root: URL) {
        self.root = root
    }

    struct Listing {
        var books: [BookMeta]
        /// 读不出来的目录（目录名 + 原因）。损坏的书不该让整个书架打不开，但也不能静默消失。
        var failures: [(id: String, message: String)]
    }

    func bookDir(_ id: String) -> URL {
        root.appendingPathComponent(id, isDirectory: true)
    }

    func contentDir(_ id: String) -> URL {
        bookDir(id).appendingPathComponent("content", isDirectory: true)
    }

    /// 扫 `meta.json`。没有 meta.json 的目录是导入中断的残留，跳过（不算错误）。
    /// 排序：最近在读优先，其次导入时间倒序。
    func listBooks() throws -> Listing {
        let fm = FileManager.default
        guard fm.fileExists(atPath: root.path) else { return Listing(books: [], failures: []) }
        let dirs: [URL]
        do {
            dirs = try fm.contentsOfDirectory(at: root, includingPropertiesForKeys: [.isDirectoryKey],
                                              options: [.skipsHiddenFiles])
        } catch {
            throw BookStoreError.listFailed("读取 \(root.path) 失败：\(error.localizedDescription)")
        }
        var books: [BookMeta] = []
        var failures: [(id: String, message: String)] = []
        for dir in dirs {
            let isDirectory: Bool
            do {
                isDirectory = try dir.resourceValues(forKeys: [.isDirectoryKey]).isDirectory ?? false
            } catch {
                // 读不出类型就跳过这一项，但原因要留痕，别让书「莫名消失」
                NSLog("[BookStore] 读取目录属性失败 \(dir.lastPathComponent)：\(error)")
                failures.append((dir.lastPathComponent, "读取目录属性失败：\(error.localizedDescription)"))
                continue
            }
            guard isDirectory else { continue }
            let id = dir.lastPathComponent
            let metaURL = dir.appendingPathComponent("meta.json")
            guard fm.fileExists(atPath: metaURL.path) else { continue }
            do {
                books.append(try decode(BookMeta.self, at: metaURL))
            } catch {
                failures.append((id, error.localizedDescription))
            }
        }
        books.sort { lhs, rhs in
            let l = lhs.lastReadAt ?? lhs.importedAt
            let r = rhs.lastReadAt ?? rhs.importedAt
            return l > r
        }
        return Listing(books: books, failures: failures)
    }

    /// 单本元数据；目录不存在返回 nil，内容损坏则抛错（别把损坏当空）。
    func meta(id: String) throws -> BookMeta? {
        let url = bookDir(id).appendingPathComponent("meta.json")
        guard FileManager.default.fileExists(atPath: url.path) else { return nil }
        return try decode(BookMeta.self, at: url)
    }

    func save(_ meta: BookMeta) throws {
        try FileManager.default.createDirectory(at: bookDir(meta.id), withIntermediateDirectories: true)
        try writeJSON(meta, to: bookDir(meta.id).appendingPathComponent("meta.json"))
    }

    func delete(id: String) throws {
        let dir = bookDir(id)
        guard FileManager.default.fileExists(atPath: dir.path) else { return }
        do {
            try FileManager.default.removeItem(at: dir)
        } catch {
            throw BookStoreError.deleteFailed("删除 \(id) 失败：\(error.localizedDescription)")
        }
    }

    /// 按原文件 SHA256 查重（导入前挡一次重复）。
    func findBySHA256(_ sha: String) throws -> BookMeta? {
        let listing = try listBooks()
        return listing.books.first { $0.sourceSHA256 == sha }
    }

    // MARK: - 批注

    /// 缺失 = 还没有批注（返回空）；**损坏则抛错** —— 不能拿空列表覆盖用户已有的批注。
    func annotations(bookID: String) throws -> [BookAnnotation] {
        let url = bookDir(bookID).appendingPathComponent("annotations.json")
        guard FileManager.default.fileExists(atPath: url.path) else { return [] }
        return try decode([BookAnnotation].self, at: url)
    }

    func saveAnnotations(_ items: [BookAnnotation], bookID: String) throws {
        try writeJSON(items, to: bookDir(bookID).appendingPathComponent("annotations.json"))
    }

    /// 用已解包内容重算目录 / 章节 / **节**（不重新解压、不动批注）。
    /// 解析器升级后（补锚点、滤空标题、大章切节）老 meta.json 靠这条自愈，
    /// 不必让用户删书重导 —— 重导会丢批注。
    func refreshStructure(id: String) throws -> BookMeta? {
        guard var meta = try meta(id: id) else { return nil }
        let package = try EpubPackage.parse(contentDir: contentDir(id))
        meta.toc = package.toc
        meta.chapters = package.chapters
        meta.chapterCount = package.chapters.count
        meta.sections = package.sections
        meta.parserVersion = Self.currentParserVersion
        if meta.title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            meta.title = package.title
        }
        // 老进度的 offset 是「章内扁平偏移」，新口径是「节内偏移」：
        // 按节字数把位置落到对应节的节首（一次性，最多偏一节）
        if let progress = meta.progress, progress.sectionIndex == nil {
            let sections = meta.effectiveSections
            let chapterSections = sections.filter { $0.chapterIndex == progress.chapterIndex }
            var remaining = max(0, progress.offset)
            var landed = chapterSections.first ?? sections.first
            for section in chapterSections {
                if remaining <= section.charCount { landed = section; break }
                remaining -= section.charCount
                landed = section
            }
            meta.progress = BookProgress(chapterIndex: landed?.chapterIndex ?? progress.chapterIndex,
                                          sectionIndex: landed?.index ?? 0,
                                          offset: 0,
                                          updatedAt: progress.updatedAt)
        }
        try save(meta)
        return meta
    }

    // MARK: - 正文

    /// 读一节：读出章节文件 → 抠 `<body>` → 按节的源码区间切片。
    /// 只切字符串、不解析 DOM（区间由 `HTMLText.planSections` 在导入时算好）。
    func sectionHTML(id: String, chapterHref: String, section: BookSection) throws -> (html: String, dir: String) {
        let raw = try chapterHTML(id: id, href: chapterHref)
        let body = HTMLText.bodyHTML(raw)
        let units = body.utf16
        let length = units.count
        let start = min(max(0, section.start), length)
        let end = min(max(start, section.end), length)
        // 老 meta 没有区间（start = end = 0）时退回整章
        let slice: String
        if end <= start {
            slice = body
        } else {
            guard let lower = units.index(units.startIndex, offsetBy: start, limitedBy: units.endIndex)?
                    .samePosition(in: body),
                  let upper = units.index(units.startIndex, offsetBy: end, limitedBy: units.endIndex)?
                    .samePosition(in: body) else {
                throw BookStoreError.readFailed("节的源码区间越界（\(start)..\(end)）")
            }
            slice = String(body[lower..<upper])
        }
        let dir = contentDir(id).appendingPathComponent(EpubArchive.dirName(of: chapterHref)).absoluteString
        return (slice, dir)
    }

    /// 读一章的 XHTML 原文。少数老书不是 UTF-8，让 Foundation 猜一次编码。
    func chapterHTML(id: String, href: String) throws -> String {
        let url = contentDir(id).appendingPathComponent(href)
        do {
            return try String(contentsOf: url, encoding: .utf8)
        } catch {
            NSLog("[BookStore] \(href) 不是 UTF-8（\(error.localizedDescription)），尝试自动识别编码")
            var used = String.Encoding.utf8
            do {
                return try String(contentsOf: url, usedEncoding: &used)
            } catch {
                throw BookStoreError.readFailed("章节解码失败（\(href)）：\(error.localizedDescription)")
            }
        }
    }

    // MARK: - JSON

    private func decode<T: Decodable>(_ type: T.Type, at url: URL) throws -> T {
        let data: Data
        do {
            data = try Data(contentsOf: url)
        } catch {
            throw BookStoreError.readFailed("\(url.lastPathComponent) 读取失败：\(error.localizedDescription)")
        }
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        do {
            return try decoder.decode(type, from: data)
        } catch {
            throw BookStoreError.readFailed("\(url.lastPathComponent) 解析失败：\(error.localizedDescription)")
        }
    }

    private func writeJSON<T: Encodable>(_ value: T, to url: URL) throws {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        let data: Data
        do {
            data = try encoder.encode(value)
        } catch {
            throw BookStoreError.writeFailed("\(url.lastPathComponent) 编码失败：\(error.localizedDescription)")
        }
        do {
            // `.atomic` = 同目录临时文件 + rename，中断不会留下半截 JSON
            try data.write(to: url, options: .atomic)
        } catch {
            throw BookStoreError.writeFailed("\(url.lastPathComponent) 写入失败：\(error.localizedDescription)")
        }
    }
}

// MARK: - 导入管线

nonisolated extension BookStore {

    /// 导入上限（与设计文档 §四 一致）
    static let maxBookBytes = 200 * 1024 * 1024

    /// 解析器版本：改了章节/目录的解析口径就 +1，老书打开时会自动补一次（见 `refreshStructure`）
    /// 3 = 大章按目录锚点切「节」（阅读单元从 spine 文件变成节）
    /// 4 = 平目录按标题级别（h1…h6）推层级 + 「第 N 部分」下沉一级
    static let currentParserVersion = 4

    /// 复制 → 解包 → 解析 → 抽封面 → 写 meta。失败时删掉半成品目录（没有 meta.json，书架也不会显示它）。
    ///
    /// 查重在复制之前做：同一份文件重复拖进来只提示，不建第二个目录。
    @discardableResult
    func importBook(source: URL,
                    progress: (EpubImportStep) -> Void = { _ in }) throws -> BookMeta {
        let fm = FileManager.default
        guard source.pathExtension.lowercased() == "epub" else {
            throw BookImportError.invalidFile("不是 .epub 文件：\(source.lastPathComponent)")
        }
        guard let size = (try? fm.attributesOfItem(atPath: source.path))?[.size] as? NSNumber else {
            throw BookImportError.invalidFile("无法读取文件大小：\(source.path)")
        }
        if size.intValue > Self.maxBookBytes {
            throw BookImportError.tooLarge("全书 \(size.intValue) 字节")
        }

        progress(.hashing)
        let sha = try EpubArchive.sha256Hex(source)
        let existing: BookMeta?
        do {
            existing = try findBySHA256(sha)
        } catch {
            // 书架读不出来不该说成「文件无效」：统一折进 .writeFailed（UI 显示「导入失败」）
            throw BookImportError.writeFailed("查重失败：\(error.localizedDescription)")
        }
        if let existing {
            throw BookImportError.duplicate(title: existing.title)
        }

        // 解包后的总量也要卡：一个 5MB 的包能解出几十 GB
        if let total = EpubArchive.totalUncompressedBytes(source), total > Self.maxBookBytes {
            throw BookImportError.tooLarge("解包后 \(total) 字节")
        }

        progress(.unpacking)
        let id = UUID().uuidString
        let dir = bookDir(id)
        var completed = false
        defer {
            if !completed {
                do { try fm.removeItem(at: dir) } catch {
                    // 清不掉就留着：没有 meta.json，listBooks 会跳过它
                    NSLog("[EpubImporter] 清理导入残留失败 \(dir.path)：\(error)")
                }
            }
        }
        do {
            try fm.createDirectory(at: dir, withIntermediateDirectories: true)
        } catch {
            throw BookImportError.writeFailed("创建书目录失败：\(error.localizedDescription)")
        }

        let sourceCopy = dir.appendingPathComponent("source.epub")
        do {
            try fm.copyItem(at: source, to: sourceCopy)
        } catch {
            throw BookImportError.writeFailed("复制源文件失败：\(error.localizedDescription)")
        }

        let contentDir = contentDir(id)
        try EpubArchive.unzip(sourceCopy, into: contentDir)

        progress(.parsing)
        let package = try EpubPackage.parse(contentDir: contentDir)

        progress(.writing)
        let coverFile = copyCover(package: package, from: contentDir, into: dir)
        let trimmedTitle = package.title.trimmingCharacters(in: .whitespacesAndNewlines)
        let meta = BookMeta(
            id: id,
            title: trimmedTitle.isEmpty ? source.deletingPathExtension().lastPathComponent : trimmedTitle,
            author: package.author,
            language: package.language,
            chapterCount: package.chapters.count,
            chapters: package.chapters,
            toc: package.toc,
            sections: package.sections,
            coverFile: coverFile,
            sourceBytes: size.intValue,
            sourceSHA256: sha,
            importedAt: Date(),
            lastReadAt: nil,
            progress: nil,
            parserVersion: Self.currentParserVersion
        )
        do {
            try save(meta)
        } catch {
            throw BookImportError.writeFailed("写 meta.json 失败：\(error.localizedDescription)")
        }
        completed = true
        return meta
    }

    /// 抽封面：只认常见位图格式，原名不要（避免 `../` 之类的花活），统一叫 `cover.<ext>`。
    private func copyCover(package: EpubPackage, from contentDir: URL, into dir: URL) -> String? {
        guard let coverPath = package.coverPath else { return nil }
        let ext = (coverPath as NSString).pathExtension.lowercased()
        let allowed = ["jpg": "jpg", "jpeg": "jpg", "png": "png", "gif": "gif", "webp": "webp"]
        guard let normalized = allowed[ext] else { return nil }
        let name = "cover.\(normalized)"
        do {
            try FileManager.default.copyItem(
                at: contentDir.appendingPathComponent(coverPath),
                to: dir.appendingPathComponent(name)
            )
            return name
        } catch {
            NSLog("[EpubImporter] 封面复制失败 \(coverPath)：\(error)")
            return nil
        }
    }
}

nonisolated enum BookStoreError: Error, LocalizedError {
    case listFailed(String)
    case readFailed(String)
    case writeFailed(String)
    case deleteFailed(String)

    var errorDescription: String? {
        switch self {
        case .listFailed(let why), .readFailed(let why), .writeFailed(let why), .deleteFailed(let why):
            return why
        }
    }
}
#endif
