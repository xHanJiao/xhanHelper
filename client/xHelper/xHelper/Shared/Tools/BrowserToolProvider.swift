import WebKit
import Foundation
import SwiftUI

@MainActor
final class BrowserToolProvider: NSObject, LocalToolProvider {
    static let shared = BrowserToolProvider()
    let namespace = "browser"
    weak var rightSidebar: RightSidebarManager?

    private var tabs: [String: TabSession] = [:]
    private var activeTabId: String?
    private var tabCounter = 0
    private let maxResultChars: Int

    init(maxResultChars: Int = 10_000) {
        self.maxResultChars = maxResultChars
        super.init()
    }

    // MARK: - LocalToolProvider

    func toolSchemas() async -> [LocalToolSchema] {
        var schemas: [LocalToolSchema] = [
            LocalToolSchema(name: "agent_browser_open",             description: "打开指定 URL",                          inputSchema: objProps(["url": strProp("要打开的网址")])),
            LocalToolSchema(name: "agent_browser_close",            description: "关闭所有浏览器标签页",                       inputSchema: objProps([:])),
            LocalToolSchema(name: "agent_browser_list_tabs",        description: "列出当前所有打开的标签页（id/url/title）",      inputSchema: objProps([:])),
            LocalToolSchema(name: "agent_browser_switch_tab",       description: "切换到指定标签页",                        inputSchema: objProps(["tabId": strProp("tab的id，从list_tabs获取")])),
            LocalToolSchema(name: "agent_browser_read",             description: "读取页面文本。分层用法：outline=标题树（长文导航，省 token）；filter=按关键词返回匹配章节；full=全文（最后手段，很耗 token）。建议先 snapshot → read(outline) → read(filter)/get_text(@eN) 定位 → 确需通读才 read(full)", inputSchema: objProps([
                "mode": strProp("full（默认，全文）| outline（h1-h6 标题树）| filter（按关键词返回匹配章节）"),
                "query": strProp("filter 模式的关键词"),
                "max_items": numProp("outline 标题数上限，默认 50"),
                "max_chars": numProp("filter 每块截断 / full 全文截断字符数，默认 filter 2000 / full 10000")
            ])),
            LocalToolSchema(name: "agent_browser_snapshot",         description: "获取页面可访问性快照（紧凑元素树，元素带 @eN 编号可用于 ref 交互；页面变化后编号会失效需重新快照）", inputSchema: objProps([
                "mode": strProp("interactive（默认，只列可交互元素与标题）| full（追加页面可见文本前800字）"),
                "include_href": strProp("是否输出链接 href，默认 true"),
                "max_items": numProp("元素数量上限，默认 60")
            ])),
            LocalToolSchema(name: "agent_browser_get_text",         description: "获取页面/元素可见文本。元素级（ref/selector）远省 token；无参数返回全量", inputSchema: objProps([
                "ref": strProp("元素编号，如 @e8，只读该元素文本"),
                "selector": strProp("CSS选择器，只读该区域文本"),
                "max_chars": numProp("元素级截断字符数，默认 2000")
            ])),
            LocalToolSchema(name: "agent_browser_get_value",        description: "读取表单元素当前值（fill/type 后回读验证、select 选中项、textarea 内容；checkbox/radio 返回 checked 状态）", inputSchema: objProps([
                "ref": strProp("元素编号，如 @e5"),
                "selector": strProp("CSS选择器（ref 缺失时使用）")
            ])),
            LocalToolSchema(name: "agent_browser_get_url",          description: "获取当前页面 URL",                      inputSchema: objProps([:])),
            LocalToolSchema(name: "agent_browser_get_title",        description: "获取当前页面标题",                       inputSchema: objProps([:])),
            LocalToolSchema(name: "agent_browser_click",            description: "点击元素（ref 优先，如 @e5；无 ref 用 CSS 选择器）", inputSchema: objProps(["ref": strProp("元素编号，如 @e5，从 snapshot 获取"), "selector": strProp("CSS选择器（ref 缺失时使用）")])),
            LocalToolSchema(name: "agent_browser_click_text",       description: "按文本点击元素（模糊匹配 snapshot 中元素的文本/占位符，多个匹配用 index 指定）", inputSchema: objProps(["text": strProp("要点击的文本或关键词"), "index": numProp("多个匹配时指定第几个，默认 0")])),
            LocalToolSchema(name: "agent_browser_cgclick",         description: "真实系统级点击（兜底）：仅当 click/click_text 对目标无效（页面校验真实输入事件）时使用。执行时会前置激活 xhanHelper 窗口（可能打断用户当前操作），且需要辅助功能权限（未授权会弹窗引导）", inputSchema: objProps(["ref": strProp("元素编号，如 @e5，从 snapshot 获取"), "x": numProp("直接指定 viewport 坐标 x（调试用，与 ref 二选一）"), "y": numProp("直接指定 viewport 坐标 y（调试用，与 ref 二选一）")])),
            LocalToolSchema(name: "agent_browser_drag",            description: "真实拖拽（地图/滑块/画布，系统级事件，需辅助功能权限）：从起点按下拖动到终点。起点可用 ref（如地图 canvas 的 @eN）或 x/y", inputSchema: objProps(["ref": strProp("起点元素编号，如 @e12（地图 canvas），与 x/y 二选一"), "x": numProp("起点 viewport x（无 ref 时使用）"), "y": numProp("起点 viewport y（无 ref 时使用）"), "dx": numProp("水平位移量（viewport 像素）"), "dy": numProp("垂直位移量（viewport 像素）"), "steps": numProp("拖动步数，默认 10")])),
            LocalToolSchema(name: "agent_browser_fill",             description: "填充表单字段",                         inputSchema: objProps(["ref": strProp("元素编号，如 @e3"), "selector": strProp("CSS选择器（ref 缺失时使用）"), "value": strProp("填充值")])),
            LocalToolSchema(name: "agent_browser_type",             description: "在元素中逐字符输入文本（追加语义，如需覆盖请先用 fill 清空）", inputSchema: objProps(["ref": strProp("元素编号，如 @e3"), "selector": strProp("CSS选择器（ref 缺失时使用）"), "text": strProp("输入文本")])),
            LocalToolSchema(name: "agent_browser_press",            description: "按下键盘按键",                         inputSchema: objProps(["key": strProp("按键名称")])),
            LocalToolSchema(name: "agent_browser_select",           description: "选择下拉选项",                         inputSchema: objProps(["ref": strProp("元素编号，如 @e6"), "selector": strProp("CSS选择器（ref 缺失时使用）"), "value": strProp("选项值")])),
            LocalToolSchema(name: "agent_browser_check",            description: "勾选复选框",                           inputSchema: objProps(["ref": strProp("元素编号，如 @e5"), "selector": strProp("CSS选择器（ref 缺失时使用）")])),
            LocalToolSchema(name: "agent_browser_uncheck",          description: "取消勾选复选框",                         inputSchema: objProps(["ref": strProp("元素编号，如 @e5"), "selector": strProp("CSS选择器（ref 缺失时使用）")])),
            LocalToolSchema(name: "agent_browser_scroll",           description: "滚动页面（x/y 像素）",                   inputSchema: objProps(["x": intProp, "y": intProp])),
            LocalToolSchema(name: "agent_browser_wait_for_load",    description: "等待页面加载完成",                       inputSchema: objProps(["timeout_ms": numProp("超时毫秒，默认10000")])),
            LocalToolSchema(name: "agent_browser_wait_for_selector", description: "等待指定选择器出现",                   inputSchema: objProps(["selector": strProp("CSS选择器"), "timeout_ms": numProp("超时毫秒，默认5000")])),
            LocalToolSchema(name: "agent_browser_go_back",          description: "浏览器后退到上一页",                         inputSchema: objProps([:])),
            LocalToolSchema(name: "agent_browser_go_forward",       description: "浏览器前进到下一页（有页面可前进时生效）",          inputSchema: objProps([:])),
        ]
#if DEBUG
        schemas.append(LocalToolSchema(name: "agent_browser_execute_script", description: "执行 JS 脚本并返回结果（仅开发环境）", inputSchema: objProps(["script": strProp("要执行的JS代码，用return返回结果，如 return JSON.stringify({w:window.innerWidth})")])))
#endif
        return schemas
    }

