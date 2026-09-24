#if os(macOS)
import Foundation
import Security

/// Keychain 存取：本地后端密钥（LLM / Embedding）+ 本地 token。
///
/// 走**传统登录钥匙串**（file-based）。**刻意不用** `kSecUseDataProtectionKeychain`：
/// 数据保护钥匙串 + `kSecAttrAccessControl`（生物 ACL）都要求 app 带 `application-identifier` /
/// `keychain-access-groups` entitlement，而该 entitlement 只随描述文件下发 → 需要付费开发者账号。
/// 实测（无 entitlement）：两套钥匙串的 `kSecAttrAccessControl` 写入都是 `errSecMissingEntitlement (-34018)`，
/// 传统钥匙串的普通项则完全可用。指纹门改由 app 层把守（`LocalAuthService.authenticate`）。
///
/// 传统钥匙串的保护：item 的 ACL 绑定创建它的 app 签名，别的 app 读取会弹一次授权，不是静默可读。
/// dev/prod 两个 build 同机共享密钥时，跨 bundle id 顶多弹一次授权——不需要 access group。
///
/// 写操作**返回 OSStatus**（不再静默吞）：写失败必须让 UI 看得见，否则症状是「点了继续毫无反应」。
nonisolated enum LocalKeychain {

    /// 统一服务名（不随 bundle id 走；密钥两项刻意两环境共享，只有 token 按环境分家）。
    static let service = "xhanx.cn.xHelperMac.local"

    enum Account: String {
        case llmKey = "llm_api_key"
        case embeddingKey = "embedding_api_key"
        // 本地 token 按环境分家：dev / prod 两种构建的 token 前缀不同（后端据此 403），
        // 共用一项的话先跑的那个会把前缀写死，另一个必然自 403。
        case tokenDev = "local_token_dev"
        case tokenProd = "local_token_prod"
    }

    /// 当前构建（Debug=dev / Release=prod）对应的本地 token 项。
    static var tokenAccount: Account { LocalPaths.isDev ? .tokenDev : .tokenProd }

    enum ReadResult: Sendable {
        case found(String)
        case notFound
        /// 用户拒绝了跨 app 的钥匙串授权（`errSecUserCanceled` / `errSecAuthFailed`）。
        case denied
        case failed(OSStatus)
    }

    /// 读取并区分「没有」与「被拒绝」——token 流程必须分得清，否则会把拒绝当没有、重生成 token。
    static func read(_ account: Account) -> ReadResult {
        var query = baseQuery(account)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        switch status {
        case errSecSuccess:
            guard let data = result as? Data, let s = String(data: data, encoding: .utf8) else {
                return .failed(status)
            }
            return .found(s)
        case errSecItemNotFound:
            return .notFound
        case errSecUserCanceled, errSecAuthFailed:
            return .denied
        default:
            return .failed(status)
        }
    }

    /// 便利读取：任何非 found 一律 nil（仅用于密钥等无需区分拒绝的场景）。
    static func string(_ account: Account) -> String? {
        if case .found(let s) = read(account) { return s }
        return nil
    }

    /// 写入。**返回 OSStatus**——调用方必须处理失败（写不进钥匙串 = 配置没生效）。
    @discardableResult
    static func set(_ value: String, for account: Account) -> OSStatus {
        delete(account)
        var query = baseQuery(account)
        query[kSecValueData as String] = Data(value.utf8)
        query[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlocked
        let status = SecItemAdd(query as CFDictionary, nil)
        if status != errSecSuccess {
            warn("Keychain 写入失败 \(account.rawValue)：\(describe(status))")
        }
        return status
    }

    @discardableResult
    static func delete(_ account: Account) -> OSStatus {
        let status = SecItemDelete(baseQuery(account) as CFDictionary)
        if status != errSecSuccess && status != errSecItemNotFound {
            warn("Keychain 删除失败 \(account.rawValue)：\(describe(status))")
        }
        return status
    }

    /// 人话描述 OSStatus（给 UI 用）。
    static func describe(_ status: OSStatus) -> String {
        let text = SecCopyErrorMessageString(status, nil) as String? ?? "未知错误"
        return "\(text)（OSStatus \(status)）"
    }

    // MARK: - 私密

    private static func baseQuery(_ account: Account) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account.rawValue,
        ]
    }

    private static func warn(_ msg: String) {
        Task { @MainActor in LogBuffer.shared.warning(msg) }
    }
}
#endif
