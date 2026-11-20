import Foundation
import os

/// 聊天草稿的本地落盘（**只存文字**）。按会话 id 分条，空白「新对话」用 `blankKey`。
///
/// 为什么放 UserDefaults 而不是文件：单条上限 20 万字符，总量天然很小；与
/// `UnsentDraftStore`（401 恢复）同款做法，读写都在主线程、没有额外 IO 生命周期要管。
/// 纯逻辑、不依赖 LogBuffer / SwiftUI，便于 `swift test` 覆盖（Tests/DraftAutosaveStoreTests）。
enum DraftAutosaveStore {
    /// 没有会话 id 的 tab（空白「新对话」）用的 key。
    static let blankKey = "__new__"

    private static let log = Logger(subsystem: "com.xhan.xhanHelper", category: "draft-autosave")

    private static let defaultsKey = "xhan_draft_autosave"
    /// 单条上限：超了就不存 —— 宁可不存，也不截断用户写的长文。
    private static let maxTextLength = 200_000
    /// 30 天没动过的草稿自动清掉，防条目无限堆积。
    private static let maxAge: TimeInterval = 30 * 24 * 60 * 60
    /// 最多保留的会话条数（按更新时间留最新的）。
    private static let maxEntries = 50

    private struct Entry: Codable {
        var text: String
        var updatedAt: Date
    }

    /// 保存（空串 = 删除该条）。超长拒存返回 false，调用方负责说明原因。
    @discardableResult
    static func save(_ text: String, for key: String, now: Date = Date(),
                     defaults: UserDefaults = .standard) -> Bool {
        if text.isEmpty {
            remove(for: key, defaults: defaults)
            return true
        }
        guard text.count <= maxTextLength else { return false }
        var entries = read(defaults)
        entries[key] = Entry(text: text, updatedAt: now)
        write(pruned(entries, now: now), defaults: defaults)
        return true
    }

    /// 读取某会话的草稿；没有（或已过期）返回 nil。
    static func load(for key: String, now: Date = Date(),
                     defaults: UserDefaults = .standard) -> String? {
        pruned(read(defaults), now: now)[key]?.text
    }

    static func remove(for key: String, defaults: UserDefaults = .standard) {
        var entries = read(defaults)
        guard entries.removeValue(forKey: key) != nil else { return }
        write(entries, defaults: defaults)
    }

    // MARK: - Private

    private static func read(_ defaults: UserDefaults) -> [String: Entry] {
        guard let data = defaults.data(forKey: defaultsKey) else { return [:] }
        do {
            return try JSONDecoder().decode([String: Entry].self, from: data)
        } catch {
            // 解不出来当没有，不能挡住后续写入；但必须留痕——否则下一次 save 会用空字典
            // 整体覆盖 blob，之前所有草稿无声消失。
            log.error("解码草稿 blob 失败，按空处理（旧条目会被下一次写入覆盖）：\(String(describing: error))")
            return [:]
        }
    }

    private static func write(_ entries: [String: Entry], defaults: UserDefaults) {
        guard !entries.isEmpty else {
            defaults.removeObject(forKey: defaultsKey)
            return
        }
        // shortcut: 编码失败只静默保留旧值，下次输入会再试；纯字段结构实际不会失败。
        guard let data = try? JSONEncoder().encode(entries) else { return }
        defaults.set(data, forKey: defaultsKey)
    }

    private static func pruned(_ entries: [String: Entry], now: Date) -> [String: Entry] {
        var kept = entries.filter {
            !$0.value.text.isEmpty && now.timeIntervalSince($0.value.updatedAt) <= maxAge
        }
        if kept.count > maxEntries {
            let newest = kept.sorted { $0.value.updatedAt > $1.value.updatedAt }.prefix(maxEntries)
            kept = Dictionary(uniqueKeysWithValues: newest.map { ($0.key, $0.value) })
        }
        return kept
    }
}
