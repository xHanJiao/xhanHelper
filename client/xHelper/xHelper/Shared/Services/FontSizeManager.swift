import Combine
import SwiftUI

@MainActor
final class FontSizeManager: ObservableObject {
    static let shared = FontSizeManager()

    static let minScale: Double = 0.7
    static let maxScale: Double = 1.5
    static let step: Double = 0.05
    static let baseSize: CGFloat = 17.0

    @Published var scale: Double = {
        let v = UserDefaults.standard.double(forKey: "chat_font_scale")
        return v >= minScale && v <= maxScale ? v : 1.0
    }() {
        didSet { UserDefaults.standard.set(scale, forKey: "chat_font_scale") }
    }

    var scaledSize: CGFloat { Self.baseSize * scale }

    private init() {}

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
