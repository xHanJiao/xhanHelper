# 071 · 手机浏览器 / PWA 首次授权麦克风后，长按说话录不到音、状态栏橙点不灭

**日期**：2026-11-19
**影响面**：网页端 / PWA（`front-end/`），语音条「长按说话」与输入卡「按住说话」共用
`beginHoldCapture`，两条都受影响。iOS Safari 首次授权时必然触发一次；之后同一页面里可能一直坏。

## 现象

1. 第一次长按麦克风 → 弹「是否允许麦克风」，点允许；
2. **手指已经松开，但状态栏橙色的录音指示灯一直亮着**，等于在空录（最长到 120 秒自动提交）；
3. 之后说话松手，提示「说话时间太短」，但实际说了很久；录音期间**波形一动不动**；
4. 重按多次也一样，只能退出语音条再进才有机会恢复。

## 根因

三件事叠在一起：

**① 授权弹窗吞掉手势**
弹窗期间 `pointerup` 不送到页面。`startHoldTalk` 只有「松手了就别开麦」的判据
（`aborted: () => !holdRecording.value`），手指没了它并不知道，于是麦克风流一直开着 → 橙点不灭。
输入卡 PTT 早就有这条兜底（开麦耗时 > 1.5 秒视作手势被打断），语音条长按档漏了。

**② 弹窗把 AudioContext 打断，且被缓存复用**
`ensureHoldGraph()` 在 `await addModule / await getUserMedia` **之前**就建好并缓存 `holdCtx`。
iOS 弹授权会重配 audio session，把这个 ctx 打成 interrupted；`ctx.resume()` 写在 `await` 之后，
已不在手势任务里，救不回来 → worklet 永远不 `process`。
没帧 = 波形恒 0（第 ③ 条）+ `holdChunks` 为空。
`holdCtx` 只有退出语音条 / `cleanup()` 才关，所以**一旦踩中就一直是坏的**（「有一定几率」的真相）。
`start()` 那段注释记的是同一种失败，连续档靠「在手指任务里建 ctx」躲过去了，长按档躲不过缓存。

**③ 松手时把「没接上采集」当成「说完了」**
`stopHoldTalk` 缺 PTT 里的 `live = !!holdStream` 判定。弹窗期间松手 → `frames=[]` →
`judgeHoldSpan` 判 `tooShort` → 谎报「说话时间太短」。

## 修法

1. **预热权限**：点麦克风进语音条（有效手势）时先 `getUserMedia` 拿权限、立刻停流，
   正式长按时不再弹窗。只在粗指针设备做，桌面端不弹。
2. **手势兜底**：`startHoldTalk({ isHeld })`，`ChatView` 传 `() => holdHeld`；
   开麦耗时 > 1.5 秒视作手势被弹窗吞掉 → 收手、停流、扔图、提示「麦克风已允许，请再按住说一次」。
3. **采集心跳**：接上 source 后 0.8 秒一帧都没有（**没人说话时 worklet 也每 ~46ms 上报，
   静音也有帧**，所以这是可靠的死图信号）→ 只提示「麦克风好像没工作，松手后再按一次」，不打断录音。
4. **失败即扔图**：松手时没接上采集 → 静默收；一帧都没有 → `resetHoldGraph()` 丢掉被打断的
   `AudioContext`，下一次 `pointerdown`（有效手势）里重建，并提示重按。
5. `releaseHoldTalk` 复用新的 `resetHoldGraph()`。

## 验收

- 清掉站点权限后首次进语音条：弹权限 → 长按 → 提示「请再按住说一次」→ 再长按波形有动画、
  松手出字；日志里 `mic permission primed` / `hold talk started`，不应出现 `capture stalled`
- 拒绝授权：提示麦克风启动失败，不卡状态，再点能重试
- 连续对话档首次授权行为不变
- 按住后先想几秒再说话：不误报（静音也有心跳帧）

## 影响文件

- `front-end/src/composables/useVoiceChat.js`
- `front-end/src/views/ChatView.vue`
- `docs/design/voice-hold-mode/design.md`（麦克风生命周期 + 后续变更）