    func execute(tool: String, arguments: [String: Any], context: ExecuteContext) async throws -> LocalToolResult {
        switch tool {
        case "agent_browser_open":
            return await handleOpen(arguments)
        case "agent_browser_close":
            return handleClose()
        case "agent_browser_list_tabs":
            return handleListTabs()
        case "agent_browser_switch_tab":
            return handleSwitchTab(arguments)
        default:
            guard let session = activeSession else {
                return .init(serverResult: nil, serverError: "没有打开的页面", clientAction: nil)
            }
            switch tool {
            case "agent_browser_get_url":
                guard session.webView.url != nil else {
                    return .init(serverResult: nil, serverError: "页面未加载", clientAction: nil)
                }
                return .init(serverResult: Self.jsonDict(["url": session.webView.url?.absoluteString ?? ""]), serverError: nil, clientAction: nil)
            case "agent_browser_get_title":
                guard session.webView.url != nil else {
                    return .init(serverResult: nil, serverError: "页面未加载", clientAction: nil)
                }
                return .init(serverResult: Self.jsonDict(["title": session.webView.title ?? ""]), serverError: nil, clientAction: nil)
            case "agent_browser_read":
                return await handleRead(session: session, arguments: arguments)
            case "agent_browser_get_text":
                return await handleGetText(session: session, arguments: arguments)
            case "agent_browser_get_value":
                return await handleGetValue(session: session, arguments: arguments)
            case "agent_browser_snapshot":
                return await handleSnapshot(session: session, arguments: arguments)
            case "agent_browser_scroll":
                return await withNavContext(session) {
                    let x = (arguments["x"] as? Int) ?? 0
                    let y = (arguments["y"] as? Int) ?? 0
                    _ = await session.jsResult("window.scrollBy(\(x), \(y))")
                    return .init(serverResult: Self.jsonDict(["scrolled": true]), serverError: nil, clientAction: nil)
                }
            case "agent_browser_wait_for_load":
                return await withNavContext(session) {
                    let timeout = (arguments["timeout_ms"] as? Int) ?? 10000
                    return await session.waitForLoad(timeoutMs: timeout)
                }
            case "agent_browser_wait_for_selector":
                return await withNavContext(session) {
                    let sel = arguments["selector"] as? String ?? ""
                    let timeout = (arguments["timeout_ms"] as? Int) ?? 5000
                    return await waitForSelector(session: session, selector: sel, timeoutMs: timeout)
                }
            case "agent_browser_go_back":
                return await withNavContext(session) {
                    session.webView.goBack()
                    return .init(serverResult: Self.jsonDict(["back": true]), serverError: nil, clientAction: nil)
                }
            case "agent_browser_go_forward":
                return await withNavContext(session) {
                    if session.webView.canGoForward { session.webView.goForward() }
                    return .init(serverResult: Self.jsonDict(["forward": session.webView.canGoForward]), serverError: nil, clientAction: nil)
                }
#if DEBUG
            case "agent_browser_execute_script":
                let script = arguments["script"] as? String ?? ""
                return await session.eval(script)
#endif
            case "agent_browser_click":
                return await withNavContext(session) {
                    await interact(session: session, arguments: arguments, kind: .click, successKey: "clicked")
                }
            case "agent_browser_cgclick":
                return await withNavContext(session) {
                    await cgClick(session: session, arguments: arguments)
                }
            case "agent_browser_drag":
                return await withNavContext(session) {
                    await cgDrag(session: session, arguments: arguments)
                }
            case "agent_browser_click_text":
                return await withNavContext(session) {
                    let text = arguments["text"] as? String ?? ""
                    let index = (arguments["index"] as? NSNumber)?.intValue ?? 0
                    let result = await session.jsJSON(clickTextDownJS(text, index))
                    guard let dict = result as? [String: Any] else {
                        return .init(serverResult: nil, serverError: "点击失败：JS 异常", clientAction: nil)
                    }
                    if let ok = dict["ok"] as? Bool, ok {
                        try? await Task.sleep(for: .milliseconds(Int.random(in: 50...150)))
                        _ = await session.jsBool(refClickUpJS())
                        return .init(serverResult: Self.jsonDict(["clicked": true, "text": dict["text"] ?? ""]), serverError: nil, clientAction: nil)
                    }
                    return .init(serverResult: nil, serverError: dict["err"] as? String ?? "点击失败", clientAction: nil)
                }
            case "agent_browser_fill":
                return await withNavContext(session) {
                    await interact(session: session, arguments: arguments, kind: .fill, successKey: "filled")
                }
            case "agent_browser_type":
                return await withNavContext(session) {
                    await interact(session: session, arguments: arguments, kind: .type, successKey: "typed")
                }
            case "agent_browser_press":
                return await withNavContext(session) {
                    let key = arguments["key"] as? String ?? ""
                    // 特殊键（Enter/方向键/Tab/Escape 等）走真实键盘事件，触发浏览器默认行为
                    if let code = CGEventInput.keyCode(for: key) {
                        print("[B-Press] key=\(key) vk=\(code)")
                        CGEventInput.activateWindow(for: session.webView)
                        session.webView.window?.makeFirstResponder(session.webView)
                        CGEventInput.pressKey(code)
                        return .init(serverResult: Self.jsonDict(["pressed": true, "real": true]), serverError: nil, clientAction: nil)
                    }
                    let (_, jsErr) = await session.jsBool(pressJS(key))
                    if let e = jsErr {
                        return .init(serverResult: nil, serverError: "press失败: \(e)", clientAction: nil)
                    }
                    return .init(serverResult: Self.jsonDict(["pressed": true]), serverError: nil, clientAction: nil)
                }
            case "agent_browser_select":
                return await withNavContext(session) {
                    await interact(session: session, arguments: arguments, kind: .select, successKey: "selected")
                }
            case "agent_browser_check", "agent_browser_uncheck":
                return await withNavContext(session) {
                    let checked = tool == "agent_browser_check"
                    return await interactChecked(session: session, arguments: arguments, checked: checked)
                }
            default:
                return .init(serverResult: nil, serverError: "unknown tool: \(tool)", clientAction: nil)
            }
        }
    }

    // MARK: - Interaction helpers

    private enum InteractKind {
        case click, fill, type, select
    }

    /// ref（@eN）解析为 collect 数组索引；格式不合法返回 nil
    private func refIndex(_ ref: String) -> Int? {
        let s = ref.trimmingCharacters(in: .whitespaces)
        guard s.hasPrefix("@") else { return nil }
        let body = s.dropFirst().lowercased().hasPrefix("e") ? String(s.dropFirst().dropFirst()) : String(s.dropFirst())
        guard let n = Int(body), n >= 1 else { return nil }
        return n - 1
    }

    /// ref 特征校验：将当前 DOM 第 N 个元素的特征与快照时记录的签名对比，
    /// 不一致（页面变化导致编号错位）时返回错误提示，要求重新 snapshot。
    /// 对比规则：tag/href/type/id/name 必比；text 仅非表单元素比（input value 动态，不误拦）。
    private func refMismatchError(session: TabSession, index: Int) async -> String? {
        guard let items = session.lastSnapshotItems else {
            return "请先 snapshot 获取 @eN 编号"
        }
        guard index < items.count else {
            return "ref @e\(index + 1) 超出快照范围，请重新 snapshot"
        }
        guard let probe = await session.jsJSON(refProbeJS(index)) as? [String: Any],
              let ok = probe["ok"] as? Bool, ok else {
            return "ref @e\(index + 1) 不存在，请重新 snapshot"
        }
        let expected = items[index]
        let expTag = expected["tag"] as? String ?? ""
        let tag = probe["tag"] as? String ?? ""
        if tag != expTag {
            return "页面内容已变化（@e\(index + 1) 由 [\(expTag)] 变为 [\(tag)]），请重新 snapshot 获取最新编号"
        }
        let expHref = expected["href"] as? String ?? ""
        let href = probe["href"] as? String ?? ""
        if href != expHref {
            return "页面内容已变化（@e\(index + 1) 链接目标不同），请重新 snapshot 获取最新编号"
        }
        let expType = expected["type"] as? String ?? ""
        let type = probe["type"] as? String ?? ""
        if type != expType {
            return "页面内容已变化（@e\(index + 1) 类型由 \(expType) 变为 \(type)），请重新 snapshot 获取最新编号"
        }
        let expId = expected["id"] as? String ?? ""
        let id = probe["id"] as? String ?? ""
        let expName = expected["name"] as? String ?? ""
        let name = probe["name"] as? String ?? ""
        if id != expId || name != expName {
            return "页面内容已变化（@e\(index + 1) 表单标识不同），请重新 snapshot 获取最新编号"
        }
        let formTags = ["input", "select", "textarea"]
        if !formTags.contains(expTag) {
            let expText = expected["text"] as? String ?? ""
            let text = probe["text"] as? String ?? ""
            if text != expText {
                return "页面内容已变化（@e\(index + 1) 文本不一致），请重新 snapshot 获取最新编号"
            }
        }
        return nil
    }

