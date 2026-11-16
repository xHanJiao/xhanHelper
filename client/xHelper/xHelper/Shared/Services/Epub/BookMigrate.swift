#if os(macOS)
import Foundation

/// 解析器升级（parser v5）可能改变节边界（下探更多层 wrapper、修前缀标签、跳过 script）。
/// 老批注/进度里存的是 `sectionIndex`，边界一变就指到错的节 —— 划线全画不出、进度跳到书首。
/// 口径统一成「章内字数偏移」：先按旧结构换算，再在新结构里落节。
nonisolated enum BookStructureMigration {

    /// 节内偏移 → 章内字数偏移（同章前面各节 charCount 之和 + 节内偏移）
    static func chapterOffset(sectionIndex: Int, offset: Int,
                              in sections: [BookSection]) -> (chapter: Int, offset: Int)? {
        guard sections.indices.contains(sectionIndex) else { return nil }
        let section = sections[sectionIndex]
        let before = sections[..<sectionIndex]
            .filter { $0.chapterIndex == section.chapterIndex }
            .reduce(0) { $0 + $1.charCount }
        return (section.chapterIndex, before + min(max(offset, 0), section.charCount))
    }

    /// 章内字数偏移 → 新结构里的（节下标, 节内偏移）；越过章尾就夹在最后一节末尾
    static func locate(chapter: Int, offset: Int,
                       in sections: [BookSection]) -> (index: Int, offset: Int)? {
        var acc = 0
        var last: (index: Int, offset: Int)?
        for (index, section) in sections.enumerated() where section.chapterIndex == chapter {
            if offset < acc + section.charCount { return (index, offset - acc) }
            acc += section.charCount
            last = (index, max(0, offset - acc))
        }
        return last
    }
}
#endif
