import SwiftUI
import MarkdownUI

struct NoteDetailOverlay: View {
    let note: Note
    let onClose: () -> Void

    var body: some View {
        ZStack {
            Color.black.opacity(0.2)
                .ignoresSafeArea()
                .onTapGesture { onClose() }

            DetailSheet(onClose: onClose) {
                VStack(alignment: .leading, spacing: 14) {
                    Text(note.topic)
                        .font(.system(size: FontSizeManager.shared.scaledSize * 1.65, weight: .semibold))

                    Markdown(note.content)
                        .markdownTheme(.chatBubble(fontSize: FontSizeManager.shared.scaledSize * 1.18))
                        .markdownCodeSyntaxHighlighter(.plainText)

                    if let tags = note.tags, !tags.isEmpty {
                        Divider()

                        #if os(macOS)
                        TagScrollRow {
                            ForEach(parseTags(tags)) { tag in
                                TagChip(
                                    text: tag.text,
                                    color: tag.color,
                                    font: .system(size: FontSizeManager.shared.scaledSize * 0.76),
                                    hPad: 8,
                                    vPad: 3
                                )
                            }
                        }
                        #else
                        HStack(spacing: 6) {
                            ForEach(parseTags(tags)) { tag in
                                Text(tag.text)
                                    .font(.system(size: FontSizeManager.shared.scaledSize * 0.76))
                                    .foregroundColor(tag.color)
                                    .padding(.horizontal, 8)
                                    .padding(.vertical, 3)
                                    .background(tag.color.opacity(0.12))
                                    .clipShape(Capsule())
                            }
                        }
                        #endif
                    }
                }
            }
            .frame(minWidth: 400, maxWidth: 700)
            .clipShape(RoundedRectangle(cornerRadius: 16))
            .padding(40)
            .transition(.opacity.combined(with: .scale(0.95)))
        }
        .animation(.easeOut(duration: 0.2), value: note.id)
    }

    private func parseTags(_ tags: String?) -> [TagBadge] {
        guard let tags, !tags.isEmpty else { return [] }
        return tags.split(separator: ",").map {
            TagBadge(text: String($0).trimmingCharacters(in: .whitespaces), color: Color("appAccent"))
        }
    }
}
