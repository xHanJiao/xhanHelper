# 051 语音打断（barge-in）在 macOS 上永远不触发，会话被空闲计时器收掉

## 问题表现

macOS 连续对话中，AI 播报时用户直接开口（PWA 上同样的操作能打断），macOS 毫无反应：

- 朗读继续念完，用户这句话没被识别；
- 30 秒后弹「30 秒没有听到说话，语音会话已结束」，看起来像「报错退出」。

## 根因

1. **打断阈值定得比真实电平高。** `VoiceChatService.handleLevel` 用 `max(0.32, 残响本底 × 1.8)`
   判「用户开口」，而 `onLevel` 传的是 RMS×4 的刻度。真机日志（`logs-dev/current.log`，
   2026-11-08 21:53）播报期间：

   ```
   level raw=0.085 peak=0.247 gated=yes threshold=0.320
   level raw=0.217 peak=0.281 gated=yes threshold=0.320
   level raw=0.079 peak=0.183 gated=yes threshold=0.320
   ```

   用户明确在说话，但帧值 0.08~0.28 全部低于 0.32；同一段里 AEC 残留回声只有 0.00~0.02
   （说明 VPIO 消回声是好的，是阈值本身错了）。外放时 VPIO 会压输入电平，比耳机更低，
   0.32 这个值等于只在「对着麦克风喊」时才可能触发。

2. **空闲收口只看 `sentence`。** `armIdleTimer` 只在收到判停分句时重置；被打断吞掉的话不产生
   分句，于是最后一次分句 30 秒后计时器照常收口——用户刚说过话也照样收。

## 方案

- `bargeInLevel` 0.32 → 0.10；门刚打开的 250ms 内不判定（播放起始的爆音不算打断）。
  残响本底仍是 EMA × 1.8，外放漏音变大时阈值自动跟着抬。
- 新增 `lastVoiceAt`：`handleLevel` 里电平 > 0.08 就刷新；空闲计时器到点时若
  `now - lastVoiceAt < 30s` 则顺延（`isSpeaking` / `isChatBusy` 的顺延逻辑不变）。

影响文件：`client/xHelper/xHelper/Shared/Services/VoiceChatService.swift`、
`docs/design/voice-chat/design.md`（C3a 阈值与后续变更）。

## 验证

macOS `swiftc -typecheck` 通过。真机待验：播报中直接开口 → 朗读立即停、`[voice] barge-in triggered`
日志出现、这句话被提交成新一轮；说完后静默 30 秒以上才收口。
