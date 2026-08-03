import Combine
import Foundation

/// agent 操作浏览器的可见性中心（G2）：在 `handleClientToolCall` 咽喉点广播浏览器工具的
/// 执行动作，供浏览器内容区顶部细条指示器展示。仅反映浏览器 namespace 的工具。
/// 文案不展示敏感值（fill/type 只显示目标占位符，不显示输入内容）。
@MainActor
final class BrowserActivityCenter: ObservableObject {
    static let shared = BrowserActivityCenter()

    struct Activity {
        var verb: String
        var target: String
        var state: State

        enum State {
            case running
            case done
        }
    }

    /// nil = 空闲（指示器隐藏）
    @Published private(set) var activity: Activity?

    /// 状态 C：待用户确认的浏览器 L3 请求（浏览器内同步确认条）。nil = 无待确认。
    @Published private(set) var pendingConfirmation: PendingBrowserConfirm?

    /// 浏览器 L3 确认请求（由 awaitToolConfirmation 桥接发布，onResolve 走 resolveToolConfirmation）
    struct PendingBrowserConfirm {
        let displayName: String
        let argsSummary: String
        let onResolve: (Bool) -> Void
    }

    private var dismissTask: Task<Void, Never>?

    private init() {}

    /// 发布浏览器确认请求（awaitToolConfirmation 内部调用）。
    /// 确认条出现时清掉残留的活动条（避免"确认栏 + 提示条"同时出现）。
    func presentConfirmation(displayName: String, argsSummary: String, onResolve: @escaping (Bool) -> Void) {
        dismissTask?.cancel()
        dismissTask = nil
        activity = nil
        pendingConfirmation = PendingBrowserConfirm(displayName: displayName, argsSummary: argsSummary, onResolve: onResolve)
    }

    /// 清除浏览器确认请求（确认被解决或超时时调用）
    func dismissConfirmation() {
        pendingConfirmation = nil
    }

    /// 浏览器工具开始执行（执行前调用）
    func start(flatName: String, arguments: [String: Any]) {
        let desc = describe(flatName: flatName, arguments: arguments)
        dismissTask?.cancel()
        activity = Activity(verb: desc.verb, target: desc.target, state: .running)
    }

    /// 浏览器工具执行结束（成功/失败都算），短暂停留后消退
    func finish() {
        guard var a = activity else { return }
        a.state = .done
        activity = a
        dismissTask = Task { @MainActor [weak self] in
            try? await Task.sleep(for: .seconds(2.5))
            self?.activity = nil
        }
    }

    /// 工具执行失败时立即消退（避免"运行中"误导）
    func clear() {
        dismissTask?.cancel()
        dismissTask = nil
        activity = nil
    }

    /// 把工具名 + 参数映射为「动作 + 目标」展示文案（短，脱敏）
    private func describe(flatName: String, arguments: [String: Any]) -> (verb: String, target: String) {
        let tool = flatName.components(separatedBy: "__").last ?? flatName
        let arg = { (key: String) -> String in
            (arguments[key] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        }
        // 目标文案不截断，尽量展示全（指示器可换行，短暂显示）
        let target = { (t: String) -> String in
            t
        }
        // @eN ref → 上次 snapshot 的元素文本（"点击「登录」"而非"点击 @e13"）；解析不到回退原样
        let resolved = { (locator: String) -> String in
            if locator.hasPrefix("@e"), let t = BrowserToolProvider.shared.snapshotText(forRef: locator) {
                return "「\(t)」"
            }
            return locator
        }
        switch tool {
        case "agent_browser_open":
            return ("打开", target(arg("url")))
        case "agent_browser_close":
            return ("关闭", "所有标签页")
        case "agent_browser_close_tab":
            return ("关闭标签页", target(arg("tabId")))
        case "agent_browser_switch_tab":
            return ("切换标签页", target(arg("tabId")))
        case "agent_browser_click":
            let sel = arg("selector")
            return ("点击", target(sel.isEmpty ? resolved(arg("ref")) : sel))
        case "agent_browser_click_text":
            let t = arg("text")
            return ("点击", target(t.isEmpty ? "" : "「\(t)」"))
        case "agent_browser_cgclick":
            return ("真实点击", target(resolved(arg("ref"))))
        case "agent_browser_drag":
            return ("拖拽", "页面元素")
        case "agent_browser_fill":
            // 只显示目标，不显示填入内容（脱敏）
            let sel = arg("selector")
            return ("填写", target(sel.isEmpty ? resolved(arg("ref")) : sel))
        case "agent_browser_type":
            let sel = arg("selector")
            return ("输入", target(sel.isEmpty ? resolved(arg("ref")) : sel))
        case "agent_browser_select":
            let sel = arg("selector")
            return ("选择", target(sel.isEmpty ? resolved(arg("ref")) : sel))
        case "agent_browser_check":
            let sel = arg("selector")
            return ("勾选", target(sel.isEmpty ? resolved(arg("ref")) : sel))
        case "agent_browser_uncheck":
            let sel = arg("selector")
            return ("取消勾选", target(sel.isEmpty ? resolved(arg("ref")) : sel))
        case "agent_browser_press":
            return ("按键", target(arg("key")))
        case "agent_browser_scroll":
            return ("滚动", "页面")
        case "agent_browser_snapshot", "agent_browser_read", "agent_browser_get_text", "agent_browser_get_value", "agent_browser_get_url", "agent_browser_get_title":
            return ("读取", "页面")
        case "agent_browser_download":
            return ("下载", target(arg("filename").isEmpty ? arg("url") : arg("filename")))
        default:
            return ("操作", "浏览器")
        }
    }
}