    /// 交互统一入口：ref 优先，无 ref 退回 selector（向后兼容）
    private func interact(session: TabSession, arguments: [String: Any], kind: InteractKind, successKey: String) async -> LocalToolResult {
        if kind == .type {
            return await typeText(session: session, arguments: arguments, successKey: successKey)
        }
        if let idx = refIndex(arguments["ref"] as? String ?? "") {
            // ref 编号基于快照，页面变化后先拦截提示重新 snapshot
            if let stale = await refMismatchError(session: session, index: idx) {
                return .init(serverResult: nil, serverError: stale, clientAction: nil)
            }
        }
        if let idx = refIndex(arguments["ref"] as? String ?? "") {
            switch kind {
            case .click:
                let result = await session.jsJSON(refClickDownJS(idx))
                guard let dict = result as? [String: Any], let ok = dict["ok"] as? Bool, ok else {
                    return .init(serverResult: nil, serverError: dictErr(dict: result, fallback: "ref @e\(idx + 1) 点击失败，请重新 snapshot"), clientAction: nil)
                }
                let px = (dict["x"] as? NSNumber)?.intValue ?? -1
                let py = (dict["y"] as? NSNumber)?.intValue ?? -1
                let delay = Int.random(in: 50...150)
                print("[B-Click] ref=@e\(idx + 1) pos=(\(px),\(py)) release_delay=\(delay)ms")
                try? await Task.sleep(for: .milliseconds(delay))
                _ = await session.jsBool(refClickUpJS())
                return .init(serverResult: Self.jsonDict([successKey: true]), serverError: nil, clientAction: nil)
            case .fill:
                let js = refFillJS(idx, arguments["value"] as? String ?? "")
                return await interpretRefResult(await session.jsJSON(js), successKey: successKey)
            case .type:
                return await typeText(session: session, arguments: arguments, successKey: successKey)
            case .select:
                let js = refSelectJS(idx, arguments["value"] as? String ?? "")
                return await interpretRefResult(await session.jsJSON(js), successKey: successKey)
            }
        }
        let sel = arguments["selector"] as? String ?? ""
        switch kind {
        case .click:
            let result = await session.jsJSON(clickDownJS(sel))
            guard let dict = result as? [String: Any], let ok = dict["ok"] as? Bool, ok else {
                return .init(serverResult: nil, serverError: "未找到元素: \(sel)", clientAction: nil)
            }
            let px = (dict["x"] as? NSNumber)?.intValue ?? -1
            let py = (dict["y"] as? NSNumber)?.intValue ?? -1
            let delay = Int.random(in: 50...150)
            print("[B-Click] sel=\(sel) pos=(\(px),\(py)) release_delay=\(delay)ms")
            try? await Task.sleep(for: .milliseconds(delay))
            _ = await session.jsBool(clickUpJS())
            return .init(serverResult: Self.jsonDict([successKey: true]), serverError: nil, clientAction: nil)
        case .fill:
            let (ok, jsErr) = await session.jsBool(fillJS(sel, arguments["value"] as? String ?? ""))
            return ok ? .init(serverResult: Self.jsonDict([successKey: true]), serverError: nil, clientAction: nil)
                      : .init(serverResult: nil, serverError: jsErr ?? "未找到元素: \(sel)", clientAction: nil)
        case .type:
            return await typeText(session: session, arguments: arguments, successKey: successKey)
        case .select:
            let (ok, jsErr) = await session.jsBool(selectJS(sel, arguments["value"] as? String ?? ""))
            return ok ? .init(serverResult: Self.jsonDict([successKey: true]), serverError: nil, clientAction: nil)
                      : .init(serverResult: nil, serverError: jsErr ?? "未找到元素: \(sel)", clientAction: nil)
        }
    }

    /// 逐字符输入（拟人化：每字符随机 40-200ms 击键间隔）。
    /// 元素先打 data-xh-type 标记，避免每次字符重新收集定位。
    private func typeText(session: TabSession, arguments: [String: Any], successKey: String) async -> LocalToolResult {
        let text = arguments["text"] as? String ?? ""
        guard !text.isEmpty else {
            return .init(serverResult: nil, serverError: "type 需要 text", clientAction: nil)
        }
        if let idx = refIndex(arguments["ref"] as? String ?? "") {
            if let stale = await refMismatchError(session: session, index: idx) {
                return .init(serverResult: nil, serverError: stale, clientAction: nil)
            }
        }
        let initOK: (ok: Bool, jsError: String?)
        if let idx = refIndex(arguments["ref"] as? String ?? "") {
            initOK = await session.jsBool(typeInitRefJS(idx))
        } else if let sel = arguments["selector"] as? String, !sel.isEmpty {
            initOK = await session.jsBool(typeInitSelJS(sel))
        } else {
            return .init(serverResult: nil, serverError: "type 需要 ref 或 selector", clientAction: nil)
        }
        guard initOK.ok else {
            return .init(serverResult: nil, serverError: initOK.jsError ?? "type 定位失败", clientAction: nil)
        }
        var intervals: [Int] = []
        for ch in text {
            let (ok, jsErr) = await session.jsBool(typeCharJS(String(ch)))
            guard ok else {
                _ = await session.jsBool(typeEndJS())
                return .init(serverResult: nil, serverError: jsErr ?? "type 输入失败", clientAction: nil)
            }
            let delay = Int.random(in: 40...200)
            intervals.append(delay)
            try? await Task.sleep(for: .milliseconds(delay))
        }
        // 回读 value 验证（拟人化诊断）
        if let peek = await session.jsJSON(typePeekValueJS()) as? [String: Any] {
            let value = peek["value"] as? String ?? ""
            print("[B-Type] len=\(text.count) intervals=[\(intervals.map(String.init).joined(separator: ","))] value=\"\(value)\"")
        }
        _ = await session.jsBool(typeEndJS())
        return .init(serverResult: Self.jsonDict([successKey: true]), serverError: nil, clientAction: nil)
    }

    private func interactChecked(session: TabSession, arguments: [String: Any], checked: Bool) async -> LocalToolResult {
        if let idx = refIndex(arguments["ref"] as? String ?? "") {
            return await interpretRefResult(await session.jsJSON(refCheckJS(idx, checked)), successKey: checked ? "checked" : "unchecked")
        }
        let sel = arguments["selector"] as? String ?? ""
        let (ok, jsErr) = await session.jsBool(checkJS(sel, checked))
        return ok ? .init(serverResult: Self.jsonDict([checked ? "checked" : "unchecked": true]), serverError: nil, clientAction: nil)
                  : .init(serverResult: nil, serverError: jsErr ?? "未找到元素: \(sel)", clientAction: nil)
    }

    /// 真实系统级点击（CGEvent 兜底）：权限检查 → 定位元素中心 → 坐标转换 → 前置窗口 → 注入
    private func cgClick(session: TabSession, arguments: [String: Any]) async -> LocalToolResult {
        guard CGEventInput.isTrusted() else {
            let granted = CGEventInput.requestPermission()
            print("[CGEvent] permission granted=\(granted)")
            return .init(serverResult: nil, serverError: "需要辅助功能权限：请在 系统设置→隐私与安全→辅助功能 勾选 xhanHelper 后重试（已授权仍报错请重启 App）", clientAction: nil)
        }

        // 定位目标点：ref（元素中心）或直接 x/y（调试）
        var viewportPoint: CGPoint?
        var targetDesc = ""
        if let idx = refIndex(arguments["ref"] as? String ?? "") {
            if let stale = await refMismatchError(session: session, index: idx) {
                return .init(serverResult: nil, serverError: stale, clientAction: nil)
            }
            let result = await session.jsJSON(refCenterJS(idx))
            guard let dict = result as? [String: Any],
                  let ok = dict["ok"] as? Bool, ok,
                  let x = (dict["x"] as? NSNumber)?.doubleValue,
                  let y = (dict["y"] as? NSNumber)?.doubleValue
            else {
                return .init(serverResult: nil, serverError: dictErr(dict: result, fallback: "cgclick 定位失败，请重新 snapshot"), clientAction: nil)
            }
            viewportPoint = CGPoint(x: x, y: y)
            targetDesc = "ref @e\(idx + 1)"
        } else if let x = (arguments["x"] as? NSNumber)?.doubleValue,
                  let y = (arguments["y"] as? NSNumber)?.doubleValue {
            viewportPoint = CGPoint(x: x, y: y)
            targetDesc = "viewport(\(Int(x)),\(Int(y)))"
        }

        guard let vp = viewportPoint else {
            return .init(serverResult: nil, serverError: "cgclick 需要 ref（@eN）或 x/y 坐标", clientAction: nil)
        }
        guard let screenPoint = CGEventInput.screenPoint(for: vp, in: session.webView) else {
            // Bad Case 17 诊断：窗口不可用时输出工具层上下文
            print("[B-CGClick] diag activeTabId=\(activeTabId ?? "nil") rightSidebarTab=\(rightSidebar?.activeTabId ?? "nil") tabs=\(tabs.count)")
            return .init(serverResult: nil, serverError: "cgclick 坐标转换失败：页面窗口不可用", clientAction: nil)
        }
        // 越界校验：目标超出屏幕范围会被 macOS clamp 到屏幕边缘，可能误触 Dock/其他区域，拒绝注入
        guard CGEventInput.isPointOnScreen(screenPoint) else {
            let maxY = NSScreen.screens.map { $0.frame.maxY }.max() ?? 0
            let screenSize = NSScreen.screens.first?.frame.size ?? .zero
            return .init(serverResult: nil, serverError: "cgclick 目标超出屏幕范围 (screen=\(Int(screenSize.width))x\(Int(screenSize.height)), target=\(Int(screenPoint.x)),\(Int(screenPoint.y)))，请移动窗口或滚动页面后重试", clientAction: nil)
        }

        CGEventInput.activateWindow(for: session.webView)
        CGEventInput.click(at: screenPoint)

        // 拟人化诊断：回读页面焦点，确认点击命中
        if let active = await session.jsJSON("(function(){var e=document.activeElement;return JSON.stringify({tag:e?e.tagName.toLowerCase():'',id:e?e.id:'',name:e?e.name:''});})()") as? [String: Any] {
            let tag = active["tag"] as? String ?? ""
            let id = active["id"] as? String ?? ""
            let name = active["name"] as? String ?? ""
            print("[B-CGClick] active=\(tag) id=\(id) name=\(name)")
        }

        return .init(serverResult: Self.jsonDict(["clicked": true, "target": targetDesc, "point_debug": "viewport=\(Int(vp.x)),\(Int(vp.y)) screen=\(Int(screenPoint.x)),\(Int(screenPoint.y))"]), serverError: nil, clientAction: nil)
    }

    private func dictErr(dict: Any?, fallback: String) -> String {
        if let d = dict as? [String: Any], let err = d["err"] as? String {
            return err
        }
        return fallback
    }

