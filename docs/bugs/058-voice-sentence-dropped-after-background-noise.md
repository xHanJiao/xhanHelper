# 058 · 说完话后远处有声音，自己那整句被近场闸吞掉

## 问题表现

- 连续对话里说完一句，远处（隔壁房间 / 客厅外）有声音，识别结果**迟迟不提交**，会话一直「听着」。
- 过几秒到十几秒才弹一次「已忽略：声音来自远处」，而**自己刚才说的那整句丢了**。
- 全程只有远处声音、自己没说话时，丢弃是预期行为（近场闸本来就在做这件事）。

## 根因：闸门的窗口锚错了时间点

1. 豆包判停要等**连续 800ms 静音**（`voice.asr.end_window_size: 800`）才下发 `sentence`。
   背景噪声让「静音」一直不成立 → 句子被拖后数秒才到达。
2. 提交闸（`NearFieldGate.shouldAccept`）只看**句子到达那一刻往前 2 秒**的电平帧
   （历史缓冲只留 2.5 秒）。等句子到达时，用户说话那几帧**早已滚出窗口**，窗口里只剩远处噪声
   （实测隔壁 ≈ `0.006`，阈值 `0.05`）→ 判 `reject` → 整句丢弃 + 限频提示。

也就是说：被判定的音频发生在几秒前，闸门却在几秒后按「最近 2 秒」取样，两者错位。

正常路径的日志对照（`07:19:08~12` 说话，`07:19:14` 才来 `sentence`，此时窗口尾部还留 0.276 的
尾音才侥幸通过）；噪声一多，尾音被挤出窗口就必丢。

## 最终方案

- `NearFieldGate.containsNearSpeech`：**任意 2 秒窗**判成 `accept` 就算「本次发言出现过近处人声」；
  数据不足（帧少 / 跨度 <0.8s）返回 false，不记证据。
- `VoiceChatService.handleLevel` 每帧算一次证据（`nearFieldHeard`，命中即锁存）；
  `sentence` 到达时用 `nearFieldHeard || shouldAccept(当前窗)` 放行，**取用即清零**。
- 只清标记不够：清零时 2 秒窗里还留着本句的帧，下一帧会立刻把证据重新锁存，于是
  「说完一句后的 2 秒内，任何远场句子都被放行」。所以再记一个 `lastSentenceAt`
  （提交时刻），`containsNearSpeech(since:)` 只用**提交之后**的帧当证据。
- 保留 `shouldAccept` 那一路是为了守住「数据不足放行」（首句很短、历史不足 2 秒时行为不变）。
- PWA 同款：`useVoiceChat.js` 在 `handleWorkletMessage` 里按 100ms 节流记证据，`acceptNearField` 取用。
- 跨会话清零：`startSession` / `cancel` / `endContinuous`（上一轮的证据不能放行新会话第一句）。
- 本轮发言结束时也清零（`beginSpeechStream` / `beginReplyStream`）：近场那句若被 ASR 丢掉，
  不会有 `sentence` 来消费证据，留着会把下一句远处的声音放行。
- 再加 15 秒作废（`nearFieldHeardAt`）：整轮一条 `sentence` 都没有时连回复都不会开始，
  上面那个清零点也走不到。15 秒不能设太短——句子本来就会被噪声拖后几秒，太短等于把本 bug 修回去。

## 为什么不做「本地 VAD 前处理」把远场音频直接清零

三个原因，先记在这，避免以后重复讨论：

1. **不可撤销**：后处理判错只是丢一句（还能调阈值重来）；前处理把帧清零，音频就没了，
   而且连「已忽略」提示都给不出来。用户只会觉得「语音坏了」。
2. **逐帧切会挖洞**：近/远不是每帧稳定（音节间隙、换气、轻声辅音都低），按阈值逐帧切会在
   自己的话里挖出洞，豆包可能提前判停或识别残缺；要避免就得加迟滞 + 预卷，状态机复杂度和
   「播报门 × 打断门」再叠一层。
3. **连续档省不到钱**：豆包按音频时长计费且 8 秒收不到包就断会话，远场帧清成静音后包照发、
   时长照算。唯一省钱的是录音发送档——它本来就是前处理（整段不上传）。

先加**只写日志的观察探针**（`nearfield probe: chars=… span=… holes=…% maxHole=…`）评估可行性：
统计「第一次过阈值」到「句子到达」之间被阈值关掉的时长占比，句间静音与尾部静音不计入。
判据：占比 <5% 且集中在句首/句尾，才值得考虑真上本地门。

## 验证

- 说完一句后立刻放远处声音（外放视频 / 隔壁说话）：字应在噪声停下后正常提交，不再弹「已忽略」。
- 全程只有远处声音、没人说话：仍被丢弃 + 提示（行为不变）。
- 日志 `nearfield probe` 的 `holes` 占比（<5% 才考虑前处理）。
- 首句很短（历史不足 2 秒）：仍走 fail-open，不被误吞。

## 相关

- `docs/design/voice-nearfield/design.md`（闸门设计，含实测电平数据）
- `docs/bugs/055-voice-idle-keepalive-farfield-noise.md`（同一批问题的续命闸）
- `client/xHelper/xHelper/Shared/Services/NearFieldGate.swift`、`VoiceChatService.swift`
- `front-end/src/utils/nearFieldGate.js`、`front-end/src/composables/useVoiceChat.js`
