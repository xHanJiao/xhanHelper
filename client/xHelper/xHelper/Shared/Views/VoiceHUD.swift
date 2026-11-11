#if os(macOS)
import SwiftUI
import AppKit
// 声明 ObservableObject / @Published 的文件必须显式 import Combine：
// 只 import SwiftUI 在 swiftc -typecheck 下能过，Xcode 真编会报「missing import of defining module」
import Combine

/// 全局按住说话的浮层内容：一块玻璃胶囊 + 中间那条语音波形。
///
/// 波形就是输入框语音条里的同一份 `VoiceWaveLine`（听 / 想 / 说三态同一套参数），
/// 这里**不显示任何文字**：只回答「有人在听 / 在想 / 在说」。
/// 面板本身由 `VoiceHotKeyController` 挂在 NSPanel 上（不吃鼠标、不抢焦点）。
struct VoiceHUDContent: View {
    let phase: VoiceWaveLine.Phase
    let level: () -> Double
    /// 胶囊宽（高度固定）。等于 `height` 时就是正圆
    var width: CGFloat = VoiceHUDContent.defaultWidth
    /// 玻璃透度（1 = 系统默认，越小越透）。定稿 0.7：能透出底下的桌面/网页，又不至于糊成一片
    static let glassAlpha: CGFloat = 0.7

    /// 胶囊高（波形 36 + 上下各 10）；圆角取一半，正圆头
    static let height: CGFloat = 56
    /// 默认宽（定稿：矮胖型 76 × 56）
    static let defaultWidth: CGFloat = 76

    /// 左右留白：正圆时收窄，否则波形会顶到边
    private var hPad: CGFloat { width <= 64 ? 12 : 18 }

    var body: some View {
        VoiceWaveLine(phase: phase, level: level)
            .frame(width: max(24, width - hPad * 2))
            .padding(.horizontal, hPad)
            .padding(.vertical, 10)
            .background { HUDGlass(cornerRadius: Self.height / 2, alpha: Self.glassAlpha) }
            .overlay(Capsule().strokeBorder(.white.opacity(0.18), lineWidth: 0.5))
            // 浮层里没有可点的东西；不参与命中测试，鼠标落到底下的窗口上，
            // `isMovableByWindowBackground` 才能把整个胶囊当把手拖动
            .allowsHitTesting(false)
            // 纯装饰：读屏没必要念它（真的语音状态在聊天窗里有无障碍标签）
            .accessibilityHidden(true)
    }
}

/// 真玻璃底。macOS 26 上 `NSVisualEffectView` 的 `.behindWindow` 已经不再采样窗口后面的桌面
/// （实测各种 material 都是不透明实心灰），系统自己的玻璃换成了 `NSGlassEffectView`
/// （Liquid Glass）。这里用它的 `.clear` 档 + 清空 tint，再降一点整体不透明度，
/// 才能透出底下的桌面/网页。
private struct HUDGlass: NSViewRepresentable {
    let cornerRadius: CGFloat
    let alpha: CGFloat

    func makeNSView(context: Context) -> NSView {
        if #available(macOS 26.0, *) {
            let glass = NSGlassEffectView()
            glass.style = .clear
            glass.cornerRadius = cornerRadius
            glass.tintColor = .clear
            glass.alphaValue = alpha
            return glass
        }
        let fallback = NSVisualEffectView()
        fallback.blendingMode = .behindWindow
        fallback.material = .underWindowBackground
        fallback.state = .active
        fallback.wantsLayer = true
        fallback.layer?.cornerRadius = cornerRadius
        fallback.layer?.cornerCurve = .continuous
        fallback.layer?.masksToBounds = true
        fallback.alphaValue = alpha
        return fallback
    }

    func updateNSView(_ view: NSView, context: Context) {
        if #available(macOS 26.0, *), let glass = view as? NSGlassEffectView {
            glass.cornerRadius = cornerRadius
            glass.alphaValue = alpha
        } else {
            view.layer?.cornerRadius = cornerRadius
            view.alphaValue = alpha
        }
    }
}

/// 浮层窗口：可拖、不抢焦点、跨全屏 Space 常驻。位置记住上次拖到的地方（没拖过就底部居中）。
@MainActor
final class VoiceHUDPanel {
    final class PhaseState: ObservableObject {
        @Published var phase: VoiceWaveLine.Phase = .listening
    }

    /// 用户拖到的位置（窗口 origin，全局坐标）。没存过就用默认位置
    private static let originXKey = "voiceHUD.originX"
    private static let originYKey = "voiceHUD.originY"

