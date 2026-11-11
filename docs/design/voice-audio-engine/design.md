# 语音音频引擎：单例 + owner 租约

**估算（定稿）：4 面 · 2 缝（播放抢占的收尾语义、门控从 bool 变 owner 集合）· 1 次真机往返 · 0 个待拍决定 · 2.5~3.5h**

## 一、要解决什么

`VoiceAudioEngine` 是设备级资源的封装：一个 `AVAudioEngine`、一份 VPIO、一个空闲释放定时器。
但进程里原来有**多份**实例（`ChatViewModel.voice` 每 tab 一份 + `VoiceHotKeyController.ptt` 一份），
而 `capturing` / `playbackActive` / `releaseTask` 是实例级的 —— 一份实例空闲 8 秒后关 VPIO，
会把另一份正在采集的引擎一起停掉（详见 `docs/bugs/063`）。

根因不是「实例多」，而是**设备级资源按实例记账**。本设计把记账收进引擎自己：
一个引擎，采集 / 播放 / 语音条 pin 各自登记显式 owner。

## 二、所有权模型

| 资源 | 独占性 | 规则 |
|---|---|---|
| 采集 | 同一时刻 1 个 owner | 别人占了再开 → 抛 `VoiceAudioEngineError.busy`；同一个 owner 重复开 → 幂等 |
| 播放 | 同一时刻 1 个 owner | 新播放**抢占**旧的；被抢方 completion 收 `interrupted=true`，自己收尾 |
| 语音条 pin | 多 owner | `Set<ObjectIdentifier>`，谁 pin 谁清（ptt 改变状态不会清掉视图的 pin） |
| 麦克风门控 | 多 owner | 按 owner 记账，但**只对当前采集 owner 生效**；非采集 owner 的门一律忽略 |
| VPIO 空闲释放 | 全局 | `captureOwner == nil && playbackOwner == nil && !voiceBarOpen` |

`VoiceAudioLease`（纯逻辑，无 AVFoundation 依赖）持有这张表；`VoiceAudioEngine` 只做 IO。

## 三、引擎 API

```swift
final class VoiceAudioEngine {
    static let shared: VoiceAudioEngine

    func startCapture(owner: ObjectIdentifier,
                      onChunk: @escaping @Sendable (Data) -> Void,
                      onLevel: @escaping @Sendable (Float) -> Void,
                      onError: @escaping @Sendable (String) -> Void) throws

    @discardableResult
    func stopCapture(owner: ObjectIdentifier) -> Data?

    func setGated(_ value: Bool, owner: ObjectIdentifier, flushPreroll: Bool = false)

    func play(_ data: Data, owner: ObjectIdentifier,
              completion: @escaping @Sendable (_ interrupted: Bool) -> Void) throws
    func stopPlayback(owner: ObjectIdentifier)

    func setVoiceBarOpen(_ open: Bool, owner: ObjectIdentifier)
}

enum VoiceAudioEngineError: LocalizedError { case busy }
```

`beginCapture` 判定顺序（写死在 `VoiceAudioLease.beginCapture`）：

```
owner 已在采 → .alreadyMine（幂等，沿用现有 "capture start ignored" 语义）
别人在采     → .busy（抛错，上层提示「正在语音输入，请先松手」）
空闲         → .started（开图 + 装 tap + 启动；清 preroll/pending）
```

## 四、四种交叠情形

| 情形 | 行为 | 说明 |
|---|---|---|
| 连续对话开着 → 按双 ⌘ | 路由到**同一个** service（`voiceBarActive` 判定保持），走 `startHold()` 暂存/合并提交，不新开采集 | 这条路由是「同一时刻只有一路采集」的前提，不能删 |
| 按住双 ⌘ 时 → 点开连续对话 | 引擎抛 `busy` → toast「正在语音输入，请先松手」，连续对话不启动、按钮回弹；ptt 照常提交 | 后到者被拒（不静默丢话） |
| 双 ⌘ 已松手、识别中 → 点开连续对话 | 允许（采集 owner 已释放）；ptt 那句稍后提交，两轮前后脚出现 | 与修前一致 |
| 连续对话开着 → 语音条的「按住说话」 | 同一个 owner（`holdModeActive`），不新开采集 | 与修前一致 |

