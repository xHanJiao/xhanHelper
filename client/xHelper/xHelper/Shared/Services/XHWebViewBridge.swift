import Foundation
import WebKit

/// JS 桥接工厂：创建带 user scripts + script message handlers 的 WKWebViewConfiguration。
/// 业务无关——注入什么脚本、注册什么 handler 由调用方决定。
enum XHWebViewBridge {

    /// 通用 JS 消息处理器：把 closure 包装成 WKScriptMessageHandler。
    /// 被 userContentController 强持有，随 WebView 生命周期存活。
    final class ScriptMessageHandler: NSObject, WKScriptMessageHandler {
        let handler: (Any) -> Void

        init(_ handler: @escaping (Any) -> Void) {
            self.handler = handler
        }

        func userContentController(_ userContentController: WKUserContentController,
                                   didReceive message: WKScriptMessage) {
            handler(message.body)
        }
    }

    /// 创建配置。
    /// - Parameters:
    ///   - userScripts: 注入页面的 JS 片段（如挂载全局 bridge 对象）
    ///   - messageHandlers: JS→Swift 消息通道（如 externalLink）
    static func makeConfiguration(
        userScripts: [WKUserScript] = [],
        messageHandlers: [(name: String, handler: ScriptMessageHandler)] = []
    ) -> WKWebViewConfiguration {
        let config = WKWebViewConfiguration()
        let controller = WKUserContentController()

        for script in userScripts {
            controller.addUserScript(script)
        }
        for (name, handler) in messageHandlers {
            controller.add(handler, name: name)
        }

        config.userContentController = controller
        return config
    }

    /// 便捷方法：注入一个全局字典对象 + 注册 message handlers。
    /// - Parameters:
    ///   - bridgeObjectName: 全局对象名，注入 `window.<name> = { key: value, ... }`
    ///   - dictionary: 桥接字典（如 baseURL / platform）
    ///   - messageHandlers: 消息通道
    static func makeConfiguration(
        bridgeObjectName: String,
        dictionary: [String: String],
        messageHandlers: [(name: String, handler: ScriptMessageHandler)] = []
    ) -> WKWebViewConfiguration {
        let pairs = dictionary
            .map { "\($0.key): '\(escaped($0.value))'" }
            .joined(separator: ", ")
        let scriptSource = "window.\(bridgeObjectName) = { \(pairs) };"
        let script = WKUserScript(source: scriptSource,
                                  injectionTime: .atDocumentStart,
                                  forMainFrameOnly: true)
        return makeConfiguration(userScripts: [script], messageHandlers: messageHandlers)
    }

    private static func escaped(_ s: String) -> String {
        s.replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "'", with: "\\'")
            .replacingOccurrences(of: "\n", with: "\\n")
    }
}
