import SwiftUI

struct InputBar: View {
    @Binding var text: String
    let isStreaming: Bool
    let onSend: () -> Void
    let onStop: () -> Void

    @Binding var isExpanded: Bool
    @State private var showExpandButton = true
    @FocusState private var isFocused: Bool

    var body: some View {
        HStack(alignment: .center, spacing: 8) {
            TextField("输入消息...", text: $text, axis: .vertical)
                .textFieldStyle(.plain)
                .focused($isFocused)
                .submitLabel(.send)
                .lineLimit(isExpanded ? 20 : 1)
                .padding(.leading, 12)
                .padding(.trailing, 28)
                .padding(.vertical, 8)
                .frame(minHeight: isExpanded ? 240 : 0, alignment: .top)
                .background(Color.gray.opacity(0.1))
                .cornerRadius(16)
                .disabled(isStreaming)
                .onChange(of: text) { _, newValue in
                    if newValue.contains("\n"), !isStreaming, !isExpanded {
                        text = newValue.replacingOccurrences(of: "\n", with: "")
                        onSend()
                    }
                }
                .overlay(alignment: isExpanded ? .topTrailing : .trailing) {
                    if !isStreaming, showExpandButton {
                        Button {
                            showExpandButton = false
                            withAnimation(.easeInOut(duration: 0.2)) {
                                isExpanded = !isExpanded
                                if isExpanded { isFocused = true }
                            }
                            DispatchQueue.main.asyncAfter(deadline: .now() + 0.25) {
                                showExpandButton = true
                            }
                        } label: {
                            Image(systemName: isExpanded
                                ? "arrow.down.right.and.arrow.up.left"
                                : "arrow.up.left.and.arrow.down.right")
                                .font(.caption2)
                                .foregroundColor(.secondary)
                        }
                        .buttonStyle(.plain)
                        .padding(.top, isExpanded ? 10 : 0)
                        .padding(.trailing, 6)
                    }
                }

            if isStreaming {
                Button(action: onStop) {
                    Image(systemName: "stop.circle.fill")
                        .font(.title2)
                        .foregroundColor(.red)
                }
                .buttonStyle(.plain)
            } else {
                Button(action: onSend) {
                    Image(systemName: "arrow.up.circle.fill")
                        .font(.title2)
                        .foregroundColor(text.trimmingCharacters(in: .whitespaces).isEmpty ? .gray : .blue)
                }
                .buttonStyle(.plain)
                .disabled(text.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(.horizontal)
        .padding(.vertical, 8)
        .background(.regularMaterial)
    }
}
