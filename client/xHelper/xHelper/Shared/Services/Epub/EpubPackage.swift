#if os(macOS)
import Foundation

/// 解析结果（还没落 `meta.json`）。
nonisolated struct EpubPackage {
    var title: String
    var author: String?
    var language: String?
    var chapters: [BookChapter]
    /// 阅读单元（大章按目录锚点切成的节）
    var sections: [BookSection]
    var toc: [BookTOCItem]
    /// `content/` 内相对路径
    var coverPath: String?
}

nonisolated extension EpubPackage {

    /// 单章上限（与设计文档 §四 一致）：超过就当整个文件无效。
    static let maxChapterBytes = 20 * 1024 * 1024

    /// 从已解包的 `content/` 解析 container → OPF → spine → TOC → 封面。
    ///
    /// 不解析正文 HTML：章节原样留给阅读器 JS `DOMParser`。这里只为了**目录、标题、
    /// 近似字数、封面**去读一遍正文（`HTMLText` 用正则，不建 DOM）。
    static func parse(contentDir: URL) throws -> EpubPackage {
        let fm = FileManager.default

        // DRM：LCP 许可证 = 加密；encryption.xml 里只放行「字体混淆」，其余算法一律拒
        // （字体混淆只打乱字体内码，正文是明文；阅读器用系统字体，不解密也能读）
        if fm.fileExists(atPath: contentDir.appendingPathComponent("META-INF/license.lcpl").path) {
            throw BookImportError.drm
        }
        let encryptionURL = contentDir.appendingPathComponent("META-INF/encryption.xml")
        if fm.fileExists(atPath: encryptionURL.path) {
            guard let encryptionData = readData(encryptionURL) else {
                throw BookImportError.invalidFile("META-INF/encryption.xml 读不出来")
            }
            if XMLScan.encryptionIsDRM(encryptionData) { throw BookImportError.drm }
        }

        // container.xml → 根 OPF
        let containerURL = contentDir.appendingPathComponent("META-INF/container.xml")
        guard let containerData = readData(containerURL) else {
            throw BookImportError.invalidFile("缺少 META-INF/container.xml")
        }
        guard let rootfile = XMLScan.container(containerData) else {
            throw BookImportError.invalidFile("container.xml 里没有 rootfile")
        }
        guard let opfRel = EpubArchive.normalize(rootfile, baseDir: "") else {
            throw BookImportError.invalidFile("OPF 路径逃出 content/：\(rootfile)")
        }
        let opfURL = contentDir.appendingPathComponent(opfRel)
        guard let opfData = readData(opfURL) else {
            throw BookImportError.invalidFile("OPF 不存在：\(opfRel)")
        }
        guard let opf = XMLScan.opf(opfData) else {
            throw BookImportError.invalidFile("OPF 不是合法 XML：\(opfRel)")
        }
        if opf.fixedLayout { throw BookImportError.fixedLayout }

        let opfDir = EpubArchive.dirName(of: opfRel)

        // TOC 先解析：切「节」要用它的锚点（EPUB3 nav 优先，坏了退回 NCX，设计文档 §四）
        var entries: [TOCEntry] = []
        var tocBase = opfDir
        if let navItem = opf.items.values.first(where: { item in
            item.properties.split(separator: " ").contains("nav")
        }), let navRel = EpubArchive.normalize(navItem.href, baseDir: opfDir),
           let navText = readTextBestEffort(contentDir.appendingPathComponent(navRel)),
           let navEntries = XMLScan.navTOC(navText) {
            entries = navEntries
            tocBase = EpubArchive.dirName(of: navRel)
        } else if let ncxItem = ncxItem(opf: opf),
                  let ncxRel = EpubArchive.normalize(ncxItem.href, baseDir: opfDir),
                  let ncxData = readData(contentDir.appendingPathComponent(ncxRel)),
                  let ncxEntries = XMLScan.ncxTOC(ncxData) {
            entries = ncxEntries
            tocBase = EpubArchive.dirName(of: ncxRel)
        }

        // spine → 章节（顺序即阅读顺序），每章顺手切成若干「节」。
        // 坏项 / 非正文项（封面 jpg、缺失文件、manifest 没登记）只跳过：一本坏目录不该让整本书打不开，
        // 一章都不剩时才算「没有正文」（设计文档 §四）。
        var chapters: [BookChapter] = []
        var sections: [BookSection] = []
        var sectionByFragment: [String: Int] = [:]
        var firstSectionByChapter: [Int: Int] = [:]
        /// 每章 fragment → 标题级别（`h1` → 1）。平目录的书靠它恢复层级
        var headingLevelsByChapter: [[String: Int]] = []
        /// 每章第一个标题的级别：目录条目只指向整个文件时用它兜底
        var firstHeadingLevelByChapter: [Int?] = []
        var skipped: [String] = []
        for idref in opf.spine {
            guard let item = opf.items[idref] else {
                skipped.append("manifest 缺 \(idref)")
                continue
            }
            guard item.isTextChapter else {
                skipped.append(item.href)
                continue
            }
            // 逃出 content/ 的 href 是构造攻击，不当「坏目录」容忍（设计文档 §四）
            guard let href = EpubArchive.normalize(item.href, baseDir: opfDir) else {
                throw BookImportError.invalidFile("OPF 里的 href 逃出解包目录：\(item.href)")
            }
            let fileURL = contentDir.appendingPathComponent(href)
            guard let size = fileSize(fileURL) else {
                skipped.append(href)
                continue
            }
            if size > maxChapterBytes {
                throw BookImportError.tooLarge("单章 \(size) 字节超过上限：\(href)")
            }
            let html = try readText(fileURL)
            let chapterIndex = chapters.count
            chapters.append(BookChapter(
                index: chapterIndex,
                href: href,
                title: HTMLText.chapterTitle(html: html, href: href, index: chapterIndex),
                // 纯图章字数为 0，保底 1 免得后续按字数加权时除零
                charCount: max(1, HTMLText.charCount(html))
            ))

            // 这一章的目录锚点（保持目录顺序）
            let body = HTMLText.bodyHTML(html)
            var anchors: [(fragment: String, title: String)] = []
            for entry in entries {
                guard let rel = EpubArchive.normalize(entry.href, baseDir: tocBase), rel == href,
                      let fragment = EpubArchive.fragment(of: entry.href) else { continue }
                let title = entry.title.trimmingCharacters(in: .whitespacesAndNewlines)
                guard !title.isEmpty else { continue }
                anchors.append((fragment, title))
            }
            let plan = HTMLText.planSections(body: body, anchors: anchors)
            headingLevelsByChapter.append(plan.headingLevelByFragment)
            firstHeadingLevelByChapter.append(plan.firstHeadingLevel)
            for (localIndex, section) in plan.sections.enumerated() {
                var value = section
                value.index = sections.count
                value.chapterIndex = chapterIndex
                sections.append(value)
                if localIndex == 0 { firstSectionByChapter[chapterIndex] = value.index }
            }
            let base = firstSectionByChapter[chapterIndex] ?? 0
            for (fragment, localIndex) in plan.sectionByFragment {
                // 跨章同名 id（各章都有 `id="notes"`）很常见：键必须带章号，否则后者覆盖前者
                sectionByFragment["\(chapterIndex)#\(fragment)"] = base + localIndex
            }
        }
        if !skipped.isEmpty {
            NSLog("[EpubPackage] 跳过 spine 项：\(skipped.joined(separator: ", "))")
        }
        guard !chapters.isEmpty else { throw BookImportError.noContent }

        // TOC href → spine 序号；指不到正文（封面页、外部链接）的条目丢掉
        var indexByHref: [String: Int] = [:]
        for chapter in chapters { indexByHref[chapter.href] = chapter.index }
        struct Accepted {
            var title: String
            var chapter: Int
            var fragment: String?
            var depth: Int
        }
        var accepted: [Accepted] = []
        for entry in entries {
            // 空标题条目（常见于 `<li><ol>…</ol></li>` 这种只用来分组的包装层）在面板里
            // 就是一行行大空白，直接丢掉；它的子条目是独立 entry，不受影响。
            let title = entry.title.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !title.isEmpty,
                  let rel = EpubArchive.normalize(entry.href, baseDir: tocBase),
                  let index = indexByHref[rel] else { continue }
            accepted.append(Accepted(title: title, chapter: index,
                                     fragment: EpubArchive.fragment(of: entry.href),
                                     depth: entry.depth))
        }
        // 目录自己带层级（NCX / nav 的 navPoint 嵌套）就照用作者的编排；全平（改编书常见）
        // 时才退回标题级别推层级。判「有没有编排」只看真能落到正文的条目 ——
        // 只在封面/版权页那一小簇里有嵌套，不代表全书编排过。
        let hasNestedTOC = Set(accepted.map(\.depth)).count > 1
        var toc: [BookTOCItem] = []
        var titleByChapter: [Int: String] = [:]
        for item in accepted {
            let depth: Int
            if hasNestedTOC {
                depth = item.depth
            } else if let fragment = item.fragment {
                // 有锚点：只认锚点所在标题（打在 <p>/容器上就不猜）
                if let level = headingLevelsByChapter[item.chapter][fragment] {
                    depth = level - 1
                } else {
                    depth = item.depth
                }
            } else if let level = firstHeadingLevelByChapter[item.chapter] {
                // 条目只指向整个文件（一 navPoint 一文件的平目录最常见）→ 用章首标题级别
                depth = level - 1
            } else {
                depth = item.depth
            }
            toc.append(BookTOCItem(title: item.title,
                                   chapterIndex: item.chapter,
                                   depth: depth,
                                   fragment: item.fragment,
                                   sectionIndex: item.fragment.flatMap { sectionByFragment["\(item.chapter)#\($0)"] }
                                       ?? firstSectionByChapter[item.chapter]))
            if titleByChapter[item.chapter] == nil { titleByChapter[item.chapter] = item.title }
        }
        // 层级归一：有的书整份目录被一个空标题包装层套住（`<li><ol>…`），
        // 所有条目都 depth=1 → 面板里整列平白缩进一格。把最小层级拉到 0。
        if let minDepth = toc.map(\.depth).min(), minDepth > 0 {
            for i in toc.indices { toc[i].depth -= minDepth }
        }
        // 「部分」下沉：部分标题保持原层，其后的条目整体 +1，遇下一个部分 / 附录重置。
        // 只在层级是推出来的（平目录）时做 —— 作者自己编排过的层级不再动。
        if !hasNestedTOC {
            var inPart = false
            for i in toc.indices {
                if HTMLText.isPartTitle(toc[i].title) {
                    inPart = true
                } else if HTMLText.endsPartGroup(toc[i].title) {
                    inPart = false
                } else if inPart {
                    toc[i].depth = min(toc[i].depth + 1, HTMLText.maxTOCDepth)
                }
            }
        }
        for i in toc.indices { toc[i].depth = max(0, min(toc[i].depth, HTMLText.maxTOCDepth)) }

        // TOC 标题比内文首个 h1/h2 更可信，覆盖掉
        for i in chapters.indices {
            if let title = titleByChapter[chapters[i].index] { chapters[i].title = title }
        }

        // 封面：EPUB2 `<meta name="cover" content="id">` 优先，EPUB3 `properties="cover-image"`
        var coverPath: String?
        let coverItem = opf.coverID.flatMap { opf.items[$0] }
            ?? opf.items.values.first { $0.properties.split(separator: " ").contains("cover-image") }
        if let item = coverItem,
           let rel = EpubArchive.normalize(item.href, baseDir: opfDir),
           fm.fileExists(atPath: contentDir.appendingPathComponent(rel).path) {
            coverPath = rel
        }
        // 没有 manifest 封面：很多老书把封面做成「封面页 XHTML 里放一张图」，顺着 guide /
        // spine 的 cover、titlepage 页抓第一张图。只对新导入生效（结构重算不补封面）
        if coverPath == nil {
            coverPath = coverFromPage(opf: opf, chapters: chapters,
                                      contentDir: contentDir, opfDir: opfDir)
        }

        return EpubPackage(
            title: opf.title,
            author: opf.author,
            language: opf.language,
            chapters: chapters,
            sections: sections,
            toc: toc,
            coverPath: coverPath
        )
    }

    /// 封面页书：manifest 没声明封面时，从 guide 的 cover / spine 里 href 含 cover、
    /// titlepage 的 XHTML 抓第一张图。图片文件名带 `cover` 的最可信（封面页里常先放出版社 logo）。
    private static func coverFromPage(opf: OPFDocument, chapters: [BookChapter],
                                      contentDir: URL, opfDir: String) -> String? {
        var candidates: [String] = []
        if let guide = opf.guideCover,
           let rel = EpubArchive.normalize(guide, baseDir: opfDir) { candidates.append(rel) }
        let lowered = chapters.map { (href: $0.href, lower: $0.href.lowercased()) }
        candidates += lowered.filter { $0.lower.contains("cover") }.map { $0.href }
        candidates += lowered.filter { $0.lower.contains("titlepage") || $0.lower.contains("title_page") }
            .map { $0.href }

        let allowed: Set<String> = ["jpg", "jpeg", "png", "gif", "webp"]
        var found: [String] = []
        var seen = Set<String>()
        for href in candidates where seen.insert(href).inserted {
            guard let html = readTextBestEffort(contentDir.appendingPathComponent(href)),
                  let src = HTMLText.firstImageSrc(html),
                  let rel = EpubArchive.normalize(src, baseDir: EpubArchive.dirName(of: href)),
                  allowed.contains((rel as NSString).pathExtension.lowercased()),
                  FileManager.default.fileExists(
                    atPath: contentDir.appendingPathComponent(rel).path) else { continue }
            found.append(rel)
        }
        return found.first { ($0 as NSString).lastPathComponent.lowercased().contains("cover") }
            ?? found.first
    }

    /// spine@toc → manifest item；没有就按 media-type 找 NCX。
    private static func ncxItem(opf: OPFDocument) -> OPFManifestItem? {
        let byID = opf.spineToc.flatMap { opf.items[$0] }
        if let byID { return byID }
        let byType = opf.items.values.first { $0.mediaType == "application/x-dtbncx+xml" }
        return byType
    }

    // MARK: - 文件小工具

    private static func readData(_ url: URL) -> Data? {
        do { return try Data(contentsOf: url) } catch {
            // 文件不存在 / 无权限都并存到上层，由调用点决定这是 fatal 还是可退回；
            // 这里只把原始原因打进日志，避免「打不开」被误当成「内容为空」。
            NSLog("[EpubPackage] 读取失败 \(url.path)：\(error)")
            return nil
        }
    }

    /// 先按 UTF-8，失败再让 Foundation 嗅探（老书常见 GBK/UTF-16）。
    private static func readText(_ url: URL) throws -> String {
        if let text = try? String(contentsOf: url, encoding: .utf8) { return text }
        var encoding = String.Encoding.utf8
        do {
            return try String(contentsOf: url, usedEncoding: &encoding)
        } catch {
            throw BookImportError.invalidFile("章节不是可读文本：\(url.lastPathComponent)（\(error.localizedDescription)）")
        }
    }

    /// 读文本，失败只记日志返回 nil（调用方本来就有兜底路径）。
    private static func readTextBestEffort(_ url: URL) -> String? {
        do { return try readText(url) } catch {
            NSLog("[EpubPackage] 读取失败 \(url.path)：\(error)")
            return nil
        }
    }

    private static func fileSize(_ url: URL) -> Int? {
        do {
            let attrs = try FileManager.default.attributesOfItem(atPath: url.path)
            return (attrs[.size] as? NSNumber)?.intValue
        } catch let error as CocoaError where error.code == .fileReadNoSuchFile {
            return nil  // 文件不存在 = 这一项跳过（不算错误）
        } catch {
            // 权限之类的失败：这一项跳过，但原因要留痕，别报成「文件缺失」
            NSLog("[EpubPackage] 读取文件属性失败 \(url.path)：\(error)")
            return nil
        }
    }
}

