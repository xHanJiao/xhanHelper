#if os(macOS)
import SwiftUI
import AppKit
import Combine
import Security

// MARK: - UI-10 解锁门 + 空闲/回前台重新锁定

/// Touch ID 访问门的状态机：启动靠 token 读取触发指纹（见 `LocalBackendManager.state == .locked`）；
/// 本控制器只管**运行期**的重新锁定（回前台 / 空闲 15 分钟）。
@MainActor
final class LocalLockController: ObservableObject {
    static let shared = LocalLockController()

    @Published private(set) var isLocked = false

    private let idleInterval: TimeInterval = 15 * 60
    private var lastActivity = Date()
    private var inactiveSince: Date?
    private var timer: Timer?
    private var started = false
    private var isAuthenticating = false

    /// 启动空闲计时（在 app 首次 ready 后调用）。
    func start() {
        guard !started else { return }
        started = true
        lastActivity = Date()
        timer = Timer.scheduledTimer(withTimeInterval: 30, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.tick() }
        }
    }

    /// 任意交互都算「还在用」。由全局事件监视器调用。
    func noteActivity() {
        lastActivity = Date()
    }

    func noteResignActive() {
        inactiveSince = Date()
    }

    /// 回前台即锁——但要求确实离开够久（>60s），避免 Touch ID 弹窗引起的
    /// 去激活/回激活把自己立刻再锁一遍（解锁死循环）。
    func lockOnActivate() {
        defer { inactiveSince = nil }
        guard LocalAuthService.shared.isActive, started, !isLocked else { return }
        guard let since = inactiveSince, Date().timeIntervalSince(since) > 60 else { return }
        lastActivity = Date()
        isLocked = true
    }

    /// 认证成功才解开（失败保持锁定）。
    /// 重入保护：主窗口与设置窗口各有一块锁屏，两个「解锁」按钮可能同时被点 → 只放行一次。
    func unlock() async {
        guard !isAuthenticating else { return }
        isAuthenticating = true
        defer { isAuthenticating = false }
        guard await LocalAuthService.shared.authenticate(reason: "解锁 xhanHelper") else { return }
        lastActivity = Date()
        isLocked = false
    }

    /// 手动锁定（设置页「锁定」按钮）。
    func lock() {
        lastActivity = Date()
        isLocked = true
    }

    private func tick() {
        guard LocalAuthService.shared.isActive, started else { return }
        if Date().timeIntervalSince(lastActivity) >= idleInterval {
            isLocked = true
        }
    }
}

/// Touch ID 凭据读不出来时的逃生口：关开关 + 删旧凭据 + 重启后端。
/// 放在解锁页和本地失败页——否则遇到持续读失败就永远出不去（开关只在首次配置页里）。
struct LocalTouchIDRecoveryLink: View {
    var body: some View {
        Button("关闭 Touch ID 并重置凭据") {
            Task { await LocalBackendManager.shared.disableTouchIDAndRetry() }
        }
        .buttonStyle(.link)
        .font(.caption)
    }
}

/// UI-10：锁定门（复用登录门的外观，去掉账号密码，改为指纹解锁）。
struct LocalUnlockView: View {
    let onUnlock: () -> Void
    @State private var isUnlocking = false

