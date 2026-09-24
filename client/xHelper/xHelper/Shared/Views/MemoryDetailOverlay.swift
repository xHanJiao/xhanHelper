import SwiftUI
import MarkdownUI

struct MemoryDetailOverlay: View {
    let memory: UserMemory
    let onClose: () -> Void

    var body: some View {
        ZStack {
            Color.black.opacity(0.2)
                .ignoresSafeArea()
                .onTapGesture { onClose() }

            DetailSheet(onClose: onClose) {
                VStack(alignment: .leading, spacing: 14) {
                    Text(memory.topic ?? memory.source)
                        .font(.system(size: FontSizeManager.shared.scaledSize * 1.65, weight: .semibold))

                    Markdown(memory.memoryValue)
                        .markdownTheme(.chatBubble(fontSize: FontSizeManager.shared.scaledSize * 1.18))
                        .markdownCodeSyntaxHighlighter(.plainText)

                    if let facts = memory.factsList, !facts.isEmpty {
                        Divider()
                        VStack(alignment: .leading, spacing: 6) {
                            ForEach(Array(facts.enumerated()), id: \.offset) { _, fact in
                                HStack(alignment: .top, spacing: 8) {
                                    Text("•")
                                        .foregroundColor(.secondary)
                                    Text(fact)
                                        .font(.system(size: FontSizeManager.shared.scaledSize * 0.88))
                                        .foregroundColor(.secondary)
                                }
                            }
                        }
                    } else if let raw = memory.facts, !raw.isEmpty {
                        Divider()
                        Markdown(raw)
                            .font(.system(size: FontSizeManager.shared.scaledSize * 0.88))
                            .foregroundColor(.secondary)
                    }

                    Divider()

                    #if os(macOS)
                    TagScrollRow {
                        TagChip(
                            text: memory.source,
                            color: Color("appAccent"),
                            font: .system(size: FontSizeManager.shared.scaledSize * 0.76),
                            hPad: 8,
                            vPad: 3
                        )

                        if let tags = memory.tags, !tags.isEmpty {
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
                    }
                    #else
                    HStack(spacing: 6) {
                        Text(memory.source)
                            .font(.system(size: FontSizeManager.shared.scaledSize * 0.76))
                            .foregroundColor(Color("appAccent"))
                            .padding(.horizontal, 8)
                            .padding(.vertical, 3)
                            .background(Color("appAccent").opacity(0.12))
                            .clipShape(Capsule())

                        if let tags = memory.tags, !tags.isEmpty {
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
                    }
                    #endif
                }
            }
            .frame(minWidth: 400, maxWidth: 700)
            .clipShape(RoundedRectangle(cornerRadius: 16))
            .padding(40)
            .transition(.opacity.combined(with: .scale(0.95)))
        }
        .animation(.easeOut(duration: 0.2), value: memory.id)
    }

    private func parseTags(_ tags: String) -> [TagBadge] {
        tags.split(separator: ",").map {
            TagBadge(text: String($0).trimmingCharacters(in: .whitespaces), color: Color("appAccent"))
        }
    }
}
