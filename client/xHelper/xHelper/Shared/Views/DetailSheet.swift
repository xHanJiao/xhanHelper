import SwiftUI

struct DetailSheet<Content: View>: View {
    let onClose: () -> Void
    @ViewBuilder let content: () -> Content

    init(onClose: @escaping () -> Void, @ViewBuilder content: @escaping () -> Content) {
        self.onClose = onClose
        self.content = content
    }

    var body: some View {
        ZStack(alignment: .topTrailing) {
            ViewThatFits(in: .vertical) {
                content()
                    .padding(24)
                    .frame(maxWidth: .infinity, alignment: .leading)

                ScrollView {
                    content()
                        .padding(24)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                .frame(maxHeight: 500)
            }

            Button(action: onClose) {
                Image(systemName: "xmark.circle.fill")
                    .font(.title2)
                    .foregroundColor(.secondary)
            }
            .buttonStyle(.plain)
            .padding(12)
        }
        .frame(maxHeight: 500)
        .fixedSize(horizontal: false, vertical: true)
        .background(.regularMaterial)
    }
}