    private let state = PhaseState()
    private let level: () -> Double
    private var panel: NSPanel?
    private var idleSince: Date?
    private let linger: TimeInterval = 1.2
    /// 最近一次由程序摆到的位置：`didMove` 里和它相同就说明不是用户拖的，别存
    private var placedOrigin: NSPoint?
    private var moveObserver: NSObjectProtocol?

    init(level: @escaping () -> Double) {
        self.level = level
    }

    var isVisible: Bool { panel?.isVisible == true }

    func show(phase: VoiceWaveLine.Phase) {
        state.phase = phase
        idleSince = nil
        if let panel {
            place(panel)
            panel.orderFrontRegardless()
            return
        }
        let size = NSSize(width: VoiceHUDContent.defaultWidth, height: VoiceHUDContent.height)
        let panel = NSPanel(
            contentRect: NSRect(origin: .zero, size: size),
            styleMask: [.borderless, .nonactivatingPanel],
            backing: .buffered,
            defer: false
        )
        panel.isOpaque = false
        panel.backgroundColor = .clear
        panel.hasShadow = false // 投影由 SwiftUI 画，避免系统再加一层方影
        panel.level = .statusBar
        panel.isFloatingPanel = true
        panel.hidesOnDeactivate = false
        // 可拖：整块胶囊当把手（内容不参与命中测试，见 VoiceHUDContent）
        panel.ignoresMouseEvents = false
        panel.isMovableByWindowBackground = true
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]

        let host = NSHostingView(rootView: HUDRoot(state: state, level: level))
        host.wantsLayer = true
        host.layer?.backgroundColor = NSColor.clear.cgColor
        panel.contentView = host

        place(panel)

        self.panel = panel
        // 拖完记住位置
        moveObserver = NotificationCenter.default.addObserver(
            forName: NSWindow.didMoveNotification, object: panel, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated { self?.savePosition() }
        }
        panel.orderFrontRegardless()
    }

    /// 有存过的位置就用它（钳进屏幕可视区），否则默认鼠标所在屏的底部居中
    private func place(_ panel: NSPanel) {
        let size = panel.frame.size
        let defaults = UserDefaults.standard
        if defaults.object(forKey: Self.originXKey) != nil {
            let saved = NSPoint(x: defaults.double(forKey: Self.originXKey),
                                y: defaults.double(forKey: Self.originYKey))
            let rect = NSRect(origin: saved, size: size)
            if let screen = NSScreen.screens.first(where: { $0.visibleFrame.intersects(rect) }) {
                let frame = screen.visibleFrame
                panel.setFrameOrigin(NSPoint(
                    x: min(max(saved.x, frame.minX), frame.maxX - size.width),
                    y: min(max(saved.y, frame.minY), frame.maxY - size.height)
                ))
                placedOrigin = panel.frame.origin
                return
            }
        }
        let screen = NSScreen.screens.first { $0.frame.contains(NSEvent.mouseLocation) } ?? NSScreen.main
        let visible = screen?.visibleFrame ?? .zero
        panel.setFrameOrigin(NSPoint(x: visible.midX - size.width / 2, y: visible.minY + 96))
        placedOrigin = panel.frame.origin
    }

    private func savePosition() {
        guard let panel, panel.frame.origin != placedOrigin else { return }
        let defaults = UserDefaults.standard
        defaults.set(panel.frame.origin.x, forKey: Self.originXKey)
        defaults.set(panel.frame.origin.y, forKey: Self.originYKey)
    }

    func setPhase(_ phase: VoiceWaveLine.Phase) {
        idleSince = nil
        if state.phase != phase { state.phase = phase }
    }

    /// 这一轮结束了：停留一会儿再收，别让浮层闪一下就消失
    func idle(now: Date) {
        if idleSince == nil { idleSince = now }
        if now.timeIntervalSince(idleSince ?? now) >= linger { hide() }
    }

    func hide() {
        // 只收起来，不销毁：下次按下要立刻出画面，重建窗口 + NSHostingView + 玻璃视图
        // 要几十毫秒，正是「按下去要等一下」的来源
        panel?.orderOut(nil)
        idleSince = nil
    }
}

private struct HUDRoot: View {
    @ObservedObject var state: VoiceHUDPanel.PhaseState
    let level: () -> Double

    var body: some View {
        VoiceHUDContent(phase: state.phase, level: level)
    }
}
#endif
