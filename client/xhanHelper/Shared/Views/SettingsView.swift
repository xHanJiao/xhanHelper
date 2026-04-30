import SwiftUI

struct SettingsView: View {
    @ObservedObject var deviceManager: DeviceManager
    @AppStorage("xhan_server_url") private var serverURL = "http://localhost:8080"

    var body: some View {
        Form {
            Section("设备信息") {
                if deviceManager.isRegistered {
                    LabeledContent("设备 ID") {
                        Text(deviceManager.deviceId ?? "-").font(.caption).monospaced()
                    }
                    LabeledContent("Token") {
                        Text(deviceManager.token ?? "-").font(.caption).monospaced().lineLimit(1)
                    }
                } else {
                    Text("未注册")
                        .foregroundColor(.secondary)
                }
            }

            Section("服务器") {
                TextField("服务地址", text: $serverURL)
                    .autocorrectionDisabled()
                    #if os(iOS)
                    .keyboardType(.URL)
                    #endif
            }

            Section {
                Button("重新注册设备", role: .destructive) {
                    if let bundleID = Bundle.main.bundleIdentifier {
                        #if os(iOS)
                        let name = UIDevice.current.name
                        #else
                        let name = Host.current().localizedName ?? "Mac"
                        #endif
                        Task {
                            try? await deviceManager.register(name: name, platform: platformName)
                        }
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
}
