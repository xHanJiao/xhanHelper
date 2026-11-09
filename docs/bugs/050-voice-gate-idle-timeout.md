# 050 语音连续对话：播报期间停止发音频触发豆包 8 秒等包超时（2026-11-08）

## 问题表现

macOS 连续对话里，说一句 → AI 回答 → **TTS 播报到一半**（8–9 秒）报「语音识别失败」，退出语音模式；
退出后菜单栏的麦克风占用一直不消失（仍显示「语音进行中」）。

服务端日志：

```
asr read failed (user=..., elapsed=24s): payload size 45000081 exceeds frame 137 bytes
```

## 根因（两个）

### 1. 门控期间完全停发音频 → 豆包等包超时（45000081）

火山引擎错误码文档写得很明确：

> 45000081 — 服务端在 8 秒内未收到客户端的下一个音频包，触发等包超时，主动断开当前会话。

C3a 为避免把 TTS 回声送进 ASR，播报期间会「麦克风门控」：分片不进 ASR、只留 600ms pre-roll。
大约 10 秒以上的回复，门控就超过 8 秒 → 豆包主动断开。

我们日志里那个「payload size 45000081」不是长度，是**错误码被当成了 payload size**：
官方错误帧布局是 `header(4) + 错误码(4) + payload size(4) + JSON`，比普通帧多一个错误码字段。
`decodeFrame` 按普通帧解析，把 45000081 读成 payload size，于是报「超过帧长 137 字节」，
真实错误被掩盖成「语音识别失败」。

### 2. 播放完 engine 不停 → 麦克风占用不退

`stopEngineIfIdle()` 用 `player.isPlaying` 判断是否还在播。`AVAudioPlayerNode` 的 buffer
播完后 `isPlaying` **仍然是 true**，所以 engine 永远不满足「空闲」条件，麦克风一直占着。

## 最终方案

- **门控期改发等长静音**：`VoiceAudioEngine.handle` 在 `gated` 时把真实音频存进 pre-roll（打断用），
  同时按原 200ms 节拍给 ASR 发**全 0 的等长 PCM**，流不再空闲。识别内容不受影响。
- **错误帧按官方布局解析**：`decodeFrame` 对 `msgErrorResponse` 先读 4 字节错误码，再读 payload size；
  错误信息现在是真实的 `doubao asr error 45000081: {...}`。
- **播放标志**：`VoiceAudioEngine` 增加 `playbackActive`（`play` 置 true，完成回调/`stopPlayback` 置 false），
  `stopEngineIfIdle()` 用它判断；buffer 播完 → engine 停 → 菜单栏麦克风占用释放。
  不在 completion 里调 `player.stop()`（会重入完成回调）。

## 验证

`cmd/voice-smoke` 新增 `-gap-at / -gap-seconds / -gap-silence`：

```bash
# 静默门控 12 秒 → 复现
go run ./cmd/voice-smoke -wav /tmp/t.wav -result-type single -gap-at 6 -gap-seconds 12
#   接收失败: doubao asr error 45000081: {"error":"Timed out waiting for the next audio packet..."}

# 门控期间发静音 → 不再断
go run ./cmd/voice-smoke -wav /tmp/t.wav -result-type single -gap-at 6 -gap-seconds 12 -gap-silence
#   完成，总耗时 81203ms（连接保持）
```

真机仍需确认：外放播报 15 秒以上不再报「识别失败」；退出语音模式后麦克风图标消失。
