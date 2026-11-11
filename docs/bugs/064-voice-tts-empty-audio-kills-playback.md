# 064 · TTS 返回 0 字节音频被当成功，播到那一句整轮朗读中断

**日期**：2026-11-11
**影响面**：网页端（`front-end/`）连续对话的句级朗读；macOS / iOS 客户端同样吃服务端这个 200 响应，
但客户端泵是「单句失败只记日志、继续下一句」，所以只丢一句、不会整轮静音。

## 现象

一段正常回复朗读到第 4 句时突然没声，控制台：

```
[voice] playback failed  Error: audio element error        useVoiceChat.js:1110
[voice] mic gate off                                       useVoiceChat.js:95
```

界面提示「语音播放失败」，**后面几句虽然已经合成好也不再念**。

## 证据

`server/logs/dev-2026-11-11.log`：

```
20:56:58 [voice] tts ok (chars=22, billed=22, bytes=34221)
20:56:58 [voice] tts ok (chars=28, billed=28, bytes=41901)
20:56:58 [voice] tts ok (chars=19, billed=0,  bytes=0)     ← 这一句
20:56:58 [voice] tts ok (chars=22, billed=22, bytes=35565)
```

那一轮的回复切句后，19 字符的是「热水池叫 お風呂 或 湯船（ゆぶね）。」——豆包对这句一个字节都没吐
（`billed=0`，它认为没啥可念），但仍然发了结束标记。

## 根因（三层）

1. `server/internal/voice/tts.go` 的 `Synthesize`：只在「流自然结束、没见到结束标记」分支检查
   `len(audio)==0`；走到 `code == 20000000` 时**直接 `return audio, nil`**。0 字节音频被当成功。
   （同一处还漏了「没等到结束标记但 EOF 干净」——那是残缺 mp3，也被当成功。）
2. `controller/voice.go` 照单下发 `200 + Content-Type: audio/mpeg + 0 字节`。
3. `front-end` 的 `api.postBlob` 只认 `r.ok`，拿到 0 字节 blob；`playBlob` 设 `el.src` 后
   `<audio>` 触发 `error` → `runSpeechPump` 的整段 `try` 直接 `catch` 退出 → 整轮朗读结束。

## 修法

- **服务端（成功的判据收紧）**：`Synthesize` 只有「收到结束标记 **且** 已有音频」才算成功；
  结束标记前没音频、或结束标记都没等到，都返回 error。handler 于是回 502「语音合成失败」，
  前端 `freezeSentence` 的 `.catch` 早已 `return null` → 泵跳过这一句继续念后面的。
- **前端（失败不放大）**：`runSpeechPump` 里 `playBlob` 的失败改为「单句跳过」，
  连续两句都放不出来（设备/浏览器级故障，例如自动播放被拦）才抛出走原来的中断 + 提示。
- 单测：`TestSynthesizeEmptyAudioOnEndCode`、`TestSynthesizeMissingEndCode`。
- 设计文档同步：`docs/design/voice-chat/design.md` 的 TTS 响应契约。

## 已知未做

- 没去追豆包为什么对这句回 0 字节（服务端看不到原因，`billed=0` 说明它判定无可念内容）。
- 念不出来的句子仍会发一次 TTS 请求（多一次往返，但不计费）；要省掉得在前端做 kana/符号判定，属优化。
