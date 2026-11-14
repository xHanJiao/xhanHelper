# 长按说话（本地录音 + 松手整段灌入流式 ASR）设计

> 状态：**已实现，待真机验收**（可行性已用真实接口验证）
> 关联：`docs/design/voice-chat/design.md`（语音链路总设计）、`docs/design/voice-nearfield/design.md`（近场电平闸）、`docs/bugs/055-voice-idle-keepalive-farfield-noise.md`
> UI 预览：`docs/design/voice-hold-mode/mockup.html`（移动/桌面、浅/深、三种状态可切）
> 验证工具：`server/cmd/voice-smoke -fast`（见「可行性验证」）

## 估算（定稿）

- **5 面**：PWA 录音+灌入+提交、PWA 交互、Swift 录音+灌入+提交、Swift 交互、近场闸整段判定
- **3 缝**：iOS PWA 上滑手势与页面滚动冲突、移动网络下的建连+上传延迟未测、长按模式的麦克风启停与现有引擎生命周期
- **2 次真机往返**：PWA（iPhone）、Mac App
- **1 个待拍决定**：移动网络下松手到出字若超过 2 秒，是否加「识别中…」加载态
- **9–12 小时**

## 目标

1. 长按说话模式做到接近微信「按住说话」：按住录音、松手出字。
2. 长按模式**不建常驻 ASR 连接**：不按住就没有连接，空闲零计费、不需要空闲收口。
3. 长按模式**不朗读**回复，只把文字放进气泡。
4. 近场闸用**整段音频**离线判定，不通过就不上传。
5. 连续对话模式**行为完全不变**。

## 非目标

- 不做长按期间的实时字幕（微信也没有）。
- 不做 iOS 原生客户端的语音入口（用户 iPhone 用 PWA）。
- 不做声纹 / 说话人区分。
- 不改连续对话的传输方式（仍是长连接流式 + 续命闸 + 30 秒空闲收口）。
- 不引入录音文件识别（见下节：控制台没有该产品）。

## 为什么不用录音文件识别

原本打算用豆包「录音文件识别极速版」（一次 POST 同步返回、支持 base64）。实际排查后放弃：

- 该账号的控制台「语音识别」下**只有「流式语音识别 2.0」**，没有录音文件识别（极速版、标准版都没有入口）
- 标准版（`volc.seedasr.auc`）即使开通，提交只接受**公网 URL**，还要 submit + 轮询，延迟数秒起

改用现有流式接口的**整段灌入**（不按实时节拍，一次性推完再发结束包），效果等价：

- 服务端零改动（现有 WS + `end` 控制帧）
- 不需要任何新资源
- 松手才建连 → 按下零延迟
- 不按就没有连接 → 空闲零成本

## 可行性验证（已做）

用 `server/cmd/voice-smoke -fast -result-type single`（`-fast` = 一次性推完，不按 200ms 节拍）打真实接口：

| 音频 | 连接 | 首条 partial | final | 总耗时 |
|---|---|---|---|---|
| 6.20 秒 | 114ms | 451ms | 794ms | **794ms** |
| 24.84 秒 | 95ms | 465ms | 1943ms | **1943ms** |

- **流式接口接受快于实时的灌入**，不会限速、不报错
- 处理耗时约 `0.3 秒 + 0.06 × 音频时长`
- `result_type=single` 下：中间内容随 `definite` 下发，`final` 只带最后一句；服务端的
  `SentenceCommitter` 会按分句转成多条 `sentence`，客户端拼起来即完整文本
- 结论：**可行**。剩下的是移动网络下的建连与上行耗时（真机测）

## 方案总览

| | 连续对话（不变） | 长按说话（新增） |
|---|---|---|
| 传输 | 一条常驻 ASR WebSocket | 每次松手建一条短连接，灌完即断 |
| 空闲成本 | 静音也计费，30 秒收口 | 0（不按就没有连接） |
| 按下延迟 | 无（已连） | 无（只本地录音） |
| 松手到出字 | 0.2–1 秒（等 final） | Mac 实测 0.8–1.9 秒；移动网络待测 |
| 近场闸 | 实时帧、2 秒窗 | 整段离线判定，不通过不上传 |
| 朗读 | 有 | **无** |
| 失败影响面 | 整条会话 | 单次按下 |

完整链路：

