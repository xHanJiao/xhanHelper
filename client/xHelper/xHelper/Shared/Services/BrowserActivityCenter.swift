import Combine
import Foundation

/// 浏览器确认条的内容中心：为浏览器内同步确认条提供「待确认请求」状态，并为聊天栏 L3
/// 确认条提供人话摘要（confirmationBody）。确认来源为浏览器内部确认（有下载进行中时关标签、
/// 用户点链接触发下载）。文案不展示敏感值（密码字段脱敏）。
@MainActor
final class BrowserActivityCenter: ObservableObject {
    static let shared = BrowserActivityCenter()

    /// 待用户确认的浏览器请求（浏览器内同步确认条）。nil = 无待确认。
    @Published private(set) var pendingConfirmation: PendingBrowserConfirm?

    /// 浏览器确认请求（onResolve 决定放行/拒绝）
    struct PendingBrowserConfirm {
        let displayName: String
        let argsSummary: String
        let onResolve: (Bool) -> Void
    }

    private init() {}

    /// 发布浏览器确认请求。
    func presentConfirmation(displayName: String, argsSummary: String, onResolve: @escaping (Bool) -> Void) {
        pendingConfirmation = PendingBrowserConfirm(displayName: displayName, argsSummary: argsSummary, onResolve: onResolve)
    }

    /// 清除浏览器确认请求（确认被解决或超时时调用）
    func dismissConfirmation() {
        pendingConfirmation = nil
    }

    /// 确认条人话摘要（带值、元素类型、来源，供用户核验）。
    /// 值会显示但密码字段脱敏——确认条是验证闸门，需要让用户看清 agent 要做什么。
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
