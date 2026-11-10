# 057 · VPIO 常开把整机音量压到 1/6（退出 app 才恢复）

## 问题表现

- macOS 上试过一次语音后，原本音量 3~4 档就很响的机器，要推到 **50 档**才相当于原来的 3~4 档。
- 输出设备没变（内置扬声器）、没有虚拟声卡（BlackHole 之类）在跑，`sudo killall coreaudiod` **无效**。
- **退出 xhanHelper 立刻恢复**，重开 app 后再用一次语音又会变小。

## 根因

`VoiceAudioEngine.ensureGraph()` 对 `inputNode` 和 `outputNode` 都开了
`setVoiceProcessingEnabled(true)`，之后**整个 app 生命周期内再没关过**：

- `graphReady` 只置 `true`、永不复位；`stopEngineIfIdle()` 只调 `engine.stop()`。
- macOS 上 output 端 VPIO 是**设备级**的：即使引擎已经停掉，只要持有者进程还活着且 VPIO 还开着，
  系统输出就一直走在 AEC 参考通道上，整机音量被压（−15 dB 量级），**其他 App 的声音同样变小**。
- `killall coreaudiod` 无效的原因也在这：coreaudiod 重启后，还活着的 app 立刻把设备拉回 VPIO 状态。

顺手纠正一个误判：本机内置扬声器的 nominal 采样率**本来就是 44100**（探针实测 `before` 就是 44100），
不是 VPIO 改的。拿采样率当证据会跑偏。

## 方案

先用探针 `client/xHelper/scripts/vpio-release-probe.swift` 确认可行性：
**关掉 VPIO 不抛错，之后重新打开 + 重启 engine 都成功**（挂着 `AVAudioPlayerNode` 连线也成立）。
所以不需要销毁 engine，走「空闲后关 VPIO」这条最小改：

- `VoiceAudioEngine` 新增 `inputVPIO` / `outputVPIO` / `releaseTask`：
  - `ensureGraph()` 拆成「首次接播放图」+「按需开 VPIO」两段，引擎复用后重入只补开缺的那端；
  - `stopEngineIfIdle()` 停引擎后调 `scheduleDeviceRelease()`，3 秒后 `releaseDevice()` 关掉两端 VPIO；
  - `startEngineIfNeeded()` 先取消待执行的释放（下一句 TTS 不会踩进释放窗口）；
  - 3 秒的窗口是为了跳过逐句 TTS 之间的空档，避免每播完一句都重建一次 VPIO（重建有延迟）。
  - 语音条开着时不释放（`setVoiceBarOpen`）：释放后再开要重建音频图，录音发送档按下瞬间的
    音频会进不了 tap，短句容易被判「说话时间太短」。关掉语音条后才走空闲释放。
- 释放失败只 `log` 降级，并在 3 秒后重试（最多 3 次，每个空闲周期重新计数）；仍失败就停手，避免无限打日志。

## 验证

- 结束语音会话 3 秒后，日志出现 `vpio released: ok=true engineRunning=false`；
- 同一首歌，音量 3~4 档恢复到原来的响度；
- 再进一次语音会话：TTS 正常、回声消除仍生效（需真机听，客户端构建由用户在 Xcode 做）；
- 语音条开着期间不应出现 `vpio released`（被钉住）；退出语音 3 秒后才出现；
- 录音发送档：隔 10 秒以上再按麦克风，**首帧不应被吞**（说「一二三」能完整出字，不被判「说话时间太短」）。

## 相关

- `docs/bugs/048-voice-vpio-multichannel-silence.md`——同一处 VPIO 的另一个坑（多声道导致识别为空）
- `client/xHelper/xHelper/Shared/Services/VoiceAudioEngine.swift`
- `client/xHelper/scripts/vpio-release-probe.swift`
