import SwiftUI
import AppKit

@main
struct xhanHelperMacApp: App {
    @StateObject private var deviceManager = DeviceManager()
    @StateObject private var colorSchemeManager = ColorSchemeManager()

    var body: some Scene {
        WindowGroup {
            Group {
                if deviceManager.isLoggedIn {
                    ContentView(deviceManager: deviceManager)
                        .frame(minWidth: 600, minHeight: 500)
                } else {
                    LoginMacView(deviceManager: deviceManager)
                        .frame(width: 400, height: 380)
                }
            }
            .environmentObject(colorSchemeManager)
            .preferredColorScheme(colorSchemeManager.selectedScheme.colorScheme)
        }
        .windowResizability(.contentMinSize)
    }
}

struct LoginMacView: View {
    @ObservedObject var deviceManager: DeviceManager
    @State private var username = ""
    @State private var password = ""
    @State private var isRegisterMode = false
    @State private var errorMessage: String?
    @State private var isLoading = false

    var body: some View {
        VStack(spacing: 24) {
            Image(systemName: "bubble.left.and.bubble.right.fill")
                .font(.system(size: 60))
                .foregroundColor(Color("appAccent"))

            Text("xhanHelper")
                .font(.largeTitle)
                .fontWeight(.bold)

            VStack(spacing: 12) {
                TextField("用户名", text: $username)
                    .textFieldStyle(.roundedBorder)

                SecureField("密码", text: $password)
                    .textFieldStyle(.roundedBorder)
            }
            .padding(.horizontal, 40)

            if let err = errorMessage {
                Text(err).font(.caption).foregroundColor(.red)
            }

            if isLoading {
                ProgressView()
            } else {
                Button(isRegisterMode ? "注册" : "登录") {
                    Task { await performAuth() }
                }
                .buttonStyle(.borderedProminent)
                .disabled(username.isEmpty || password.isEmpty)

                Button(isRegisterMode ? "已有账号？登录" : "没有账号？注册") {
                    isRegisterMode.toggle()
                    errorMessage = nil
                }
                .font(.caption)
            }
        }
    }

    private func performAuth() async {
        isLoading = true
        errorMessage = nil
        do {
            if isRegisterMode {
                try await deviceManager.register(username: username, password: password)
            } else {
                try await deviceManager.login(username: username, password: password)
            }
        } catch {
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }
}
