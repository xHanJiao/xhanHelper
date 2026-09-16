import Combine
import SwiftUI

/// 应用内大图浮层。macOS 的 sheet 是窗口模态、iOS 的 sheet 是卡片，
/// 点外部都不会关，也没有开关能改——所以不用 sheet，改用窗口级浮层。
@MainActor
final class LightboxCenter: ObservableObject {
    static let shared = LightboxCenter()

    @Published var content: AnyView?

    private init() {}

    func present<V: View>(@ViewBuilder _ content: () -> V) {
        self.content = AnyView(content())
    }

    func dismiss() {
        content = nil
    }
}

struct LightboxModifier: ViewModifier {
    @ObservedObject private var center = LightboxCenter.shared

    func body(content: Content) -> some View {
        content.overlay {
            if let lightbox = center.content {
                ZStack {
                    // 点图外即关；图片之外的留白也是这层，所以点哪都能关
                    Color.black.opacity(0.5)
                        .ignoresSafeArea()
                        .onTapGesture { center.dismiss() }

                    lightbox
                }
            }
        }
    }
}

extension View {
    func lightbox() -> some View {
        modifier(LightboxModifier())
    }
}