    var body: some View {
        VStack(spacing: 22) {
            Spacer()

            Image(systemName: "touchid")
                .font(.system(size: 60))
                .foregroundColor(Color("appAccent"))
                .padding(.bottom, 2)

            Text("xhanHelper")
                .font(.system(size: 28, weight: .bold))

            Text("用 Touch ID 解锁本地服务")
                .font(.subheadline)
                .foregroundColor(.secondary)

            Button {
                isUnlocking = true
                onUnlock()
                // 解锁是异步的（指纹弹窗），按钮短暂禁用避免连点；成功后本视图会被替换掉。
                DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { isUnlocking = false }
            } label: {
                Text(isUnlocking ? "解锁中…" : "解锁")
                    .frame(maxWidth: 200)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .disabled(isUnlocking)

            if LocalAuthService.shared.isActive {
                LocalTouchIDRecoveryLink()
            }

            Spacer()
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(GlassBackground())
    }
}

// MARK: - UI-1 首次配置

/// 首次配置窗口内容：两个密钥 + 高级区（model / base_url）+ 测试连接 + 启用 Touch ID + 继续。
/// 单页，高级区折叠；无账号密码。
struct LocalOnboardingView: View {
    let onDone: () -> Void
    let onSkip: () -> Void

    @State private var secrets = LocalSecretsStore.load()
    @State private var showAdvanced = false
    @State private var touchID = LocalAuthService.shared.isEnabled
    @State private var test: ConnectionTestState = .idle
    /// 保存失败（写钥匙串失败）必须显式报出——否则点了「继续」毫无反应。
    @State private var saveError: String?

    private var keyMissing: Bool {
        !LocalBackendDiagnostics.missingSecrets(secrets).isEmpty
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                header
                keysCard
                advancedCard
                touchIDCard
                testRow
                actions
            }
            .frame(width: 540)
            .padding(.horizontal, 24)
            // 顶部让开红绿灯（hiddenTitleBar 下安全区为 0，不主动留白会贴上去）。
            .padding(.top, 44)
            .padding(.bottom, 24)
        }
        .background(GlassBackground())
        // 改了任何配置项就让上次「连接正常」作废（它已不代表当前值）。
        .onChange(of: secrets) { _, _ in
            test = .idle
            saveError = nil
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 10) {
                Image(systemName: "bubble.left.and.bubble.right.fill")
                    .font(.system(size: 26))
                    .foregroundColor(Color("appAccent"))
                Text("欢迎使用 xhanHelper")
                    .font(.system(size: 22, weight: .bold))
            }
            Text("本地服务需要一个 DeepSeek LLM 密钥和一个 Ark Embedding 密钥。\n密钥只存在本机钥匙串，不会上传；Debug / Release 各存一份，互不影响。")
                .font(.subheadline)
                .foregroundColor(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private var keysCard: some View {
        SettingsCard(icon: "key.fill", title: "API 密钥", hint: "必需") {
            VStack(alignment: .leading, spacing: 10) {
                LabeledContent("DeepSeek LLM Key") {
                    SecureField("", text: $secrets.llmKey, prompt: Text("sk-…"))
                        .textFieldStyle(.roundedBorder)
                        .frame(width: 300)
                }
                LabeledContent("Ark Embedding Key") {
                    SecureField("", text: $secrets.embeddingKey, prompt: Text("…"))
                        .textFieldStyle(.roundedBorder)
                        .frame(width: 300)
                }
            }
            .padding(14)
        }
    }

    private var advancedCard: some View {
        SettingsCard(icon: "slider.horizontal.3", title: "高级", hint: "可选") {
            DisclosureGroup("模型与地址（留空用默认）", isExpanded: $showAdvanced) {
                VStack(alignment: .leading, spacing: 10) {
                    LabeledContent("模型") {
                        TextField("", text: $secrets.llmModel, prompt: Text("默认：\(LocalSecrets.defaultLLMModel)"))
                            .textFieldStyle(.roundedBorder)
                            .frame(width: 300)
                    }
                    LabeledContent("Base URL") {
                        TextField("", text: $secrets.llmBaseURL, prompt: Text("默认：\(LocalSecrets.defaultLLMBaseURL)"))
                            .textFieldStyle(.roundedBorder)
                            .frame(width: 300)
                    }
                }
                .padding(.top, 8)
            }
            .padding(14)
        }
    }

    private var touchIDCard: some View {
        SettingsCard(icon: "touchid", title: "访问门") {
            VStack(alignment: .leading, spacing: 6) {
                Toggle("启用 Touch ID 解锁", isOn: $touchID)
                    .disabled(!LocalAuthService.shared.biometricsAvailable)
                    .onChange(of: touchID) { _, enabled in
                        // 打开时先过一次指纹（关掉不用，稍后保存时生效）。
                        guard enabled else { return }
                        Task {
                            if !(await LocalAuthService.shared.enableWithAuthentication()) { touchID = false }
                        }
                    }
                if !LocalAuthService.shared.biometricsAvailable {
                    Text("本机未检测到可用的 Touch ID（或未录入指纹）。")
                        .font(.caption)
                        .foregroundColor(.secondary)
                } else {
                    LocalTouchIDDebugHint()
                }
            }
            .padding(14)
        }
    }

    private var testRow: some View {
        HStack(spacing: 10) {
            Button {
                Task { await runTest() }
            } label: {
                if test == .testing {
                    HStack(spacing: 6) { ProgressView().controlSize(.small); Text("测试中…") }
                } else {
                    Label("测试连接", systemImage: "bolt.horizontal.circle")
                }
            }
            .disabled(test == .testing || keyMissing)
            .help(keyMissing ? "请先填写两个密钥" : "用当前密钥各发一个最小请求验证")

            LocalConnectionTestStatus(state: test)
        }
    }

    private var actions: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let saveError {
                Label(saveError, systemImage: "xmark.octagon")
                    .foregroundColor(.red)
                    .font(.caption)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Button("稍后配置", action: onSkip)
                    .buttonStyle(.link)
                Spacer()
                Button("继续") {
                    Task { await saveAndContinue() }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(keyMissing || test == .testing)
                .keyboardShortcut(.defaultAction)
            }
        }
    }

    private func runTest() async {
        test = .testing
        test = await LocalConnectionProbe.testState(secrets)
    }

    /// 与设置页一致：存之前**强制**验一次，密钥错了就别落盘、别拉后端。
    /// 失败时不落盘，红图标 + hover 里有原因；离线用户仍可走「稍后配置」。
    private func saveAndContinue() async {
        test = .testing
        let result = await LocalConnectionProbe.testState(secrets)
        test = result
        guard case .ok = result else { return }

        LocalAuthService.shared.isEnabled = touchID
        let status = LocalSecretsStore.save(secrets)
        guard status == errSecSuccess else {
            saveError = "无法写入本机钥匙串：\(LocalKeychain.describe(status))"
            return
        }
        saveError = nil
        onDone()
    }
}

// MARK: - UI-4 未配置空态

struct LocalNotConfiguredView: View {
    let onConfigure: () -> Void

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "key.slash")
                .font(.system(size: 36))
                .foregroundColor(.secondary)
            Text("尚未配置本地服务密钥")
                .font(.headline)
            Text("配置 DeepSeek 与 Ark Embedding 密钥后即可启动本地服务。")
                .font(.subheadline)
                .foregroundColor(.secondary)
                .multilineTextAlignment(.center)
            Button("去配置", action: onConfigure)
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
        }
        .padding(32)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

