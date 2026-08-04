#if os(macOS)
import AppKit
import ApplicationServices
import CoreGraphics
import Foundation
import WebKit

/// CGEvent 真实输入事件工具（macOS）。
/// 用于 JS 合成事件失效的场景（拖拽、表单提交、方向键等浏览器默认行为）。
/// 需要「辅助功能」权限；事件是系统级注入，注入前必须将目标窗口置于前台。
enum CGEventInput {
    /// 是否已获得辅助功能权限
    static func isTrusted() -> Bool {
        AXIsProcessTrusted()
    }

    /// 触发系统授权弹窗（系统设置 → 隐私与安全 → 辅助功能）
    @discardableResult
    static func requestPermission() -> Bool {
        let options = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true] as CFDictionary
        return AXIsProcessTrustedWithOptions(options)
    }

    /// 将页面 viewport 坐标（CSS 像素、左上原点）转换为 Quartz 全局屏幕坐标（左上原点）。
    /// 基准：webView.convertToScreen(bounds) 直接量出 WebView 在屏幕上的真实矩形（AppKit 左下原点），
    /// 绕开 convert(_:to: nil) 在 WKWebView 包装层级上的不可靠换算。
    /// 注意：忽略 WKWebView 缩放（allowsMagnification），默认 1.0。
    static func screenPoint(for viewportPoint: CGPoint, in webView: WKWebView) -> CGPoint? {
        guard let window = webView.window else {
            // Bad Case 17 诊断：webView 脱离窗口层级，输出上下文帮助定位根因
            let appWindows = NSApp.windows.map { w in
                "\(String(describing: w.title).prefix(30))(vis:\(w.isVisible),mini:\(w.isMiniaturized))"
            }
            let inAnyWindow = NSApp.windows.contains { w in
                guard let cv = w.contentView else { return false }
                return webView.isDescendant(of: cv)
            }
            LogBuffer.shared.warning("[CGEvent] screenPoint: webView has no window; inAnyWindow=\(inAnyWindow)")
            #if DEBUG
            print("[CGEvent] diag inAnyWindow=\(inAnyWindow) webViewSuper=\(String(describing: webView.superview))")
            #endif
            #if DEBUG
            print("[CGEvent] diag appWindows=[\(appWindows.joined(separator: ", "))]")
            #endif
            return nil
        }
        // 诊断预埋：WebView/窗口布局信息，用于核对转换基准
        #if DEBUG
        print("[CGEvent] diag webView.frame=(\(Int(webView.frame.minX)),\(Int(webView.frame.minY)),\(Int(webView.frame.width)),\(Int(webView.frame.height))) " +
              "bounds=(\(Int(webView.bounds.width)),\(Int(webView.bounds.height))) " +
              "window.frame=(\(Int(window.frame.minX)),\(Int(window.frame.minY)),\(Int(window.frame.width)),\(Int(window.frame.height)))")
        #endif

        // WebView 在屏幕上的真实矩形（AppKit 屏幕坐标，左下原点）
        // convert(bounds, to: nil) 得到 window base 坐标，window.convertToScreen 转屏幕坐标
        let rectInWindow = webView.convert(webView.bounds, to: nil)
        let webRect = window.convertToScreen(rectInWindow)
        #if DEBUG
        print("[CGEvent] webRectOnScreen=(minX:\(Int(webRect.minX)),minY:\(Int(webRect.minY)),maxX:\(Int(webRect.maxX)),maxY:\(Int(webRect.maxY)))")
        #endif

        // viewport（左上原点）→ AppKit 屏幕坐标（左下原点）：y 从底部数
        let screenPt = CGPoint(x: webRect.minX + viewportPoint.x, y: webRect.maxY - viewportPoint.y)

        // AppKit 屏幕坐标 → Quartz（左上原点）：所有屏的最高点做基准翻转
        let maxY = NSScreen.screens.map { $0.frame.maxY }.max() ?? 0
        let quartz = CGPoint(x: screenPt.x, y: maxY - screenPt.y)

        #if DEBUG
        print("[CGEvent] viewport=(\(Int(viewportPoint.x)),\(Int(viewportPoint.y))) " +
              "screenPt=(\(Int(screenPt.x)),\(Int(screenPt.y))) maxY=\(Int(maxY)) quartz=(\(Int(quartz.x)),\(Int(quartz.y)))")
        #endif
        return quartz
    }

    /// 校验 Quartz 全局坐标（左上原点）是否落在任一屏幕内（防止 macOS 越界 clamp 误触）
    static func isPointOnScreen(_ point: CGPoint) -> Bool {
        let maxY = NSScreen.screens.map { $0.frame.maxY }.max() ?? 0
        let appKitPoint = CGPoint(x: point.x, y: maxY - point.y)
        return NSScreen.screens.contains { $0.frame.contains(appKitPoint) }
    }

    /// 将目标窗口置于前台（CGEvent 系统级注入的前提），等待激活完成
    static func activateWindow(for webView: WKWebView) {
        NSApp.activate(ignoringOtherApps: true)
        webView.window?.makeKeyAndOrderFront(nil)
        Thread.sleep(forTimeInterval: 0.15)
    }

    // MARK: - 拟人化（反反爬）

    /// 随机延迟（毫秒区间），模拟人类操作节奏
    static func randomDelay(minMs: UInt32, maxMs: UInt32) {
        usleep(useconds_t(Int.random(in: Int(minMs * 1000)...Int(maxMs * 1000))))
    }

    /// 鼠标移动轨迹：二次贝塞尔 + 噪声抖动（拟人化，避免瞬移）
    static func trajectory(from: CGPoint, to: CGPoint, steps: Int) -> [CGPoint] {
        let dx = to.x - from.x
        let dy = to.y - from.y
        let dist = max(sqrt(dx * dx + dy * dy), 1)
        let nx = -dy / dist
        let ny = dx / dist
        // 控制点垂直于直线方向随机偏移（±20% 距离）
        let ctrlOffset = dist * 0.2 * CGFloat.random(in: -1...1)
        let cx = (from.x + to.x) / 2 + nx * ctrlOffset
        let cy = (from.y + to.y) / 2 + ny * ctrlOffset
        let jitter = dist * 0.01
        var pts: [CGPoint] = []
        for i in 0...steps {
            let t = CGFloat(i) / CGFloat(steps)
            let x = (1 - t) * (1 - t) * from.x + 2 * (1 - t) * t * cx + t * t * to.x
            let y = (1 - t) * (1 - t) * from.y + 2 * (1 - t) * t * cy + t * t * to.y
            pts.append(CGPoint(x: x + CGFloat.random(in: -jitter...jitter),
                               y: y + CGFloat.random(in: -jitter...jitter)))
        }
        return pts
    }

    /// ease-in-out 缓动插值系数（拖拽速度曲线：慢→快→慢）
    private static func easeInOut(_ t: CGFloat) -> CGFloat {
        t < 0.5 ? 2 * t * t : 1 - pow(-2 * t + 2, 2) / 2
    }

    // MARK: - 鼠标注入（拟人化）

    /// 真实左键点击：轨迹移动 → 按下（随机停留）→ 抬起
    static func click(at point: CGPoint) {
        guard let source = CGEventSource(stateID: .hidSystemState) else { return }
        let start = CGEvent(source: nil)?.location ?? point
        let dist = hypot(point.x - start.x, point.y - start.y)
        let steps = max(3, min(10, Int(dist / 60)))
        for p in trajectory(from: start, to: point, steps: steps) {
            CGEvent(mouseEventSource: source, mouseType: .mouseMoved, mouseCursorPosition: p, mouseButton: .left)?.post(tap: .cghidEventTap)
            randomDelay(minMs: 8, maxMs: 20)
        }
        CGEvent(mouseEventSource: source, mouseType: .leftMouseDown, mouseCursorPosition: point, mouseButton: .left)?.post(tap: .cghidEventTap)
        randomDelay(minMs: 60, maxMs: 150)
        CGEvent(mouseEventSource: source, mouseType: .leftMouseUp, mouseCursorPosition: point, mouseButton: .left)?.post(tap: .cghidEventTap)

        // 注入后交叉验证：读取实际鼠标位置（Quartz 坐标）与目标对比
        if let actual = CGEvent(source: nil)?.location {
            let delta = CGPoint(x: actual.x - point.x, y: actual.y - point.y)
            #if DEBUG
            print("[CGEvent] post-click mouse=(\(Int(actual.x)),\(Int(actual.y))) expected=(\(Int(point.x)),\(Int(point.y))) delta=(\(Int(delta.x)),\(Int(delta.y)))")
            #endif
        }
    }

    /// 真实拖拽：按下 → 曲线插值移动（随机间隔+抖动）→ 抬起
    static func drag(from: CGPoint, to: CGPoint, steps: Int) {
        let startTime = Date()
        guard let source = CGEventSource(stateID: .hidSystemState) else { return }
        let start = CGEvent(source: nil)?.location ?? from
        let dist = hypot(to.x - from.x, to.y - from.y)
        let approachSteps = max(3, min(10, Int(dist / 80)))
        for p in trajectory(from: start, to: from, steps: approachSteps) {
            CGEvent(mouseEventSource: source, mouseType: .mouseMoved, mouseCursorPosition: p, mouseButton: .left)?.post(tap: .cghidEventTap)
            randomDelay(minMs: 8, maxMs: 20)
        }
        CGEvent(mouseEventSource: source, mouseType: .leftMouseDown, mouseCursorPosition: from, mouseButton: .left)?.post(tap: .cghidEventTap)
        randomDelay(minMs: 60, maxMs: 120)

        let jitter = max(dist * 0.008, 1)
        for i in 1...steps {
            let t = easeInOut(CGFloat(i) / CGFloat(steps))
            let x = from.x + (to.x - from.x) * t
            let y = from.y + (to.y - from.y) * t
            let p = CGPoint(x: x + CGFloat.random(in: -jitter...jitter),
                            y: y + CGFloat.random(in: -jitter...jitter))
            CGEvent(mouseEventSource: source, mouseType: .leftMouseDragged, mouseCursorPosition: p, mouseButton: .left)?.post(tap: .cghidEventTap)
            randomDelay(minMs: 10, maxMs: 30)
        }
        CGEvent(mouseEventSource: source, mouseType: .leftMouseUp, mouseCursorPosition: to, mouseButton: .left)?.post(tap: .cghidEventTap)
        #if DEBUG
        print("[B-Drag] from=(\(Int(from.x)),\(Int(from.y))) to=(\(Int(to.x)),\(Int(to.y))) steps=\(steps) elapsed=\(Int(Date().timeIntervalSince(startTime) * 1000))ms")
        #endif
    }

    // MARK: - 键盘注入（真实默认行为：Enter 提交/方向键滚动）

    /// 特殊键 → 虚拟键码映射（真实键盘事件路径）
    static func keyCode(for key: String) -> CGKeyCode? {
        let map: [String: CGKeyCode] = [
            "Enter": 36, "Return": 36,
            "Tab": 48,
            "Escape": 53, "Esc": 53,
            "ArrowUp": 126, "Up": 126,
            "ArrowDown": 125, "Down": 125,
            "ArrowLeft": 123, "Left": 123,
            "ArrowRight": 124, "Right": 124,
            "Backspace": 51,
            "Delete": 117,
            "Space": 49,
            "Home": 115,
            "End": 119,
            "PageUp": 116,
            "PageDown": 121,
        ]
        if let code = map[key] { return code }
        return map[key.capitalized]
    }

    /// 真实键盘按键（keyDown → 随机间隔 → keyUp），触发浏览器默认行为
    static func pressKey(_ virtualKey: CGKeyCode) {
        let startTime = Date()
        guard let source = CGEventSource(stateID: .hidSystemState) else { return }
        CGEvent(keyboardEventSource: source, virtualKey: virtualKey, keyDown: true)?.post(tap: .cghidEventTap)
        let hold = Int.random(in: 40...90)
        usleep(useconds_t(hold * 1000))
        CGEvent(keyboardEventSource: source, virtualKey: virtualKey, keyDown: false)?.post(tap: .cghidEventTap)
        #if DEBUG
        print("[B-Press] vk=\(virtualKey) hold=\(hold)ms elapsed=\(Int(Date().timeIntervalSince(startTime) * 1000))ms")
        #endif
    }
}
#endif
