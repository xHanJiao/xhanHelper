import Foundation
import Combine

/// 用户可扩展配置（持久化到 user-config.json）：在不可修改的内置默认值之上，
/// 追加用户自定义条目。四类配置：
/// - 高敏域名 / 高敏路径（agent 交互工具在这些页面自动升 L3 确认）
/// - 高危扩展名（下载安全黑名单）
/// - 内容屏蔽域名（WKContentRuleList 广告/追踪拦截）
/// 所有读取方调用 merged* 属性（内置 + 用户去重合并），改动即时生效。
@MainActor
final class UserConfigStore: ObservableObject {
    static let shared = UserConfigStore()

    struct UserConfig: Codable {
        var sensitiveHosts: [String] = []
        var sensitivePaths: [String] = []
        var dangerousExtensions: [String] = []
        var contentBlockerDomains: [String] = []
    }

    @Published var config: UserConfig = .init()

    // MARK: - 内置默认值（不可改）

    static let builtInSensitiveHosts: [String] = [
        "bank", "banking", "alipay", "wechatpay", "paypal", "stripe", "2fa",
        "mail", "webmail", "email", "verify", "auth",
    ]

    static let builtInSensitivePaths: [String] = [
        "/transfer", "/payment", "/withdraw", "/security", "/account",
        "/login", "/signin", "/2fa", "/verify",
    ]

    static let builtInDangerousExtensions: [String] = [
        "sh", "dmg", "zip", "exe", "app", "pkg", "command", "run", "msi", "jar",
        "bat", "bin", "py", "php", "js", "html", "htm", "swift", "go", "rb",
    ]

    static let builtInContentBlockerDomains: [String] = [
        "doubleclick.net", "googlesyndication.com", "googletagservices.com",
        "googleadservices.com", "adservice.google.com", "google-analytics.com",
        "googletagmanager.com", "connect.facebook.net", "facebook.com/tr",
        "criteo.com", "taboola.com", "outbrain.com", "cnzz.com", "umeng.com", "mmstat.com",
    ]

    // MARK: - 合并视图（内置 + 用户，去重，内置在前）

    var mergedSensitiveHosts: [String] { merged(Self.builtInSensitiveHosts, config.sensitiveHosts) }
    var mergedSensitivePaths: [String] { merged(Self.builtInSensitivePaths, config.sensitivePaths) }
    var mergedDangerousExtensions: [String] { merged(Self.builtInDangerousExtensions, config.dangerousExtensions) }
    var mergedContentBlockerDomains: [String] { merged(Self.builtInContentBlockerDomains, config.contentBlockerDomains) }

    private func merged(_ builtIn: [String], _ user: [String]) -> [String] {
        var seen = Set<String>()
        var out: [String] = []
        for v in builtIn {
            let k = v.lowercased()
            if !seen.contains(k) { seen.insert(k); out.append(v) }
        }
        for v in user {
            let k = v.lowercased()
            if !seen.contains(k) { seen.insert(k); out.append(v) }
        }
        return out
    }

    /// 值是否已存在（内置或用户，大小写不敏感）
    func contains(_ value: String, in list: MergedList) -> Bool {
        switch list {
        case .hosts: return mergedSensitiveHosts.contains { $0.lowercased() == value.lowercased() }
        case .paths: return mergedSensitivePaths.contains { $0.lowercased() == value.lowercased() }
        case .extensions: return mergedDangerousExtensions.contains { $0.lowercased() == value.lowercased() }
        case .blocker: return mergedContentBlockerDomains.contains { $0.lowercased() == value.lowercased() }
        }
    }

    enum MergedList {
        case hosts, paths, extensions, blocker
    }

    // MARK: - 增删（用户条目）

    @discardableResult
    func add(_ raw: String, to list: MergedList) -> Bool {
        let value = normalize(raw, for: list)
        guard !value.isEmpty, !contains(value, in: list) else { return false }
        switch list {
        case .hosts: config.sensitiveHosts.append(value)
        case .paths: config.sensitivePaths.append(value)
        case .extensions: config.dangerousExtensions.append(value)
        case .blocker: config.contentBlockerDomains.append(value)
        }
        scheduleSave()
        return true
    }

    func remove(_ value: String, from list: MergedList) {
        switch list {
        case .hosts: config.sensitiveHosts.removeAll { $0.lowercased() == value.lowercased() }
        case .paths: config.sensitivePaths.removeAll { $0.lowercased() == value.lowercased() }
        case .extensions: config.dangerousExtensions.removeAll { $0.lowercased() == value.lowercased() }
        case .blocker: config.contentBlockerDomains.removeAll { $0.lowercased() == value.lowercased() }
        }
        scheduleSave()
    }

    /// 输入归一化：去首尾空白、小写、按类别校验。
    /// 扩展名去前导点（.sh → sh）；路径建议带 / 前缀（不强校验）；
    /// 域名要求含至少一个点且无非域名字符。
    private func normalize(_ raw: String, for list: MergedList) -> String {
        var v = raw.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !v.isEmpty else { return "" }
        switch list {
        case .extensions:
            while v.hasPrefix(".") { v.removeFirst() }
            let allowed = CharacterSet.alphanumerics
            guard v.unicodeScalars.allSatisfy({ allowed.contains($0) }) else { return "" }
        case .blocker:
            let allowed = CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyz0123456789.-")
            guard v.contains("."), v.unicodeScalars.allSatisfy({ allowed.contains($0) }) else { return "" }
        case .hosts:
            let allowed = CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyz0123456789.-")
            guard v.unicodeScalars.allSatisfy({ allowed.contains($0) }) else { return "" }
        case .paths:
            let allowed = CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyz0123456789/._-")
            guard v.unicodeScalars.allSatisfy({ allowed.contains($0) }) else { return "" }
        }
        return v
    }

    // MARK: - 持久化

    private let storeURL: URL
    private var saveTask: Task<Void, Never>?

    private init() {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("xhanHelper", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        storeURL = dir.appendingPathComponent("user-config.json")
        load()
    }

    private func load() {
        guard let data = try? Data(contentsOf: storeURL),
              let decoded = try? JSONDecoder().decode(UserConfig.self, from: data) else { return }
        config = decoded
    }

    private func scheduleSave() {
        saveTask?.cancel()
        saveTask = Task { @MainActor [weak self] in
            try? await Task.sleep(for: .milliseconds(300))
            self?.save()
        }
    }

    private func save() {
        guard let data = try? JSONEncoder().encode(config) else { return }
        try? data.write(to: storeURL, options: .atomic)
    }
}
