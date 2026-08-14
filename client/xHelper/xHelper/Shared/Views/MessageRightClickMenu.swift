import SwiftUI
#if os(macOS)
import AppKit
#endif

#if os(macOS)

/// 行帧注册表（非响应式）：写字典不触发 SwiftUI 重算，滚动高频更新无卡顿。
/// 行矩形互不重叠，命中测试唯一。
final class MessageRowFrameRegistry {
    struct Entry {
        let id: String
        let role: String
        let content: String
        var frame: CGRect
    }

    /// 坐标锚点：挂在 ScrollView 上的透明 NSView，用于把事件窗口坐标换算成 chatList 空间坐标
    weak var anchor: NSView?

    private var entries: [String: Entry] = [:]

    func update(id: String, role: String, content: String, frame: CGRect) {
        if var e = entries[id] {
            e.frame = frame
            entries[id] = e
        } else {
            entries[id] = Entry(id: id, role: role, content: content, frame: frame)
        }
    }

    func remove(id: String) {
        entries.removeValue(forKey: id)
    }

    /// 命中测试：点包含于某行矩形即命中
    func hitTest(_ point: CGPoint) -> Entry? {
        for e in entries.values where e.frame.contains(point) {
            return e
        }
        return nil
    }
}

/// 坐标锚点视图：挂在 ScrollView 上占满其尺寸，几何与 chatList 命名空间原点一致。
/// 提供 NSView 引用供 convert 换算（AppKit 几何，不感知 scaleEffect 渲染变换）。
struct CoordinateAnchor: NSViewRepresentable {
    let onReady: (NSView) -> Void

    func makeNSView(context: Context) -> NSView {
        let v = NSView()
        onReady(v)
        return v
    }

    func updateNSView(_ nsView: NSView, context: Context) {}
}

/// NSMenuItem 闭包 action 载体（NSMenuItem.target 为弱引用，需强持有）
private final class MenuActionTarget: NSObject {
    private let handler: () -> Void
    init(_ handler: @escaping () -> Void) { self.handler = handler }
    @objc func run() { handler() }
}

/// 右键监视器：拦截气泡行上的 rightMouseDown，弹统一自定义菜单（调试/拷贝/分享）。
/// 解决 selectable text 的系统菜单抢占 SwiftUI contextMenu 的冲突；
/// 未命中气泡行时原样放行。
///
/// 坐标换算全程走布局几何：事件窗口坐标 → 锚点 convert → chatList 命名空间。
/// 列表的 scaleEffect 翻转只是渲染变换，不影响几何，因此反转列表下命中稳定。
final class MessageRightClickMenu: NSObject {
    var onDebug: ((String) -> Void)?  // messageID
    var onCopy: ((String) -> Void)?   // content
    var onShare: (() -> Void)?
    var isSelectionMode: (() -> Bool)? // 分享选择模式下放行

    let registry = MessageRowFrameRegistry()
    private var monitor: Any?
    private var retainedTargets: [MenuActionTarget] = []
    private var menuClosedAt = Date.distantPast

    func install() {
        guard monitor == nil else { return }
        monitor = NSEvent.addLocalMonitorForEvents(matching: [.rightMouseDown]) { [weak self] event in
            self?.handle(event) ?? event
        }
    }

    func uninstall() {
        if let monitor {
            NSEvent.removeMonitor(monitor)
        }
        monitor = nil
    }

    private func handle(_ event: NSEvent) -> NSEvent? {
        // 菜单刚关闭：吞掉 AppKit re-dispatch 的原始 rightMouseDown，防系统菜单再弹
        // （点菜单项关闭时事件被消费不 re-dispatch；点外面取消时会 re-dispatch）
        if event.type == .rightMouseDown,
           Date().timeIntervalSince(menuClosedAt) < 0.3 {
            return nil
        }
        guard isSelectionMode?() != true,
              let window = event.window,
              let anchor = registry.anchor,
              anchor.window === window else { return event }

        // 窗口坐标 → 锚点局部（左下原点）→ chatList 空间（左上原点，y 向下）
        let local = anchor.convert(event.locationInWindow, from: nil)
        let point = CGPoint(x: local.x, y: anchor.bounds.height - local.y)

        guard let entry = registry.hitTest(point) else { return event }
        popMenu(for: entry, event: event)
        return nil
    }

    private func popMenu(for entry: MessageRowFrameRegistry.Entry, event: NSEvent) {
        let menu = NSMenu()
        retainedTargets.removeAll()

        if entry.role == "user", entry.id != "__streaming__", let onDebug {
            menu.addItem(makeItem("调试", symbol: "ladybug.fill") { onDebug(entry.id) })
        }
        if let onCopy {
            menu.addItem(makeItem("拷贝", symbol: "doc.on.doc") { onCopy(entry.content) })
        }
        if let onShare {
            menu.addItem(makeItem("分享", symbol: "square.and.arrow.up") { onShare() })
        }
        guard menu.items.count > 0, let anchor = registry.anchor else { return }
        // 用坐标定位而非传原始事件：popUpContextMenu 会把 rightMouseDown 传进菜单，
        // 关闭后 re-dispatch 到视图再弹系统菜单；popUp(positioning:) 定位更干净
        let pos = anchor.convert(event.locationInWindow, from: nil)
        menu.popUp(positioning: nil, at: pos, in: anchor)
        // 菜单已关闭：标记时间，handle 内吞掉 re-dispatch 的原始右键
        menuClosedAt = Date()
    }

    private func makeItem(_ title: String, symbol: String, action: @escaping () -> Void) -> NSMenuItem {
        let target = MenuActionTarget(action)
        retainedTargets.append(target)
        let item = NSMenuItem(title: title, action: #selector(MenuActionTarget.run), keyEquivalent: "")
        item.target = target
        if let img = NSImage(systemSymbolName: symbol, accessibilityDescription: nil) {
            item.image = img
        }
        return item
    }
}

#endif
