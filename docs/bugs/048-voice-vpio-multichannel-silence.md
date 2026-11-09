# 048 语音聊天：VPIO 多声道输入导致识别永远为空（2026-11-08）

## 问题表现

C 阶段（C3a 之后）在 macOS 上：

- 连续对话：麦克风指示灯亮、全屏语音环**纹丝不动**，说完没有回复，客户端/服务端都没有任何报错日志。
- A 模式（单次录音）同样失效，停止录音后提示「没有识别到内容」。

两条路径都是「采集看似正常、结果恒为空」，所以第一轮排查会误以为是 ASR 或会话逻辑的问题。

## 根因

C3a 为了让 TTS 与采集共用一个 `AVAudioEngine` 以便启用 VPIO（回声消除），把采集从
`VoiceAudioCapture` 换成了 `VoiceAudioEngine`。开启 VPIO 后，**输入节点的格式会从
`44.1kHz/1ch` 变成输出设备的格式**——本机外接多声道输出，实测是：

```
vpio input = 48000Hz / 5ch / planar
```

`VoiceAudioEngine` 仍然用一行 `AVAudioConverter(from: inputFormat, to: 16k/mono/int16)` 做转换。
5ch→mono 这个转换**不报错、`convert` 返回 `.haveData`、帧数也正常**，但产出的样本全是 0。
于是：

- 客户端每 200ms 照常给服务端发 6400 字节**纯静音**；
- 豆包 ASR 收到 4.8 秒静音 → 没有 definite 分句 / final 文本为空；
- A 模式 `finishTurn` 拿到空文本 → 「没有识别到内容」；
- 音量也是从转换后的 buffer 算的 → 恒为 0 → 语音环不动；
- 全链路没有 error → 没有任何日志。

用 `client/xHelper/scripts/voice-capture-probe.swift` 一次跑出对比：

```
plain (不开 VPIO): 44100Hz/1ch  peak=0.254   ← 正常
vpio  (app 实现) : 48000Hz/5ch  peak=0.000   ← 转 mono 后全零
vpio-raw(原始)   : ch0..ch4 都有 0.783       ← 音频确实在输入里
```

## 最终方案

`VoiceAudioEngine` 不再把多声道 buffer 直接丢给转换器：

1. 转换器改为「**mono（原始采样率）→ 16k/16bit/mono**」；
2. `handle(_:)` 里若输入 `channelCount > 1`，先 `downmixToMono()` 逐帧取各声道平均，
   得到原始采样率的 mono float buffer，再走转换器；
3. 单声道输入（不开 VPIO 的老设备 / 单声道麦克风）路径不变。

顺带修掉一个同源的哑火路径：`stopSpeaking()` 只在 `wasSpeaking` 时解门控，
而 TTS 播完有 600ms「尾音门」窗口（`isSpeaking` 已为 false、`micGated` 仍为 true）。
在这个窗口里启动新一轮连续对话，门控会永久留在「关」的状态，一帧都不会发给 ASR。
现在 `wasSpeaking || micGated` 都解门控，`startSession` 里再做一次兜底。

## 验证

- 探针 `vpio` 模式修复后：`level=0.197`，`chunks=24/5s`（修复前 `level=0.000`）；
- `go build ./...`、`go test ./...` 全绿；macOS / iOS `swiftc -typecheck` 0 error；
- 真机仍需确认：A 模式能出文本、C 模式能出分句并朗读、外放时不误打断。

## 复现/诊断入口

```bash
xcrun swiftc -o /tmp/voice-probe \
  client/xHelper/scripts/voice-capture-probe.swift \
  client/xHelper/xHelper/Shared/Services/VoiceAudioEngine.swift \
  -sdk "$(xcrun --show-sdk-path --sdk macosx)" -target arm64-apple-macos26.3
/tmp/voice-probe vpio 5     # 走 app 的采集实现
/tmp/voice-probe plain 5    # 不开 VPIO 的对照
/tmp/voice-probe vpio-raw 5 # 直接看原始多声道
```