```
按下小手按钮
  → 启动麦克风，累积 16k/16bit/mono PCM，同时照常算电平帧（波形）
按住期间
  → 上滑超过阈值 = 取消态（松手丢弃）
  → 到 60 秒上限 = 自动结束并提交
松手
  → 停止采集
  → 近场闸：整段帧判定（关闭档跳过）
     不通过 → 不上传，轻提示「已忽略：声音来自远处」（限频 10 秒）
     太短   → 不上传，提示「说话时间太短」
  → POST /api/voice/asr-ticket 换一次性 ticket
  → 连 /api/voice/asr?mode=continuous，把整段 PCM 一次性推完
  → 发 {"type":"end"}
  → 收集所有 sentence，拼成文本，连接关闭
  → 作为一轮用户消息提交（不挂 TTS 回调）
  → 回复文字照常流式进气泡
```

## 服务端

**零改动。** 复用：

- `POST /api/voice/asr-ticket`：一次性 ticket
- `GET /api/voice/asr?mode=continuous`：现有连续模式处理器
  - 二进制帧 = PCM（6.4KB / 200ms 粒度）
  - 文本帧 `{"type":"end"}` → `asr.Finish()`，随后把剩余分句吐完并关闭
  - 下行 `sentence` / `voice` / `error`
- `acquireContinuous` 每用户一条会话的限制照旧（长按模式一次只有一条）

需要确认的边界：

- 一次灌入最多 60 秒音频（≈1.9MB），远低于现有 `MaxSeconds` 与 64KB 单帧上限
- 客户端必须收完 `sentence` 再断开；`end` 之后服务端会主动关连接

`shortcut:` 不复用常驻连接池，每句话一次 WS 握手（约 100ms，实测）。若移动网络下握手明显变慢，再考虑「松手后保留 10 秒连接」的复用窗口。

## 客户端

### 模式与偏好

新增本地偏好 `voice.mode`：

| 值 | 含义 |
|---|---|
| `continuous` | 连续对话（默认，与现状一致） |
| `hold` | 录音发送（移动端长按说话 / 桌面端点击说话） |

- PWA：`front-end/src/utils/voicePref.js` 增加 `readVoiceMode` / `setVoiceMode`
- Swift：`Shared/Services/LocalPaths.swift` 的 `VoicePreference` 增加 `voiceModeKey`，默认 `continuous`
- 没设过一律 `continuous`：老用户升级后行为不变

### PWA

**采集与编码**

- 复用现有 `public/voice-pcm-worklet.js` 的 200ms Int16 chunk；长按模式把 chunk 累积进数组，不往 WS 发
- 同时照常算 `level = min(1, RMS × 4)` 并记录 `{t, level, gated:false}`（长按模式不播报，没有门控帧）
- 松手后直接拿累积的 Int16 发，**不需要 WAV 头**（流式接口收裸 PCM）

**上传与提交**

- 松手 → `POST /api/voice/asr-ticket` → `new WebSocket('/api/voice/asr?mode=continuous&ticket=...')`
- `ready` 后按 6.4KB 分片连续发完（不等节拍），再发 `{"type":"end"}`
- 收 `sentence` 累积文本；连接关闭或 15 秒超时结束
- 成功后调用现有提交路径，但**不挂 TTS 回调**：
  ```js
  await sendMessage(text, {
    ...,
    voiceMode: true,              // 仍走语音轮的短回答约束
    // 不传 onAssistantReplyDelta / onAssistantReply
  })
  ```
  回复照常流式进气泡；`speechSentences` / `speaking` / `preparing` 全程不启动，
  波形停在「想 → 听」，不会出现「说」态。

**麦克风生命周期**

- 进入长按模式**不启动**麦克风；按下才 `getUserMedia` + `AudioContext`
- 松手后停掉采集（保留 `AudioContext` 以便下次快速启动；退出模式/页面隐藏时彻底释放）
- `shortcut:` 首次按下有 100–300ms 麦克风启动延迟，微信同样存在；若实测明显，再改成进入模式时预热

**取消与边界**

- 移动端上滑取消：`pointermove` 记录纵坐标，相对按下点上移 > 60px 进入取消态（提示条变红「松开取消」），松手丢弃
- 移动端录音按钮必须加 `touch-action: none`，否则 iOS PWA 上滑会被页面滚动抢走
- 桌面端：两态按钮，录音中左侧浮出取消钮（绝对定位，不挤动录音钮）；`Esc` 同取消；窗口失焦不取消
- 太短：按住 < 300ms，或整段没有有声帧 → 丢弃 + 提示「说话时间太短」
- 上限：60 秒自动结束并提交（提示「已到 60 秒，自动发送」）

### Swift（macOS）