// MARK: - 正文纯文本

/// 从 XHTML 里抠「首个标题」和「近似字数」。刻意不建 DOM：正文渲染交给 JS。
nonisolated enum HTMLText {

    /// 章节标题：内文 h1/h2 → 按 href 关键词兜底（封面页 / 扉页这类本来就没有标题的页）。
    /// 不删章节、不重排 —— 章节下标变了，进度和批注全错位。
    static func chapterTitle(html: String, href: String, index: Int) -> String {
        if let heading = firstHeading(html) { return heading }
        let lower = href.lowercased()
        if lower.contains("cover") { return "封面" }
        if lower.contains("titlepage") || lower.contains("title_page") { return "扉页" }
        if lower.contains("copyright") { return "版权信息" }
        if lower.contains("colophon") { return "版权页" }
        // 「toc」要卡路径边界：`protocol.xhtml` 里也含这三个字母
        if lower.contains("/toc") || lower.hasPrefix("toc")
            || lower.contains("/nav") || lower.hasPrefix("nav") { return "目录" }
        return "第 \(index + 1) 章"
    }

    /// 首个 h1（没有就 h2）的纯文本；空标签视为没有。
    /// 标签允许命名空间前缀（`<xhtml:h1>`：有些书整篇带前缀）
    static func firstHeading(_ html: String) -> String? {
        for tag in ["h1", "h2"] {
            let pattern = "<(?:[A-Za-z][\\w.\\-]*:)?\(tag)\\b[^>]*>(.*?)</(?:[A-Za-z][\\w.\\-]*:)?\(tag)>"
            guard let raw = firstMatch(pattern, in: html) else { continue }
            let text = plainText(raw).trimmingCharacters(in: .whitespacesAndNewlines)
            if !text.isEmpty { return text }
        }
        return nil
    }

    /// 正文里第一张图的地址（`<img src>` 或 SVG `<image xlink:href|href>`）
    static func firstImageSrc(_ html: String) -> String? {
        let patterns = [
            "<img\\b[^>]*?\\ssrc\\s*=\\s*[\"']([^\"']+)[\"']",
            "<image\\b[^>]*?\\b(?:xlink:href|href)\\s*=\\s*[\"']([^\"']+)[\"']",
        ]
        for pattern in patterns {
            if let raw = firstMatch(pattern, in: html) { return raw }
        }
        return nil
    }

    /// 去标签后的正文（块边界折叠成空格，便于阅读，不用于精确偏移）。
    static func plainText(_ html: String) -> String {
        var s = html
        for pattern in ["<script\\b[^>]*>.*?</script>", "<style\\b[^>]*>.*?</style>", "<!--.*?-->"] {
            s = replaceAll(pattern, in: s, with: " ")
        }
        s = replaceAll("<[^>]+>", in: s, with: " ")
        s = decodeEntities(s)
        s = replaceAll("\\s+", in: s, with: " ")
        return s.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// 取 `<body>` 内容：章节 XHTML 是整篇文档，直接塞进 `#content` 的 innerHTML 会把
    /// `<head>` 里的 `<title>` 等一并解析成正文元素（污染拍平文本与进度偏移）。
    static func bodyHTML(_ html: String) -> String {
        guard let open = html.range(of: "<body", options: .caseInsensitive),
              let openEnd = html.range(of: ">", range: open.upperBound..<html.endIndex) else {
            return html
        }
        let start = openEnd.upperBound
        guard let close = html.range(of: "</body", options: .caseInsensitive,
                                     range: start..<html.endIndex) else {
            return String(html[start...])
        }
        return String(html[start..<close.lowerBound])
    }

    // MARK: - 分节（大章按目录锚点切「节」）

    /// 单个阅读单元的字数上限：超过就切节。
    /// 一本「一个 xhtml 装整本书」的排版书（常见于设计/画册类）整章可达 10 万字，
    /// 一次性插进 DOM 要几秒，切成节之后每次只送几 KB（design §5.8）。
    static let sectionCharLimit = 30_000

    struct SectionPlan {
        var sections: [BookSection]
        /// fragment → 节下标（章内局部下标，由调用方加全局偏移）
        var sectionByFragment: [String: Int]
        /// fragment → 标题级别（目录推层级用，见 `parse`）
        var headingLevelByFragment: [String: Int]
        /// 章里第一个标题的级别：目录条目只指向整个文件时的层级兜底
        var firstHeadingLevel: Int?
    }

    /// 把一章的 `<body>` 切成节：有目录锚点按锚点切，没有就按顶层元素累计到上限切。
    /// 返回的节 `index` 是章内局部值（0 起），`chapterIndex` 由调用方填。
    static func planSections(body: String, anchors: [(fragment: String, title: String)]) -> SectionPlan {
        // 一次扫描同时拿到「锚点位置」与「标题级别」：切节和目录层级共用
        let scanned = scanBody(body)
        let levels = scanned.anchors.compactMapValues(\.headingLevel)
        var pieces = topLevelRanges(body)
        var total = pieces.reduce(0) { $0 + $1.textCount }
        // 正文被 `<div>` 之类的 wrapper 包住时（常见于排版工具：`<body><h1>书名</h1><div>全书</div>`），
        // 顶层片段切不开 —— 174 个锚点全落同一片，切分等于没做。把**每一个**超限的 wrapper
        // 逐层展开（多层容器也认），兄弟片段（章首标题等）原样保留。
        var descent = 0
        while descent < 8 {
            var changed = false
            var i = 0
            while i < pieces.count {
                if pieces[i].textCount > sectionCharLimit,
                   let inner = innerPieces(of: pieces[i].range, in: body) {
                    pieces.replaceSubrange(i...i, with: inner)
                    changed = true
                    i += inner.count
                } else {
                    i += 1
                }
            }
            if !changed { break }
            descent += 1
        }
        total = pieces.reduce(0) { $0 + $1.textCount }
        let bodyLength = body.utf16.count

        // 小章整章一节：绝大多数书走这条路，行为与切分前完全一致
        if total <= sectionCharLimit || pieces.isEmpty {
            let section = BookSection(index: 0, chapterIndex: 0, title: anchors.first?.title ?? "",
                                      fragment: anchors.first?.fragment,
                                      start: 0, end: bodyLength, charCount: max(1, total))
            var map: [String: Int] = [:]
            for a in anchors { map[a.fragment] = 0 }
            return SectionPlan(sections: [section], sectionByFragment: map,
                               headingLevelByFragment: levels, firstHeadingLevel: scanned.firstHeadingLevel)
        }

        // 锚点 → 它所在的顶层片段
        var anchorPieces: [(piece: Int, fragment: String, title: String)] = []
        for anchor in anchors {
            guard let hit = scanned.anchors[anchor.fragment],
                  let piece = pieceIndex(containing: hit.position, in: pieces) else { continue }
            anchorPieces.append((piece, anchor.fragment, anchor.title))
        }

        var cuts: [Int] = []
        for anchor in anchorPieces.sorted(by: { $0.piece < $1.piece }) where !cuts.contains(anchor.piece) {
            cuts.append(anchor.piece)
        }

        var boundaries: [Int] = [0]
        if cuts.isEmpty {
            // 没有可用锚点（单文件无目录的书）：按字数切，别让一节无限长
            var acc = 0
            for (i, piece) in pieces.enumerated() {
                if acc >= sectionCharLimit {
                    boundaries.append(i)
                    acc = 0
                }
                acc += piece.textCount
            }
        } else {
            // 第一个锚点之前的内容（扉页/前言之类）并进第一节：不单开一节，
            // 否则那节没有目录条目，读到它时面板里一条都不高亮、底栏也没标题
            cuts[0] = 0
            boundaries = cuts
        }

        var sections: [BookSection] = []
        var sectionByFragment: [String: Int] = [:]
        for k in boundaries.indices {
            let from = boundaries[k]
            let to = k + 1 < boundaries.count ? boundaries[k + 1] : pieces.count
            guard from < to else { continue }
            let start = pieces[from].range.lowerBound
            let end = pieces[to - 1].range.upperBound
            let count = (from..<to).reduce(0) { $0 + pieces[$1].textCount }
            let inside = anchorPieces.filter { $0.piece >= from && $0.piece < to }
            let first = inside.min { $0.piece < $1.piece }
            let index = sections.count
            sections.append(BookSection(index: index, chapterIndex: 0,
                                        title: first?.title ?? "",
                                        fragment: first?.fragment,
                                        start: start, end: end, charCount: max(1, count)))
            for anchor in inside { sectionByFragment[anchor.fragment] = index }
        }
        return SectionPlan(sections: sections, sectionByFragment: sectionByFragment,
                           headingLevelByFragment: levels, firstHeadingLevel: scanned.firstHeadingLevel)
    }

    /// 取某个顶层片段内部的顶层片段（去掉它自己的开/闭标签），偏移仍相对整个 body。
    private static func innerPieces(of range: Range<Int>, in body: String)
        -> [(range: Range<Int>, textCount: Int)]? {
        let units = body.utf16
        let lower = units.index(units.startIndex, offsetBy: range.lowerBound)
        let upper = units.index(units.startIndex, offsetBy: range.upperBound)
        guard let openEnd = body.range(of: ">", range: lower..<upper)?.upperBound,
              let closeStart = body.range(of: "<", options: .backwards, range: openEnd..<upper)?.lowerBound,
              openEnd < closeStart else { return nil }
        let innerStart = units.distance(from: units.startIndex, to: openEnd)
        let inner = String(body[openEnd..<closeStart])
        let pieces = topLevelRanges(inner)
        // 只有一个子片段也照样交回去：外层是 wrapper、里层还是 wrapper 的书不少，
        // 一刀切「必须多于一个」会让两层容器的书永远切不开（此前的单层下探就栽在这）。
        // 纯文本（没标签）就别再往下钻了，钻进去只会把段落标签切掉。
        guard !pieces.isEmpty, pieces.count > 1 || inner.contains("<") else { return nil }
        return pieces.map { piece in
            ((piece.range.lowerBound + innerStart)..<(piece.range.upperBound + innerStart), piece.textCount)
        }
    }

    /// 一个锚点在 body 里的位置（UTF-16 下标）与所在标题的级别
    struct AnchorHit: Equatable {
        var position: Int
        /// 锚点落在标题里时是级别（`<h2 id="x">` 与 `<h2><a id="x">` 都算）；打在 `<p>` / 容器上为 nil
        var headingLevel: Int?
    }

    struct BodyIndex {
        var anchors: [String: AnchorHit]
        /// 章里第一个标题的级别（目录条目只指向整个文件、不带锚点时的层级兜底）
        var firstHeadingLevel: Int?
    }

    /// 一次顺序扫描 `<body>`：收集每个 `id=` / `name=` 锚点的位置 + 所在标题级别。
    ///
    /// 逐锚点做全文搜索在大部头上太贵（单文件装整本书、174 条目录 ≈ 上百次 10MB 级扫描）；
    /// 而且 `<h1><a id="x">标题</a></h1>` 这种写法要知道**外层**的标题，一次扫描顺手就记下了。
    /// 注释 / CDATA / PI 里的假标签跳过，属性值里的 `>` 不算标签结束。
    static func scanBody(_ body: String) -> BodyIndex {
        let units = body.utf16
        var out = BodyIndex(anchors: [:], firstHeadingLevel: nil)
        /// 当前所在的标题（正常 HTML 不会嵌套，但坏文件里有，用栈防丢外层）
        var headingStack: [(level: Int, tag: String)] = []

        func offset(_ index: String.UTF16View.Index) -> Int {
            units.distance(from: units.startIndex, to: index)
        }
        func matches(_ index: String.UTF16View.Index, _ ascii: [UInt16]) -> Bool {
            var j = index
            for value in ascii {
                guard j < units.endIndex, units[j] == value else { return false }
                j = units.index(after: j)
            }
            return true
        }
        func skip(_ index: String.UTF16View.Index, to terminator: [UInt16]) -> String.UTF16View.Index {
            var j = index
            while j < units.endIndex {
                if matches(j, terminator) {
                    var k = j
                    for _ in terminator { k = units.index(after: k) }
                    return k
                }
                j = units.index(after: j)
            }
            return units.endIndex
        }
        /// 读 index 处的标签名（已小写）
        func tagName(_ index: String.UTF16View.Index) -> (name: String, after: String.UTF16View.Index) {
            var j = index
            var name = ""
            while j < units.endIndex {
                let c = units[j]
                if c == 32 || c == 9 || c == 10 || c == 13 || c == 47 || c == 62 { break }
                name.unicodeScalars.append(UnicodeScalar(c) ?? "?")
                j = units.index(after: j)
            }
            return (localTag(name), j)
        }
        /// 跳过整个元素内容（`<script>` / `<style>` 里的 `<` 不是标签）
        func skipElement(_ name: String, from index: String.UTF16View.Index) -> String.UTF16View.Index {
            var j = index
            while j < units.endIndex {
                if units[j] == 60, matches(j, Self.closeTag) {
                    let inner = tagName(units.index(j, offsetBy: 2))
                    if inner.name == name { return skip(inner.after, to: [62]) }
                }
                j = units.index(after: j)
            }
            return units.endIndex
        }

        var i = units.startIndex
        while i < units.endIndex {
            guard units[i] == 60 else {           // '<'
                i = units.index(after: i)
                continue
            }
            if matches(i, Self.commentOpen) {
                i = skip(i, to: Self.commentClose)
                continue
            }
            if matches(i, Self.cdataOpen) {
                i = skip(i, to: Self.cdataClose)
                continue
            }
            if matches(i, Self.markupOpen) || matches(i, Self.piOpen) {
                i = skip(i, to: [62])
                continue
            }

            let closing = matches(i, Self.closeTag)
            let scanned = tagName(closing ? units.index(i, offsetBy: 2) : units.index(after: i))
            let tag = scanned.name

            if closing {
                if headingStack.last?.tag == tag { headingStack.removeLast() }
                i = skip(scanned.after, to: [62])
                continue
            }

            // 扫到 '>'（属性值里的 '>' 不算），顺手记下是不是自闭合
            var quote: UInt16 = 0
            var lastNonSpace: UInt16 = 0
            var k = scanned.after
            while k < units.endIndex {
                let c = units[k]
                if quote != 0 {
                    if c == quote { quote = 0 }
                } else if c == 34 || c == 39 {    // " 或 '
                    quote = c
                } else if c == 62 {               // '>'
                    k = units.index(after: k)
                    break
                }
                if c != 32 && c != 9 && c != 10 && c != 13 { lastNonSpace = c }
                k = units.index(after: k)
            }
            let end = k

            let level = headingLevel(ofTag: tag)
            if out.firstHeadingLevel == nil, let level { out.firstHeadingLevel = level }
            // 属性直接扫 UTF-16 切片，不做整标签解码（20MB 章是百万级标签）
            if let id = attribute("id", in: units, from: scanned.after, to: end)
                   ?? attribute("name", in: units, from: scanned.after, to: end),
               !id.isEmpty, out.anchors[id] == nil {
                out.anchors[id] = AnchorHit(position: offset(i),
                                            headingLevel: level ?? headingStack.last?.level)
            }
            if let level, lastNonSpace != 47 { headingStack.append((level, tag)) }   // 不是 `<h2/>`
            if tag == "script" || tag == "style" {
                i = skipElement(tag, from: end)
            } else {
                i = end
            }
        }
        return out
    }

    private static let commentOpen = Array("<!--".utf16)
    private static let commentClose = Array("-->".utf16)
    private static let cdataOpen = Array("<![CDATA[".utf16)
    private static let cdataClose = Array("]]>".utf16)
    private static let markupOpen = Array("<!".utf16)
    private static let piOpen = Array("<?".utf16)
    private static let closeTag = Array("</".utf16)

    /// 去命名空间前缀：`<xhtml:p>` / `<xhtml:br/>` 这类写法要当 `p` / `br` 处理
    private static func localTag(_ name: String) -> String {
        name.split(separator: ":").last.map { $0.lowercased() } ?? name.lowercased()
    }

    private static func headingLevel(ofTag tag: String) -> Int? {
        guard tag.count == 2, tag.hasPrefix("h"),
              let level = Int(String(tag.suffix(1))), (1...6).contains(level) else { return nil }
        return level
    }

    /// 按属性名取值（`id="x"` / `ID = 'x'` 都认）。只认「空白之后的完整属性名」，
    /// 所以 `data-id="x"` / `filename="x"` 不会被当成 `id`。
    private static func attribute(_ name: String, in units: String.UTF16View,
                                  from start: String.UTF16View.Index,
                                  to end: String.UTF16View.Index) -> String? {
        func isSpace(_ c: UInt16) -> Bool { c == 32 || c == 9 || c == 10 || c == 13 }
        var i = start
        while i < end {
            guard isSpace(units[i]) else {
                i = units.index(after: i)
                continue
            }
            var j = units.index(after: i)
            while j < end, isSpace(units[j]) { j = units.index(after: j) }
            var key = ""
            while j < end {
                let c = units[j]
                if isSpace(c) || c == 61 || c == 47 || c == 62 { break }   // 空白 / '=' / '/' / '>'
                key.unicodeScalars.append(UnicodeScalar(c) ?? "?")
                j = units.index(after: j)
            }
            let keyName = key.lowercased()
            while j < end, isSpace(units[j]) { j = units.index(after: j) }
            guard j < end, units[j] == 61 else {                               // '='
                i = units.index(after: i)
                continue
            }
            j = units.index(after: j)
            while j < end, isSpace(units[j]) { j = units.index(after: j) }
            guard j < end, units[j] == 34 || units[j] == 39 else {              // 引号
                i = units.index(after: i)
                continue
            }
            let quote = units[j]
            let valueStart = units.index(after: j)
            var valueEnd = valueStart
            while valueEnd < end, units[valueEnd] != quote { valueEnd = units.index(after: valueEnd) }
            if keyName == name, valueStart < valueEnd {
                return String(decoding: units[valueStart..<valueEnd], as: UTF16.self)
            }
            // 不是目标属性：跳过这段值继续找
            i = valueEnd < end ? units.index(after: valueEnd) : end
        }
        return nil
    }

    /// 目录最多显示到第几级（面板宽 220pt，再深文字就没地方了）
    static let maxTOCDepth = 3

    /// 「第 N 部分 / Part II / 上篇」这类分组标题：改编书把部分和章都写成 h1，
    /// 标题级别区分不出来，只能靠标题文字把章再沉一级。
    static func isPartTitle(_ title: String) -> Bool {
        let patterns = [
            "^第\\s*[一二三四五六七八九十百零〇两0-9]+\\s*部分",
            // 序号后面必须紧跟分隔符或行尾：「Part 1 of the story」是章标题，不是分组标题
            "^Part\\s+([IVXLC0-9]+|One|Two|Three|Four|Five|Six|Seven|Eight|Nine|Ten)\\s*([:：|·—-]|$)",
            "^(上|中|下)篇",
        ]
        return patterns.contains {
            title.range(of: $0, options: [.regularExpression, .caseInsensitive]) != nil
        }
    }

    /// 附录 / 后记这类收尾分组：从它们开始不再继承上一「部分」的缩进
    static func endsPartGroup(_ title: String) -> Bool {
        // 中文按前缀（「致谢词」也要算），英文加词尾边界（别把 "Indexing" 当索引）
        title.range(of: "^(附录|后记|致谢|索引)|^(Appendix|Acknowledg\\w*|Index)\\b",
                    options: [.regularExpression, .caseInsensitive]) != nil
    }

    /// 二分找包含该位置的顶层片段
    private static func pieceIndex(containing position: Int,
                                   in pieces: [(range: Range<Int>, textCount: Int)]) -> Int? {
        var low = 0, high = pieces.count - 1
        while low <= high {
            let mid = (low + high) / 2
            let range = pieces[mid].range
            if position < range.lowerBound { high = mid - 1 }
            else if position >= range.upperBound { low = mid + 1 }
            else { return mid }
        }
        return nil
    }

    /// HTML 的 void 元素：没有闭合标签，扫描时当成自闭合
    private static let voidElements: Set<String> = [
        "area", "base", "br", "col", "embed", "hr", "img", "input",
        "link", "meta", "param", "source", "track", "wbr",
    ]

    /// `<body>` 里每个顶层片段的源码区间 + 正文长度。
    /// 区间**连续覆盖**整个 body（片段之间的空白算进后一段），按边界切就能拼回原文。
    ///
    /// XHTML 必良构 ⇒ 标签平衡扫描即可：不做 HTML 解析、也不重序列化
    /// （重序列化要处理命名实体 / 自闭合 / 命名空间，代价和风险都比切字符串大得多）。
    static func topLevelRanges(_ body: String) -> [(range: Range<Int>, textCount: Int)] {
        let units = body.utf16
        var out: [(range: Range<Int>, textCount: Int)] = []
        var depth = 0
        var pieceStart = 0
        var textInPiece = 0
        var i = units.startIndex

        func offset(_ index: String.UTF16View.Index) -> Int {
            units.distance(from: units.startIndex, to: index)
        }
        func matches(_ index: String.UTF16View.Index, _ ascii: [UInt16]) -> Bool {
            var j = index
            for value in ascii {
                guard j < units.endIndex, units[j] == value else { return false }
                j = units.index(after: j)
            }
            return true
        }
        /// 跳到 `</name>` 之后：`<script>` / `<style>` 的内容里可能有裸 `<`（老书常见），
        /// 按标签扫描会把它们当成元素、把 depth 弄乱。
        func skipElement(_ name: String, from start: String.UTF16View.Index) -> String.UTF16View.Index {
            var j = start
            while j < units.endIndex {
                if units[j] == 60, matches(j, Array("</".utf16)) {
                    var k = units.index(j, offsetBy: 2)
                    var found = ""
                    while k < units.endIndex {
                        let c = units[k]
                        if c == 32 || c == 9 || c == 10 || c == 13 || c == 62 { break }
                        found.unicodeScalars.append(UnicodeScalar(c) ?? "?")
                        k = units.index(after: k)
                    }
                    if found.lowercased() == name { return skip(j, to: [62]) }
                }
                j = units.index(after: j)
            }
            return units.endIndex
        }
        /// 从 index 起找到 `terminator` 之后的位置（找不到就返回 endIndex）
        func skip(_ index: String.UTF16View.Index, to terminator: [UInt16]) -> String.UTF16View.Index {
            var j = index
            while j < units.endIndex {
                if matches(j, terminator) {
                    var k = j
                    for _ in terminator { k = units.index(after: k) }
                    return k
                }
                j = units.index(after: j)
            }
            return units.endIndex
        }
        /// 开始标签：扫到真正的 `>`（属性值里的 `>` 不算），返回结束位置、是否自闭合、标签名
        func scanStartTag(_ index: String.UTF16View.Index)
            -> (end: String.UTF16View.Index, selfClosing: Bool, name: String) {
            var j = units.index(after: index)   // 跳过 '<'
            var quote: UInt16 = 0
            var lastNonSpace: UInt16 = 0
            var name = ""
            var inName = true
            while j < units.endIndex {
                let c = units[j]
                if quote != 0 {
                    if c == quote { quote = 0 }
                } else if c == 34 || c == 39 {          // " 或 '
                    quote = c
                    inName = false
                } else if c == 62 {                     // '>'
                    let end = units.index(after: j)
                    return (end, lastNonSpace == 47, localTag(name))       // '/'
                } else if inName, c != 32 && c != 9 && c != 10 && c != 13 && c != 47 {
                    name.unicodeScalars.append(UnicodeScalar(c) ?? "?")
                } else {
                    inName = false
                }
                if c != 32 && c != 9 && c != 10 && c != 13 { lastNonSpace = c }
                j = units.index(after: j)
            }
            return (units.endIndex, false, localTag(name))
        }

        while i < units.endIndex {
            if units[i] != 60 {                         // '<'
                textInPiece += 1
                i = units.index(after: i)
                continue
            }
            if matches(i, Array("<!--".utf16)) {
                i = skip(i, to: Array("-->".utf16))
                continue
            }
            if matches(i, Array("<![CDATA[".utf16)) {
                let after = skip(i, to: Array("]]>".utf16))
                textInPiece += max(0, offset(after) - offset(i) - 12)
                i = after
                continue
            }
            if matches(i, Array("<!".utf16)) || matches(i, Array("<?".utf16)) {
                i = skip(i, to: [62])
                continue
            }
            if matches(i, Array("</".utf16)) {
                i = skip(i, to: [62])
                if depth > 0 { depth -= 1 }
                if depth == 0 {
                    out.append((pieceStart..<offset(i), max(1, textInPiece)))
                    pieceStart = offset(i)
                    textInPiece = 0
                }
                continue
            }
            let tag = scanStartTag(i)
            i = tag.end
            // `<script>` / `<style>`：整段跳过去（含闭合标签），不改 depth
            if !tag.selfClosing, tag.name == "script" || tag.name == "style" {
                i = skipElement(tag.name, from: tag.end)
                continue
            }
            // void 元素（`<br>` / `<img>` 这种 HTML 写法，EPUB 里并不罕见）也必须当自闭合，
            // 否则 depth 永远回不到 0，整本并成一个「顶层片段」→ 切分静默失效。
            if tag.selfClosing || Self.voidElements.contains(tag.name) {
                if depth == 0 {
                    out.append((pieceStart..<offset(i), max(1, textInPiece)))
                    pieceStart = offset(i)
                    textInPiece = 0
                }
            } else {
                depth += 1
            }
        }
        // 结尾残余的纯文本也单独成段，保证区间覆盖整个 body
        if pieceStart < units.count {
            out.append((pieceStart..<units.count, max(1, textInPiece)))
        }
        return out
    }

    /// 近似字数：去标签、去空白后的 UTF-16 长度（与 JS `String.length` 同口径）。
    static func charCount(_ html: String) -> Int {
        let text = plainText(html)
        return text.unicodeScalars.reduce(into: 0) { count, scalar in
            if !CharacterSet.whitespacesAndNewlines.contains(scalar) {
                count += UTF16.width(scalar)
            }
        }
    }

    // MARK: 正则 / 实体

    private static func firstMatch(_ pattern: String, in text: String) -> String? {
        guard let regex = try? NSRegularExpression(
            pattern: pattern, options: [.caseInsensitive, .dotMatchesLineSeparators]
        ) else { return nil }
        let ns = text as NSString
        guard let match = regex.firstMatch(in: text, range: NSRange(location: 0, length: ns.length)),
              match.numberOfRanges > 1 else { return nil }
        return ns.substring(with: match.range(at: 1))
    }

    private static func replaceAll(_ pattern: String, in text: String, with template: String) -> String {
        guard let regex = try? NSRegularExpression(
            pattern: pattern, options: [.caseInsensitive, .dotMatchesLineSeparators]
        ) else { return text }
        return regex.stringByReplacingMatches(
            in: text,
            range: NSRange(location: 0, length: (text as NSString).length),
            withTemplate: template
        )
    }

    /// 具名实体 → 数字实体（标题里的 `&nbsp;` / `&mdash;` 要显示成字符，而不是字面量）
    private static func decodeEntities(_ text: String) -> String {
        guard text.contains("&") else { return text }
        var s = HTMLNamedEntities.toNumeric(text)
        let map: [(String, String)] = [
            ("&amp;", "&"), ("&lt;", "<"), ("&gt;", ">"), ("&quot;", "\""), ("&apos;", "'"),
        ]
        for (entity, char) in map {
            s = s.replacingOccurrences(of: entity, with: char)
        }
        return decodeNumericEntities(s)
    }

    /// `&#160;` / `&#xA0;` → 字符。逐字符扫，避免正则 + NSString 下标来回换算。
    private static func decodeNumericEntities(_ text: String) -> String {
        var out = ""
        var index = text.startIndex
        while index < text.endIndex {
            if text[index] == "&",
               let semicolon = text[index...].firstIndex(of: ";"),
               text.distance(from: index, to: semicolon) <= 9 {
                let body = text[text.index(after: index)..<semicolon]
                if body.hasPrefix("#") {
                    let digits = body.dropFirst()
                    let isHex = digits.hasPrefix("x") || digits.hasPrefix("X")
                    let number = isHex ? String(digits.dropFirst()) : String(digits)
                    if let value = UInt32(number, radix: isHex ? 16 : 10),
                       let scalar = UnicodeScalar(value) {
                        out.unicodeScalars.append(scalar)
                        index = text.index(after: semicolon)
                        continue
                    }
                }
            }
            out.append(text[index])
            index = text.index(after: index)
        }
        return out
    }
}

/// HTML 5 常见具名实体 → 数字实体。
///
/// 为什么需要：EPUB3 的 nav 是 XHTML，作者常写 `&nbsp;` / `&mdash;` —— XML 只认 5 个内置实体，
/// 遇到它们 `XMLParser` 直接报错、整个目录丢失。转成数字实体后再交给 XMLParser。
nonisolated enum HTMLNamedEntities {
    private static let map: [String: Int] = [
        // XML 内置的 5 个也要转：不转的话 `&amp;` 会被当成未知实体，变成 `&amp;amp;`（标题里露馅）
        "amp": 38, "lt": 60, "gt": 62, "quot": 34, "apos": 39,
        "nbsp": 160, "iexcl": 161, "cent": 162, "pound": 163, "yen": 165, "sect": 167,
        "copy": 169, "reg": 174, "deg": 176, "plusmn": 177, "middot": 183, "frac12": 189,
        "times": 215, "divide": 247, "ndash": 8211, "mdash": 8212, "lsquo": 8216,
        "rsquo": 8217, "ldquo": 8220, "rdquo": 8221, "shy": 173, "hellip": 8230,
        "trade": 8482, "bull": 8226, "dagger": 8224, "permil": 8240, "prime": 8242,
        "laquo": 171, "raquo": 187, "agrave": 224, "aacute": 225, "acirc": 226,
        "atilde": 227, "auml": 228, "aring": 229, "ccedil": 231, "egrave": 232,
        "eacute": 233, "ecirc": 234, "euml": 235, "igrave": 236, "iacute": 237,
        "iuml": 239, "ntilde": 241, "ograve": 242, "oacute": 243, "ocirc": 244,
        "otilde": 245, "ouml": 246, "ugrave": 249, "uacute": 250, "uuml": 252,
        "szlig": 223,
    ]

    /// 已知的换数字实体；未知的转义成 `&amp;name;`（保住 XML 合法，TOC 里显示原样）。
    static func toNumeric(_ text: String) -> String {
        guard let regex = try? NSRegularExpression(pattern: "&([A-Za-z][A-Za-z0-9]{1,31});") else {
            return text
        }
        let ns = text as NSString
        var result = ""
        var cursor = text.startIndex
        regex.enumerateMatches(in: text, range: NSRange(location: 0, length: ns.length)) { match, _, _ in
            guard let match else { return }
            let range = match.range(at: 0)
            guard let swiftRange = Range(range, in: text) else { return }
            let name = ns.substring(with: match.range(at: 1))
            result += text[cursor..<swiftRange.lowerBound]
            result += map[name].map { "&#\($0);" } ?? "&amp;\(name);"
            cursor = swiftRange.upperBound
        }
        result += text[cursor..<text.endIndex]
        return result
    }
}

// MARK: - XML 扫描（container / OPF / NCX / nav）

/// 四个小解析器共用的结果类型，够用就行，不建通用 XML 树。
/// 一条目录项（NCX / nav 都归一成这个）
nonisolated struct TOCEntry: Equatable {
    var title: String
    var href: String
    var depth: Int
}

nonisolated struct OPFManifestItem {
    var id: String
    var href: String
    var mediaType: String
    var properties: String

    /// spine 里常混进封面图 / 广告页之类的非正文项；media-type 缺失时按正文处理（老书会漏写）
    var isTextChapter: Bool {
        mediaType.isEmpty || mediaType.contains("html")
    }
}

nonisolated struct OPFDocument {
    var title: String
    var author: String?
    var language: String?
    var items: [String: OPFManifestItem]
    var spine: [String]
    var spineToc: String?
    var coverID: String?
    /// EPUB2 `<guide><reference type="cover" href="…">`（封面页书的兜底）
    var guideCover: String?
    var fixedLayout: Bool
}

nonisolated enum XMLScan {

    static func container(_ data: Data) -> String? {
        let delegate = ContainerDelegate()
        return run(delegate, data) ? delegate.rootfile : nil
    }

    static func opf(_ data: Data) -> OPFDocument? {
        let delegate = OPFDelegate()
        guard run(delegate, data) else { return nil }
        return OPFDocument(
            title: delegate.title,
            author: delegate.author,
            language: delegate.language,
            items: delegate.items,
            spine: delegate.spine,
            spineToc: delegate.spineToc,
            coverID: delegate.coverID,
            guideCover: delegate.guideCover,
            fixedLayout: delegate.fixedLayout
        )
    }

    /// EPUB3 nav（XHTML）。返回 nil = 这份 nav 读不出目录（调用方退回 NCX）。
    /// 先转成 String（老书可能是 UTF-16/GBK），**必须去掉 XML 声明**：
    /// 源文件声明 `encoding="utf-16"`，重新按 UTF-8 编码后声明与内容不符，XMLParser 会直接失败。
    static func navTOC(_ text: String) -> [TOCEntry]? {
        let normalized = HTMLNamedEntities.toNumeric(stripXMLDeclaration(text))
        guard let data = normalized.data(using: .utf8) else { return nil }
        let delegate = NavDelegate()
        guard run(delegate, data), !delegate.entries.isEmpty else { return nil }
        return delegate.entries
    }

    private static func stripXMLDeclaration(_ text: String) -> String {
        guard let start = text.firstIndex(where: { !$0.isWhitespace }), text[start...].hasPrefix("<?xml"),
              let close = text.range(of: "?>") else { return text }
        return String(text[close.upperBound...])
    }

    /// encryption.xml 是否算 DRM。放行两种「字体混淆」算法，其余（AES / 未知 / 缺 URI / 解析失败）都拒。
    /// 空白清单（`<encryption/>`）放行 —— 有清单不等于加密。
    static func encryptionIsDRM(_ data: Data) -> Bool {
        let delegate = EncryptionDelegate()
        guard run(delegate, data) else { return true }
        return delegate.entries.contains { entry in
            guard let algorithm = entry.algorithm, let uri = entry.uri, !uri.isEmpty else { return true }
            let key = algorithm.lowercased().trimmingCharacters(in: CharacterSet(charactersIn: "/"))
            return !allowedEncryptionAlgorithms.contains(key)
        }
    }

    /// 两种「只是打乱字体内码」的算法：IDPF 字体混淆 / Adobe 字体混淆
    private static let allowedEncryptionAlgorithms: Set<String> = [
        "http://www.idpf.org/2008/embedding",
        "http://ns.adobe.com/pdf/enc#rc",
    ]

    static func ncxTOC(_ data: Data) -> [TOCEntry]? {
        let delegate = NCXDelegate()
        guard run(delegate, data) else { return nil }
        let entries = delegate.buildEntries()
        return entries.isEmpty ? nil : entries
    }

    private static func run(_ delegate: XMLParserDelegate, _ data: Data) -> Bool {
        let parser = XMLParser(data: data)
        parser.shouldProcessNamespaces = false
        parser.delegate = delegate
        return parser.parse()
    }

    /// 去命名空间前缀：`dc:title` → `title`
    static func local(_ name: String) -> String {
        name.split(separator: ":").last.map(String.init) ?? name
    }
}

nonisolated private final class ContainerDelegate: NSObject, XMLParserDelegate {
    var rootfile: String?
    func parser(_ parser: XMLParser, didStartElement elementName: String,
                namespaceURI: String?, qualifiedName: String?, attributes: [String: String]) {
        if rootfile == nil, XMLScan.local(elementName) == "rootfile" {
            rootfile = attributes["full-path"]
        }
    }
}

nonisolated private final class EncryptionDelegate: NSObject, XMLParserDelegate {
    struct Entry { var algorithm: String?; var uri: String? }
    private(set) var entries: [Entry] = []
    private var current: Entry?

    func parser(_ parser: XMLParser, didStartElement elementName: String,
                namespaceURI: String?, qualifiedName: String?, attributes: [String: String]) {
        switch XMLScan.local(elementName) {
        case "EncryptedData":
            current = Entry(algorithm: nil, uri: nil)
        case "EncryptionMethod":
            if current == nil { current = Entry(algorithm: nil, uri: nil) }
            current?.algorithm = attributes["Algorithm"]
        case "CipherReference":
            if current == nil { current = Entry(algorithm: nil, uri: nil) }
            current?.uri = attributes["URI"]
        default:
            break
        }
    }

    func parser(_ parser: XMLParser, didEndElement elementName: String,
                namespaceURI: String?, qualifiedName: String?) {
        guard XMLScan.local(elementName) == "EncryptedData", let entry = current else { return }
        entries.append(entry)
        current = nil
    }
}

nonisolated private final class OPFDelegate: NSObject, XMLParserDelegate {
    var title = ""
    var author: String?
    var language: String?
    var items: [String: OPFManifestItem] = [:]
    var spine: [String] = []
    var spineToc: String?
    var coverID: String?
    var guideCover: String?
    var fixedLayout = false

    private var inMetadata = false
    private var inGuide = false
    private var capture: String?
    private var buffer = ""

    func parser(_ parser: XMLParser, didStartElement elementName: String,
                namespaceURI: String?, qualifiedName: String?, attributes: [String: String]) {
        let el = XMLScan.local(elementName)
        switch el {
        case "metadata":
            inMetadata = true
        case "title", "creator", "language":
            if inMetadata { beginCapture(el) }
        case "meta":
            guard inMetadata else { return }
            if attributes["name"] == "cover" { coverID = attributes["content"] }
            if attributes["property"] == "rendition:layout" {
                if attributes["content"] == "pre-paginated" {
                    fixedLayout = true
                } else {
                    beginCapture("layout")
                }
            }
        case "item":
            if let id = attributes["id"], let href = attributes["href"] {
                items[id] = OPFManifestItem(
                    id: id,
                    href: href,
                    mediaType: attributes["media-type"] ?? "",
                    properties: attributes["properties"] ?? ""
                )
            }
        case "guide":
            inGuide = true
        case "reference":
            if inGuide, attributes["type"]?.lowercased() == "cover" { guideCover = attributes["href"] }
        case "spine":
            spineToc = attributes["toc"]
        case "itemref":
            if let idref = attributes["idref"], attributes["linear"] != "no" { spine.append(idref) }
        default:
            break
        }
    }

    func parser(_ parser: XMLParser, foundCharacters string: String) {
        if capture != nil { buffer += string }
    }

    func parser(_ parser: XMLParser, didEndElement elementName: String,
                namespaceURI: String?, qualifiedName: String?) {
        let el = XMLScan.local(elementName)
        if el == "metadata" { inMetadata = false }
        if el == "guide" { inGuide = false }
        guard el == "title" || el == "creator" || el == "language" || el == "meta" else { return }
        guard let field = capture else { return }
        let text = buffer.trimmingCharacters(in: .whitespacesAndNewlines)
        capture = nil
        buffer = ""
        guard !text.isEmpty else { return }
        switch field {
        case "title": title = text
        case "creator": if author == nil { author = text }
        case "language": if language == nil { language = text }
        case "layout": if text == "pre-paginated" { fixedLayout = true }
        default: break
        }
    }

    private func beginCapture(_ field: String) {
        capture = field
        buffer = ""
    }
}

nonisolated private final class NCXDelegate: NSObject, XMLParserDelegate {
    /// 按 navPoint 出现的顺序输出：父子嵌套时父节点要到子节点结束后才闭合，
    /// 直接按闭合顺序 append 会得到「子在前、父在后」的倒序目录。
    private var ordered: [(order: Int, entry: TOCEntry)] = []
    private var counter = 0

    private struct Point { var label: String?; var src: String?; var order: Int }
    private var stack: [Point] = []
    private var inText = false
    private var text = ""

    func buildEntries() -> [TOCEntry] {
        ordered.sorted { $0.order < $1.order }.map(\.entry)
    }

    func parser(_ parser: XMLParser, didStartElement elementName: String,
                namespaceURI: String?, qualifiedName: String?, attributes: [String: String]) {
        switch XMLScan.local(elementName) {
        case "navPoint":
            stack.append(Point(order: counter))
            counter += 1
        case "text":
            inText = true
            text = ""
        case "content":
            if !stack.isEmpty, stack[stack.count - 1].src == nil {
                stack[stack.count - 1].src = attributes["src"]
            }
        default:
            break
        }
    }

    func parser(_ parser: XMLParser, foundCharacters string: String) {
        if inText { text += string }
    }

    func parser(_ parser: XMLParser, didEndElement elementName: String,
                namespaceURI: String?, qualifiedName: String?) {
        switch XMLScan.local(elementName) {
        case "text":
            inText = false
            let label = text.trimmingCharacters(in: .whitespacesAndNewlines)
            if !stack.isEmpty, stack[stack.count - 1].label == nil, !label.isEmpty {
                stack[stack.count - 1].label = label
            }
        case "navPoint":
            guard let point = stack.popLast() else { return }
            if let label = point.label, let src = point.src {
                ordered.append((point.order, TOCEntry(title: label, href: src, depth: stack.count)))
            }
        default:
            break
        }
    }
}

nonisolated private final class NavDelegate: NSObject, XMLParserDelegate {
    var entries: [TOCEntry] = []

    private var inTOC = false
    private var sawNav = false
    private var olDepth = 0
    private var inAnchor = false
    private var anchorHref: String?
    private var anchorText = ""

    func parser(_ parser: XMLParser, didStartElement elementName: String,
                namespaceURI: String?, qualifiedName: String?, attributes: [String: String]) {
        switch XMLScan.local(elementName) {
        case "nav":
            guard !inTOC else { return }
            // `epub:type="toc"` 是标准写法；没写 type 的第一个 nav 也当目录（见设计文档 §四）
            let type = attributes["epub:type"] ?? attributes["type"] ?? ""
            let isTOC = type.split(separator: " ").contains("toc") || (!sawNav && type.isEmpty)
            if isTOC {
                inTOC = true
                olDepth = 0
            }
            sawNav = true
        case "ol":
            if inTOC { olDepth += 1 }
        case "a":
            guard inTOC else { return }
            inAnchor = true
            anchorHref = attributes["href"]
            anchorText = ""
        default:
            break
        }
    }

    func parser(_ parser: XMLParser, foundCharacters string: String) {
        if inAnchor { anchorText += string }
    }

    func parser(_ parser: XMLParser, didEndElement elementName: String,
                namespaceURI: String?, qualifiedName: String?) {
        switch XMLScan.local(elementName) {
        case "ol":
            if inTOC { olDepth = max(0, olDepth - 1) }
        case "a":
            guard inAnchor else { return }
            inAnchor = false
            let title = anchorText.trimmingCharacters(in: .whitespacesAndNewlines)
            if let href = anchorHref, !title.isEmpty {
                entries.append(TOCEntry(title: title, href: href, depth: max(0, olDepth - 1)))
            }
            anchorHref = nil
        case "nav":
            inTOC = false
        default:
            break
        }
    }
}
#endif
