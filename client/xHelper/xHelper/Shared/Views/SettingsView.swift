import SwiftUI
#if os(iOS)
import UIKit
#else
import AppKit
#endif

struct SettingsView: View {
    @ObservedObject var deviceManager: DeviceManager
    @AppStorage("xhan_server_url") private var serverURL = DeviceManager.defaultBaseURL
    @AppStorage("xhan_debug_token") private var debugToken = ""

    var body: some View {
        Form {
            Section {
                if deviceManager.isRegistered {
                    HStack {
                        Text("设备 ID")
                        Spacer()
                        Text(deviceManager.deviceId ?? "-").font(.caption).monospaced()
                    }
                    HStack {
                        Text("Token")
                        Spacer()
                        Text(deviceManager.token ?? "-").font(.caption).monospaced().lineLimit(1)
                    }
                } else {
                    Text("未注册")
                        .foregroundColor(.secondary)
                }
            } header: {
                Text("设备信息")
            }

            Section {
                TextField("服务地址", text: $serverURL)
                    .autocorrectionDisabled()
                #if os(iOS)
                    .keyboardType(.URL)
                #endif
            } header: {
                Text("服务器")
            }

            #if DEBUG
            Section {
                TextField("手动填入 Token", text: $debugToken)
                    .font(.caption).monospaced()
                    .onChange(of: debugToken) {
                        deviceManager.debugToken = debugToken
                    }
            } header: {
                Text("Debug Token（填入已导入数据的 token）")
            }
            #endif

            Section {
                Button("重新注册设备", role: .destructive) {
                    Task {
                        try? await deviceManager.register(name: platformDeviceName, platform: platformName)
                    }
                }
            }
        }
        .navigationTitle("设置")
    }

    private var platformName: String {
        #if os(iOS)
        return "ios"
        #else
        return "macos"
        #endif
    }

    private var platformDeviceName: String {
        #if os(iOS)
        return UIDevice.current.name
        #else
        return Host.current().localizedName ?? "Mac"
        #endif
    }
}