## 五、回调与门控

- `onChunk / onLevel / onError` 不再在 service init 时安装（多份 service 会互相覆盖），
  改在 `startCapture(owner:on:)` 时绑定；`stopCapture` 摘 tap 之后再清空，音频线程每块
  音频开始时取一次快照，不和主线程抢同一个闭包变量。
- `onLog` 收进引擎自己的 `logSink`（app 在 `VoiceChatService.init` 里装 LogBuffer，
  `scripts/voice-capture-probe.swift` 这类独立编译的诊断脚本可以换成立即 print）。
- 门控按 owner 记账，且**只对当前采集 owner 生效**（`isGatedLocked`）：播报方与采集方是
  同一路时才压麦。这样别的 service 遗留的门（例如它被销毁时没收干净）永远不会把新采集闷哑。
  服务侧开长按采集前先 `stopSpeaking()`，避免拿自己的播报门压住自己。
- 切换 owner 开采集时清空 `preroll` / `pending`；门控状态不随采集切换清空。

## 六、service 侧要改的点

| 位置 | 改为 |
|---|---|
| `private let audio = VoiceAudioEngine()` | `VoiceAudioEngine.shared` |
| `configureAudio()`（install 回调） | 删掉；`startCapture` 时随 owner 绑定 |
| `try audio.startCapture()` ×2（连续 / 长按） | 走同一个 `startAudioCapture()` helper |
| `audio.stopCapture()` ×3 | `stopCapture(owner: owner)` |
| `audio.play(data) { resumePlayback() }` | `play(data, owner:) { interrupted in interrupted ? stopSpeaking() : resumePlayback() }` |
| `audio.stopPlayback()` | `stopPlayback(owner: owner)` |
| `audio.setVoiceBarOpen(voiceBarActive)` | `setVoiceBarOpen(voiceBarActive, owner: owner)` |
| `audio.setGated(...)` | `setGated(..., owner: owner)` |
| `startCapture` / `beginHoldCapture` 的 catch | `busy` → 「正在语音输入，请先松手」，其余保持「麦克风采集失败：…」 |

`VoiceHotKeyController` / `ChatViewModel` 不动（只持有 service）。

## 七、纯逻辑与单测

`VoiceAudioLease.swift` 进 SwiftPM target `VoiceAudioLeaseCore`，用例：

1. 第二个 owner 开采集 → `.busy`；同一 owner 重复开 → `.alreadyMine`
2. 非 owner 停采集 → 无效果（返回 false）
3. 播放抢占返回旧 owner；同 owner 重复播 → 返回 nil
4. 非 owner 停播放 → 无效果
5. pin 多 owner：任一 owner 清 pin 不影响另一个；全清才算 `voiceBarOpen == false`
6. `canReleaseDevice`：采集 / 播放 / pin 任一存在都为 false

## 八、明确不做

- 不合并「语音服务按 tab 一份」：那是每个 tab 的 UI 状态（朗读进度、草稿、门控证据），应当分开。
- 不做 `AVAudioEngineConfigurationChange` 自动重连（切耳机 / 系统唤醒）：本次只在引擎里留一行
  配置变更日志，真机遇到再补。它属于「引擎自停」的另一条成因，与本 bug 的设备争用不同源。
- 不改语音采集 / ASR / TTS 的语义（VAD、句级合成、打断判定全不动）。

## 九、验收

见 `docs/bugs/063` 的「验收」：`audio≈held`、无采集中途 `vpio released`、四种交叠情形、
切 tab / PiP / 关窗回归。
