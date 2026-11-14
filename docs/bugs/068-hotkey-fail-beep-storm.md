# 068 · 双 ⌘ 起不来时「嘟嘟嘟」响个不停（每 33ms 重试一次）

**日期**：2026-11-14
**影响面**：macOS 客户端（`client/xHelper`）。三个问题在同一次事故里叠在一起：
① 双 ⌘ 按住说话在**启动失败**时 beep 循环（必现）；
② `DeviceManager.verifyToken()` 把「连不上服务器」当成「会话失效」，会白删登录；
③ dev / prod 两个 build 共用同一份 app 身份，user defaults / WebKit 数据（cookie、localStorage、缓存）/ TCC 授权全共享
（钥匙串项的 ACL 绑签名，不受 bundle id 影响；密钥本来就按环境分项）。

## 现象

按住左右两个 ⌘ 不放，浮层照常出现，但同时开始「嘟嘟嘟」连续响，松手才停。
日志里同一条 warning 一秒刷 30 行（正好等于轮询频率）：

```
~/Library/Caches/com.xhan.xhanHelper/logs-dev/current.log
[hotkey] 没有后端凭据（窗口没开过？），这次忽略   × 845 行
2026-11-14T04:39:49Z ~ 04:39:55Z   每秒 30 行
```

## 根因

两个 bug 串在一起。

### 1. 热键失败后没有「本次按下已处理」的闸门（beep 循环）

`VoiceHotKeyController` 用 `CGEventSource.flagsState` 30Hz 轮询判定双 ⌘（不吞键、免辅助功能授权）。
`tick()` 的进入条件是「还按着 + 没开录 + 没作废」：

```swift
} else if !cancelled, !armed, now.timeIntervalSince(chordDownAt ?? now) >= armDelay {
```

而 `begin()` 的失败出口（没有凭据 / `hotKeyPress` 返回 `.failed`）只把 `armed` 清回 `false`，
**不设置 `cancelled`**。于是下一帧（33ms 后）条件又成立 → 再进 `begin()` → 再 `NSSound.beep()`，
一直循环到松手。`armDelay` 本来就是 0，一次按下只该试一次。

这个 bug 从双 ⌘ 落地（`c9912553`，11-10）就在，只是以前按住时间短（10 号日志里只有 0.3 秒 10 行），
听起来像「响了一声」。

### 2. 断网被当成掉登录，把失败变成了必现（触发条件）

`DeviceManager.verifyToken()` 的 catch 不分错误类型，一律清会话：

```swift
} catch {
    LogBuffer.shared.error("verifyToken failed: \(error)")
    onSessionExpiring?()
    clearAuthState()          // 连不上服务器也删 token
}
```

`performTokenRefresh()` 那边只在 401 才清，`verifyToken()` 这条路径漏了同样的判断。
`docs/design/auth-device-sessions/design.md` 第 129 行本来就写明「401 → 走统一登出；网络错误忽略」，
是代码当年没对齐。
后端没起（本地 dev 的 `localhost:8080`）时启动 → `verifyToken failed: 无法连接服务器` → token 被删 →
下次启动 `app launched, hasToken=no` → 双 ⌘ 拿不到凭据 → 每次按下都落进上面那个 30Hz 循环。

日志里它自己犯过两次（01:51、03:41 各一次），之后 04:20 重启就是 `hasToken=no`。

## 最终方案

- `VoiceHotKeyController`：两个失败出口都置 `cancelled = true`（「本次按下已作废」= 不再重试），
  一次按下最多响一声。`cancelled` 在松手时照旧重置。
- `DeviceManager.verifyToken()`：只有 `code == 401` 才 `onSessionExpiring?()` + `clearAuthState()`；
  其余（连不上、超时、5xx）只记错误并保留 token。为此 `getDefaultConversationInfo()` 抛的错误
  从 `URLError(.badServerResponse)`（丢状态码）改成带 HTTP 状态码的 `NSError`。

## 验证

