#if os(macOS)
import Foundation
import LocalAuthentication
import Security

/// Touch ID 访问门 + 本地 token 生命周期（改动点 11 / UI-10）。
///
/// **分工**：token 存传统钥匙串普通项（无生物 ACL——ACL 需要付费 entitlement，见 `LocalKeychain`），
/// 指纹门由 app 层把守：
/// - 启动：`LocalBackendManager.start()` 在 `isActive` 时只进 `.locked`，用户点「解锁」→
///   `unlock()` 先 `authenticate()` 过指纹，再读/建 token 并拉起后端。
/// - 运行期：`LocalLockController` 负责回前台 / 空闲 15 分钟重新锁定，解锁同样走 `authenticate()`。
/// - DEBUG build 一律跳过（开发不弹指纹）。
@MainActor
final class LocalAuthService {
    static let shared = LocalAuthService()

    private let enabledKey = "xhan_touch_id_enabled"

    /// UI 开关（默认开）。
    var isEnabled: Bool {
        get { UserDefaults.standard.object(forKey: enabledKey) as? Bool ?? true }
        set { UserDefaults.standard.set(newValue, forKey: enabledKey) }
    }

    /// 有无**生物识别**硬件且已录入。
    /// 刻意用 `WithBiometrics`（不是 `deviceOwnerAuthentication`）：后者在任何设了登录密码的 Mac 上都为真，
    /// 会让没有指纹的机器也开锁屏、文案写「Touch ID」却弹输密码框。没有生物识别 → 门直接放行。
    var biometricsAvailable: Bool {
        var err: NSError?
        return LAContext().canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: &err)
    }

    /// 真实生效：非 DEBUG + 开着 + 硬件可用。
    /// DEBUG 默认跳过（设计：开发不弹指纹），但可用
    /// `defaults write xhanx.cn.xHelperMac.dev xhan_touch_id_debug -bool YES` 临时打开以便验证 UI-10。
    var isActive: Bool {
        #if DEBUG
        return UserDefaults.standard.bool(forKey: "xhan_touch_id_debug") && isEnabled && biometricsAvailable
        #else
        return isEnabled && biometricsAvailable
        #endif
    }

    enum TokenOutcome {
        case ok(String)
        /// 真失败（钥匙串读写失败 / 授权被拒）。必须让用户看见并有出口，不能静默。
        case failed(String)
    }

    /// 读/建本地 token（**不触发指纹**——指纹由调用方先 `authenticate()`）。
    /// - 已有且前缀与本环境一致 → 直接用（重装换新 token 不影响：Go 侧只比对不存储）。
    /// - 没有 / 前缀不对（旧格式或串了环境）→ 生成新的并写入；写失败必须报出，
    ///   否则后端拿不到 token、UI 又毫无反应。
    func loadOrCreateToken() -> TokenOutcome {
        let wantPrefix = LocalPaths.env + ":"
        switch LocalKeychain.read(LocalKeychain.tokenAccount) {
        case .found(let t) where t.hasPrefix(wantPrefix):
            return .ok(t)
        case .found, .notFound:
            break // 空串 / 前缀不符一律当作没有
        case .denied:
            return .failed("读取本地凭据被拒绝（钥匙串授权）。")
        case .failed(let status):
            return .failed("读取本地凭据失败：\(LocalKeychain.describe(status))")
        }
        let token = Self.makeToken()
        let status = LocalKeychain.set(token, for: LocalKeychain.tokenAccount)
        guard status == errSecSuccess else {
            return .failed("写入本地凭据失败：\(LocalKeychain.describe(status))")
        }
        return .ok(token)
    }

    /// 逃生口：凭据读不出来时关闭 Touch ID 并删掉旧项（清掉可能损坏/带旧 ACL 的残留）。
    func disableAndResetToken() {
        isEnabled = false
        LocalKeychain.delete(LocalKeychain.tokenAccount)
        LogBuffer.shared.info("已关闭 Touch ID 并删除本地凭据")
    }

    /// 用户显式打开开关时先验证一次：确认这台机器现在真能认证，避免「开了却认证不了」。
    /// - Returns: 是否保持开启。认证失败/取消返回 `false`，调用方应把开关弹回。
    func enableWithAuthentication(reason: String = "开启 Touch ID 解锁") async -> Bool {
        guard await authenticate(reason: reason) else { return false }
        isEnabled = true
        return true
    }

    /// 纯认证（启动解锁 / 回前台 / 空闲重新解锁都用它）。
    func authenticate(reason: String) async -> Bool {
        let context = LAContext()
        return await withCheckedContinuation { cont in
            context.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason) { ok, error in
                if !ok, let error {
                    Task { @MainActor in
                        LogBuffer.shared.info("Touch ID 认证未通过：\(error.localizedDescription)")
                    }
                }
                cont.resume(returning: ok)
            }
        }
    }

    /// token 形如 `<env>:<random>`：env 前缀即「环境参数」，后端校验不符直接 403（第 4 期）。
    private static func makeToken() -> String {
        let random = (UUID().uuidString + UUID().uuidString).replacingOccurrences(of: "-", with: "")
        return "\(LocalPaths.env):\(random)"
    }
}
#endif
