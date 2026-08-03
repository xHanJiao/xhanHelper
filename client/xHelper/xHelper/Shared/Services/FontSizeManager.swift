import Combine
import SwiftUI

@MainActor
final class FontSizeManager: ObservableObject {
    static let shared = FontSizeManager()

    static let minScale: Double = 0.7
    static let maxScale: Double = 1.5
    static let step: Double = 0.05
    static let baseSize: CGFloat = 17.0

    /// 紧凑系数：shared 为 1.0，PiP 面板为 0.75。
    let factor: Double
    private let persist: Bool
    private var sharedCancellable: AnyCancellable?

    @Published var scale: Double {
        didSet {
            guard persist else { return }
            UserDefaults.standard.set(scale, forKey: "chat_font_scale")
        }
    }

    private init(factor: Double = 1.0, persist: Bool = true) {
        self.factor = factor
        self.persist = persist
        let stored = UserDefaults.standard.double(forKey: "chat_font_scale")
        scale = stored >= Self.minScale && stored <= Self.maxScale ? stored : 1.0
    }

    /// PiP 面板紧凑字号：缩放全局字号 ×0.75，不持久化，订阅共享 instance 跟随 Cmd +/-。
    static func compact() -> FontSizeManager {
        let compact = FontSizeManager(factor: Layout.floatingPanelCompactFontFactor, persist: false)
        compact.follow(shared)
        pipLog("compact font: global=\(shared.scale) effective=\(compact.scaledSize) baseSize=\(Self.baseSize)")
        return compact
    }

    /// 跟随全局字号：面板里按 Cmd +/- 也会按比例缩放。
    private func follow(_ source: FontSizeManager) {
        sharedCancellable = source.$scale
            .receive(on: DispatchQueue.main)
            .sink { [weak self] newScale in
                self?.scale = newScale
            }
    }

    var scaledSize: CGFloat {
        Self.baseSize * CGFloat(factor) * CGFloat(scale)
    }

    func increase() {
        let newScale = min(Self.maxScale, (scale + Self.step).rounded(toPlaces: 2))
        guard newScale != scale else { return }
        scale = newScale
        ToastCenter.shared.show("字体: \(Int((scale * 100).rounded()))%")
    }

    func decrease() {
        let newScale = max(Self.minScale, (scale - Self.step).rounded(toPlaces: 2))
        guard newScale != scale else { return }
        scale = newScale
        ToastCenter.shared.show("字体: \(Int((scale * 100).rounded()))%")
    }
}

private extension Double {
    func rounded(toPlaces places: Int) -> Double {
        let divisor = pow(10.0, Double(places))
        return (self * divisor).rounded() / divisor
    }
}
