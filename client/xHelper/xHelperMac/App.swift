import SwiftUI
import AppKit

@main
struct xhanHelperMacApp: App {
    @StateObject private var deviceManager = DeviceManager()

    var body: some Scene {
        WindowGroup {
            if deviceManager.isRegistered {
                ContentView(deviceManager: deviceManager)
                    .frame(minWidth: 600, minHeight: 500)
            } else {
                RegisterMacView(deviceManager: deviceManager)
                    .frame(width: 400, height: 300)
            }
        }
        .windowResizability(.contentMinSize)
    }
}

struct RegisterMacView: View {
    @ObservedObject var deviceManager: DeviceManager
    @AppStorage("has_registered") private var hasRegistered = false
    @State private var errorMessage: String?
    @State private var isRegistering = false

    var body: some View {
        VStack(spacing: 24) {
            Image(systemName: "bubble.left.and.bubble.right.fill")
                .font(.system(size: 60))
                .foregroundColor(.blue)

            Text("xhanHelper")
                .font(.largeTitle)
                .fontWeight(.bold)

            if isRegistering {
                ProgressView()
                Text("正在注册...").foregroundColor(.secondary)
            } else if hasRegistered {
                Text("设备凭证已过期").foregroundColor(.secondary)
                if let err = errorMessage { Text(err).font(.caption).foregroundColor(.red) }
                Button("重新注册") { Task { await performRegister() } }.buttonStyle(.borderedProminent)
            } else {
                Text("欢迎使用 xhanHelper").foregroundColor(.secondary)
                if let err = errorMessage { Text(err).font(.caption).foregroundColor(.red) }
                Button("开始使用") { Task { await performRegister() } }.buttonStyle(.borderedProminent)
            }
        }
    }

    private func performRegister() async {
        isRegistering = true
        errorMessage = nil
        do {
            try await deviceManager.register(name: Host.current().localizedName ?? "Mac", platform: "macos")
            hasRegistered = true
        } catch {
            errorMessage = "注册失败: \(error.localizedDescription)"
        }
        isRegistering = false
    }
}
