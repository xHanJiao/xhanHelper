#if os(macOS)
import SwiftUI
import Combine

// MARK: - 设置页「关于我」

/// 画像设置页的数据源：拉取 / 新增 / 修改 / 删除事实。
/// 用户手写的事实由服务端标 source=user，模型永远不会覆盖或删回去。
@MainActor
final class ProfileSettingsModel: ObservableObject {
    @Published private(set) var groups: [ProfileLayerGroup] = []
    @Published private(set) var loading = false
    @Published var errorText: String?

    private let client: APIClient

    init(client: APIClient) {
        self.client = client
    }

    func load() async {
        loading = true
        defer { loading = false }
        do {
            groups = try await client.getProfile().groups
            errorText = nil
        } catch {
            errorText = "读取画像失败：\(error.localizedDescription)"
            LogBuffer.shared.error("profile load failed: \(error.localizedDescription)")
        }
    }

    func add(field: String, value: String) async -> Bool {
        do {
            _ = try await client.createProfileFact(field: field, value: value)
            await load()
            return true
        } catch {
            errorText = "新增失败：\(error.localizedDescription)"
            return false
        }
    }

    func update(id: String, value: String) async -> Bool {
        do {
            try await client.updateProfileFact(id: id, value: value)
            await load()
            return true
        } catch {
            errorText = "保存失败：\(error.localizedDescription)"
            return false
        }
    }

    func delete(id: String) async {
        do {
            try await client.deleteProfileFact(id: id)
            await load()
        } catch {
            errorText = "删除失败：\(error.localizedDescription)"
        }
    }
}

/// 编辑目标：factID 为空 = 新增。
private struct ProfileEditTarget: Identifiable {
    let id = UUID()
    let field: String
    let maxLen: Int
    let factID: String?
    let initialValue: String
}

struct ProfileSettingsView: View {
    @StateObject private var model: ProfileSettingsModel
    @State private var editing: ProfileEditTarget?

    init(apiClient: APIClient) {
        _model = StateObject(wrappedValue: ProfileSettingsModel(client: apiClient))
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                Text("助手长期记住的关于你的事实。你手写或修改过的条目会标记为「你写的」，模型只能建议、不能覆盖；删掉的条目模型也不会再写回来。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)

                if let errorText = model.errorText {
                    Text(errorText)
                        .font(.caption)
                        .foregroundStyle(.red)
                        .fixedSize(horizontal: false, vertical: true)
                }

                if model.loading && model.groups.isEmpty {
                    ProgressView().frame(maxWidth: .infinity).padding(30)
                }

                ForEach(model.groups) { layer in
                    SettingsCard(icon: icon(for: layer.layer), title: layer.name, hint: hint(for: layer.layer)) {
                        VStack(alignment: .leading, spacing: 0) {
                            ForEach(layer.fields) { field in
                                fieldSection(field)
                            }
                        }
                    }
                }
            }
            .padding(18)
        }
        .frame(minWidth: 560, minHeight: 520)
        .task { await model.load() }
        .sheet(item: $editing) { target in
            ProfileFactEditor(
                field: target.field,
                maxLen: target.maxLen,
                initialValue: target.initialValue,
                isNew: target.factID == nil,
                onSave: { value in
                    let ok: Bool
                    if let id = target.factID {
                        ok = await model.update(id: id, value: value)
                    } else {
                        ok = await model.add(field: target.field, value: value)
                    }
                    guard ok else { return model.errorText ?? "保存失败" }
                    editing = nil
                    return nil
                }
            )
        }
    }

    @ViewBuilder
    private func fieldSection(_ field: ProfileFieldGroup) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Text(field.field).font(.system(size: 12, weight: .semibold))
                Text(field.injectDesc)
                    .font(.system(size: 11))
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
                Spacer(minLength: 0)
                if field.items.count < field.maxFacts {
                    Button {
                        editing = ProfileEditTarget(field: field.field, maxLen: field.maxLen,
                                                    factID: nil, initialValue: "")
                    } label: {
                        Image(systemName: "plus.circle")
                    }
                    .buttonStyle(.borderless)
                    .help("新增一条")
                }
            }

            if field.items.isEmpty {
                Text("（暂无）")
                    .font(.system(size: 11))
                    .foregroundStyle(.tertiary)
            } else {
                ForEach(field.items) { item in
                    factRow(field: field, item: item)
                }
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 8)
    }

    @ViewBuilder
    private func factRow(field: ProfileFieldGroup, item: ProfileFactItem) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text(item.value)
                .font(.system(size: 12))
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 8)
            Text(subtitle(for: item))
                .font(.system(size: 10))
                .foregroundStyle(.tertiary)
                .lineLimit(1)
            Button {
                editing = ProfileEditTarget(field: field.field, maxLen: field.maxLen,
                                            factID: item.id, initialValue: item.value)
            } label: {
                Image(systemName: "pencil")
            }
            .buttonStyle(.borderless)
            .help("编辑")
            Button {
                Task { await model.delete(id: item.id) }
            } label: {
                Image(systemName: "trash")
            }
            .buttonStyle(.borderless)
            .help("删除（模型以后不会再写回）")
        }
        .padding(.leading, 8)
    }

    private func subtitle(for item: ProfileFactItem) -> String {
        var parts: [String] = []
        if let seen = item.lastSeenLabel { parts.append("最近证实 \(seen)") }
        parts.append(item.sourceLabel)
        return parts.joined(separator: " · ")
    }

    private func icon(for layer: Int) -> String {
        switch layer {
        case 0: return "person.text.rectangle"
        case 1: return "bubble.left.and.text.bubble.right"
        case 2: return "exclamationmark.shield"
        case 3: return "briefcase"
        case 4: return "fork.knife"
        default: return "target"
        }
    }

    private func hint(for layer: Int) -> String? {
        switch layer {
        case 0, 1, 2: return "常驻"
        default: return "有预算才注入"
        }
    }
}

/// 单条事实的编辑弹窗（新增 / 修改共用）。
private struct ProfileFactEditor: View {
    let field: String
    let maxLen: Int
    let initialValue: String
    let isNew: Bool
    /// 返回 nil = 保存成功；否则返回给用户看的错误文案（错误要显示在弹窗里，不能藏在背后的页面）。
    let onSave: (String) async -> String?

    @State private var text: String
    @State private var saving = false
    @State private var errorText: String?
    @Environment(\.dismiss) private var dismiss

    init(field: String, maxLen: Int, initialValue: String, isNew: Bool, onSave: @escaping (String) async -> String?) {
        self.field = field
        self.maxLen = maxLen
        self.initialValue = initialValue
        self.isNew = isNew
        self.onSave = onSave
        _text = State(initialValue: initialValue)
    }

    private var overLimit: Bool { text.count > maxLen }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(isNew ? "新增「\(field)」" : "修改「\(field)」")
                .font(.headline)
            TextField("内容", text: $text, axis: .vertical)
                .textFieldStyle(.roundedBorder)
                .lineLimit(3...6)
            if let errorText {
                Text(errorText)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Text("\(text.count)/\(maxLen) 字")
                    .font(.caption)
                    .foregroundStyle(overLimit ? .red : .secondary)
                Spacer()
                Button("取消") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(saving ? "保存中…" : "保存") {
                    saving = true
                    Task {
                        errorText = await onSave(text.trimmingCharacters(in: .whitespacesAndNewlines))
                        saving = false
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(saving || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || overLimit)
            }
        }
        .padding(20)
        .frame(width: 420)
    }
}
#endif