- 正常登录按住双 ⌘ 3 秒：HUD 出、开录、松手识别，无重复 beep。
- 停掉后端 / 清掉 token 再按住 3 秒：只响一声，日志只多一行。
- 停掉后端后重启 app：`hasToken=yes` 保留，进主界面（接口报错但不登出）；真 401 才回登录页。
- Debug 构建开一个 Chromium 标签（内置浏览器选 CEF）：确认内核能起 —— CEF 的 5 个 Helper 的 bundle id 仍是
  `xhanx.cn.xHelperMac.helper*`，与 Debug 父 app 的 `.dev` 前缀不一致（`fetch-cef.sh` 里有 `shortcut:` 说明）。

## 第三个问题：dev / prod 共用同一份 app 身份

查上面两条时顺带发现：两个 build 的 `PRODUCT_BUNDLE_IDENTIFIER` 都是 `xhanx.cn.xHelperMac`
（`project.pbxproj` 里 Debug / Release 同一行值，Debug 只是另设了显示名 `xhanHelper dev`）。
macOS 把下面这些都挂在 bundle id 下，于是两边其实是一份：

| 共享物 | 位置 | 后果 |
|---|---|---|
| UserDefaults 整个域 | `~/Library/Preferences/xhanx.cn.xHelperMac.plist` | token、草稿、窗口位置、文件授权目录都是同一份 |
| Keychain | 传统钥匙串项（`LocalKeychain`），ACL 绑签名而不是 bundle id | 密钥 / 本地 token 本身**早已按环境分项**（`llm_api_key_dev` / `_prod`），同 id 时不会串；换成独立 id 后跨 build 读取最多弹一次钥匙串授权 |
| WKWebView 数据 | `~/Library/WebKit/<bundle id>` | 一边在内置浏览器登录过的网站，另一边也登录着 |

所以 dev 只要跑一次 `clearAuthState()`，prod 下次启动就是未登录（prod 正在跑的进程内存里还有 token，
看起来才「没事」）；反过来 dev 登录 localhost 会把 prod 的云端 token 覆盖成 localhost 的，
下次 prod 刷新就 401 被踢。**这条是事故的真正入口**：dev 掉登录 → 双 ⌘ 每次按下都失败 → 撞上第一条的 beep 循环。

### 修法

- macOS **Debug** 配置的 bundle id 改成 `xhanx.cn.xHelperMac.dev`（Release 不动）。
  一条设置，UserDefaults 域 / WebKit 数据（cookie、localStorage、缓存）/ TCC 授权（麦克风、通知）全部自动分开，
  不必再逐个 key 加 `_dev` 后缀；钥匙串项的 ACL 绑签名，随 bundle id 变化 → 跨 build 读取最多弹一次授权
  （密钥本来就按环境分项，不会串）。
- Debug 构建的日历镜像默认关（`ReminderCalendarMirror.defaultEnabled = !LocalPaths.isDev`），
  否则两个 build 会往**同一个真实日历**写同一件事。
- 三处「调试用 `defaults write`」说明改指 `.dev` 域（`BrowserEngineRouter` / `LocalAuthService` / `LocalSettingsViews`）。
- 决定记录见 `docs/design/local-sqlite/design.md` 决定 #11 与尾部后续变更（推翻了第 4 期「同一 app 身份」的决定）。

### 一次性代价

dev 重新登录 + 重给麦克风/通知权限；旧的 `UserDefaults` 域留在原处不删，dev 设置里的永久文件授权目录、内置浏览器登录态要重建；
本地模式密钥项本来按环境分项（`llm_api_key_dev` / `_prod`），跨 build 读取最多弹一次钥匙串授权；
Xcode 自动签名要注册新 App ID（需联网）。

还有一处要手动收尾：改动前 dev 已经写进**真实日历**的 `[xhanhelper:<reminder_id>]` 事件，映射还在旧 `UserDefaults` 域里，
新域读不到 → 不会再被自动清理，`设置 → 清理已镜像事件` 也会显示 0。需要的话手动在日历里删一次。
