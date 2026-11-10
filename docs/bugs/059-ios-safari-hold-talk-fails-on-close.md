# 059 · 手机 Safari 长按说话：识别成功却报「语音识别失败」

**日期**：2026-11-10
**影响面**：PWA 录音发送档（移动端长按说话）在 iOS Safari / 所有 WebKit 上**必挂**；Chrome 桌面正常。
**现象**：手机 Safari 长按麦克风、松手后弹红色「语音识别失败」，消息没发出去。

## 根因

服务端 ASR 正常识别并下发 `sentence`，是**客户端把结果当失败丢掉了**：

1. `server/internal/controller/voice.go` 收尾只有 `defer client.Close()`。gorilla 的 `Close()`
   直接关 TCP，**不发 WebSocket 关闭帧** → 浏览器看到异常关闭（code 1006）。
2. 两个引擎对「异常关闭」的事件不一样（本地最小复现，服务端只回一句话然后直接关 TCP）：

   | 引擎 | 事件顺序 |
   |---|---|
   | Chrome | `OPEN → MESSAGE → CLOSE code=1006`（无 error） |
   | **WebKit**（iOS Safari 内核） | `OPEN → MESSAGE → **ERROR** → CLOSE code=1006` |

3. `front-end/src/composables/useVoiceChat.js` 的 `injectAudio` 里
   `ws.onerror = () => finish(new Error('语音识别连接失败'))`，而 `finish` 一进来就 `reject`，
   **已经攒在 `text` 里的识别结果被丢掉** → `transcribeHold` 的 catch → `onError('语音识别失败')`。

线上日志佐证（2026-11-10）：三次长按都是服务端 `asr sentence out` 成功，但同一时刻
**没有任何 `POST /api/chat`**——句子没到对话，被客户端扔了。

## 修法

- **客户端（关键）**：`injectAudio` 的 `finish` 里，只要已经收到过文本就按成功算，
  连接怎么断都不再报错：

  ```js
  if (text) resolve(text)
  else if (err) reject(err)
  else resolve(text)
  ```

- **服务端（治本）**：正常收尾路径补一个关闭帧，WebKit 就不会再发 `onerror`：

  ```go
  client.WriteControl(websocket.CloseMessage,
      websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
  ```

  只在「正常结束」分支发（读失败/客户端已断开那几条路发了也白发，还会刷日志）。

修完复验（同一个最小复现）：WebKit 与 Chrome 都变成
`OPEN → MESSAGE → CLOSE code=1000 wasClean=true`，不再有 `ERROR`。

## 为什么只有手机挂

桌面 Chrome 对 1006 只发 `onclose`，`finish()` 无 err → resolve(text) 正常出字；
WebKit 多一次 `onerror`，正好撞上「有 err 就 reject」。所以这个 bug 在桌面上一直看不出来。

## 相关

- 客户端兜底后，即使后端是旧版本（不发关闭帧）也不会再丢话；后端那行只是让 WebKit 不再报 error。
- 遗留（P3，未改）：`transcribeHold` 里 `gen !== generation` 提前 return 时 `holdTranscribing`
  不复位，波形条会卡在「想」。
