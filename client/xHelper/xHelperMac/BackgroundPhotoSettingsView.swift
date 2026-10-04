import SwiftUI
import AppKit

/// 设置页「背景图片」子页（.sheet）：默认图 + 用户池管理 + 导入 + 轮换。
/// 分组统一用 `SettingsCard`（见 docs/design/mac-apps/settings-sheet-style.md）。
struct BackgroundPhotoSettingsView: View {
    @ObservedObject private var store = BackgroundPhotoStore.shared
    @Environment(\.dismiss) private var dismiss

    @State private var showDirPicker = false
    @State private var showClearConfirm = false

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                Text("背景图片")
                    .font(.headline)
                Spacer()
                Button("关闭") { dismiss() }
                    .keyboardShortcut(.cancelAction)
            }

            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    defaultCard
                    poolCard
                    importCard
                    rotationCard
                }
                .padding(.bottom, 4)
            }
        }
        .padding(20)
        .frame(width: 560, height: 600)
        .background(GlassBackground())
        // `OpenDirectoryPanel` 在 `makeNSView` 里读 `isPresented` 并弹面板——必须在切到 true
        // 的同一帧才插入它（静态挂载会读到初始 false，之后 updateNSView 不弹）。故用 if 条件插拔。
        .background {
            if showDirPicker {
                OpenDirectoryPanel(
                    isPresented: $showDirPicker,
                    chooseMessage: "选择背景图片目录（只导入顶层图片）"
                ) { url in
                    store.importDirectory(url)
                }
            }
        }
        .onAppear { store.clearImportSummary() }
        .alert("清空背景图片？", isPresented: $showClearConfirm) {
            Button("取消", role: .cancel) {}
            Button("清空", role: .destructive) { store.clear() }
        } message: {
            Text("将删除所有已导入的照片，回到内置默认图。")
        }
    }

    private var defaultCard: some View {
        SettingsCard(icon: "photo.on.rectangle", title: "默认底图") {
            HStack(spacing: 12) {
                Image("bgDefault")
                    .resizable()
                    .scaledToFill()
                    .frame(width: 96, height: 64)
                    .clipped()
                    .clipShape(RoundedRectangle(cornerRadius: 8))
                Text("内置默认；没有导入照片时用它。永远有一张底图。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 8)
        }
    }

    private var poolCard: some View {
        SettingsCard(icon: "photo.stack", title: "已导入", hint: "\(store.pool.count) 张") {
            if store.pool.isEmpty {
                Text("还没有导入照片。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 10)
            } else {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 84), spacing: 8)], spacing: 8) {
                    ForEach(store.pool, id: \.self) { url in
                        ZStack(alignment: .topTrailing) {
                            if let img = NSImage(contentsOf: url) {
                                Image(nsImage: img)
                                    .resizable()
                                    .scaledToFill()
                                    .frame(width: 84, height: 56)
                                    .clipped()
                                    .clipShape(RoundedRectangle(cornerRadius: 8))
                            } else {
                                RoundedRectangle(cornerRadius: 8)
                                    .fill(.quaternary)
                                    .frame(width: 84, height: 56)
                            }
                            Button {
                                store.remove(url)
                            } label: {
                                Image(systemName: "xmark.circle.fill")
                                    .font(.system(size: 14))
                                    .foregroundStyle(.white, .black.opacity(0.55))
                            }
                            .buttonStyle(.plain)
                            .padding(3)
                            .help("删除这张")
                        }
                    }
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 10)
            }
        }
    }

    private var importCard: some View {
        SettingsCard(icon: "folder.badge.plus", title: "导入目录") {
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 12) {
                    Button {
                        showDirPicker = true
                    } label: {
                        Label("选择目录…", systemImage: "folder")
                    }
                    .disabled(store.isImporting)

                    if store.isImporting {
                        ProgressView().controlSize(.small)
                        Button("取消") { store.cancelImport() }
                    }

                    Spacer()

                    if !store.pool.isEmpty {
                        Button("清空", role: .destructive) { showClearConfirm = true }
                            .disabled(store.isImporting)
                    }
                }
                if let summary = store.importSummary {
                    Text(summary).font(.caption).foregroundStyle(.secondary)
                }
                Text("只导入所选目录顶层的图片；过大或非 JPEG 会压到长边 192。导入后与源目录无关。")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 8)
        }
    }

    private var rotationCard: some View {
        SettingsCard(icon: "shuffle", title: "轮换") {
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 12) {
                    Picker("", selection: $store.mode) {
                        ForEach(BackgroundPhotoStore.RotationMode.allCases) { mode in
                            Text(mode.label).tag(mode)
                        }
                    }
                    .labelsHidden()
                    .frame(maxWidth: 220)

                    Button("换一张") { store.rotateNow() }
                        .disabled(store.pool.count < 2)
                }
                if store.pool.count < 2 {
                    Text("至少导入 2 张才能轮换；现在固定用这一张（或默认图）。")
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 8)
        }
    }
}
