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
            LocalToolSchema(name: "agent_browser_read",             description: "读取页面纯文本内容",                     inputSchema: objProps([:])),
            LocalToolSchema(name: "agent_browser_snapshot",         description: "获取页面可访问性快照",                   inputSchema: objProps([:])),
            LocalToolSchema(name: "agent_browser_get_text",         description: "获取页面可见文本",                       inputSchema: objProps([:])),
            LocalToolSchema(name: "agent_browser_get_url",          description: "获取当前页面 URL",                      inputSchema: objProps([:])),
            LocalToolSchema(name: "agent_browser_get_title",        description: "获取当前页面标题",                       inputSchema: objProps([:])),
            LocalToolSchema(name: "agent_browser_click",            description: "点击指定选择器的元素",                   inputSchema: objProps(["selector": strProp("CSS选择器")])),
            LocalToolSchema(name: "agent_browser_fill",             description: "填充表单字段",                         inputSchema: objProps(["selector": strProp("CSS选择器"), "value": strProp("填充值")])),
            LocalToolSchema(name: "agent_browser_type",             description: "在元素中输入文本",                       inputSchema: objProps(["selector": strProp("CSS选择器"), "text": strProp("输入文本")])),
            LocalToolSchema(name: "agent_browser_press",            description: "按下键盘按键",                         inputSchema: objProps(["key": strProp("按键名称")])),
            LocalToolSchema(name: "agent_browser_select",           description: "选择下拉选项",                         inputSchema: objProps(["selector": strProp("CSS选择器"), "value": strProp("选项值")])),
            LocalToolSchema(name: "agent_browser_check",            description: "勾选复选框",                           inputSchema: objProps(["selector": strProp("CSS选择器")])),
            LocalToolSchema(name: "agent_browser_uncheck",          description: "取消勾选复选框",                         inputSchema: objProps(["selector": strProp("CSS选择器")])),
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
                let (text, _) = await session.jsResult("document.body?.innerText ?? ''")
                return .init(serverResult: Self.jsonDict(["content": String((text ?? "").prefix(maxResultChars))]), serverError: nil, clientAction: nil)
            case "agent_browser_get_text":
                let (text, _) = await session.jsResult("document.body?.innerText ?? ''")
                return .init(serverResult: Self.jsonDict(["text": String((text ?? "").prefix(maxResultChars))]), serverError: nil, clientAction: nil)
            case "agent_browser_snapshot":
                let (html, _) = await session.jsResult(snapshotJS)
                let budget = context.budgetBytes ?? 16000
                return .init(serverResult: Self.jsonDict(["snapshot": String((html ?? "").prefix(budget))]), serverError: nil, clientAction: nil)
            case "agent_browser_scroll":
                let x = (arguments["x"] as? Int) ?? 0
                let y = (arguments["y"] as? Int) ?? 0
                _ = await session.jsResult("window.scrollBy(\(x), \(y))")
                return .init(serverResult: Self.jsonDict(["scrolled": true]), serverError: nil, clientAction: nil)
            case "agent_browser_wait_for_load":
                let timeout = (arguments["timeout_ms"] as? Int) ?? 10000
                return await session.waitForLoad(timeoutMs: timeout)
            case "agent_browser_wait_for_selector":
                let sel = arguments["selector"] as? String ?? ""
                let timeout = (arguments["timeout_ms"] as? Int) ?? 5000
                return await waitForSelector(session: session, selector: sel, timeoutMs: timeout)
            case "agent_browser_go_back":
                session.webView.goBack()
                return .init(serverResult: Self.jsonDict(["back": true]), serverError: nil, clientAction: nil)
            case "agent_browser_go_forward":
                if session.webView.canGoForward { session.webView.goForward() }
                return .init(serverResult: Self.jsonDict(["forward": session.webView.canGoForward]), serverError: nil, clientAction: nil)
#if DEBUG
            case "agent_browser_execute_script":
                let script = arguments["script"] as? String ?? ""
                return await session.eval(script)
