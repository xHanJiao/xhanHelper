#if os(macOS)
import Foundation

// MARK: - 元数据

/// 一本书的元数据，落 `Books/<id>/meta.json`（唯一真源，不另建索引）。
nonisolated struct BookMeta: Codable, Identifiable, Equatable {
    var id: String
    var title: String
    var author: String?
    var language: String?
    var chapterCount: Int
    /// 按 spine 顺序
    var chapters: [BookChapter]
    /// 扁平化后的目录（含层级）
    var toc: [BookTOCItem]
    /// 阅读单元（= 「节」）。大章按目录锚点切成多节，小章整章一节。
    /// 老 meta 没有这个字段 → nil，打开时按 `parserVersion` 重建。
    var sections: [BookSection]?
    /// 书目录下的封面文件名，如 `cover.jpg`；没有封面为 nil
    var coverFile: String?
    var sourceBytes: Int
    /// 原文件 SHA256：导入查重 + 重导入识别（设计文档 E4；原 `BookMeta` 漏了这一项）
    var sourceSHA256: String
    var importedAt: Date
    var lastReadAt: Date?
    var progress: BookProgress?
    /// 写这份 meta 时的解析器版本。低于 `BookStore.currentParserVersion` 的书在打开时补一次目录
    /// （解析器修了目录锚点/层级这类问题后，老书不必删掉重导）。老 meta 没有这个字段 → nil。
    var parserVersion: Int?

    /// 是否按章导航（E20）：spine 有多章就按章导航，**不看目录覆盖了几章**。
    ///
    /// 曾经按「TOC 全指向同一文件」判定，坏 NCX 只列了 Part I 时会把整本书降级成无章节书，
    /// 目录 / 章号 / 上一下一章全被藏掉，第 2 章往后根本进不去。宁可多显示章号，不能丢章。
    var hasChapterNavigation: Bool {
        // 单文件装整本书的书：spine 只有 1 项，但有目录 → 仍然要给目录/上一节下一节
        chapterCount > 1 || !toc.isEmpty
    }

    func chapter(at index: Int) -> BookChapter? {
        chapters.indices.contains(index) ? chapters[index] : nil
    }

    /// 实际可用的阅读单元：没有 `sections`（老 meta）时按「一章一节」兜底
    var effectiveSections: [BookSection] {
        if let sections, !sections.isEmpty { return sections }
        return chapters.map {
            BookSection(index: $0.index, chapterIndex: $0.index, title: $0.title,
                        fragment: nil, start: 0, end: 0, charCount: $0.charCount)
        }
    }

    func sections(inChapter chapter: Int) -> [BookSection] {
        effectiveSections.filter { $0.chapterIndex == chapter }
    }

    func section(at index: Int) -> BookSection? {
        let all = effectiveSections
        return all.indices.contains(index) ? all[index] : nil
    }

    /// 全书进度（0…1）：按「节」字数加权 + 节内字符偏移，与阅读进度同一口径。
    var progressFraction: Double {
        let all = effectiveSections
        let total = all.reduce(0) { $0 + $1.charCount }
        guard total > 0, let p = progress else { return 0 }
        let index = min(max(p.sectionIndex ?? 0, 0), max(all.count - 1, 0))
        guard all.indices.contains(index) else { return 0 }
        let before = all.prefix(index).reduce(0) { $0 + $1.charCount }
        let inner = min(max(p.offset, 0), all[index].charCount)
        return min(1, max(0, Double(before + inner) / Double(total)))
    }
}

/// 一个阅读单元（「节」）：某个 spine 文件里 `[start, end)` 的一段正文。
nonisolated struct BookSection: Codable, Equatable, Identifiable {
    /// 全书内序号（= `BookMeta.effectiveSections` 的下标）
    var index: Int
    var chapterIndex: Int
    var title: String
    /// 该节的起始锚点（目录条目的 `#frag`）；自动切分的节为 nil
    var fragment: String?
    /// `<body>` 源码内的 UTF-16 区间（含标签，落在顶层元素边界上）
    var start: Int
    var end: Int
    /// 近似字数（去标签、去空白），只用于进度加权
    var charCount: Int
    var id: Int { index }
}

nonisolated struct BookChapter: Codable, Equatable {
    var index: Int
    /// `content/` 内的相对路径（已归一，不含 `..`）
    var href: String
    /// 目录标题 → 首个 h1/h2 → 「第 N 章」
    var title: String
    /// 近似字数（去标签、去空白），只用于进度加权
    var charCount: Int
}

nonisolated struct BookTOCItem: Codable, Equatable {
    var title: String
    var chapterIndex: Int
    var depth: Int
    /// 章节文件内的锚点（`chapter.xhtml#sec3` 的 `sec3`）。
    /// 大量 EPUB 是「一个 spine 文件装整本书、目录全靠锚点」——不带锚点跳转就只能落到文件开头。
    /// 老 meta.json 没这个字段，可选类型才能继续解出来。
    var fragment: String?
    /// 指向哪一节（`BookMeta.effectiveSections` 的下标）。老 meta 为 nil → 按章兜底。
    var sectionIndex: Int?

    /// 面板高亮 / 「当前读到哪一条」用的稳定键。
    /// 带上 depth + title：同一章同锚点的重复条目（作者把两个标题指到同一个 id）也要能区分行。
    var key: String { "\(chapterIndex):\(fragment ?? ""):\(depth):\(title)" }
}

