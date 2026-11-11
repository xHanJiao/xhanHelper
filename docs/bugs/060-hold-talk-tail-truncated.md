# 060 · 按住说话结尾丢字（松手瞬间的最后几个字没了）

**日期**：2026-11-11
**影响面**：Mac 双 ⌘ 按住说话（`VoiceHotKeyController` → `VoiceChatService` 的 push-to-talk 路径）；
语音条「按住说话」走同一个收尾内核 `finishHoldCapture`，同样受影响。
**现象**：说完立刻松手，句尾的字稳定丢失。用户原话：「为了测试测试」结尾的 **「测试测试」四个字**，
两次都丢；话没说完就松手的那种正常。

## 根因

松手 = 立刻停采集：

```swift
isHoldRecording = false
holdCapturing = false
if let tail = audio.stopCapture() { holdPCM.append(tail) }   // removeTap + engine.stop()
```

而麦克风音频不是「实时到齐」的：VPIO（`inputNode.setVoiceProcessingEnabled(true)`，回声消除要
lookahead）+ tap 的 3200 帧缓冲，本身就有 **150~300ms** 的延迟。松手时最后这句话还在
设备/引擎的缓冲里、没送到 tap 回调，`removeTap()` + `engine.stop()` 把它们直接丢掉。
`stopCapture()` 返回的 `tail` 只是「已到 tap、但不足一个 6400B 块」的余量（实测 ~6KB ≈ 190ms），
救不回还在设备缓冲里的那一段 —— 表现为句尾整块消失。

## 修法

松手后**先留着 tap 再收 300ms 尾巴**，然后才停：

```swift
let held = Date().timeIntervalSince(holdStartedAt)
if !cancelled {
    try? await Task.sleep(for: .milliseconds(holdTailGraceMs))   // 300ms
    guard isHoldRecording else { return false }                  // 这期间被切档/退出语音
}
isHoldRecording = false
holdCapturing = false
if let tail = audio.stopCapture() { holdPCM.append(tail) }
```

- 这 300ms 用户已经不说话了，收到的是静音，ASR 会忽略；不改变识别内容
- 代价：每轮提交晚 300ms（识别本身要 1s 左右，感知不明显）
- 近场判定 `NearFieldGate.judgeHoldSpan` 用 20 分位估噪声底，多 300ms 静音不影响结果
- 「说话时间太短」的 `duration` 用**松手时**的 `held`，不把这 300ms 算进去

顺带把诊断日志补成 `hold done chars=… audio=…s held=…s`：`audio`（`pcm.count/32000`）
明显小于 `held` 就说明还在丢尾巴，两者接近说明采集没丢、问题在 ASR 收尾。

## 验收

真机按住双 ⌘ 说「咱们这是测试环境，我只是为了测试测试」→ 立刻松手：

- 日志 `audio` 与 `held` 接近（差值 = VPIO 延迟，应 < 0.1s）
- 识别文本含结尾的「测试测试」

若 `audio ≈ held` 但字仍丢 → 换方案：`end` 之前补 200~300ms 静音（豆包对「说完就 end」
的最后一小段常常不吐 final）。

## 影响文件

- `client/xHelper/xHelper/Shared/Services/VoiceChatService.swift`
  （`finishHoldCapture`、`holdTailGraceMs`、`hold done` 日志）
