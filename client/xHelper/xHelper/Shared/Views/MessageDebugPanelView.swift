import SwiftUI
import WebKit

#if os(macOS)
import AppKit
#endif

/// 消息调试面板 sheet 的 item 包装（Identifiable，id 即消息 ID）
struct MessageDebugTarget: Identifiable {
    let id: String
}

/// 消息调试面板 sheet：内嵌前端 /admin/message-debug/:id 页面。
/// 复用监控看板同款 XH_BRIDGE token 注入（免登录）+ 外链转发系统浏览器。
struct MessageDebugPanelView: View {
    @StateObject private var webModel = XHWebViewModel()
    let baseURL: String
    let token: String
    let messageID: String
    let onClose: () -> Void

    private var url: URL? {
        URL(string: DeviceManager.webBaseURL + "/admin/message-debug/\(messageID)")
    }

    private var configuration: WKWebViewConfiguration {
        let externalLink = XHWebViewBridge.ScriptMessageHandler { body in
            // 仅接受 dict 形式 {url: "..."}；scheme 白名单 http/https + 长度上限
            guard let urlString = (body as? [String: Any])?["url"] as? String,
                  urlString.count <= 4096,
                  let url = URL(string: urlString),
                  let scheme = url.scheme?.lowercased(),
                  scheme == "http" || scheme == "https" else { return }
            #if os(macOS)
            NSWorkspace.shared.open(url)
            #endif
        }
        return XHWebViewBridge.makeConfiguration(
            bridgeObjectName: "XH_BRIDGE",
            dictionary: [
                "baseURL": baseURL,
                "platform": "macOS",
                "token": token,
            ],
            messageHandlers: [("externalLink", externalLink)]
        )
    }

    /// 导航白名单：仅允许前端所在同源（含本地内嵌 127.0.0.1:<LocalPaths.backendPort>，与监控看板一致）
    private var allowedOrigins: [XHWebViewOrigin] {
        XHWebViewOrigin.frontendOrigins(webBaseURL: DeviceManager.webBaseURL)
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("消息调试")
                    .font(.headline)
                Spacer()
                Button(action: onClose) {
                    Image(systemName: "xmark.circle.fill")
                        .font(.system(size: 16))
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 10)

            Divider()

            if let url {
                XHWebViewPanel(model: webModel, url: url, configuration: configuration, allowedOrigins: allowedOrigins)
            }
        }
        .frame(minWidth: 700, idealWidth: 900, minHeight: 500, idealHeight: 720)
    }
}
