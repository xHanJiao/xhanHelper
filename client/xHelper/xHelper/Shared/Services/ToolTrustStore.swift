import Foundation
import Combine

/// 「完全信任」全局开关（持久化，默认关）：跳过客户端工具的静态风险确认（含 fs 写、shell 写/执行）。
///
/// 不跳过：防注入升级的确认（`forcedL3`，高敏域 / 明文页 / 用户标签授权）与硬拦截
/// （命令白名单 / 高危路径 / 敏感文件名 / 未授权目录）——见 `ToolConfirmPolicy`。
///
/// 单例：主聊天、PiP 两个 `ChatView` 与执行侧（`ChatViewModel+ClientTools`）共用同一份状态。
@MainActor
final class ToolTrustStore: ObservableObject {
    static let shared = ToolTrustStore()

    private static let key = "xhan_full_trust"

    @Published var fullTrust: Bool {
        didSet { UserDefaults.standard.set(fullTrust, forKey: Self.key) }
    }

    private init() {
        fullTrust = UserDefaults.standard.bool(forKey: Self.key)
    }
}