- 复用 `VoiceAudioEngine` 的 tap：点麦克风启动引擎采集、累积 Int16，再点一下停采集（两态按钮）
- 停采集后同样走 ticket + WS 灌入；`transcribeHold(pcm:)` 负责整段上传
- 提交用现成开关：`viewModel.sendMessage(..., speakReply: false, voiceMode: true)`
- 录音档**不建立**常驻 ASR 会话（`continuousActive` 为 false），会话计时器、空闲收口、续命闸都不参与

## 交互规格

见 `mockup.html`（控制条可切形态 / 状态），要点：

**同一档、两套交互**：移动端按住说话（触摸），桌面端两态按钮（鼠标）。档位 id 仍是 `hold`。
胶囊**恒为图标**（连续 = 波形，说话 = 点按手势 `hand.tap`），功能说明走桌面 hover
（移动端「长按说话」/ 桌面端「点击说话」只出现在 `aria-label` 与 tooltip 文案里）。
判定用 PWA 的 `isMobile`（宽度 < 1100），与项目其它地方一致。

### 移动端（触摸）

| 状态 | 语音条 |
|---|---|
| 连续对话 | `[波形 \| 点按] [退出]`，连续侧高亮，无录音按钮 |
| 待录 | 胶囊高亮点按档（`hand.tap`），左侧出现小手按钮 |
| 按住中 | 小手实心绿 + 脉冲；输入卡上方浮出提示条「上滑取消」 |
| 上滑 > 60px | 小手与提示条变红「松开取消」，松手丢弃 |
| 滑回原位 | 恢复「上滑取消」，松手正常发送 |
| 识别中 | 波形「想」态（加载态见待拍决定） |

### 桌面端（鼠标 / 键盘）

| 状态 | 语音条 |
|---|---|
| 待录 | 胶囊高亮点按档（`hand.tap`），麦克风圆钮（绿） |
| 录音中 | 麦克风变红底 + 停止方块；左侧浮出取消钮（×）；再点一下 = 停止并发送 |
| 点取消 / Esc | 丢弃本次录音，不建连、不提交 |
| 空格 | 同点击：一下开始、一下发送（焦点在文本框时不接管） |

- 桌面端**不做上滑取消**，也**不因窗口失焦取消**（录到一半切窗口不该丢）
- 移动端窗口失焦仍然丢弃（手指不在了）
- 取消钮只在录音中出现，且是**绝对定位**（不挤动录音钮，否则按钮会在鼠标底下平移）
- 胶囊点击只切档，**不录音**；退出按钮常驻，两档都在
- 模式记忆：下次进入沿用上次的档位

## 近场闸（整段判定）

现有 `shouldAccept(frames, threshold, now)` 用固定 2 秒窗，是给流式 `sentence` 用的。长按模式整段音频都在手上，改成整段判定：

```
松手
  → frames = 按下到松手的全部帧（无门控）
  → 关闭档：直接放行
  → 整段跨度 < 0.8 秒 → 按「太短」处理（不是 fail-open）
  → 用同一套「p20 估本底、剔静音帧、取有声帧中位数」逻辑判定
  → 中位数 >= 阈值 ? 放行 : 丢弃 + 轻提示
```

- 新增 `judgeHoldSpan(frames, threshold)`（三态 `accept` / `reject` / `tooShort`），与 `shouldAccept`
  共用同一套统计核心（`judgeSpan`）；`shouldAccept` 保持原语义（`tooShort` 视为放行）
- **数据不足的语义要改**：流式模式下帧不够是 fail-open（放行），长按模式下整段就是我们想判的东西，
  帧不够说明本来就没说够 → 按「太短」提示，不静默放行
- 不通过时**不建连、不上传**：省一次识别费用，也避免噪声文本进对话
- 两端纯逻辑保持一致：`front-end/src/utils/nearFieldGate.js`、`client/xHelper/xHelper/Shared/Services/NearFieldGate.swift`

## 与现有代码的边界

| 现有能力 | 长按模式 |
|---|---|
| 常驻 ASR WebSocket | 不使用（每次松手建一条短连接） |
| `sentence` / `voice` 事件 | 使用 `sentence` 拼文本；`voice` 忽略 |
| 30 秒空闲收口 | 不使用（没有常驻连接） |
| 续命闸（`shouldKeepAlive`） | 不使用 |
| 提交闸（`shouldAccept`） | 复用同一套统计，改整段窗口 |
| 句级 TTS 流水线 | 不使用（不朗读） |
| 打断闸（Swift） | 不使用（没有播报） |
| 音波条组件 | 复用，`phase` 只会在「听 / 想」之间切换 |

连续对话模式这些全部保留，**一行不改行为**。

## 风险

