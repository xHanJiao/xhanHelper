import Foundation
import WebKit

/// G3：内容拦截（WKContentRuleList）——屏蔽主流广告/追踪脚本。
/// 少加载第三方脚本 = 减少注入面 + 隐私 + 节能 + 更快（既是安全也支持"节能/接近 Chrome"）。
/// 内置清单（精选，只拦 script/image/xhr/media）+ 用户自定义域名（UserConfigStore 合并，动态生效）。
@MainActor
enum ContentBlocker {
    /// 编译并缓存的内容规则列表（启动时 load()，WebView 创建时注入）。
    nonisolated(unsafe) static var ruleList: WKContentRuleList?

    private static let identifier = "xhan_blocklist"

    /// 内置规则（空，仅保留用户自定义域名扩展能力；默认不拦截任何站点）。
    private static let builtInRulesJSON = "[]"

    /// 生成完整规则 JSON：内置规则 + 用户自定义域名（转义 `.` 为正则 `\.`）。
    private static func rulesJSON() -> String {
        let userDomains = UserConfigStore.shared.mergedContentBlockerDomains
            .filter { !UserConfigStore.builtInContentBlockerDomains.contains($0.lowercased()) }
        guard !userDomains.isEmpty else { return builtInRulesJSON }

        guard let data = builtInRulesJSON.data(using: .utf8),
              var rules = (try? JSONSerialization.jsonObject(with: data)) as? [[String: Any]] else {
            return builtInRulesJSON
        }

        for domain in userDomains {
            let escaped = domain.replacingOccurrences(of: ".", with: "\\.")
            rules.append([
                "trigger": [
                    "url-filter": ".*\(escaped).*",
                    "resource-type": ["script", "image", "xmlhttprequest", "media"],
                ],
                "action": ["type": "block"],
            ])
        }

        if let out = try? JSONSerialization.data(withJSONObject: rules),
           let str = String(data: out, encoding: .utf8) {
            return str
        }
        return builtInRulesJSON
    }

    /// 启动时调用：先查已编译缓存，无则编译。完成后注入到后续创建的 WebView。
    static func load() {
        guard let store = WKContentRuleListStore.default() else {
            LogBuffer.shared.error("[Blocker] WKContentRuleListStore unavailable")
            return
        }
        store.getAvailableContentRuleListIdentifiers { ids in
            if ids?.contains(identifier) == true {
                store.lookUpContentRuleList(forIdentifier: identifier) { list, _ in
                    Self.ruleList = list
                }
            } else {
                Self.compile(store: store)
            }
        }
    }

    /// 用户配置变化后重编译（新增域名立即对新 WebView 生效）。
    static func reload() {
        guard let store = WKContentRuleListStore.default() else { return }
        store.removeContentRuleList(forIdentifier: identifier) { _ in
            Self.compile(store: store)
        }
    }

    private static func compile(store: WKContentRuleListStore) {
        let json = rulesJSON()
        store.compileContentRuleList(forIdentifier: identifier, encodedContentRuleList: json) { list, error in
            if let error {
                Task { @MainActor in
                    LogBuffer.shared.error("[Blocker] compile failed: \(error.localizedDescription)")
                }
            }
            Self.ruleList = list
            if list != nil {
                Task { @MainActor in
                    LogBuffer.shared.info("[Blocker] content rule list loaded")
                }
            }
        }
    }
}
