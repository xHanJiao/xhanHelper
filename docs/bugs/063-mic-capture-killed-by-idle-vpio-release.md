# 063 · 按住说话超过约 7.5 秒，后面的字静默丢失

**日期**：2026-11-11
**影响面**：macOS 客户端（`client/xHelper`）。双 ⌘ 按住说话与语音条「按住说话」共用同一个收尾内核
`finishHoldCapture`，都受影响。iOS 没有双 ⌘ 热键、网页端没有 `AVAudioEngine`，不涉及。

## 现象

按住说一句长话（>8 秒），松手后只识别出前半句；**「说完停一秒再松手」也救不回来**。
用户原话：「说完，等一秒松手，还是会丢字」。

多次真机日志里 `hold done` 的 `audio=` 恒定落在 7.5~7.7s，而 `held=` 是 11~29s：

```
[voice] hold done chars=31 audio=7.687s held=14.370s
[voice] hold done chars=38 audio=7.500s held=27.049s
[voice] hold done chars=25 audio=7.585s held=11.037s
[voice] hold done chars=111 audio=36.495s held=36.874s   ← 没有交叠的那几轮 normal
```

## 根因

进程里有**两份** `VoiceAudioEngine`，各自一个 `AVAudioEngine`、各自一份 VPIO 状态：

| 实例 | 谁在用 |
|---|---|
| `ChatViewModel.voice`（每 tab 一份） | 语音条 / 连续对话 / TTS |
| `VoiceHotKeyController.ptt` | 双 ⌘ / ⌃⌥V 按住说话（关窗也要能用） |

VPIO 是**设备级**资源（`VoiceAudioEngine.swift` 顶部的注释自己写了「VPIO 是**设备级**的」），
但 `capturing` / `playbackActive` / `releaseTask` 都是**实例级**字段。

触发链：

1. 双 ⌘ 按下 → `VoiceHotKeyController.begin()` 先 `stopVoicePlayback?()` 停掉视图那套的 TTS；
2. 视图实例因此走 `stopPlayback() → stopEngineIfIdle() → scheduleDeviceRelease()`（延迟 8 秒）；
3. ptt 实例开录（另一份引擎，麦克风共享）；
4. 8 秒后视图实例的 `releaseDevice()` 关掉**自己的** VPIO —— 它的 `guard !capturing` 只看自己，
   而此时正在采集的是 ptt 实例；
5. 设备上的 voice-processing IO 被拆掉，ptt 的引擎随之静默停止，tap 不再回调；
   没有配置变更处理、`capture watchdog` 只在开录后 2 秒看一次，于是**没人发现**。

客户端日志（`~/Library/Caches/com.xhan.xhanHelper/logs/current.log`）：

```
07:03:00 [voice] hold start / capture started: engineRunning=true
07:03:07 [voice] capture stats: buffers=64 frames_out=102307 ...
07:03:08 [voice] vpio released: ok=true engineRunning=false     ← 恰好在 TTS 停止后 8 秒
07:03:27 [voice] capture stop: buffers=75 frames_out=119992 ...  ← 19 秒里只采到 7.5s
07:03:28 [voice] hold done chars=38 audio=7.500s held=27.049s
```

服务端同一轮（`logs/xhan-helper-*.log`）：`asr end received bytes=239984`（=7.5s）、
只吐出一句 38 字、`end_time=7492` —— 与客户端完全对得上：丢的是**采集**，不是 ASR 收尾。
`docs/bugs/060` 补的 300ms 尾巴只能在 <8 秒的短句上生效。

同源问题还有两处（都被单引擎顺带消掉）：两份引擎可以同时开麦（按住说话时点开连续对话会
两路音频各灌一条 ASR）；两份引擎可以同时念 TTS（音频叠在一起）。

## 修法

引擎收成进程级单例，采集 / 播放 / 语音条 pin 都登记**显式 owner**，设备级资源只有一个真源：

1. `VoiceAudioEngine.shared` + `VoiceAudioLease`（纯逻辑）：采集独占、播放抢占、pin 多 owner。
2. `releaseDevice()` 的判据变成租约的 `canReleaseDevice`（无采集 owner + 无播放 owner + 无人 pin），
   不再看实例自己的字段。
3. 采集回调（`onChunk` / `onLevel` / `onError`）改成在 `startCapture(owner:on:)` 时绑定，
   不再「谁最后 init 谁覆盖」；`onLog` 收进引擎自己。
4. 麦克风门控也按 owner 记账（`gateOwners`），被抢占或退出的服务清自己的门，不会误开别人的。
5. 第二个 owner 来开采集 → 抛 `busy`，上层提示「正在语音输入，请先松手」（后到者被拒）。
   详见 `docs/design/voice-audio-engine/design.md` 的四种交叠情形。

## 验收

- 播报刚结束立刻按住双 ⌘ 长说 20 秒以上：`hold done ... audio≈held`，结尾字齐全；
  日志里不应再出现「采集过程中 `vpio released`」
- 连续对话开着按双 ⌘：不新开采集、不重复提交，松手 1.5 秒后只出现一条消息
- 按住双 ⌘ 说一半时点麦克风开连续对话：提示「正在语音输入，请先松手」，ptt 那轮照常提交；
  松手后再点能正常开
- 切 tab、PiP 进出、关窗后双 ⌘：朗读/麦克风行为与修前一致

## 已知未修

- `AVAudioEngineConfigurationChange`（切耳机、系统唤醒会让引擎自停）只记一行日志，不自动重连。
- 播放被另一路抢占时，被抢方的 `stopSpeaking()` 还会顺带跑一次自己的门控收尾；门控已按 owner
  记账，影响仅限于极窄的跨 owner 抢占窗口。

## 影响文件

- `client/xHelper/xHelper/Shared/Services/VoiceAudioEngine.swift`（单例 + owner 化）
- `client/xHelper/xHelper/Shared/Services/VoiceAudioLease.swift`（新，纯逻辑，可单测）
- `client/xHelper/xHelper/Shared/Services/VoiceChatService.swift`（吃共享引擎、回调绑定点、`busy` 文案）
- `client/xHelper/Package.swift`、`client/xHelper/Tests/VoiceAudioLeaseCoreTests/`