#endif
            case "agent_browser_click":
                let sel = arguments["selector"] as? String ?? ""
                let (ok, jsErr) = await session.jsBool(clickJS(sel))
                return ok ? .init(serverResult: Self.jsonDict(["clicked": true]), serverError: nil, clientAction: nil)
                          : .init(serverResult: nil, serverError: jsErr ?? "未找到元素: \(sel)", clientAction: nil)
            case "agent_browser_fill":
                let sel = arguments["selector"] as? String ?? ""
                let val = arguments["value"] as? String ?? ""
                let (ok, jsErr) = await session.jsBool(fillJS(sel, val))
                return ok ? .init(serverResult: Self.jsonDict(["filled": true]), serverError: nil, clientAction: nil)
                          : .init(serverResult: nil, serverError: jsErr ?? "未找到元素: \(sel)", clientAction: nil)
            case "agent_browser_type":
                let sel = arguments["selector"] as? String ?? ""
                let val = arguments["text"] as? String ?? ""
                let (ok, jsErr) = await session.jsBool(typeJS(sel, val))
                return ok ? .init(serverResult: Self.jsonDict(["typed": true]), serverError: nil, clientAction: nil)
                          : .init(serverResult: nil, serverError: jsErr ?? "未找到元素: \(sel)", clientAction: nil)
            case "agent_browser_press":
                let key = arguments["key"] as? String ?? ""
                let (_, jsErr) = await session.jsBool(pressJS(key))
                if let e = jsErr {
                    return .init(serverResult: nil, serverError: "press失败: \(e)", clientAction: nil)
                }
                return .init(serverResult: Self.jsonDict(["pressed": true]), serverError: nil, clientAction: nil)
            case "agent_browser_select":
                let sel = arguments["selector"] as? String ?? ""
                let val = arguments["value"] as? String ?? ""
                let (ok, jsErr) = await session.jsBool(selectJS(sel, val))
                return ok ? .init(serverResult: Self.jsonDict(["selected": true]), serverError: nil, clientAction: nil)
                          : .init(serverResult: nil, serverError: jsErr ?? "未找到元素: \(sel)", clientAction: nil)
            case "agent_browser_check", "agent_browser_uncheck":
                let sel = arguments["selector"] as? String ?? ""
                let checked = tool == "agent_browser_check"
                let (ok, jsErr) = await session.jsBool(checkJS(sel, checked))
                return ok ? .init(serverResult: Self.jsonDict(["checked": checked]), serverError: nil, clientAction: nil)
                          : .init(serverResult: nil, serverError: jsErr ?? "未找到元素: \(sel)", clientAction: nil)
            default:
                return .init(serverResult: nil, serverError: "unknown tool: \(tool)", clientAction: nil)
            }
        }
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
        }

        func waitForLoad(timeoutMs: Int) async -> LocalToolResult {
            cancelLoadWait()
            let errorMsg = await withCheckedContinuation { (cont: CheckedContinuation<String?, Never>) in
                loadContinuation = cont
                loadTimeout = Task {
                    try? await Task.sleep(for: .milliseconds(timeoutMs))
                    guard !Task.isCancelled else { return }
                    cont.resume(returning: "timeout")
                    loadContinuation = nil
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

        nonisolated func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
            Task { @MainActor in
                loadTimeout?.cancel()
                loadContinuation?.resume(returning: nil)
                loadContinuation = nil
            }
        }

        nonisolated func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
            Task { @MainActor in
                loadTimeout?.cancel()
                loadContinuation?.resume(returning: (error as NSError).code == NSURLErrorCancelled ? nil : error.localizedDescription)
                loadContinuation = nil
            }
        }

        nonisolated func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
            Task { @MainActor in
                loadTimeout?.cancel()
                let code = (error as NSError).code
                loadContinuation?.resume(returning: code == NSURLErrorCancelled ? nil : "加载失败: \(error.localizedDescription)")
                loadContinuation = nil
            }
        }
    }

    // MARK: - JS string builders

    private let snapshotJS = """
    (function() {
        var a=document.body;if(!a)return '';
        function t(e,d){var r=[];for(var c=e.firstChild;c;c=c.nextSibling){
            if(c.nodeType===3){var v=c.textContent.trim();if(v)r.push(v);continue;}
            if(c.nodeType!==1)continue;
            var n=c.tagName.toLowerCase();var attrs='';
            var id=c.id?'#'+c.id:'';
            var cls=c.className&&typeof c.className==='string'?'.'+c.className.trim().split(/\\s+/).join('.'):'';
            var role=c.getAttribute('role')||'';
            var label=c.getAttribute('aria-label')||'';
            if(role)attrs+='[role='+role+']';
            if(label)attrs+='[aria-label='+JSON.stringify(label)+']';
            r.push('<'+n+id+cls+attrs+'>'+t(c,d+1)+'</'+n+'>');
        }
        return r.join('');
    }
    return t(document.body,0);
    })()
    """

    private func clickJS(_ sel: String) -> String {
        "(function(){var e=document.querySelector('\(esc(sel))');if(!e)return false;" +
        "var r=e.getBoundingClientRect();var x=r.left+r.width/2;var y=r.top+r.height/2;" +
        "var o={bubbles:true,cancelable:true,clientX:x,clientY:y,button:0};" +
        "e.focus();e.dispatchEvent(new MouseEvent('mousedown',o));" +
        "e.dispatchEvent(new MouseEvent('mouseup',o));" +
        "e.dispatchEvent(new MouseEvent('click',o));return true;})()"
    }
    private func fillJS(_ sel: String, _ val: String) -> String {
        "(function(){var e=document.querySelector('\(esc(sel))');if(!e)return false;" +
        "e.focus();e.select();" +
        "e.dispatchEvent(new InputEvent('beforeinput',{bubbles:true,inputType:'insertText',data:'\(esc(val))'}));" +
        "e.value='\(esc(val))';" +
        "e.dispatchEvent(new Event('input',{bubbles:true}));" +
        "e.dispatchEvent(new Event('change',{bubbles:true}));" +
        "e.blur();return true;})()"
    }
    private func typeJS(_ sel: String, _ text: String) -> String {
        "(function(){var e=document.querySelector('\(esc(sel))');if(!e)return false;e.focus();" +
        "var chars='\(esc(text))'.split('');" +
        "for(var i=0;i<chars.length;i++){" +
        "var c=chars[i];" +
        "e.dispatchEvent(new KeyboardEvent('keydown',{key:c,bubbles:true}));" +
        "e.dispatchEvent(new KeyboardEvent('keypress',{key:c,bubbles:true}));" +
        "e.dispatchEvent(new InputEvent('beforeinput',{bubbles:true,inputType:'insertText',data:c}));" +
        "e.value+=c;" +
        "e.dispatchEvent(new Event('input',{bubbles:true}));" +
        "e.dispatchEvent(new KeyboardEvent('keyup',{key:c,bubbles:true}));" +
        "}" +
        "e.blur();return true;})()"
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
        "(function(){var e=document.querySelector('\(esc(sel))');if(!e)return false;" +
        "e.focus();e.checked=\(checked);" +
        "e.dispatchEvent(new MouseEvent('click',{bubbles:true}));" +
        "e.dispatchEvent(new Event('change',{bubbles:true}));" +
        "e.blur();return true;})()"
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
