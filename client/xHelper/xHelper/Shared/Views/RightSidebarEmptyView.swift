import SwiftUI

struct RightSidebarEmptyView: View {
    let onOpenBrowser: () -> Void
    @State private var isHovered = false

    var body: some View {
        VStack(spacing: 24) {
            Spacer()

            Button(action: onOpenBrowser) {
                VStack(spacing: 8) {
                    Image(systemName: "globe")
                        .font(.system(size: 36))
                        .foregroundColor(.accentColor)
                        .frame(width: 64, height: 64)
                        .background(.quaternary.opacity(isHovered ? 0.8 : 0.4), in: Circle())
                        .scaleEffect(isHovered ? 1.05 : 1.0)
                        .animation(.easeInOut(duration: 0.15), value: isHovered)

                    Text("打开浏览器")
                        .font(.system(size: 13, weight: .medium))
                        .foregroundColor(.primary)

                    Text("可在浏览器中先登录账号")
                        .font(.system(size: 11))
                        .foregroundColor(.secondary)
                }
            }
            .buttonStyle(.plain)
            .onHover { isHovered = $0 }

            Spacer()
        }
    }
}
