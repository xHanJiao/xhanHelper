# 061 · 网页端语音提交丢图（长按录音带图，图发不出去）

**日期**：2026-11-11
**影响面**：手机网页端（PWA）语音模式的两条提交链路 —— 长按说话（`submitHoldSentence`）、
连续对话（`submitVoiceSentence`）。macOS / iOS 原生客户端不受影响。
**现象**：先选了一张图、再进语音、长按说一句话松手 —— 消息发出去了，图没跟着发；
图一直留在输入胶囊上方的草稿托盘里，看上去像「发不出去」。

## 根因

`ChatView.vue` 有三条提交路径，只有打字发送那条读草稿图：

| 路径 | 代码 | 带图 |
|---|---|---|
| 打字 / 点发送 | `handleSend()` | ✅ `ready.map(d => d.imageId)` → 本地气泡 `imageIds` + `attachments`，随后 `clearDrafts()` |
| 连续对话 | `submitVoiceSentence()` | ❌ 写死 `imageIds: []`，`sendMessage` 不传 `attachments` |
| 长按说话 | `submitHoldSentence()` | ❌ 同上 |

而 `AttachmentTray`（草稿图托盘）在语音条打开时**照常显示**（模板里只按 `draftImages.length` 渲染，
没有 `voiceBarOpen` 判断；语音条打开后图片按钮被隐藏，所以真实顺序必然是「先选图 → 再进语音」）。
图上一直在、消息里没有 → 表现为丢图。

## 修法

- 新增 `takeVoiceImages({ wait })`：取走托盘里 `status === 'done'` 的图，返回 `imageId` 列表，
  并从托盘移除这几张（不发 `clearDrafts()`，别把上传中/失败的也清掉）。
- **长按说话**：`wait: true` —— 松手时图可能还在传，等它传完再连同文字一起发。
  等图期间弹「图片上传中，完成后自动发送」；15 秒兜底（`waitForUploads`），
  超时弹「图片上传超时，未完成的图片已跳过」；有失败图弹「部分图片上传失败，已跳过」
  （同一张草稿只提示一次，避免连续档每句都弹）。
  等图期间用 `holdSubmitting` 挡掉新的按下：识别收尾时 `holdTranscribing` 已经被清掉，
  只靠它挡不住，会并发提交并让后一条被 `sendMessage` 的 `isStreaming` 静默丢掉。
- **连续对话**：不等待 —— 每条判停分句都是一轮，等待会拖慢对话；
  只带已完成的图，没传完的留在托盘里由下一句带走。同样对失败图给提示。
- 等图用 `waitForUploads(() => draftImages.value)` 传 getter：等图期间用户删图/清空会把数组整体换掉，
  传快照会白等满 15 秒并误报超时。
- 两条链路都把 `imageIds` 写进本地气泡，并在 `sendMessage` 里传 `attachments`。

## 验收

手机网页端：

1. 选 1~3 张大图 → 进语音 → 长按说一句**立刻**松手 → 先看到「图片上传中，完成后自动发送」，
   消息在图片传完后发出，气泡里带全部图，草稿托盘清空
2. 图早就传完再按住说 → 无提示、立即发出、带图
3. 故意断网选一张 → 文字照发，弹「部分图片上传失败，已跳过」，失败的图留在托盘
4. 纯语音（无图）不回归；连续对话第一句带图、后续句子托盘为空

## 影响文件

- `front-end/src/views/ChatView.vue`（`takeVoiceImages`、`submitHoldSentence`、`submitVoiceSentence`）
- `front-end/src/utils/voiceImages.js`（`waitForUploads`）+ `voiceImages.test.js`
