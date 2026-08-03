import Foundation
import WebKit

/// G3：内容拦截（WKContentRuleList）——屏蔽主流广告/追踪脚本。
/// 少加载第三方脚本 = 减少注入面 + 隐私 + 节能 + 更快（既是安全也支持"节能/接近 Chrome"）。
/// 精选清单，易扩展（追加 trigger 规则即可）。只拦 script/image/xhr/media，不拦页面导航。
@MainActor
enum ContentBlocker {
    /// 编译并缓存的内容规则列表（启动时 load()，WebView 创建时注入）。
    nonisolated(unsafe) static var ruleList: WKContentRuleList?

    private static let identifier = "xhan_blocklist"

    private static let rulesJSON = """
    [
      {"trigger":{"url-filter":".*doubleclick\\\\.net.*","resource-type":["script","image","xmlhttprequest","media"]},"action":{"type":"block"}},
      {"trigger":{"url-filter":".*googlesyndication\\\\.com.*","resource-type":["script","image","xmlhttprequest","media"]},"action":{"type":"block"}},
      {"trigger":{"url-filter":".*googletagservices\\\\.com.*","resource-type":["script","image","xmlhttprequest","media"]},"action":{"type":"block"}},
      {"trigger":{"url-filter":".*googleadservices\\\\.com.*","resource-type":["script","image","xmlhttprequest","media"]},"action":{"type":"block"}},
      {"trigger":{"url-filter":".*adservice\\\\.google\\\\.com.*","resource-type":["script","image","xmlhttprequest","media"]},"action":{"type":"block"}},
      {"trigger":{"url-filter":".*google-analytics\\\\.com.*","resource-type":["script","image","xmlhttprequest","media"]},"action":{"type":"block"}},
      {"trigger":{"url-filter":".*googletagmanager\\\\.com.*","resource-type":["script","image","xmlhttprequest","media"]},"action":{"type":"block"}},
      {"trigger":{"url-filter":".*connect\\\\.facebook\\\\.net.*","resource-type":["script","image","xmlhttprequest","media"]},"action":{"type":"block"}},
      {"trigger":{"url-filter":".*facebook\\\\.com/tr.*","resource-type":["script","xmlhttprequest"]},"action":{"type":"block"}},
      {"trigger":{"url-filter":".*criteo\\\\.com.*","resource-type":["script","image","xmlhttprequest","media"]},"action":{"type":"block"}},
      {"trigger":{"url-filter":".*taboola\\\\.com.*","resource-type":["script","image","xmlhttprequest","media"]},"action":{"type":"block"}},
      {"trigger":{"url-filter":".*outbrain\\\\.com.*","resource-type":["script","image","xmlhttprequest","media"]},"action":{"type":"block"}},
      {"trigger":{"url-filter":".*cnzz\\\\.com.*","resource-type":["script","image","xmlhttprequest"]},"action":{"type":"block"}},
      {"trigger":{"url-filter":".*hm\\\\.baidu\\\\.com.*","resource-type":["script","image","xmlhttprequest"]},"action":{"type":"block"}},
      {"trigger":{"url-filter":".*umeng\\\\.com.*","resource-type":["script","image","xmlhttprequest"]},"action":{"type":"block"}},
      {"trigger":{"url-filter":".*mmstat\\\\.com.*","resource-type":["script","image","xmlhttprequest"]},"action":{"type":"block"}}
    ]
    """

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
                store.compileContentRuleList(forIdentifier: identifier, encodedContentRuleList: rulesJSON) { list, error in
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
    }
}