- **移动网络延迟未测**：Mac 实测松手到出字 0.8–1.9 秒；4G 下要多一次 ticket RTT + WS 握手，
  可能到 2 秒以上。超过就在按钮上显示「识别中…」
- **上滑取消在 iOS PWA**：`pointermove` 与页面滚动冲突，必须 `touch-action: none`；真机验证
- **麦克风启动延迟**：按下才开麦，首次可能有 100–300ms 空白；靠「按下立刻显示正在听」掩盖
- **整段灌入无实时反馈**：松手后到出字之间只有「想」态，没有部分文字
- **灌入的 VAD 行为**：一次性推完时豆包仍按音频里的静音判停分句，但可能把多句并成一条
  `definite`；客户端只需拼接所有 `sentence`，不依赖分句粒度
- **两套链路并存**：连续 / 长按是两条路径，必须靠 `voice.mode` 单一开关分流，避免状态串线
- **不朗读的感知**：用户可能以为没反应；「想」态必须可见，回复结束回到「听」
- **录音上限**：60 秒是拍的，长按超过会被截断提交
- **短连接失败**：握手或识别失败只影响这一句，提示「没听清，再说一次」即可

## 实施清单

| # | 做什么 | 落在哪 |
|---|---|---|
| 1 | 模式偏好（默认连续对话） | `front-end/src/utils/voicePref.js`、`LocalPaths.swift` |
| 2 | PWA：PCM 累积、ticket + WS 灌入、拼接文本、不朗读提交 | `useVoiceChat.js` |
| 3 | PWA：胶囊切档、小手按钮、上滑取消、太短、60 秒上限 | `ChatView.vue`、`VoiceWaveLine.vue` |
| 4 | Swift：PCM 累积、ticket + WS 灌入、`speakReply:false` | `VoiceChatService.swift` |
| 5 | Swift：胶囊切档、麦克风两态按钮 + 取消钮 | `ChatView.swift`、`InputBarButtons.swift` |
| 6 | 近场闸整段判定（`judgeHoldSpan` 三态 + 太短语义） | `nearFieldGate.js`、`NearFieldGate.swift` + 两端单测 |
| 7 | 真机验收 | iPhone PWA、Mac App |

**估时**：9–12 小时 + 2 次真机往返

## 验收标准

1. 移动端长按说一句 → 出字 → 回复只显示、不朗读
2. 录音档静置 10 分钟：服务端无 `/api/voice/asr` 连接、无 ASR 计费
3. 隔壁说话时录音 → 不建连、不提交、提示「已忽略：声音来自远处」
4. 移动端上滑到取消区松手 → 无提交、无连接
5. 移动端短按（<300ms）→ 提示「说话时间太短」，无连接
6. 桌面端点麦克风 → 再点一下发送 → 出字不朗读；录音中点取消钮 / 按 Esc → 无提交、无连接
7. 桌面端录音中切走窗口再回来 → 录音还在，可继续点发送（不丢）
8. 胶囊恒为图标（波形 / 点按手势）：桌面 hover 出「连续对话 · 一直收音，说完自动发送」/
   「点击说话 · 按一下开始录音，再按一下发送」；`aria-label` 仍是档位名；刷新页面 / 重开 App 沿用档位
9. 连续对话模式：行为与改动前完全一致（含 30 秒收口、续命闸、朗读）
10. 松手到出字的耗时：WiFi 下 ≤1.5 秒，4G 下 ≤3 秒（超出则加加载态）

## 待拍决定

移动网络下松手到出字若超过 2 秒，是否加「识别中…」加载态（我倾向加，成本很小）。

## 后续变更

- 2026-11-10：建立文档。最初方案是「本地录音 + 录音文件识别极速版」，排查后发现该账号控制台
  只有「流式语音识别 2.0」，没有录音文件识别入口；标准版又要公网 URL + 轮询，遂改为
  **松手后整段灌入现有流式接口**。
- 2026-11-10：用 `server/cmd/voice-smoke -fast` 打真实接口验证「快于实时灌入」可行：
  6.2 秒音频 794ms 出 final，24.8 秒音频 1943ms 出 final，服务端零改动即可支撑。
- 2026-11-10：UI 定稿为两段式胶囊 `[连续对话 | 长按说话]` + 长按档才出现的小手按钮；
  mockup 从 `docs/design/voice-nearfield/` 移到 `docs/design/voice-hold-mode/`。
- 2026-11-10：实现落地。近场闸没有改 `shouldAccept` 的签名，而是抽出共用统计核心
  `judgeSpan` 并新增三态 `judgeHoldSpan`（`accept` / `reject` / `tooShort`），流式提交闸语义不变。
  长按档的「按住说」入口取代了原连续档的小手按钮（连续档仍保留空格键暂存提交）。
  Swift 侧 `sendMessage` 新增 `voiceMode` 参数，用于「短回答约束 + 不朗读」。
