import SwiftUI

@main
struct xhanHelperApp: App {
    init() {
        NotificationDelegateRegistrar.register()
    }

    @StateObject private var deviceManager = DeviceManager()
    @StateObject private var colorSchemeManager = ColorSchemeManager()

    var body: some Scene {
        WindowGroup {
            Group {
                if deviceManager.isVerifying {
                    SplashView()
                } else if deviceManager.isLoggedIn {
                    ContentView(deviceManager: deviceManager)
                } else {
                    LoginView(deviceManager: deviceManager)
                }
            }
            .environmentObject(colorSchemeManager)
            .environmentObject(FontSizeManager.shared)
            .preferredColorScheme(colorSchemeManager.selectedScheme.colorScheme)
            .toast()
        }
    }
}

struct SplashView: View {
    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "bubble.left.and.bubble.right.fill")
                .font(.system(size: 60))
                .foregroundColor(Color("appAccent"))
            Text("xhanHelper")
                .font(.largeTitle)
                .fontWeight(.bold)
            ProgressView()
                .padding(.top, 8)
        }
    }
}

struct LoginView: View {
    @ObservedObject var deviceManager: DeviceManager
    @State private var username = ""
    @State private var password = ""
    @State private var isRegisterMode = false
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
                    .autocorrectionDisabled()
                #if os(iOS)
                    .textInputAutocapitalization(.never)
                #endif

                SecureField("密码", text: $password)
                    .textFieldStyle(.roundedBorder)
            }
            .padding(.horizontal, 40)

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
                }
                .font(.caption)
            }
        }
    }

    private func performAuth() async {
        isLoading = true
        do {
            if isRegisterMode {
                try await deviceManager.register(username: username, password: password)
            } else {
                try await deviceManager.login(username: username, password: password)
            }
        } catch {
            ToastCenter.shared.show(ToastCenter.friendlyErrorText(error, action: isRegisterMode ? "注册" : "登录"))
        }
        isLoading = false
    }
}