    /// 真实系统级拖拽（CGEvent，地图/滑块/canvas）：起点定位 → 转换 → 逐点越界预检 → 注入
    private func cgDrag(session: TabSession, arguments: [String: Any]) async -> LocalToolResult {
        guard CGEventInput.isTrusted() else {
            let granted = CGEventInput.requestPermission()
            print("[CGEvent] permission granted=\(granted)")
            return .init(serverResult: nil, serverError: "需要辅助功能权限：请在 系统设置→隐私与安全→辅助功能 勾选 xhanHelper 后重试（已授权仍报错请重启 App）", clientAction: nil)
        }
        let dx = (arguments["dx"] as? NSNumber)?.doubleValue ?? 0
        let dy = (arguments["dy"] as? NSNumber)?.doubleValue ?? 0
        let steps = max(2, min(30, (arguments["steps"] as? NSNumber)?.intValue ?? 10))
        guard dx != 0 || dy != 0 else {
            return .init(serverResult: nil, serverError: "drag 需要 dx/dy 位移量", clientAction: nil)
        }

        var vpStart: CGPoint?
        if let idx = refIndex(arguments["ref"] as? String ?? "") {
            if let stale = await refMismatchError(session: session, index: idx) {
                return .init(serverResult: nil, serverError: stale, clientAction: nil)
            }
            let result = await session.jsJSON(refCenterJS(idx))
            guard let dict = result as? [String: Any], let ok = dict["ok"] as? Bool, ok,
                  let x = (dict["x"] as? NSNumber)?.doubleValue,
                  let y = (dict["y"] as? NSNumber)?.doubleValue
            else {
                return .init(serverResult: nil, serverError: dictErr(dict: result, fallback: "drag 定位失败，请重新 snapshot"), clientAction: nil)
            }
            vpStart = CGPoint(x: x, y: y)
        } else if let x = (arguments["x"] as? NSNumber)?.doubleValue,
                  let y = (arguments["y"] as? NSNumber)?.doubleValue {
            vpStart = CGPoint(x: x, y: y)
        }
        guard let start = vpStart else {
            return .init(serverResult: nil, serverError: "drag 需要 ref（@eN）或 x/y 起点坐标", clientAction: nil)
        }

        let end = CGPoint(x: start.x + dx, y: start.y + dy)
        guard let screenStart = CGEventInput.screenPoint(for: start, in: session.webView),
              let screenEnd = CGEventInput.screenPoint(for: end, in: session.webView) else {
            print("[B-Drag] diag activeTabId=\(activeTabId ?? "nil") rightSidebarTab=\(rightSidebar?.activeTabId ?? "nil") tabs=\(tabs.count)")
            return .init(serverResult: nil, serverError: "drag 坐标转换失败：页面窗口不可用", clientAction: nil)
        }
        // 逐插值点越界预检（不注入任何事件，杜绝 clamp 误触）
        for i in 0...steps {
            let t = CGFloat(i) / CGFloat(steps)
            let p = CGPoint(x: screenStart.x + (screenEnd.x - screenStart.x) * t,
                            y: screenStart.y + (screenEnd.y - screenStart.y) * t)
            guard CGEventInput.isPointOnScreen(p) else {
                return .init(serverResult: nil, serverError: "drag 路径超出屏幕范围（step \(i)/\(steps)），请移动窗口或缩小拖拽距离", clientAction: nil)
            }
        }

        CGEventInput.activateWindow(for: session.webView)
        CGEventInput.drag(from: screenStart, to: screenEnd, steps: steps)
        return .init(serverResult: Self.jsonDict(["dragged": true, "target": "viewport(\(Int(start.x)),\(Int(start.y)))→(\(Int(end.x)),\(Int(end.y)))"]), serverError: nil, clientAction: nil)
    }

    /// ref 元素中心 viewport 坐标（cgclick/cgDrag 定位用，含 ±20% 随机偏移拟人化）
    private func refCenterJS(_ index: Int) -> String {
        let body = collectElsBody.replacingOccurrences(of: "LIMIT", with: "200")
        return "(function(){\(body)" +
            "var e=out[\(index)];if(!e)return JSON.stringify({ok:false,err:'ref @e\(index + 1) 不存在，请重新 snapshot'});" +
            "var r=e.getBoundingClientRect();" +
            "var x=Math.round(r.left+r.width/2+(Math.random()-0.5)*r.width*0.4);" +
            "var y=Math.round(r.top+r.height/2+(Math.random()-0.5)*r.height*0.4);" +
            "return JSON.stringify({ok:true,x:x,y:y});})()"
    }

    /// 解析 ref 交互 JS 的 {"ok": bool, "err": string?} 结果
    private func interpretRefResult(_ result: Any?, successKey: String) -> LocalToolResult {
        if let dict = result as? [String: Any], let ok = dict["ok"] as? Bool {
            if ok {
                return .init(serverResult: Self.jsonDict([successKey: true]), serverError: nil, clientAction: nil)
            }
            return .init(serverResult: nil, serverError: dict["err"] as? String ?? "元素交互失败", clientAction: nil)
        }
        return .init(serverResult: nil, serverError: "元素交互失败：JS 异常", clientAction: nil)
    }

    // MARK: - Snapshot

    private func handleSnapshot(session: TabSession, arguments: [String: Any]) async -> LocalToolResult {
        let mode = arguments["mode"] as? String ?? "interactive"
        let includeHref: Bool
        if let b = arguments["include_href"] as? Bool {
            includeHref = b
        } else if let s = arguments["include_href"] as? String {
            includeHref = Bool(s) ?? true
        } else {
            includeHref = true
        }
        let maxItems = (arguments["max_items"] as? NSNumber)?.intValue ?? 60
        guard let arr = await session.jsJSON(collectInteractablesJS(maxItems)) as? [[String: Any]] else {
            return .init(serverResult: nil, serverError: "快照失败：JS 异常", clientAction: nil)
        }
        var lines: [String] = []
        lines.append("Page: \(session.webView.title ?? "")")
        lines.append("URL: \(session.webView.url?.absoluteString ?? "")")
        lines.append("")
        for (i, item) in arr.enumerated() {
            lines.append(axLine(index: i + 1, item: item, includeHref: includeHref))
        }
        if mode == "full" {
            let (preview, _) = await session.jsResult("(document.body?(document.body.innerText||''):'').slice(0,800)")
            if let p = preview, !p.isEmpty {
                lines.append("")
                lines.append("--- 可见文本预览 ---")
                lines.append(String(p.prefix(800)))
            }
        }
        let hash = await session.contentHash()
        session.lastContentHash = hash
        session.lastSnapshotItems = arr
        var dict: [String: Any] = ["snapshot": lines.joined(separator: "\n"), "count": arr.count]
        if let h = hash { dict["hash"] = h }
        return .init(serverResult: Self.jsonDict(dict), serverError: nil, clientAction: nil)
    }

    private func axLine(index: Int, item: [String: Any], includeHref: Bool) -> String {
        let tag = item["tag"] as? String ?? ""
        let role = item["role"] as? String ?? ""
        let text = (item["text"] as? String ?? "").replacingOccurrences(of: "\"", with: "'")
        let href = item["href"] as? String ?? ""
        let placeholder = (item["placeholder"] as? String ?? "").replacingOccurrences(of: "\"", with: "'")
        let type = item["type"] as? String ?? ""
        let checked = (item["checked"] as? Bool) ?? false

        var attr = role.isEmpty ? tag : role
        if tag == "a" && role.isEmpty { attr = "link" }
        if tag == "h1" || tag == "h2" || tag == "h3" { attr = "heading" }
        if tag == "input" && !type.isEmpty { attr += " type=\(type)" }

        var line = "@e\(index) [\(attr)]"
        if !text.isEmpty { line += " \"\(text)\"" }
        if !placeholder.isEmpty { line += " placeholder=\"\(placeholder)\"" }
        if includeHref && tag == "a" && !href.isEmpty { line += " href=\"\(href)\"" }
        if tag == "input" && type == "checkbox" && checked { line += " checked" }
        return line
    }

    // MARK: - Read / GetText / GetValue

    /// read：分层读取。full=全文（默认），outline=标题树，filter=关键词匹配章节
    private func handleRead(session: TabSession, arguments: [String: Any]) async -> LocalToolResult {
        let mode = arguments["mode"] as? String ?? "full"
        let query = arguments["query"] as? String ?? ""
        let maxItems = (arguments["max_items"] as? NSNumber)?.intValue ?? 50
        let maxChars = (arguments["max_chars"] as? NSNumber)?.intValue ?? (mode == "filter" ? 2000 : maxResultChars)

        switch mode {
        case "outline":
            let (text, _) = await session.jsResult(outlineJS(maxItems))
            return .init(serverResult: Self.jsonDict(["content": text ?? "", "mode": "outline"]), serverError: nil, clientAction: nil)
        case "filter":
            guard !query.isEmpty else {
                return .init(serverResult: nil, serverError: "filter 模式需要 query 关键词", clientAction: nil)
            }
            let (text, _) = await session.jsResult(filterJS(query, maxChars))
            let content = text?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            if content.isEmpty {
                return .init(serverResult: Self.jsonDict(["content": "", "mode": "filter", "matched": false]), serverError: nil, clientAction: nil)
            }
            return .init(serverResult: Self.jsonDict(["content": content, "mode": "filter", "matched": true]), serverError: nil, clientAction: nil)
        default:
            let (text, _) = await session.jsResult("document.body?.innerText ?? ''")
            return .init(serverResult: Self.jsonDict(["content": String((text ?? "").prefix(maxChars)), "mode": "full"]), serverError: nil, clientAction: nil)
        }
    }

