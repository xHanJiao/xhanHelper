import SwiftUI

/// 通用面板容器：返回按钮 + 标题 + 可选 trailing 按钮 + 内容区。
/// 业务无关，可装任意 View（WebView / 原生面板）。
struct XHPanel<Content: View>: View {
    let title: String
    let onBack: () -> Void
    var trailingButtons: [XHPanelButton] = []
    @ViewBuilder var content: Content

    struct XHPanelButton: Identifiable {
        let id = UUID()
        let icon: String
        let help: String
        let action: () -> Void
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            content
        }
    }

    private var header: some View {
        HStack(spacing: 8) {
            Button {
                onBack()
            } label: {
                HStack(spacing: 3) {
                    Image(systemName: "chevron.left")
                        .font(.system(size: 11, weight: .semibold))
                    Text("返回")
                        .font(.system(size: 12))
                }
                .foregroundColor(.secondary)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("返回网格")

            Divider()
                .frame(height: 14)

            Text(title)
                .font(.system(size: 13, weight: .semibold))

            Spacer()

            ForEach(trailingButtons) { button in
                Button {
                    button.action()
                } label: {
                    Image(systemName: button.icon)
                        .font(.system(size: 12))
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain)
                .help(button.help)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }
}
