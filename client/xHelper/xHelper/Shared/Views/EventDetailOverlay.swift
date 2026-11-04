import SwiftUI

struct EventDetailOverlay: View {
    let event: AppEventThread
    @ObservedObject var viewModel: EventViewModel
    let onClose: () -> Void

    var body: some View {
        ZStack {
            Color.black.opacity(0.2)
                .ignoresSafeArea()
                .onTapGesture { onClose() }

            DetailSheet(onClose: onClose) {
                VStack(alignment: .leading, spacing: 14) {
                    HStack(spacing: 8) {
                        Text(event.name)
                            .font(.system(size: FontSizeManager.shared.scaledSize * 1.65, weight: .semibold))
                        Text("\(event.memoryCount) 条记忆")
                            .font(.system(size: FontSizeManager.shared.scaledSize * 0.76))
                            .foregroundColor(.secondary)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 2)
                            .background(Color.secondary.opacity(0.12))
                            .clipShape(Capsule())
                    }

                    if let detail = viewModel.detail {
                        if !detail.summary.isEmpty {
                            Text(detail.summary)
                                .font(.system(size: FontSizeManager.shared.scaledSize * 1.12))
                                .foregroundColor(.primary)
                                .fixedSize(horizontal: false, vertical: true)
                        }

                        if detail.memories.isEmpty {
                            Text("该事件下还没有记忆")
                                .font(.system(size: FontSizeManager.shared.scaledSize * 0.94))
                                .foregroundColor(.secondary)
                        } else {
                            Divider()
                            ForEach(detail.memories) { memory in
                                memoryRow(memory)
                            }
                        }
                    } else if viewModel.isLoadingDetail {
                        ProgressView()
                    }
                }
            }
            .frame(minWidth: 400, maxWidth: 700)
            .clipShape(RoundedRectangle(cornerRadius: 16))
            .padding(40)
            .transition(.opacity.combined(with: .scale(0.95)))
        }
        .animation(.easeOut(duration: 0.2), value: event.id)
        .task { await viewModel.loadDetail(id: event.id) }
    }

    private func memoryRow(_ memory: AppEventMemory) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                if !memory.topic.isEmpty {
                    Text(memory.topic)
                        .font(.system(size: FontSizeManager.shared.scaledSize * 0.76))
                        .foregroundColor(Color("appAccent"))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 2)
                        .background(Color("appAccent").opacity(0.10))
                        .clipShape(Capsule())
                }
                Spacer()
                Text(formatConversationDate(memory.updatedAt))
                    .font(.system(size: FontSizeManager.shared.scaledSize * 0.76))
                    .foregroundColor(.secondary)
            }
            Text(memory.content)
                .font(.system(size: FontSizeManager.shared.scaledSize * 0.94))
                .foregroundColor(.primary)
                .fixedSize(horizontal: false, vertical: true)
            if let facts = memory.facts, !facts.isEmpty {
                ForEach(Array(facts.enumerated()), id: \.offset) { _, fact in
                    Text("「\(fact)」")
                        .font(.system(size: FontSizeManager.shared.scaledSize * 0.82))
                        .foregroundColor(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
        .padding(.vertical, 2)
    }
}