- 2026-11-10：**录音档拆成两套交互**（用户拍板）。移动端按住说话 + 上滑取消（输入卡上方提示条
  「上滑取消」→ 红「松开取消」），删掉左侧取消钮；桌面端改两态按钮（点麦克风开始录、再点发送，
  录音中左侧浮出取消钮，`Esc` 同取消，窗口失焦不取消）。胶囊文案随形态：移动端「长按说话」、
  桌面端「点击说话」。Mac App 同步：`HoldTalkButton` → `RecordToggleButton` + `CancelRecordButton`，
  空格由「按住」改为「一下开始 / 一下发送」。涉及 `ChatView.vue`、`ChatView.swift`、
  `InputBarButtons.swift`、`mockup.html`。
- 2026-11-10：修「切档时输入框闪一下」。根因：`switchMode()` 先 `cancel()`（`active`/`continuousActive`
  置 false），连续档要等 ticket + `getUserMedia` + WS 建连才置回 true，这段里界面按
  `voiceActive` / `voiceBarActive` 把波形条换回输入框。方案：加切档桥接态
  （PWA `useVoiceChat.js` 的 `switching` + `ChatView.vue` 的 `voiceBarOpen`；Swift
  `VoiceChatService.isSwitchingMode` 并入 `voiceBarActive`），只在结构性判断上用桥接态，
  `voicePhase` / `aria-pressed` 仍看 `active`。退出/停止在过渡期直接硬收口（`stop()` / `exitVoiceMode`），
  避免在飞的 `start()` 把语音条又开起来。
- 2026-11-10：Mac 状态行按钮尺寸统一（用户拍板）。原 `RecordToggleButton` / `CancelRecordButton` 是
  34pt 实心圆 + 1.16 倍描边 + 红光，视觉上比同一行的 `IconButton`（22pt 字形、34pt 命中区）大一圈，
  中间停止键尤其突兀。两个自定义按钮整体删除，改用 `IconButton`：录音键 `mic` ↔ `stop.circle.fill`（红 + 脉冲），
  取消键 `xmark`（红）。三个键（麦克风 / 取消 / 退出语音）尺寸与字形重量完全一致。
  录音中用带圈的 `stop.circle.fill` 而不是 `stop.fill`：生成中的「停止生成」是后者，同形同色会点错。
- 2026-11-10（用户拍板：语音条按钮语义重排，推翻上一条的 `stop.circle.fill`）：
  - **发送键改绿**：长按档录音中原是红色 `stop.circle.fill`，与左侧同样红色的「取消」`xmark` 并排，
    一个取消一个提交、只差一个圆框；而本项目红色一贯表示「停止 / 危险」，用它表示提交自相矛盾。
    改成主输入栏 `SendStopButton` 同款 `arrow.up` + `appAccent`，`mic → arrow.up` 走现成的
    `.symbolEffect(.replace)` 过渡；「正在录」改由脉冲 + 左侧浮出的取消键表达。
  - **退出键改挂断**：原来是同一个 `waveform.circle` 只靠颜色翻转（绿=进、红=出），用户读到的是
    「语音开着呢」而不是「点我退出」。改成 `phone.down.fill`（红），与同一行的「停止生成」（方块）
    形状完全不同，整行读作通话条。tooltip 仍为「退出语音对话」。
  - **生成中退出常驻**：原来 `if isStreaming { 停止生成 } else { 退出 }`，想退出得先停生成、再点退出。
    改成退出恒在模式胶囊右侧、生成中也不收起；「停止生成」占它右侧一个**常驻 34pt 槽位**
    （不生成时是透明空位）。
    槽位常驻是因为这一行由两侧 `Spacer` 居中：增删按钮会让整行横跳 19pt，录音中点发送可能落空。
    红色在本行只表示停止 / 取消 / 挂断。
  - 影响文件：`client/xHelper/xHelper/Shared/Views/ChatView.swift`（`inputBarStatusRow`、`voiceContinuousButton`）。
    iOS 不受影响（语音条只存在于 macOS 与 PWA，PWA 见下一条）。