// MARK: - 共用：测试连接结果指示

/// 「测试连接」的结果：**只有图标、没有文字**，细节全挂 hover（`help` 是原生 tooltip）。
/// 以前把失败原因写成一行红字，既把 Form 撑高、又常常离触发它的按钮很远。
/// 失败时 tooltip 里两段：人话 + provider 原文（原文对排错有用）。
struct LocalConnectionTestStatus: View {
    let state: ConnectionTestState

    var body: some View {
        switch state {
        case .ok:
            Image(systemName: "checkmark.circle.fill")
                .foregroundColor(Color("appAccent"))
                .help("连接正常")
        case .failed(let failure):
            Image(systemName: "xmark.octagon.fill")
                .foregroundColor(.red)
                .help([failure.message, failure.detail]
                    .compactMap { $0 }
                    .joined(separator: "\n\n"))
        case .idle, .testing:
            EmptyView()
        }
    }
}

// MARK: - 共用：Debug 构建的指纹提示

/// Debug 构建默认不弹指纹（`LocalAuthService.isActive`）——onboarding 与设置页共用同一句解释，
/// 否则用户会以为「开关坏了」。
struct LocalTouchIDDebugHint: View {
    var body: some View {
        #if DEBUG
        Text("Debug 构建默认跳过指纹（设计如此）；验证时用 `defaults write xhanx.cn.xHelperMac xhan_touch_id_debug -bool YES` 临时打开。")
            .font(.caption)
            .foregroundColor(.secondary)
            .fixedSize(horizontal: false, vertical: true)
        #else
        EmptyView()
        #endif
    }
}

// MARK: - 设置页：模型与密钥（UI-5）

/// 供 `MacSettingsView` 内联的「模型与密钥」分区。Release 也可见。
struct LocalModelKeySection: View {
    @State private var secrets = LocalSecretsStore.load()
    /// 上次保存的快照（避免在每次 body 求值时读 Keychain 判断 dirty）。
    @State private var saved = LocalSecretsStore.load()
    @State private var test: ConnectionTestState = .idle
    @State private var showClearAlert = false
    /// 保存失败（写钥匙串失败）必须显式报出。
    @State private var saveError: String?
    /// Touch ID 开关（@State 驱动：`isActive` 是普通计算属性，不响应式）。
    @State private var touchID = LocalAuthService.shared.isEnabled
    let onLock: () -> Void

    private var dirty: Bool {
        secrets.normalized() != saved.normalized()
    }

    private var biometricsAvailable: Bool { LocalAuthService.shared.biometricsAvailable }

