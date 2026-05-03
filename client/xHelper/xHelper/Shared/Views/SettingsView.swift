import SwiftUI
#if os(iOS)
import UIKit
#else
import AppKit
#endif

struct SettingsView: View {
    @ObservedObject var deviceManager: DeviceManager
    @AppStorage("xhan_server_url") private var serverURL = DeviceManager.defaultBaseURL

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
                Button("退出登录", role: .destructive) {
                    Task { await deviceManager.logout() }
                }
            }
        }
        .navigationTitle("设置")
    }
}
