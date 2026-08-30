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

    /// 确认条人话摘要（比 describe 更丰富：带值、元素类型、来源，供用户核验）。
    /// 值会显示但密码字段脱敏——确认条是验证闸门，需要让用户看清 agent 要做什么；
    /// 活动条（describe）故意不显示值，两者区分开。
    func confirmationBody(toolName: String, arguments: [String: Any]) -> String {
        let tool = toolName.components(separatedBy: "__").last ?? toolName
        let arg = { (key: String) -> String in
            (arguments[key] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        }
        let num = { (key: String) -> String in
            if let n = arguments[key] as? NSNumber { return n.stringValue }
            return (arguments[key] as? String) ?? ""
        }
        var lines: [String] = []

        // @eN → 元素元数据（snapshotItem 按稳定 id 反查）；非 ref 返回 nil
        let itemFor = { (ref: String) -> [String: Any]? in
            guard ref.hasPrefix("@e") else { return nil }
            return BrowserToolProvider.shared.snapshotItem(forRef: ref)
        }
        // 元素目标描述：「文本」(tag type @eN) → href
        let elementLabel = { (ref: String, selector: String) -> String in
            let ref = ref.trimmingCharacters(in: .whitespaces)
            if !ref.isEmpty, let item = itemFor(ref) {
                let text = (item["text"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                let tag = item["tag"] as? String ?? ""
                let type = item["type"] as? String ?? ""
                var label = "「\(text.isEmpty ? "(无文本)" : String(text.prefix(40)))」(\(tag)\(type.isEmpty ? "" : " \(type)") \(ref))"
                if let href = item["href"] as? String, !href.isEmpty {
                    label += " → \(String(href.prefix(80)))"
                }
                return label
            }
            return selector.isEmpty ? (ref.isEmpty ? "(未知元素)" : ref) : selector
        }
        let isPasswordField = { (ref: String) -> Bool in
            guard let item = itemFor(ref) else { return false }
            return (item["type"] as? String)?.lowercased() == "password"
        }
        let valueLine = { (fieldIsPassword: Bool, value: String) -> String in
            if fieldIsPassword { return "值: ••••••（密码已隐藏）" }
            let v = value.trimmingCharacters(in: .whitespacesAndNewlines)
            return v.isEmpty ? "" : "值: \"\(String(v.prefix(60)))\""
        }

        switch tool {
        case "agent_browser_open":
            let url = arg("url")
            lines.append("目标: \(url.isEmpty ? "(空)" : url)")
        case "agent_browser_close":
            lines.append("将关闭所有标签页")
        case "agent_browser_close_tab", "agent_browser_switch_tab":
            lines.append("目标: 标签页 \(arg("tabId"))")
        case "agent_browser_click", "agent_browser_cgclick":
            lines.append("目标: \(elementLabel(arg("ref"), arg("selector")))")
            if tool == "agent_browser_cgclick" {
                lines.append("⚠ 真实系统级点击")
            }
        case "agent_browser_click_text":
            let t = arg("text")
            lines.append("目标: \(t.isEmpty ? "(无文本)" : "「\(t)」")")
        case "agent_browser_drag":
            lines.append("目标: \(elementLabel(arg("ref"), ""))")
            lines.append("位移: dx=\(num("dx")) dy=\(num("dy"))")
        case "agent_browser_fill", "agent_browser_type":
            let value = tool == "agent_browser_fill" ? arg("value") : arg("text")
            lines.append("目标: \(elementLabel(arg("ref"), arg("selector")))")
            if !value.isEmpty {
                lines.append(valueLine(isPasswordField(arg("ref")), value))
            }
        case "agent_browser_select":
            lines.append("目标: \(elementLabel(arg("ref"), arg("selector")))")
            let v = arg("value")
            if !v.isEmpty { lines.append("选择: \"\(v)\"") }
        case "agent_browser_check":
            lines.append("目标: \(elementLabel(arg("ref"), arg("selector")))")
            lines.append("动作: 勾选")
        case "agent_browser_uncheck":
            lines.append("目标: \(elementLabel(arg("ref"), arg("selector")))")
            lines.append("动作: 取消勾选")
        case "agent_browser_press":
            lines.append("按键: \(arg("key"))")
        case "agent_browser_scroll":
            lines.append("滚动: x=\(num("x")) y=\(num("y"))")
        case "agent_browser_read":
            lines.append("模式: \(arg("mode").isEmpty ? "full" : arg("mode"))")
        case "agent_browser_get_text", "agent_browser_get_value":
            lines.append("目标: \(elementLabel(arg("ref"), arg("selector")))")
        case "agent_browser_download":
            let name = arg("filename")
            let url = arg("url")
            lines.append("目标: \(name.isEmpty ? url : name)")
            if !url.isEmpty {
                lines.append("来源: \(url)")
                if let u = URL(string: url), let dlHost = u.host, dlHost != BrowserToolProvider.shared.activeURL?.host {
                    lines.append("⚠ 跨域下载: \(dlHost)")
                }
            }
        default:
            // 兜底：只回显非敏感参数（过滤 value/text/password）
            let safe = arguments.filter { !["value", "text", "password"].contains($0.key) }
            if !safe.isEmpty {
                lines.append(String(safe.map { "\($0.key)=\($0.value)" }.joined(separator: ", ").prefix(80)))
            }
        }

        // 用户标签写入授权标注（agent 操作你自己的标签，需明确允许本任务继续）
        if (arguments["__user_tab_grant"] as? Bool) == true {
            lines.append("⚠ 作用于你自己的标签页，允许后本任务内继续操作无需再确认")
        }

        // 敏感/明文页升级的 L3 标注（解释"为什么这次要确认"）
        if BrowserToolProvider.shared.shouldElevateToL3(flatName: toolName, arguments: arguments) {
            lines.append("⚠ 当前为敏感/明文页，操作需人工确认")
        }
        return lines.joined(separator: "\n")
    }
}