- 2026-11-10（真机反馈：最窄窗口越界 + PWA 未同步）：
  - **越界根因**：中间栏最窄 `Layout.middleMinWidth = 380` → 输入胶囊内容区只剩
    `380 - 2*24(chatHorizontalPadding) - 2*14(胶囊内边距) = 304pt`。这一行要
    `34(✕) + 4 + 34(麦克风) + 4 + 160(文本胶囊) + 4 + 34(挂断) + 4 + 34(停止槽) = 312pt`，
    超出 8pt；而 ✕ 是 `.offset(x: -38)` 的 overlay、不占布局，于是直接飘到胶囊外。
  - **修法一（取消键）**：不再用绝对定位/overlay，改成**常驻 34pt 空槽**（只有里面的图标随录音状态出现），
    既不会飘出去，也不会让麦克风从鼠标底下挪走。
  - **修法二（窄窗降级，已于同日被「恒图标」取代，见下一条）**：`VoiceModeCapsule` 增加 `compact`
    图标档（`waveform` / `mic`，160pt → 62pt），`inputBarStatusRow` 用 `ViewThatFits` 先试文本档、
    放不下再退图标档。两个变体键位一致，只是胶囊宽度不同；`help` + `accessibilityLabel` 补回被省掉的文字。
  - **PWA 同步**（`front-end/src/views/ChatView.vue`）：发送键 `.stop-core` 红方块 → 绿色上箭头
    （`.hold-send`，`.recording` 不再染红）；退出键波形圈 → Material `call_end` 实心话筒；
    生成中退出与停止键并列，停止键放在常驻 `.voice-stop-slot` 里（居中行增删按钮会横跳）；
    取消键从 `position: absolute` 改常驻 `.hold-cancel-slot`；胶囊加 `.seg-ico` 图标档 +
    `@container (max-width: 340px)` 降级（容器上下文挂在 `.chat-composer-card`）。
- 2026-11-10（模式胶囊改恒图标）：
  - 用户反馈文字胶囊（「连续对话 / 点击说话」）难看，且窄窗下「文字档 ↔ 图标档」随档位跳变。
    改为**恒用图标**：连续对话 = `waveform`，说话 = `hand.tap`（PWA 复用录音键同一份
    SF Symbol `hand.tap` 位图蒙版，不再手绘）。
    胶囊 160pt → 62pt，语音行 312pt → 214pt，最窄内容区 304pt 富余 90pt。
  - 功能说明改由 hover 承担：Mac `.help("连续对话 · 一直收音，说完自动发送" / "点击说话 · 按一下开始录音，再按一下发送")`，
    PWA 每个 `.seg` 包 `NTooltip :disabled="isMobile"`。档位名保留在 `accessibilityLabel` / `aria-label`。
  - **顺带删掉的死代码**：`VoiceModeCapsule.compact`、`inputBarStatusRow` 的 `ViewThatFits`、
    PWA 的 `.seg-text` / `@container (max-width: 340px)` / `.chat-composer-card { container-type: inline-size }`。
  - **移动端不额外加引导**（用户拍板）：没有 hover，靠 `hand.tap` 手势图标 + 录音时的「上滑取消」提示；
    胶囊文字「长按说话」是这次唯一被去掉的触屏提示。
  - 影响文件：`InputBarButtons.swift`、`client/xHelper/xHelper/Shared/Views/ChatView.swift`、
    `front-end/src/views/ChatView.vue`。
- 2026-11-10（切档时胶囊不挪位）：
  - 用户反馈点胶囊切档时它会左右移动。根因：语音行是居中的（Mac 两侧 `Spacer`、PWA
    `justify-content: center`），而录音档左侧那组键（取消槽 + 麦克风，Mac 34+4+34、
    PWA 桌面 36+4+36 / 移动端 36）是整块 `v-if`，切到连续档就消失，行宽少 76 → 居中的胶囊被挤走。
  - 修法：连续档在左侧留**同宽常驻空槽**（Mac 两个 `Color.clear.frame(width: 34)`；
    PWA `<span class="hold-slot-ghost">`，桌面 76px / 移动端 36px）。行宽恒为
    `76 + 62 + 72`，胶囊 x 固定；左右两侧各 76/72 宽，视觉仍对称。
  - 影响文件：`client/xHelper/xHelper/Shared/Views/ChatView.swift`（`voiceStatusRow`）、
    `front-end/src/views/ChatView.vue`（`.hold-slot-ghost`）。
