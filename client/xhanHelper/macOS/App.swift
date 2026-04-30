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
    @State private var errorMessage: String?

    var body: some View {
        VStack(spacing: 24) {
            Image(systemName: "bubble.left.and.bubble.right.fill")
                .font(.system(size: 60))
                .foregroundColor(.blue)

            Text("xhanHelper")
                .font(.largeTitle)
                .fontWeight(.bold)

            Text("正在注册设备...")
                .foregroundColor(.secondary)

            if let err = errorMessage {
                Text(err)
                    .font(.caption)
                    .foregroundColor(.red)
            }

            ProgressView()
        }
        .task {
            do {
                try await deviceManager.register(
                    name: Host.current().localizedName ?? "Mac",
                    platform: "macos"
                )
            } catch {
                errorMessage = "注册失败: \(error.localizedDescription)"
            }
        }
    }
}
