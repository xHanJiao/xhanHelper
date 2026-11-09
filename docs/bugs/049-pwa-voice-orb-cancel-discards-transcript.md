# 049 PWA 语音：全屏环的「结束」实际发的是 cancel，识别结果被丢弃（2026-11-08）

## 问题表现

Vue/PWA 里点麦克风说话，浮层正常显示「听」、麦克风指示灯亮，服务端日志也有
`asr first audio frame` 和若干识别结果，但停下来后没有文本、没有回复。

服务端日志末尾是 `asr cancel received`（不是 `end received`），随后连接关闭。

## 根因

C2 给 PWA 加的全屏语音环覆盖整页（`position: fixed; inset: 0; z-index: 1200`），
录音期间输入栏被它盖住，**唯一能点的就是浮层按钮「结束」**。而这个按钮绑的是
`interruptVoice()`（C3b 的点击打断）：

```js
function interruptVoice() {
  handleStop()      // 停朗读 + 断 SSE + POST /api/chat/stop
  cancelVoice()     // 发 {"type":"cancel"}，连接直接丢弃
}
```

`cancel` 的语义是「录音作废」，服务端不会吐 `final`，客户端也不会把 `transcript`
交给 `onFinalTranscript`。所以识别本身是成功的，只是结果被扔了。

## 最终方案

浮层按钮按状态分流：

- 录音中（`phase === 'listening'`）→ `stopVoice()`：发 `end`，等服务端 `final`，
  文本进输入框；按钮文案「说完了」。
- 其余状态（想 / 说）→ 仍走 `interruptVoice()`：打断朗读、停生成；按钮文案「打断」。

顺带把「听」态的提示从「说完停顿一下就会提交」（macOS C 模式语义）改成
「说完点「说完了」，识别结果会填进输入框」。

## 验证

按服务端日志复现路径：A 模式连接 → 说话 → 点浮层按钮。修复后应看到
`asr end received` → `asr session closed`，客户端 `[voice] final: chars=...`，
输入框出现识别文本。