- 2026-11-10（语音档去掉输入框外框）：
  - 用户要求：进语音模式后输入框的外框消失，音波条与按钮直接浮在消息上。
  - 两个候选出了对比图（现状 / A 全透明 / B 无框 + 模糊），用户选 **B**：正文会从浮层
    后面穿过，全透明时文字会横穿波形与按钮（红色挂断键糊在正文上），所以留一层模糊防糊。
  - 实现：两端语音档都**完全不铺底**（描边 / 底色 / 投影 / 材质全无），padding 保留
    （进出语音高度不跳）。Mac `InputBar` 的胶囊底按 `voiceStrip == nil` 条件化，
    投影同条件置空；PWA 用 `.chat-composer-card.voice-open`（`:class="{ 'voice-open': voiceBarOpen }"`）。
  - **为什么不是「无框 + 模糊」**：真机反馈 `.ultraThinMaterial` 仍然画出可见玻璃板
    —— macOS 26 的 Material 自带高光边，"只有模糊没有面"用公开 API 做不到。
    另外两端都不需要模糊：PWA 输入卡在流内（`.chat-card` 是 flex column，消息区 `flex:1`
    在它上方），正文不会从它后面穿过；Mac 是 `safeAreaInset` 浮层，滚到底时正文停在它上方，
    只有中途滚动会从控件后面穿过。
  - 影响文件：`client/xHelper/xHelper/Shared/Views/InputBar.swift`、`front-end/src/views/ChatView.vue`。
- 2026-11-10（全局按住说话改「双 ⌘」+ app 级常驻 + 玻璃浮层）：
  - 用户反馈两件事：⌃⌥V 三个键难按；PiP 模式下热键不生效。
  - **根因**：热键接线挂在 `ChatView` 的 `onAppear/onDisappear`（`wireVoiceHotKey`）。PiP 下中栏
    ChatView 被 `if !rightSidebar.isPipChat` 整个不渲染；面板**收起成小圆点**时 `PipChatContent`
    也不建 → 热键槽被 `unwireVoice` 清空后再没人重新占，按下去调的是
    `AppShortcutBridge.shared.onVoicePress?()` = nil，静默空转。红叉关窗同理（ContentView 整个销毁）。
  - **改法**：热键挪到 app 级 `VoiceHotKeyController`（App 启动即注册，与视图生命周期无关）。
    用 `CGEventSource.flagsState` 20Hz 轮询左右 Cmd 的**设备位**（0x8 / 0x10；探针实测
    `keyState(key: 54)` 读不到右 ⌘），两个同时按住
    120ms 开录、松开提交；**不吞键**（Cmd+C / Cmd+Tab / Cmd+点击 照常发给前台 app）。
    开录前若旁边还有别的键 / 鼠标按下 / 滚轮 / 第三个修饰键 → 本次作废，等两个 ⌘ 都松开才重新武装。
  - **路由**：语音条开着（`voiceBarActive`）仍交给视图那套「按住暂存、松手整段提交」；
    否则走控制器自己的 `VoiceChatService`（一次性采集）。无任何语音视图活着
    （`voiceHotKeyOwner == nil`，即关窗 / PiP 收起）时，控制器自己接管这一轮 TTS
    （`onAssistantReplyDelta/Done/Aborted` 喂给自己的服务）——所以**关窗也能听到回复**。
  - **浮层** `VoiceHUDPanel`：nonactivating NSPanel，不吃鼠标、不抢焦点、
    `[.canJoinAllSpaces, .fullScreenAuxiliary]`（别的 app 全屏也浮得出来），屏幕底部居中（跟鼠标屏）。
    内容是 `VoiceHUDContent` = 玻璃胶囊 + 输入框同一条 `VoiceWaveLine`，**不显示文字**，
    只表达 听 / 想 / 说；松手后停留 1.2s 自动收。尺寸定稿 76 × 56（候选 100 / 76 / 56 中选中间）。
  - **玻璃怎么做（macOS 26 实测）**：`NSVisualEffectView` 的 `.behindWindow` 在 Tahoe 上已经
    **不再采样窗口后面的内容**——`.underWindowBackground` / `.popover` / `.menu` / `.hudWindow`
    四种 material 全是实心灰（纯 AppKit 最小工程也复现，不是 SwiftUI 包一层的问题）。
    系统自己的玻璃在 26 换成了 `NSGlassEffectView`（Liquid Glass，`style = .clear`）。
    浮层改用它：`style = .clear` + `tintColor = .clear` + `alphaValue = 0.7`，能透出底下的
    桌面/网页（1.0 偏奶白，0.7 最清透，用户拍板）。`NSGlassEffectView` 不可用时回退
    `NSVisualEffectView`（只在 26 以下才有意义）。
    另外去掉了 `.shadow`：真面板里它会画出一个明显的灰色方影，比胶囊本身还显眼。
  - ⌃⌥V 保留作备用键（Carbon 免授权、且能吞键）。
  - 影响文件：新增 `Services/VoiceHotKeyController.swift`、`Views/VoiceHUD.swift`；
    `Services/AppShortcutBridge.swift`、`Views/ChatView.swift`、`Views/ContentView.swift`、
    `xHelperMac/App.swift`、`xHelperMac/ShortcutGuideView.swift`；
    自查脚本 `scripts/cmd-chord-probe.swift`（键位探针）。浮层定稿前用过一个真面板预览脚本
    （`hud-preview.sh`，摆多档材质/透度真 NSPanel 截图挑），定稿后已删。
  - **待真机验收**：PiP 收起态、红叉关窗态、全屏 app 上方的浮层。
    （左右 ⌘ 的读取已在真机探针验过：两个同时按住时 `flagsState = 0x100118`。）