    var body: some View {
        Section {
            // 提示一律用 `prompt:`（框内灰字）——标题参数在 Form + LabeledContent 里会被渲染成
            // 输入框右侧的行标签，看着像「框外挂了个值」。
            LabeledContent("DeepSeek LLM Key") {
                SecureField("", text: $secrets.llmKey, prompt: Text("sk-…"))
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 300)
            }
            LabeledContent("Ark Embedding Key") {
                SecureField("", text: $secrets.embeddingKey, prompt: Text("…"))
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 300)
            }
            LabeledContent("模型") {
                TextField("", text: $secrets.llmModel, prompt: Text("默认：\(LocalSecrets.defaultLLMModel)"))
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 300)
            }
            LabeledContent("Base URL") {
                TextField("", text: $secrets.llmBaseURL, prompt: Text("默认：\(LocalSecrets.defaultLLMBaseURL)"))
                    .textFieldStyle(.roundedBorder)
                    .frame(width: 300)
            }

            HStack(spacing: 10) {
                Button {
                    Task { await runTest() }
                } label: {
                    if test == .testing {
                        HStack(spacing: 6) { ProgressView().controlSize(.small); Text("测试中…") }
                    } else {
                        Text("测试连接")
                    }
                }
                .disabled(test == .testing || !secrets.isComplete)
                .help(secrets.isComplete ? "用当前密钥各发一个最小请求验证" : "请先填写两个密钥")

                LocalConnectionTestStatus(state: test)

                Button("保存并重启后端") {
                    Task { await saveAndRestart() }
                }
                .disabled(!dirty || !secrets.isComplete || test == .testing)

                if LocalAuthService.shared.isActive {
                    Button("锁定") { onLock() }
                        .help("立即锁定，需 Touch ID 重新解锁")
                }

                Button("清除密钥", role: .destructive) { showClearAlert = true }
                    .buttonStyle(.link)
            }

            if biometricsAvailable {
                Toggle("启用 Touch ID 解锁", isOn: $touchID)
                    .onChange(of: touchID) { _, enabled in
                        // 关掉是即时的：token 是普通钥匙串项，无 ACL 需要重写。
                        guard enabled else {
                            LocalAuthService.shared.isEnabled = false
                            return
                        }
                        // 打开时先过一次指纹：确认现在真能认证，别开了却认证不了。
                        Task {
                            if !(await LocalAuthService.shared.enableWithAuthentication()) { touchID = false }
                        }
                    }
                LocalTouchIDDebugHint()
            }

            if let saveError {
                Label(saveError, systemImage: "xmark.octagon")
                    .font(.caption)
                    .foregroundColor(.red)
                    .fixedSize(horizontal: false, vertical: true)
            }

        } header: {
            Text("模型与密钥")
        } footer: {
            Text("密钥存本机钥匙串（Debug / Release 各存一份，互不影响）。模型 / Base URL 留空用默认值。「保存并重启后端」会先测试一次连接，通过才保存。")
        }
        .alert("清除密钥", isPresented: $showClearAlert) {
            Button("取消", role: .cancel) {}
            Button("清除", role: .destructive) {
                LocalSecretsStore.clear()
                secrets = LocalSecretsStore.load()
                saved = secrets
                Task { await LocalBackendManager.shared.restart() }
            }
        } message: {
            Text("清除后本地后端将停止，需重新配置密钥。")
        }
        // 改了任何配置项就让上次「连接正常」作废（它已不代表当前值）。
        .onChange(of: secrets) { _, _ in
            test = .idle
            saveError = nil
        }
    }

    private func runTest() async {
        test = .testing
        test = await LocalConnectionProbe.testState(secrets)
    }

    /// 存之前**强制**验一次：密钥错了就别把后端重启进失败态（也就不会有「存了却用不了」）。
    /// 失败时不落盘、不重启，红图标 + hover 里有原因。
    private func saveAndRestart() async {
        test = .testing
        let result = await LocalConnectionProbe.testState(secrets)
        test = result
        guard case .ok = result else { return }

        let status = LocalSecretsStore.save(secrets)
        guard status == errSecSuccess else {
            saveError = "无法写入本机钥匙串：\(LocalKeychain.describe(status))"
            return
        }
        saveError = nil
        secrets = LocalSecretsStore.load()
        saved = secrets
        await LocalBackendManager.shared.restart()
    }
}

// MARK: - 设置页：本地服务（UI-6）

struct LocalServiceSection: View {
    @ObservedObject var backend: LocalBackendManager

    private var dataRoot: String { LocalPaths.dataRoot.path }

    private var statusText: String {
        switch backend.state {
        case .idle: return "未启动"
        case .locked: return "待解锁"
        case .starting: return "启动中…"
        case .ready: return "运行中"
        case .failed: return "启动失败"
        }
    }

