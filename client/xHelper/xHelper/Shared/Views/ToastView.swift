import SwiftUI

struct ToastModifier: ViewModifier {
    @ObservedObject private var center = ToastCenter.shared

    func body(content: Content) -> some View {
        content.overlay {
            if let msg = center.message {
                Text(msg)
                    .font(.callout)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 20)
                    .padding(.vertical, 12)
                    .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 14))
                    .padding(.horizontal, 40)
                    .allowsHitTesting(false)
                    .transition(.scale.combined(with: .opacity))
                    .task(id: msg) {
                        try? await Task.sleep(nanoseconds: 2_000_000_000)
                        if center.message == msg {
                            center.message = nil
                        }
                    }
            }
        }
        .animation(.easeInOut(duration: 0.2), value: center.message)
    }
}

extension View {
    func toast() -> some View {
        modifier(ToastModifier())
    }
}

#Preview {
    VStack {
        Button("Show Toast") {
            ToastCenter.shared.show("网络连接失败，请检查网络")
        }
    }
    .frame(width: 400, height: 300)
    .toast()
}