nonisolated struct BookProgress: Codable, Equatable {
    var chapterIndex: Int
    /// 当前节（`effectiveSections` 下标）。老 meta 为 nil → 0
    var sectionIndex: Int?
    /// **节内**扁平文本偏移（JS `String.length` 口径，UTF-16 下标）
    var offset: Int
    var updatedAt: Date
}

nonisolated struct BookAnnotation: Codable, Equatable, Identifiable {
    var id: String
    var chapterIndex: Int
    /// 划线所在的节。老批注为 nil（只按章过滤，可能定位不到 —— 画不出就算了）
    var sectionIndex: Int?
    /// 选区文本（空白已归一，与阅读器 JS `fold()` 同口径）
    var quote: String
    /// 选区前 / 后各 ≤60 字，供 `locate()` 消歧
    var prefix: String
    var suffix: String
    /// 空 = 纯划线
    var note: String
    var createdAt: Date
    var updatedAt: Date
}

/// 一个书签：读者按 ⌘D 记住的那一页（落 `Books/<id>/bookmarks.json`）。
nonisolated struct BookBookmark: Codable, Equatable, Identifiable {
    var id: String
    var chapterIndex: Int
    var sectionIndex: Int
    /// 节内扁平偏移（与进度同口径）
    var offset: Int
    /// 当前页首附近的一段原文，列表里当预览（纯展示，不参与定位）
    var quote: String
    var createdAt: Date
}

// MARK: - 导入失败

/// 导入失败的分类。UI 只显示「徽章 + 一行原因」，完整解释进 tooltip（设计文档 §四）。
nonisolated enum BookImportError: Error, LocalizedError, Equatable {
    case drm
    case fixedLayout
    /// 文件缺失 / 损坏 / 结构不对；附带原始原因（进日志，不上封面）
    case invalidFile(String)
    case tooLarge(String)
    case noContent
    case writeFailed(String)
    case duplicate(title: String)

    /// 卡片角标
    var badge: String {
        switch self {
        case .drm: return "DRM 保护"
        case .fixedLayout: return "固定版式"
        case .invalidFile: return "文件无效"
        case .tooLarge: return "文件过大"
        case .noContent: return "没有正文"
        case .writeFailed: return "导入失败"
        case .duplicate: return "已导入"
        }
    }

    /// 卡片上一行原因
    var reason: String {
        switch self {
        case .drm: return "内容已加密，无法读取"
        case .fixedLayout: return "暂不支持按页排版的电子书"
        case .invalidFile: return "不是有效的 EPUB，或文件已损坏"
        case .tooLarge: return "超过 200 MB，暂不支持"
        case .noContent: return "解包成功，但解析不到章节"
        case .writeFailed: return "无法写入本地存储，请检查磁盘空间"
        case .duplicate(let title): return "《\(title)》已在书架里"
        }
    }

    /// tooltip / 详情里的完整解释
    var detail: String {
        switch self {
        case .drm:
            return "这本 EPUB 受 DRM 保护，正文被加密，阅读器无法解密。请导入未加密的版本。"
        case .fixedLayout:
            return "这本书每页按像素排版（常见于画册/漫画）。当前阅读器只支持可重排版的文字书。"
        case .invalidFile:
            return "文件缺少 EPUB 必需的 container.xml / OPF，可能已损坏或后缀名不对。"
        case .tooLarge:
            return "这本书体积超出导入上限（200 MB），或单章超过 20 MB。"
        case .noContent:
            return "能解包，但 spine 为空或章节文件缺失，没有可读内容。"
        case .writeFailed:
            return "解包或写入 Books/ 失败，检查磁盘剩余空间与目录权限。"
        case .duplicate:
            return "书架里已有同一份文件（SHA256 相同）。如需重新导入，先删除已有的那本。"
        }
    }

    /// 事件里的 `error_type`（附表 A 约定：稳定的短代号，不进人话）
    var errorType: String {
        switch self {
        case .drm: return "drm"
        case .fixedLayout: return "fixed_layout"
        case .invalidFile: return "invalid_file"
        case .tooLarge: return "too_large"
        case .noContent: return "no_content"
        case .writeFailed: return "write_failed"
        case .duplicate: return "duplicate"
        }
    }

    /// 原始原因（只进日志）
    var diagnostic: String {
        switch self {
        case .invalidFile(let why), .tooLarge(let why), .writeFailed(let why):
            return "\(badge)：\(why)"
        default:
            return "\(badge)：\(reason)"
        }
    }

    var errorDescription: String? { diagnostic }
}

// MARK: - 导入步骤（UI 显示「解包 / 解析目录 / 写入」）

nonisolated enum EpubImportStep: String {
    case hashing = "校验文件"
    case unpacking = "解包"
    case parsing = "解析目录"
    case writing = "写入"
}
#endif