    /// get_text：元素级（ref/selector）读取，无参数返回全量
    private func handleGetText(session: TabSession, arguments: [String: Any]) async -> LocalToolResult {
        let maxChars = (arguments["max_chars"] as? NSNumber)?.intValue ?? 2000
        if let idx = refIndex(arguments["ref"] as? String ?? "") {
            if let stale = await refMismatchError(session: session, index: idx) {
                return .init(serverResult: nil, serverError: stale, clientAction: nil)
            }
            let result = await session.jsJSON(elementTextJS(idx, maxChars))
            return interpretTextResult(result)
        }
        if let sel = arguments["selector"] as? String, !sel.isEmpty {
            let result = await session.jsJSON(elementTextSelJS(sel, maxChars))
            return interpretTextResult(result)
        }
        let (text, _) = await session.jsResult("document.body?.innerText ?? ''")
        return .init(serverResult: Self.jsonDict(["text": String((text ?? "").prefix(maxResultChars))]), serverError: nil, clientAction: nil)
    }

    private func interpretTextResult(_ result: Any?) -> LocalToolResult {
        if let dict = result as? [String: Any], let ok = dict["ok"] as? Bool, ok {
            return .init(serverResult: Self.jsonDict(["text": dict["text"] ?? ""]), serverError: nil, clientAction: nil)
        }
        return .init(serverResult: nil, serverError: dictErr(dict: result, fallback: "元素读取失败"), clientAction: nil)
    }

    /// get_value：读取表单值；checkbox/radio 返回 checked 状态
    private func handleGetValue(session: TabSession, arguments: [String: Any]) async -> LocalToolResult {
        let locator: (js: String, desc: String)
        if let idx = refIndex(arguments["ref"] as? String ?? "") {
            if let stale = await refMismatchError(session: session, index: idx) {
                return .init(serverResult: nil, serverError: stale, clientAction: nil)
            }
            locator = (elementValueJS(idx), "@e\(idx + 1)")
        } else if let sel = arguments["selector"] as? String, !sel.isEmpty {
            locator = (elementValueSelJS(sel), sel)
        } else {
            return .init(serverResult: nil, serverError: "get_value 需要 ref 或 selector", clientAction: nil)
        }
        guard let dict = await session.jsJSON(locator.js) as? [String: Any],
              let ok = dict["ok"] as? Bool, ok else {
            return .init(serverResult: nil, serverError: "未找到元素: \(locator.desc)", clientAction: nil)
        }
        var out: [String: Any] = [:]
        if let checked = dict["checked"] as? Bool {
            out["checked"] = checked
        } else {
            out["value"] = dict["value"] ?? ""
        }
        return .init(serverResult: Self.jsonDict(out), serverError: nil, clientAction: nil)
    }

    // MARK: - Nav context wrapper