- 2026-11-11（浮层可拖 + 位置记忆）：
  - 用户要求「能拖到想要的位置」。做法：`panel.ignoresMouseEvents = false` +
    `isMovableByWindowBackground = true`，整块胶囊当把手；`VoiceHUDContent` 加
    `.allowsHitTesting(false)`（`VoiceWaveLine` 自带 `.contentShape` + `onTapGesture`，
    不关掉会把 mouseDown 吃掉，窗口就拖不动）。
  - 位置存 `UserDefaults`（`voiceHUD.originX/Y`）。`place()` 有存档就用、并钳进
    包含它的屏幕可视区；没存档才回落到「鼠标所在屏底部居中」。用 `placedOrigin`
    和 `didMove` 比对，区分「程序摆放」与「用户拖动」，默认位置不会被当成用户选择存下来。
  - 已知代价（用户拍板的取舍）：浮层在的这几秒（录音/想/说）那 76×56 区域会吃掉点击，
    点它＝拖它，不会传给它后面的 app；正常聊天时的点击穿透不受影响（浮层不在）。
- 2026-11-11（按住说话结尾丢字 → 松手后多收 300ms 尾巴）：
  - 用户反馈「说完立刻松手，句尾的字稳定丢」。根因与修法见
    `docs/bugs/060-hold-talk-tail-truncated.md`：松手即 `removeTap` + `engine.stop()`，
    而 VPIO + tap 缓冲有 150~300ms 延迟，最后一段话还在设备缓冲里就被丢了。
  - 现在 `finishHoldCapture` 松手后先留 tap 再收 `holdTailGraceMs = 300ms` 才停；
    「说话时间太短」用松手时的 `held` 判定，不把这段静音算进去。
  - `hold done` 日志补 `held=`，与 `audio=` 对比即可判断尾巴还丢不丢。
- 2026-11-11（按下吞开头：VPIO 空闲释放 3s → 8s，去抖 50ms → 0）：
  - 用户反馈「有的时候按下要等一小下才能说话，按下立刻说会吞字」。根因是
    `VoiceAudioEngine` 空闲 3 秒就把 VPIO 释放掉，下一轮要重建音频图（几百毫秒），
    这段时间的音频进不了 tap。语音条开着时有 `setVoiceBarOpen(true)` 钉住，全局热键没有，
    所以「刚说完再按」是热的、「隔一会儿再按」吞开头。
  - `deviceReleaseDelay = 8`（原 3）：覆盖连续几轮说话的间隔，又不会长时间把系统输出
    留在 AEC 通道上（影响别的 app 音量音质、蓝牙耳机可能掉通话档）。
    计时从「本轮最后一段音频活动结束」起算（TTS 念完也算），下一次按下/播放会重置。
  - 双 ⌘ 去抖 `armDelay` 50ms → 0：双 ⌘ 本身没有正常用途，误触由「旁边还有别的键/鼠标
    → 本次作废」兜底，省掉 33~50ms。
  - 影响文件：`Services/VoiceAudioEngine.swift`、`Services/VoiceHotKeyController.swift`。
- 2026-11-14（双 ⌘ 起不来时「嘟嘟嘟」停不下来 + 断网被当成掉登录）：
  - 用户反馈「按住双 ⌘ 出浮层，但一直嘟嘟嘟」。`tick()` 的 30Hz 轮询在失败出口没记
    「本次按下已处理」，每 33ms 重进 `begin()` 再 beep；同时 `verifyToken()` 把「连不上服务器」
    当成会话失效删掉 token，让每次按下都必然失败。根因与修法见
    `docs/bugs/067-hotkey-fail-beep-storm.md`。
  - 现在两个失败出口置 `cancelled = true`（一次按下最多响一声，松手时照旧重置）；
    `verifyToken()` 只在 401 才清会话，网络错误保留登录。
  - 影响文件：`Services/VoiceHotKeyController.swift`、`Services/DeviceManager.swift`。