    var body: some View {
        Section {
            LabeledContent("状态", value: statusText)
            LabeledContent("地址", value: backend.baseURL)
            LabeledContent("数据目录") {
                HStack(spacing: 6) {
                    Text(dataRoot)
                        .font(.system(.caption, design: .monospaced))
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Button("打开") {
                        NSWorkspace.shared.open(URL(fileURLWithPath: dataRoot))
                    }
                    .buttonStyle(.borderless)
                }
            }
            HStack(spacing: 10) {
                Button("重启后端") { Task { await backend.restart() } }
                Button("查看日志") { NSWorkspace.shared.open(backend.logFileURL) }
            }
        } header: {
            Text("本地服务")
        }
    }
}

// MARK: - UI-9 驻留提示（首次关窗）

/// 首次点红 X 时弹一个不挡路的小浮窗，提示 app 仍在后台运行。
/// 用独立 `NSPanel`（窗口都关了也能显示），3.5s 自动消失。
@MainActor
enum ResidentHint {
    private static let shownKey = "xhan_resident_hint_shown"
    /// 持有当前面板（否则 orderFront 后没人强引用，可能立刻被释放）。
    private static var current: NSPanel?

    static func showIfNeeded() {
        guard !UserDefaults.standard.bool(forKey: shownKey) else { return }
        UserDefaults.standard.set(true, forKey: shownKey)

        let panel = NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: 360, height: 92),
            styleMask: [.nonactivatingPanel, .hudWindow],
            backing: .buffered,
            defer: false
        )
        panel.level = .floating
        panel.isFloatingPanel = true
        panel.hidesOnDeactivate = false
        panel.hasShadow = true
        panel.contentView = NSHostingView(rootView: ResidentHintContent())
        panel.center()
        panel.setFrameOrigin(NSPoint(
            x: panel.frame.origin.x,
            y: (NSScreen.main?.visibleFrame.minY ?? 0) + 80
        ))
        panel.orderFrontRegardless()
        current = panel

        DispatchQueue.main.asyncAfter(deadline: .now() + 3.5) {
            panel.orderOut(nil)
            if current === panel { current = nil }
        }
    }
}

private struct ResidentHintContent: View {
    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: "moon.zzz.fill")
                .font(.system(size: 22))
                .foregroundColor(Color("appAccent"))
            VStack(alignment: .leading, spacing: 2) {
                Text("仍在后台运行")
                    .font(.system(size: 13, weight: .semibold))
                Text("本地服务继续工作；Command + Q 退出")
                    .font(.system(size: 11))
                    .foregroundColor(.secondary)
            }
            Spacer(minLength: 0)
        }
        .padding(16)
        .frame(width: 360, height: 92)
    }
}

// MARK: - 窗口尺寸（UI-1 首次配置按内容收窄）

/// 持有承载 SwiftUI 内容的 `NSWindow`（SwiftUI 无公开 API，靠背景层桥一下）。
@MainActor
final class LocalWindowHandle: ObservableObject {
    weak var window: NSWindow?
}

/// 背景层：拿到宿主 `NSWindow` 后回调（`makeNSView` 返回时还没进窗口，必须异步取）。
struct LocalWindowReader: NSViewRepresentable {
    let onWindow: (NSWindow) -> Void

    func makeNSView(context: Context) -> NSView {
        let view = NSView()
        DispatchQueue.main.async {
            if let window = view.window { onWindow(window) }
        }
        return view
    }

    func updateNSView(_ nsView: NSView, context: Context) {}
}

// MARK: - 预览（Xcode 画布复核；观感以真机为准）

#Preview("UI-1 首次配置") {
    LocalOnboardingView(onDone: {}, onSkip: {})
        .frame(width: 588, height: 650)
}

#Preview("UI-10 解锁门") {
    LocalUnlockView(onUnlock: {})
        .frame(width: 640, height: 700)
}

#Preview("UI-4 未配置空态") {
    LocalNotConfiguredView(onConfigure: {})
        .frame(width: 640, height: 480)
}

#Preview("UI-9 驻留提示") {
    ResidentHintContent()
        .padding(20)
        .background(.regularMaterial)
}

#Preview("UI-5 / UI-6 设置分区") {
    Form {
        LocalModelKeySection(onLock: {})
        LocalServiceSection(backend: LocalBackendManager.shared)
    }
    .formStyle(.grouped)
    .frame(width: 480, height: 620)
}

#endif
