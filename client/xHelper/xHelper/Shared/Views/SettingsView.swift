import SwiftUI
#if os(iOS)
import UIKit
import UniformTypeIdentifiers
#else
import AppKit
#endif

struct SettingsView: View {
    @ObservedObject var deviceManager: DeviceManager
    @AppStorage("xhan_server_url") private var serverURL = DeviceManager.defaultBaseURL

    @State private var showFilePicker = false
    @State private var isImporting = false
    @State private var importResult: ImportResult?
    @State private var importError: String?

    private var apiClient: APIClient {
        APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
    }

    var body: some View {
        Form {
            Section {
                if deviceManager.isLoggedIn {
                    HStack {
                        Text("用户名")
                        Spacer()
                        Text(deviceManager.username ?? "-").font(.caption)
                    }
                    #if DEBUG
                    HStack {
                        Text("用户 ID")
                        Spacer()
                        Text(deviceManager.userId ?? "-").font(.caption).monospaced().lineLimit(1)
                    }
                    #endif
                } else {
                    Text("未登录")
                        .foregroundColor(.secondary)
                }
            } header: {
                Text("账号信息")
            }

            #if DEBUG
            Section {
                TextField("服务地址", text: $serverURL)
                    .autocorrectionDisabled()
                #if os(iOS)
                    .keyboardType(.URL)
                #endif
            } header: {
                Text("服务器")
            }
            #endif

            Section {
                Button {
                    showFilePicker = true
                } label: {
                    HStack {
                        Label("导入账单 CSV", systemImage: "doc.badge.plus")
                        Spacer()
                        if isImporting {
                            ProgressView()
                        }
                    }
                }
                .disabled(isImporting)
            } header: {
                Text("数据")
            } footer: {
                Text("支持标准 CSV 格式（项目,金额,一级分类,二级分类,日期,归属人）")
            }

            Section {
                NavigationLink(destination: PromptListView(viewModel: PromptViewModel(apiClient: apiClient))) {
                    Label("系统提示词", systemImage: "terminal")
                }
                NavigationLink(destination: BillListView(apiClient: apiClient)) {
                    Label("账单记录", systemImage: "list.bullet.rectangle")
                }
                NavigationLink(destination: NoteListView(apiClient: apiClient)) {
                    Label("笔记记录", systemImage: "note.text")
                }
            } header: {
                Text("浏览")
            }

            Section {
                Button("退出登录", role: .destructive) {
                    Task { await deviceManager.logout() }
                }
            }
        }
        .navigationTitle("设置")
        #if os(iOS)
        .fileImporter(isPresented: $showFilePicker, allowedContentTypes: [
            UTType.commaSeparatedText,
            UTType(filenameExtension: "csv") ?? UTType.text,
            UTType.plainText,
        ]) { result in
            handleFilePicked(result)
        }
        #endif
        .alert("导入结果", isPresented: .init(
            get: { importResult != nil || importError != nil },
            set: { if !$0 { importResult = nil; importError = nil } }
        )) {
            Button("确定") {}
        } message: {
            if let r = importResult {
                Text("成功导入 \(r.imported) 条，跳过 \(r.skipped) 条")
            } else if let e = importError {
                Text("导入失败: \(e)")
            }
        }
    }

    private func handleFilePicked(_ result: Result<URL, Error>) {
        switch result {
        case .success(let url):
            Task {
                guard url.startAccessingSecurityScopedResource() else { return }
                defer { url.stopAccessingSecurityScopedResource() }
                await importCSV(from: url)
            }
        case .failure:
            break
        }
    }

    private func importCSV(from url: URL) async {
        isImporting = true
        do {
            let csvText = try String(contentsOf: url, encoding: .utf8)
            let result = try await apiClient.uploadBillsCSV(csvText)
            importResult = result
        } catch {
            importError = error.localizedDescription
        }
        isImporting = false
    }
}