    /// 交互操作统一附加 url/title/nav + 内容 hash/changed（供 LLM 判断页面是否变化，避免重复快照）
    private func withNavContext(_ session: TabSession, _ run: () async -> LocalToolResult) async -> LocalToolResult {
        let beforeURL = session.webView.url?.absoluteString
        let beforeHash = session.lastContentHash
        let beforeTime = Date()
        var result = await run()
        if let data = result.serverResult,
           var dict = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] {
            // 短轮询等待异步导航开始（合成 click 后导航是异步的，URL 在 didStart 后同步）
            let deadline = Date().addingTimeInterval(1.0)
            var url = session.webView.url?.absoluteString ?? ""
            while url == beforeURL && Date() < deadline {
                try? await Task.sleep(for: .milliseconds(50))
                url = session.webView.url?.absoluteString ?? ""
            }
            // 导航判定：URL 对比（SPA 路由/pushState）+ didStart 事件兜底
            let navStarted = url != beforeURL || (session.lastNavStartAt ?? .distantPast) >= beforeTime
            dict["url"] = url
            dict["title"] = session.webView.title ?? ""
            dict["nav"] = navStarted
            if navStarted {
                // 导航发生：内容必然变化，重置 hash 基准（新页面加载中 hash 不稳定）；
                // 快照特征同步失效，ref 交互将被强制要求重新 snapshot
                session.lastContentHash = nil
                session.lastSnapshotItems = nil
                dict["changed"] = true
            } else if let hash = await session.contentHash() {
                dict["hash"] = hash
                // 无基准（导航后首次操作）时不输出 changed，避免误导 LLM 以为页面未变
                if let before = beforeHash {
                    dict["changed"] = hash != before
                }
                session.lastContentHash = hash
            }
            result = .init(serverResult: try? JSONSerialization.data(withJSONObject: dict),
                           serverError: result.serverError,
                           clientAction: result.clientAction)
        }
        return result
    }

    // MARK: - Tab management

    private var activeSession: TabSession? {
        guard let id = activeTabId else { return nil }
        return tabs[id]
    }

    private func handleOpen(_ args: [String: Any]) async -> LocalToolResult {
        let urlStr = args["url"] as? String ?? ""
        guard let url = URL(string: urlStr) else {
            return .init(serverResult: Self.jsonDict(["error": "无效的URL"]), serverError: nil, clientAction: nil)
        }

        let tabId = "browser-\(tabCounter)"
        tabCounter += 1
        let session = TabSession()
        tabs[tabId] = session
        activeTabId = tabId

        session.webView.load(URLRequest(url: url))

        let errorMsg = await withCheckedContinuation { (cont: CheckedContinuation<String?, Never>) in
            session.loadContinuation = cont
            session.loadTimeout = Task {
                try? await Task.sleep(for: .seconds(15))
                guard !Task.isCancelled else { return }
                cont.resume(returning: "timeout")
                session.loadContinuation = nil
            }
        }

        if let msg = errorMsg {
            rightSidebar?.openTab(id: tabId, title: url.host ?? urlStr, icon: "globe",
                content: AnyView(BrowserErrorPageView(url: urlStr, error: msg, onRetry: { [weak self] in
                    Task { @MainActor in
                        await self?.retryOpen(session: session, tabId: tabId, url: url)
                    }
                })))
            return .init(serverResult: Self.jsonDict(["error": "打开失败: \(msg)", "tabId": tabId]), serverError: nil, clientAction: nil)
        }

        let title = session.webView.title ?? url.host ?? urlStr
        rightSidebar?.openTab(id: tabId, title: title, icon: "globe",
                              content: AnyView(BrowserTabView(webView: session.webView)))
        return .init(serverResult: Self.jsonDict(["url": urlStr, "title": title, "tabId": tabId]), serverError: nil, clientAction: nil)
    }

    private func retryOpen(session: TabSession, tabId: String, url: URL) async {
        session.webView.load(URLRequest(url: url))
        let errorMsg = await withCheckedContinuation { (cont: CheckedContinuation<String?, Never>) in
            session.loadContinuation = cont
            session.loadTimeout = Task {
                try? await Task.sleep(for: .seconds(15))
                guard !Task.isCancelled else { return }
                cont.resume(returning: "timeout")
                session.loadContinuation = nil
            }
        }
        if let msg = errorMsg {
            let urlStr = url.absoluteString
            rightSidebar?.replaceTabContent(id: tabId,
                content: AnyView(BrowserErrorPageView(url: urlStr, error: msg, onRetry: { [weak self] in
                    Task { @MainActor in
                        await self?.retryOpen(session: session, tabId: tabId, url: url)
                    }
                })))
        } else {
            let title = session.webView.title ?? url.host ?? url.absoluteString
            rightSidebar?.updateTabTitle(id: tabId, title: title)
            rightSidebar?.replaceTabContent(id: tabId,
                content: AnyView(BrowserTabView(webView: session.webView)))
        }
    }

    private func handleClose() -> LocalToolResult {
        let count = tabs.count
        for (id, session) in tabs {
            session.cancelLoadWait()
            rightSidebar?.closeTab(id)
        }
        tabs.removeAll()
        activeTabId = nil
        return .init(serverResult: Self.jsonDict(["closed": true, "count": count]), serverError: nil, clientAction: nil)
    }

    private func handleListTabs() -> LocalToolResult {
        return .init(serverResult: Self.jsonDict(["tabs": tabInfoList(), "activeTabId": activeTabId ?? ""]), serverError: nil, clientAction: nil)
    }

    private func handleSwitchTab(_ args: [String: Any]) -> LocalToolResult {
        let tabId = args["tabId"] as? String ?? ""
        guard !tabId.isEmpty, tabs[tabId] != nil else {
            return .init(serverResult: nil, serverError: "tabId 不存在: \(tabId)", clientAction: nil)
        }
        activeTabId = tabId
        if let rs = rightSidebar {
            rs.activeTabId = tabId
        }
        return .init(serverResult: Self.jsonDict(["switched": tabId]), serverError: nil, clientAction: nil)
    }

    func selectTab(_ id: String) {
        guard tabs[id] != nil else { return }
        activeTabId = id
    }

    func openBlankTab() {
        let tabId = "browser-\(tabCounter)"
        tabCounter += 1
        let session = TabSession()
        tabs[tabId] = session
        activeTabId = tabId
        session.webView.load(URLRequest(url: URL(string: "about:blank")!))
        rightSidebar?.openTab(id: tabId, title: "新标签页", icon: "globe",
                              content: AnyView(BrowserTabView(webView: session.webView)))
    }

    private func tabInfoList() -> [[String: String]] {
        tabs.map { id, session in
            ["id": id, "url": session.webView.url?.absoluteString ?? "", "title": session.webView.title ?? ""]
        }
    }

    // MARK: - TabSession

    private final class TabSession: NSObject, WKNavigationDelegate {
        let webView: WKWebView
        var loadContinuation: CheckedContinuation<String?, Never>?
        var loadTimeout: Task<Void, Never>?
        /// readyState 轮询兜底（SPA/懒加载页面 didFinish 可能不来）
        var readyPollTask: Task<Void, Never>?
        /// 最近一次 snapshot/交互后的页面内容哈希（FNV of innerText），用于 changed 判断
        var lastContentHash: String?
        /// 最近一次网络导航开始时间（didStartProvisionalNavigation），用于 nav 判定
        var lastNavStartAt: Date?
        /// 最近一次 snapshot 时各 @eN 元素的特征签名（ref 校验基准；导航时清空）
        var lastSnapshotItems: [[String: Any]]?

        override init() {
            let config = WKWebViewConfiguration()
            let prefs = WKWebpagePreferences()
            prefs.allowsContentJavaScript = true
            config.defaultWebpagePreferences = prefs
            config.preferences.isElementFullscreenEnabled = false

            let hideWebDriver = WKUserScript(
                source: "Object.defineProperty(navigator,'webdriver',{get:()=>undefined});",
                injectionTime: .atDocumentStart,
                forMainFrameOnly: true
            )
            config.userContentController.addUserScript(hideWebDriver)

            // 指纹伪装（拟人化/反反爬）：补齐自动化痕迹，WebGL 与 Mac 真实 Chrome 对齐
            let fingerprintScript = WKUserScript(
                source: BrowserToolProvider.fingerprintJS,
                injectionTime: .atDocumentStart,
                forMainFrameOnly: true
            )
            config.userContentController.addUserScript(fingerprintScript)

            webView = WKWebView(frame: .zero, configuration: config)
            webView.customUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
            webView.allowsMagnification = true
            webView.allowsBackForwardNavigationGestures = true

            super.init()
            webView.navigationDelegate = self
        }

        func cancelLoadWait() {
            loadContinuation?.resume(returning: nil)
            loadContinuation = nil
            loadTimeout?.cancel()
            loadTimeout = nil
            readyPollTask?.cancel()
            readyPollTask = nil
        }

        /// 统一结束一次加载等待（didFinish/didFail/轮询/超时共用），清理所有等待资源
        private func resumeLoad(_ value: String?) {
            loadContinuation?.resume(returning: value)
            loadContinuation = nil
            loadTimeout?.cancel()
            loadTimeout = nil
            readyPollTask?.cancel()
            readyPollTask = nil
        }

        /// 页面是否已就绪（readyState == complete）
        private func isPageReady() async -> Bool {
            let (ok, _) = await jsBool("document.readyState==='complete'")
            return ok
        }

        func waitForLoad(timeoutMs: Int) async -> LocalToolResult {
            cancelLoadWait()
            // 快速路径：页面已就绪（open 后/无新导航时避免假 timeout）
            if await isPageReady() {
                return .init(serverResult: BrowserToolProvider.jsonDict(["loaded": true]), serverError: nil, clientAction: nil)
            }
            let errorMsg = await withCheckedContinuation { (cont: CheckedContinuation<String?, Never>) in
                loadContinuation = cont
                // readyState 轮询兜底：didFinish 不来（SPA/懒加载）时页面就绪即成功
                readyPollTask = Task { @MainActor [weak self] in
                    while !Task.isCancelled {
                        try? await Task.sleep(for: .milliseconds(200))
                        guard let self, !Task.isCancelled else { return }
                        if await self.isPageReady() {
                            self.resumeLoad(nil)
                            return
                        }
                    }
                }
                loadTimeout = Task {
                    try? await Task.sleep(for: .milliseconds(timeoutMs))
                    guard !Task.isCancelled else { return }
                    cont.resume(returning: "timeout")
                    loadContinuation = nil
                    readyPollTask?.cancel()
                    readyPollTask = nil
                }
            }
            if let msg = errorMsg {
                return .init(serverResult: BrowserToolProvider.jsonDict(["error": msg]), serverError: nil, clientAction: nil)
            }
            return .init(serverResult: BrowserToolProvider.jsonDict(["loaded": true]), serverError: nil, clientAction: nil)
        }

        // MARK: - JS Helpers

        func jsResult(_ js: String) async -> (text: String?, jsError: String?) {
            do {
                let result = try await webView.evaluateJavaScript(js)
                return ((result as? String), nil)
            } catch {
                print("[Browser] JS error: \(error.localizedDescription)")
                return (nil, error.localizedDescription)
            }
        }

        func jsBool(_ js: String) async -> (ok: Bool, jsError: String?) {
            do {
                let result = try await webView.evaluateJavaScript(js)
                return ((result as? Bool) ?? false, nil)
            } catch {
                print("[Browser] JS error: \(error.localizedDescription)")
                return (false, error.localizedDescription)
            }
        }

        /// 执行返回 JSON 字符串的 JS 并解析为 Any（数组/字典）
        func jsJSON(_ js: String) async -> Any? {
            let (text, _) = await jsResult(js)
            guard let t = text, let data = t.data(using: .utf8) else { return nil }
            return try? JSONSerialization.jsonObject(with: data)
        }

        /// 当前页面可见文本的内容哈希（JS 内计算，只回传 hash 字符串）
        func contentHash() async -> String? {
            let (hash, _) = await jsResult(BrowserToolProvider.contentHashJS)
            guard let h = hash, !h.isEmpty else { return nil }
            return h
        }

        func eval(_ script: String) async -> LocalToolResult {
            do {
                let result = try await webView.evaluateJavaScript(script)
                let text: String
                if let s = result as? String {
                    text = s
                } else if let n = result as? NSNumber {
                    text = n.stringValue
                } else if result == nil {
                    text = "null"
                } else {
                    let data = try JSONSerialization.data(withJSONObject: result, options: .fragmentsAllowed)
                    text = String(data: data, encoding: .utf8) ?? "\(result)"
                }
                return .init(serverResult: BrowserToolProvider.jsonDict(["result": text]), serverError: nil, clientAction: nil)
            } catch {
                return .init(serverResult: nil, serverError: error.localizedDescription, clientAction: nil)
            }
        }

        // MARK: - WKNavigationDelegate

        nonisolated func webView(_ webView: WKWebView, didStartProvisionalNavigation navigation: WKNavigation!) {
            Task { @MainActor in
                lastNavStartAt = Date()
            }
        }

        nonisolated func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
            Task { @MainActor in
                resumeLoad(nil)
            }
        }

        nonisolated func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
            Task { @MainActor in
                resumeLoad((error as NSError).code == NSURLErrorCancelled ? nil : error.localizedDescription)
            }
        }

        nonisolated func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
            Task { @MainActor in
                let code = (error as NSError).code
                resumeLoad(code == NSURLErrorCancelled ? nil : "加载失败: \(error.localizedDescription)")
            }
        }
    }

    // MARK: - JS string builders

    /// 共享元素收集主体（元素引用数组 + 过滤）。collectInteractablesJS 与 ref 交互共用，
    /// 过滤逻辑必须一致，否则 @eN 编号会错位。LIMIT 为占位符，使用前替换。
    private let collectElsBody = """
    var out=[];var sels='h1,h2,h3,a,button,input,select,textarea,canvas,[role="button"],[role="checkbox"],[role="tab"],[role="link"]';
    var all=document.querySelectorAll(sels);
    for(var i=0;i<all.length;i++){
      var e=all[i];var r=e.getBoundingClientRect();
      if(r.width<1||r.height<1)continue;
      if(e.disabled)continue;
      var tag=e.tagName.toLowerCase();
      var t=(e.getAttribute('aria-label')||e.textContent||e.value||e.placeholder||'').trim().replace(/\\s+/g,' ').slice(0,40);
      // 表单元素与 canvas 即使无文本也保留（高频交互目标，如空搜索框）
      var isForm=tag==='input'||tag==='select'||tag==='textarea';
      if(!t&&!e.href&&!e.placeholder&&!isForm&&tag!=='canvas')continue;
      out.push(e);
      if(out.length>=LIMIT)break;
    }
    """

    /// snapshot 用：收集可交互元素并返回 JSON 元数据数组（含 id/name 供 ref 特征校验）
    private func collectInteractablesJS(_ limit: Int) -> String {
        let body = collectElsBody.replacingOccurrences(of: "LIMIT", with: String(limit))
        return """
        (function(){
        \(body)
        var meta=[];
        for(var i=0;i<out.length;i++){var e=out[i];
          meta.push({tag:e.tagName.toLowerCase(),role:e.getAttribute('role')||'',text:(e.getAttribute('aria-label')||e.textContent||e.value||e.placeholder||'').trim().replace(/\\s+/g,' ').slice(0,40),href:e.href||'',placeholder:e.placeholder||'',type:e.type||'',checked:!!e.checked,id:e.id||'',name:e.name||''});
        }
        return JSON.stringify(meta);
        })()
        """
    }

    /// ref 特征探测：解析当前 DOM 第 N 个元素的特征（与 snapshot 元数据同构），供 ref 校验对比
    private func refProbeJS(_ index: Int) -> String {
        let body = collectElsBody.replacingOccurrences(of: "LIMIT", with: "200")
        return "(function(){\(body)" +
            "var e=out[\(index)];if(!e)return JSON.stringify({ok:false});" +
            "return JSON.stringify({ok:true,tag:e.tagName.toLowerCase(),href:e.href||'',type:e.type||'',id:e.id||'',name:e.name||'',text:(e.getAttribute('aria-label')||e.textContent||e.value||e.placeholder||'').trim().replace(/\\s+/g,' ').slice(0,40)});})()"
    }

    // MARK: - 读取 JS

    /// 标题层级树（h1-h6 缩进），长文导航用
    private func outlineJS(_ maxItems: Int) -> String {
        """
        (function(){
          var out=[];
          var hs=document.querySelectorAll('h1,h2,h3,h4,h5,h6');
          for(var i=0;i<hs.length;i++){
            var h=hs[i];
            var t=(h.textContent||'').trim().replace(/\\s+/g,' ').slice(0,80);
            if(!t)continue;
            var lvl=parseInt(h.tagName[1]);
            out.push(new Array(lvl).join('  ')+'- '+t);
            if(out.length>=\(maxItems))break;
          }
          return out.join('\\n');
        })()
        """
    }

    /// 按标题分块（h1-h6 为块边界），返回含关键词的块文本
    private func filterJS(_ query: String, _ maxChars: Int) -> String {
        """
        (function(){
          var q='\(esc(query))';
          var hs=document.querySelectorAll('h1,h2,h3,h4,h5,h6');
          var chunks=[];
          for(var i=0;i<hs.length;i++){
            var title=(hs[i].textContent||'').trim().replace(/\\s+/g,' ');
            var body=[];
            var el=hs[i].nextElementSibling;
            while(el&&!/^H[1-6]$/.test(el.tagName)){
              var t=(el.innerText||'').trim();
              if(t)body.push(t);
              el=el.nextElementSibling;
            }
            chunks.push({title:title,text:body.join('\\n')});
          }
          var hit=[];
          for(var j=0;j<chunks.length;j++){
            if(chunks[j].title.indexOf(q)>=0||chunks[j].text.indexOf(q)>=0){
              hit.push(chunks[j].title+'\\n'+chunks[j].text.slice(0,\(maxChars)));
            }
          }
          return hit.join('\\n\\n');
        })()
        """
    }

    /// 元素可见文本（ref 路径）
    private func elementTextJS(_ index: Int, _ maxChars: Int) -> String {
        let body = collectElsBody.replacingOccurrences(of: "LIMIT", with: "200")
        return "(function(){\(body)" +
            "var e=out[\(index)];if(!e)return JSON.stringify({ok:false,err:'ref @e\(index + 1) 不存在，请重新 snapshot'});" +
            "var t=(e.innerText||e.textContent||'').trim().replace(/\\s+/g,' ').slice(0,\(maxChars));" +
            "return JSON.stringify({ok:true,text:t});})()"
    }

    /// 元素可见文本（selector 路径）
    private func elementTextSelJS(_ sel: String, _ maxChars: Int) -> String {
        "(function(){var e=document.querySelector('\(esc(sel))');if(!e)return JSON.stringify({ok:false});" +
        "var t=(e.innerText||e.textContent||'').trim().replace(/\\s+/g,' ').slice(0,\(maxChars));" +
        "return JSON.stringify({ok:true,text:t});})()"
    }

    /// 表单元素值（ref 路径）；checkbox/radio 返回 checked
    private func elementValueJS(_ index: Int) -> String {
        let body = collectElsBody.replacingOccurrences(of: "LIMIT", with: "200")
        return "(function(){\(body)" +
            "var e=out[\(index)];if(!e)return JSON.stringify({ok:false,err:'ref @e\(index + 1) 不存在，请重新 snapshot'});" +
            "if(e.type==='checkbox'||e.type==='radio')return JSON.stringify({ok:true,checked:!!e.checked});" +
            "return JSON.stringify({ok:true,value:e.value!=null?e.value:''});})()"
    }

    /// 表单元素值（selector 路径）
    private func elementValueSelJS(_ sel: String) -> String {
        "(function(){var e=document.querySelector('\(esc(sel))');if(!e)return JSON.stringify({ok:false});" +
        "if(e.type==='checkbox'||e.type==='radio')return JSON.stringify({ok:true,checked:!!e.checked});" +
        "return JSON.stringify({ok:true,value:e.value!=null?e.value:''});})()"
    }

    /// _blank 链接合成点击无效（不触发新标签导航），拦截并引导改用 open
    private let blankTabGuardJS = """
    if(e.getAttribute&&e.getAttribute('target')==='_blank'){
      return JSON.stringify({ok:false,err:'链接为 _blank 新标签打开（合成点击无效），建议改用 agent_browser_open 打开: '+(e.href||'')});
    }
    """

    private func refFillJS(_ index: Int, _ val: String) -> String {
        let body = collectElsBody.replacingOccurrences(of: "LIMIT", with: "200")
        return "(function(){\(body)" +
            "var e=out[\(index)];if(!e)return JSON.stringify({ok:false,err:'ref @e\(index + 1) 不存在，请重新 snapshot'});" +
            "e.focus();e.select();" +
            "var proto=e.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:(e.tagName==='SELECT'?HTMLSelectElement.prototype:HTMLInputElement.prototype);" +
            "var setter=Object.getOwnPropertyDescriptor(proto,'value').set;" +
            "setter.call(e,'\(esc(val))');" +
            "e.dispatchEvent(new InputEvent('beforeinput',{bubbles:true,inputType:'insertText',data:'\(esc(val))'}));" +
            "e.dispatchEvent(new Event('input',{bubbles:true}));" +
            "e.dispatchEvent(new Event('change',{bubbles:true}));" +
            "return JSON.stringify({ok:true});})()"
    }

    private func refSelectJS(_ index: Int, _ val: String) -> String {
        let body = collectElsBody.replacingOccurrences(of: "LIMIT", with: "200")
        return "(function(){\(body)" +
            "var e=out[\(index)];if(!e)return JSON.stringify({ok:false,err:'ref @e\(index + 1) 不存在，请重新 snapshot'});" +
            "e.focus();e.value='\(esc(val))';" +
            "e.dispatchEvent(new Event('input',{bubbles:true}));" +
            "e.dispatchEvent(new Event('change',{bubbles:true}));" +
            "e.blur();return JSON.stringify({ok:true});})()"
    }

    private func refCheckJS(_ index: Int, _ checked: Bool) -> String {
        let body = collectElsBody.replacingOccurrences(of: "LIMIT", with: "200")
        return "(function(){\(body)" +
            "var e=out[\(index)];if(!e)return JSON.stringify({ok:false,err:'ref @e\(index + 1) 不存在，请重新 snapshot'});" +
            "e.focus();" +
            "if(!!e.checked!==\(checked))e.click();" +
            "e.dispatchEvent(new Event('change',{bubbles:true}));" +
            "return JSON.stringify({ok:true});})()"
    }

    /// 页面可见文本 + 表单值（input/select/textarea 的 value 与 checkbox/radio 的 checked）的 FNV 哈希
    static let contentHashJS = """
    (function(){
      var s=document.body?(document.body.innerText||''):'';
      var fs=document.querySelectorAll('input,select,textarea');
      for(var i=0;i<fs.length;i++){var f=fs[i];
        if(f.type==='checkbox'||f.type==='radio'){s+=f.tagName+':'+f.name+'='+(f.checked?1:0)+';';}
        else if(f.value!=null)s+=f.tagName+':'+f.value+';';
      }
      var h=5381;
      for(var j=0;j<s.length;j++){h=((h<<5)+h+s.charCodeAt(j))|0;}
      return String(h>>>0);
    })()
    """

    /// 浏览器指纹伪装：与 Mac 真实 Chrome 对齐，消除自动化痕迹
    static let fingerprintJS = """
    (function(){
      try{
        var mk=function(obj,key,val){
          try{Object.defineProperty(obj,key,{get:function(){return val;}});}catch(e){}
        };
        var plugins=[];
        var names=[['Chrome PDF Plugin','PDF Viewer'],['Chrome PDF Viewer','pdf'],['Native Client','application/x-nacl'],['Widevine Content Decryption Module','application/x-ppapi-widevine-cdm']];
        for(var i=0;i<names.length;i++){
          var p={name:names[i][0],filename:names[i][0]+'.plugin',description:names[i][0],length:1};
          try{plugins.push(p);}catch(e){}
        }
        mk(navigator,'plugins',plugins);
        mk(navigator,'languages',['zh-CN','zh','en-US']);
        mk(navigator,'hardwareConcurrency',8);
        mk(navigator,'platform','MacIntel');
        mk(navigator,'deviceMemory',8);
        mk(navigator,'maxTouchPoints',0);
        var gp=WebGLRenderingContext.prototype.getParameter;
        if(gp){
          WebGLRenderingContext.prototype.getParameter=function(p){
            if(p===37445)return 'Apple Inc.';
            if(p===37446)return 'Apple GPU';
            return gp.apply(this,arguments);
          };
        }
      }catch(e){}
    })()
    """

    /// 合成点击第一步：定位元素（随机偏移点）→ 聚焦 → mousedown → 打标记
    /// 第二步 clickUpJS 抬起（两步间由 Swift 随机延迟，拟人化）
    private func clickDownJS(_ sel: String) -> String {
        "(function(){var e=document.querySelector('\(esc(sel))');if(!e)return JSON.stringify({ok:false});" +
        "var r=e.getBoundingClientRect();" +
        "var x=r.left+r.width/2+(Math.random()-0.5)*r.width*0.4;" +
        "var y=r.top+r.height/2+(Math.random()-0.5)*r.height*0.4;" +
        "e.focus();" +
        "e.dispatchEvent(new MouseEvent('mousedown',{bubbles:true,cancelable:true,clientX:x,clientY:y,button:0}));" +
        "e.setAttribute('data-xh-down',Math.round(x)+','+Math.round(y));" +
        "return JSON.stringify({ok:true,x:Math.round(x),y:Math.round(y)});})()"
    }
    private func clickUpJS() -> String {
        "(function(){var e=document.querySelector('[data-xh-down]');if(!e)return false;" +
        "var p=e.getAttribute('data-xh-down').split(',');var x=+p[0],y=+p[1];" +
        "e.removeAttribute('data-xh-down');" +
        "e.dispatchEvent(new MouseEvent('mouseup',{bubbles:true,cancelable:true,clientX:x,clientY:y,button:0}));" +
        "e.dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true,clientX:x,clientY:y,button:0}));return true;})()"
    }

    private func fillJS(_ sel: String, _ val: String) -> String {
        // 原生 property setter 赋值（React/Vue 受控组件可感知），保留焦点
        "(function(){var e=document.querySelector('\(esc(sel))');if(!e)return false;" +
        "e.focus();e.select();" +
        "var proto=e.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:(e.tagName==='SELECT'?HTMLSelectElement.prototype:HTMLInputElement.prototype);" +
        "var setter=Object.getOwnPropertyDescriptor(proto,'value').set;" +
        "setter.call(e,'\(esc(val))');" +
        "e.dispatchEvent(new InputEvent('beforeinput',{bubbles:true,inputType:'insertText',data:'\(esc(val))'}));" +
        "e.dispatchEvent(new Event('input',{bubbles:true}));" +
        "e.dispatchEvent(new Event('change',{bubbles:true}));" +
        "return true;})()"
    }

    /// ref 点击第一步（含 _blank 拦截）
    private func refClickDownJS(_ index: Int) -> String {
        let body = collectElsBody.replacingOccurrences(of: "LIMIT", with: "200")
        return "(function(){\(body)" +
            "var e=out[\(index)];if(!e)return JSON.stringify({ok:false,err:'ref @e\(index + 1) 不存在，请重新 snapshot'});" +
            blankTabGuardJS +
            "var r=e.getBoundingClientRect();" +
            "var x=r.left+r.width/2+(Math.random()-0.5)*r.width*0.4;" +
            "var y=r.top+r.height/2+(Math.random()-0.5)*r.height*0.4;" +
            "e.focus();" +
            "e.dispatchEvent(new MouseEvent('mousedown',{bubbles:true,cancelable:true,clientX:x,clientY:y,button:0}));" +
            "e.setAttribute('data-xh-down',Math.round(x)+','+Math.round(y));" +
            "return JSON.stringify({ok:true,x:Math.round(x),y:Math.round(y)});})()"
    }
    /// ref 点击第二步（共用 clickUpJS 标记语义，但返回 JSON 兼容）
    private func refClickUpJS() -> String {
        "(function(){var e=document.querySelector('[data-xh-down]');if(!e)return JSON.stringify({ok:false,err:'点击中断：未找到标记元素'});" +
        "var p=e.getAttribute('data-xh-down').split(',');var x=+p[0],y=+p[1];" +
        "e.removeAttribute('data-xh-down');" +
        "e.dispatchEvent(new MouseEvent('mouseup',{bubbles:true,cancelable:true,clientX:x,clientY:y,button:0}));" +
        "e.dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true,clientX:x,clientY:y,button:0}));" +
        "return JSON.stringify({ok:true});})()"
    }

    /// 按文本匹配第一步（贝壳「两居」类场景兜底；点击由 clickUpJS 完成）
    private func clickTextDownJS(_ text: String, _ index: Int) -> String {
        let body = collectElsBody.replacingOccurrences(of: "LIMIT", with: "200")
        return "(function(){\(body)" +
            "var hits=[];for(var i=0;i<out.length;i++){var e=out[i];" +
            "var t=(e.getAttribute('aria-label')||e.textContent||e.value||e.placeholder||'').trim();" +
            "if(t.indexOf('\(esc(text))')>=0||(e.placeholder||'').indexOf('\(esc(text))')>=0)hits.push(i);}" +
            "if(hits.length===0)return JSON.stringify({ok:false,err:'未找到文本: \(esc(text))'});" +
            "if(\(index)>=hits.length)return JSON.stringify({ok:false,err:'匹配到 '+hits.length+' 个，可用 index 指定，当前 index=\(index)'});" +
            "var e=out[hits[\(index)]];" +
            blankTabGuardJS +
            "var r=e.getBoundingClientRect();" +
            "var x=r.left+r.width/2+(Math.random()-0.5)*r.width*0.4;" +
            "var y=r.top+r.height/2+(Math.random()-0.5)*r.height*0.4;" +
            "e.focus();" +
            "e.dispatchEvent(new MouseEvent('mousedown',{bubbles:true,cancelable:true,clientX:x,clientY:y,button:0}));" +
            "e.setAttribute('data-xh-down',Math.round(x)+','+Math.round(y));" +
            "return JSON.stringify({ok:true,text:(e.getAttribute('aria-label')||e.textContent||e.value||e.placeholder||'').trim().slice(0,40),matches:hits.length,x:Math.round(x),y:Math.round(y)});})()"
    }

    // MARK: - 逐字符输入（拟人化）

    private func typeInitRefJS(_ index: Int) -> String {
        let body = collectElsBody.replacingOccurrences(of: "LIMIT", with: "200")
        return "(function(){\(body)var e=out[\(index)];if(!e)return false;e.setAttribute('data-xh-type','1');e.focus();return true;})()"
    }
    private func typeInitSelJS(_ sel: String) -> String {
        "(function(){var e=document.querySelector('\(esc(sel))');if(!e)return false;e.setAttribute('data-xh-type','1');e.focus();return true;})()"
    }
    private func typeCharJS(_ char: String) -> String {
        "(function(){var e=document.querySelector('[data-xh-type]');if(!e)return false;" +
        "var c='\(esc(char))';" +
        "e.dispatchEvent(new KeyboardEvent('keydown',{key:c,bubbles:true}));" +
        "e.dispatchEvent(new KeyboardEvent('keypress',{key:c,bubbles:true}));" +
        "e.dispatchEvent(new InputEvent('beforeinput',{bubbles:true,inputType:'insertText',data:c}));" +
        "e.value+=c;" +
        "e.dispatchEvent(new Event('input',{bubbles:true}));" +
        "e.dispatchEvent(new KeyboardEvent('keyup',{key:c,bubbles:true}));return true;})()"
    }
    private func typeEndJS() -> String {
        "(function(){var e=document.querySelector('[data-xh-type]');if(e){e.removeAttribute('data-xh-type');e.blur();}return true;})()"
    }

    /// type 回读当前输入值（标记移除前调用）
    private func typePeekValueJS() -> String {
        "(function(){var e=document.querySelector('[data-xh-type]');if(!e)return JSON.stringify({ok:false});" +
        "return JSON.stringify({ok:true,value:e.value!=null?e.value:''});})()"
    }
    private func pressJS(_ key: String) -> String {
        "(function(){var o={key:'\(esc(key))',bubbles:true};" +
        "var a=document.activeElement||document.body;" +
        "a.dispatchEvent(new KeyboardEvent('keydown',o));" +
        "a.dispatchEvent(new KeyboardEvent('keypress',o));" +
        "a.dispatchEvent(new KeyboardEvent('keyup',o));return true;})()"
    }
    private func selectJS(_ sel: String, _ val: String) -> String {
        "(function(){var e=document.querySelector('\(esc(sel))');if(!e)return false;" +
        "e.focus();e.value='\(esc(val))';" +
        "e.dispatchEvent(new Event('input',{bubbles:true}));" +
        "e.dispatchEvent(new Event('change',{bubbles:true}));" +
        "e.blur();return true;})()"
    }
    private func checkJS(_ sel: String, _ checked: Bool) -> String {
        // 仿真实用户：原生 el.click()（可信事件+默认 toggle），当前状态与目标不符才点击
        "(function(){var e=document.querySelector('\(esc(sel))');if(!e)return false;" +
        "e.focus();" +
        "if(!!e.checked!==\(checked))e.click();" +
        "e.dispatchEvent(new Event('change',{bubbles:true}));" +
        "return true;})()"
    }

    private func esc(_ s: String) -> String { s.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "'", with: "\\'") }

    // MARK: - Static helpers

    private func waitForSelector(session: TabSession, selector: String, timeoutMs: Int) async -> LocalToolResult {
        let start = Date()
        while Date().timeIntervalSince(start) * 1000 < Double(timeoutMs) {
            if await session.jsBool("document.querySelector('\(esc(selector))') !== null").ok {
                return .init(serverResult: Self.jsonDict(["found": true]), serverError: nil, clientAction: nil)
            }
            try? await Task.sleep(for: .milliseconds(200))
        }
        return .init(serverResult: nil, serverError: "选择器超时: \(selector)", clientAction: nil)
    }

    private static func jsonDict(_ dict: [String: Any]) -> Data {
        (try? JSONSerialization.data(withJSONObject: dict)) ?? Data("{}".utf8)
    }

    private func strProp(_ desc: String) -> [String: Any] { ["type": "string", "description": desc] }
    private var intProp: [String: Any] { ["type": "integer", "description": "像素值"] }
    private func numProp(_ desc: String) -> [String: Any] { ["type": "number", "description": desc] }
    private func objProps(_ props: [String: [String: Any]]) -> [String: Any] { ["type": "object", "properties": props] }
}
