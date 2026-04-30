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
                    .multilineTextAlignment(.center)
                    .padding(.horizontal)
            }

            ProgressView()
        }
        .task {
            do {
                try await deviceManager.register(
                    name: platformDeviceName,
                    platform: platformName
                )
            } catch {
                errorMessage = "注册失败: \(error.localizedDescription)"
            }
        }
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
