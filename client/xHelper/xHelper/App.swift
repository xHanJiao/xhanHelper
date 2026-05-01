import SwiftUI
import UIKit

@main
struct xhanHelperApp: App {
    @StateObject private var deviceManager = DeviceManager()

    var body: some Scene {
        WindowGroup {
            if deviceManager.isRegistered {
                ContentView(deviceManager: deviceManager)
            } else {
                RegisterView(deviceManager: deviceManager)
            }
        }
    }
}

struct RegisterView: View {
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
                // 首次启动，等用户点击按钮后再发请求
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
            try await deviceManager.register(name: platformDeviceName, platform: platformName)
            hasRegistered = true
        } catch {
            errorMessage = "注册失败: \(error.localizedDescription)"
        }
        isRegistering = false
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
