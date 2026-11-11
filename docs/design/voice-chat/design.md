# 语音聊天（豆包 TTS + 流式 ASR）设计

> 状态：首版 A 已实现，待配置豆包 Key 后真机联调
> 分支：`feat/voice-chat`
> 实现：后端 `internal/voice` + `controller/voice.go`；macOS `VoiceAudioEngine` / `VoiceChatService`；PWA `useVoiceChat` + `public/voice-pcm-worklet.js`

## 估算（定稿）

- 4 面（后端语音适配 / macOS 录音播放 / PWA 录音播放 / 联调）
- 3 缝（豆包二进制 ASR 协议、浏览器 WebSocket 鉴权、自动播放解锁）
- 3 次真机往返（macOS 麦克风、PWA 麦克风、中文音色试听）
- 2 个待拍决定（音色、PWA 验收设备）
- 约 2–3 天

## 目标

1. macOS 客户端与 Vue/PWA 都能语音对话：macOS 只留「连续对话」一个入口（点开即开麦、判停自动提交、
   回复自动朗读；原「语音输入 → 填输入框」入口已按用户要求删除）；PWA 同样是连续对话。
2. TTS 走豆包语音合成模型 2.0，ASR 走豆包流式语音识别模型 2.0。
3. API Key 只在服务端；客户端不接触豆包凭证。
4. 为后续 C（静音自动提交 + 输入框音波线 + 打断）保留传输层与状态机接口。

## 非目标

- 不做 iOS 原生客户端（PWA 代替）。
- 后端不做逐句流式 TTS：`POST /api/voice/tts` 仍整段返回 mp3；客户端按句切分后复用该接口（见 C2b）。
- 不做声音复刻、付费精品音库。
- 不把音频落库、不进 LLM I/O 审计、不写活动事件。

## 架构

```
macOS / PWA
  麦克风 PCM 16k/16bit/mono
        │  WebSocket（自家后端）
        ▼
  GET /api/voice/asr?ticket=...
        │
        ▼
  豆包 wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async
        │
        ▼
  {type:result, text, definite, final}

回复文本
        │  POST /api/voice/tts {text}
        ▼
  豆包 POST /api/v3/tts/unidirectional
        │  逐行 JSON + base64 音频块
        ▼
  audio/mpeg → 客户端播放
```

### 鉴权

- 浏览器 WebSocket 不能设置 `Authorization` 头。
- 客户端先 `POST /api/voice/asr-ticket`（Bearer），拿到一次性 ticket（60 秒、单次使用、绑定 user_id）。
- 再用 `GET /api/voice/asr?ticket=...` 升级 WebSocket。
- macOS 也走同一套，避免两套鉴权。

### 前置检查

`GET /api/voice/config` 返回 `enabled/ready/speaker/language/max_seconds`（`ready = enabled && api_key 非空`）；
`ready=false` 时客户端隐藏麦克风按钮。`max_seconds` 下发给客户端做录音上限。

## 豆包接口（已核对官方文档）

### TTS

文档：`docs/doubao-voice/unidirectional-streaming-text-to-speech-http`

```
POST https://openspeech.bytedance.com/api/v3/tts/unidirectional
X-Api-Key: <API Key>
X-Api-Resource-Id: seed-tts-2.0
X-Api-Request-Id: <UUID>
X-Control-Require-Usage-Tokens-Return: *
Content-Type: application/json
```

```json
{
  "req_params": {
    "text": "……",
    "speaker": "zh_female_vv_uranus_bigtts",
    "audio_params": {
      "format": "mp3",
      "sample_rate": 24000,
      "disable_markdown_filter": true,
      "disable_emoji_filter": true
    }
  }
}
```

响应为 HTTP Chunked，每行一个 JSON：

```json
{"code":0,"message":"OK","data":"<base64 音频块>","usage":{"text_words":7}}
```

- `code == 0`：追加 base64 解码后的音频。
- `code == 20000000`：结束。
- 其他 code：错误。

### 流式 ASR

文档：`docs/doubao-voice/large-model-streaming-automatic-speech-recognition-api-1`

```
wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async
X-Api-Key: <API Key>
X-Api-Resource-Id: volc.seedasr.sauc.duration
X-Api-Request-Id: <UUID>
X-Api-Connect-Id: <UUID>
```

二进制帧：

```
byte0 = 0x11             // version 1 + header size 4
byte1 = type<<4 | flags  // 0x10 full client request；0x20 audio only
byte2 = serialization<<4 | compression // 0x11 = JSON + gzip
byte3 = 0
4 字节 payload size（大端）
payload（gzip）
```

- 首包 JSON：`audio.format=pcm`、`rate=16000`、`bits=16`、`channel=1`、`language=zh-CN`；`request.model_name=bigmodel`、`enable_itn=true`、`enable_punc=true`、`show_utterances=true`、`result_type=full`、`enable_nonstream=true`、`end_window_size=800`。
- 音频包：16k/16bit/mono PCM，建议 200ms 一包。
- 结束包：`flags=0b0010`（负包）。
- 服务端返回同格式二进制帧，payload 为 gzip JSON，取 `result.text` 与 `utterances[].definite`；最后一包 `flags=0b0011`。

## 后端

新增包 `internal/voice`：

| 文件 | 职责 |
|---|---|
| `frames.go` | 豆包二进制帧编码/解码、gzip |
| `asr.go` | WebSocket 连接、发送 PCM、读取结果 |
| `tts.go` | HTTP Chunked 解析、base64 拼 mp3 |

联调工具 `cmd/voice-smoke`：把一个 16k/16bit/mono wav 按 200ms 节拍喂给真实 ASR，逐条打印
partial / final 与耗时，用来排查「说完不出 final」「同一句重复下发」这类协议问题；
不加载完整配置，只需 `DOUBAO_VOICE_API_KEY`。

新增 `controller/voice.go`：

| 路由 | 说明 |
|---|---|
| `GET /api/voice/config` | 能力探测 |
| `POST /api/voice/asr-ticket` | 一次性 ticket |
| `POST /api/voice/tts` | `{text}` → `audio/mpeg` |
| `GET /api/voice/asr?ticket=[&mode=continuous]` | WebSocket 音频流；`mode=continuous` 走 C 模式分句下发 |

限制：

- TTS 单次 ≤ `voice.tts.max_chars`（默认 2000 字符），音频 ≤ 20MB。
- ASR 单轮 ≤ `voice.asr.max_seconds`（默认 60 秒），按 PCM 字节数折算。
- 不记录音频、识别文本；日志只记长度/字节数/错误。

## 配置

```yaml
# base config 里 voice.enabled=false；dev / prod 各自打开，
# 这样 fail-fast 只在明确启用了语音的环境生效（CI / go test 不需要 Key）
voice:
  enabled: false
  api_key: ${DOUBAO_VOICE_API_KEY}
  tts:
    endpoint: https://openspeech.bytedance.com/api/v3/tts/unidirectional
    resource_id: seed-tts-2.0
    speaker: zh_female_vv_uranus_bigtts
    format: mp3
    sample_rate: 24000
    speech_rate: 0
    max_chars: 2000
    timeout_seconds: 90
  asr:
    endpoint: wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async
    resource_id: volc.seedasr.sauc.duration
    language: zh-CN
    max_seconds: 60    # A 模式单轮上限
    session_seconds: 600 # C 模式单次会话上限（按音频时长计费，静音也计时）
    timeout_seconds: 30
    result_type: full   # A 模式；C 模式由服务端强制 single（增量，只带当前分句）
    end_window_size: 800 # 静音判停阈值 ms，范围 [300,5000]
```

- 新版火山控制台只需 API Key，不需要 AppID / AccessToken / Cluster。
- 开通产品：豆包语音合成模型 2.0、豆包流式语音识别模型 2.0（小时版）。
- `asr.timeout_seconds` 用作 **WS 握手超时**（连豆包），不是整轮录音超时。

## WebSocket 下行消息

| type | 说明 |
|---|---|
| `ready` | 服务端已连上豆包、开始接收音频（仅作提示，客户端不等它） |
| `result` | `{text, definite, final}`，增量覆盖（**A 模式**；`text` 是累计全文） |
| `sentence` | `{text}`，一条已判停的分句（**仅 C 模式**）。`result_type=single`，`text` 只含本句，客户端据此提交一轮对话 |
| `voice` | `{}`，ASR 收到**内容有变化**的非空识别结果（含未判停的 partial，**仅 C 模式**）。不带文本，只用来告诉客户端「有人在说话」→ 推后空闲收口；重复结果不发（否则静音期也能一直续命） |
| `limit` | 到达单连接上限；服务端已 `Finish`，随后会吐 `final`（A 模式）或 `sentence`（C 模式）。客户端应停止采集但**不要**再发 `end`，并 arm 一个兜底计时器 |
| `error` | 识别失败 |

C 模式说明：

- `mode=continuous` 时服务端强制 `result_type=single`，**不再下发 `result`**，只下发 `sentence`，避免两套累计逻辑并存；
  另外在「识别结果非空且文本有变化」（含还没判停的 partial）时补一条不带文本的 `{"type":"voice"}`，
  客户端据此推后空闲收口（噪声不会产生识别文本；重复结果不发，避免静音期被重复文本续命）。
- `sentence` 只在 `definite=true` 时发；重复文本去重；连接结束时的 `final` 只含最后一句，也会作为 `sentence` 补出残句。
- 一条连接可连续识别多句（实测 83 秒 / 18 句不断），退出语音模式才发 `end`。

### C1 连续对话（macOS）

- 输入栏新增「连续对话」按钮（`waveform.circle`）：点一下开麦，之后一直收音频。
- 每收到一条 `sentence` 就**直接提交一轮对话**（`speakReply=true`，回复自动朗读）；
  上一轮还在跑时先 `stopStreaming()` + `stopSpeaking()`，避免两个 ReAct 循环交叉写库。
- 会话上限取 `/api/voice/config` 的 `session_seconds`（默认 600 秒）：到点提示并收口。
- 空闲收口：30 秒没有收到 `voice` / `sentence`（即没人说话）自动结束会话，避免占并发、白扣音频时长。
- 结束/掉线都会发 `cancel` 或等负包结果后关连接；`limit` 后立即停采集（负包之后不能再发音频）。
- `shortcut:` 连接意外断开不自动重连，10 分钟会话内掉线需用户再点一次；真机常掉再加重连。

### C2 输入框音波线

- 会话期间**不再有全屏浮层**：聊天消息、滚动、右栏都不动，输入栏的文本行让位给一条 36pt 高的音波线
  （草稿保留，结束会话后编辑器原样回来；底部走 `safeAreaInset`，消息不会重排）。
- 形态是一条**以中线镜像填充的刺状波形带**（不是正弦线）：值噪声叠出真实录音那种稀疏尖峰，
  两端用 `sin(πx)^0.55` 收口，**只变振幅、不跟着音节横向跑**。定稿参数（调参页 `mockup.html`，
  已接真麦克风）：**幅度 1.00 / 响应 2.00 / 波包 1.15 / 静息 1.00 / 横向流动 0.05**。
  - 听：包络 `(2 + 12·√(level/0.22)) × 1.00`，静息时用 `2.6 ± 0.8·sin(0.7t)` 的呼吸并平滑过渡；
    快起 15ms / 慢落 60ms。
  - 想：一道高斯小鼓包从左穿到右（周期 `2.4/1.15 ≈ 2.1s`），幅度固定为 `7 × 0.8`（比听/说轻一档）。
    **TTS 合成期**（macOS `isPreparingSpeech` / PWA `preparing`）也算「想」，否则回复流刚结束、
    音频还没到时先闪回「听」（听→想→听→说里那个多余的「听」）。
  - 说：振幅 `(8 + 12·cadence) × 1.00`，cadence 是两条不可通约正弦相乘（重音疏密），
    青色 `accentSecondary` + 发光。
  - 横向流动 0.05：三组噪声（空间频率 70 / 190 / 6）的时间项统一乘 0.05，波形约 10~50 秒扫过一屏。
    原来的 1.0 让大尺度起伏每秒扫一屏，说话时看着烦躁。
- **不写字**：状态字「听/想/说」和「点一下打断」都已去掉（状态靠颜色与波形表达）；
  点整条 = 停朗读（PWA 只在 `chat.isStreaming` 时再 `handleStop()`；macOS 有自动打断，点条只停朗读）。
- macOS 实现在 `Shared/Views/VoiceOrbView.swift` 的 `VoiceWaveLine`（**文件名保留**，改名要动 Xcode 工程），
  由 `InputBar` 的 `voiceStrip` 槽顶替编辑器；PWA 实现在 `components/VoiceWaveLine.vue`（canvas，同一套数学）。
- `prefers-reduced-motion` / `accessibilityReduceMotion` 时冻结相位与波包，只保留振幅响应。

### C2b 朗读节奏：句级流水线 + 字幕跟随

- 问题：TTS 原本在 `done` 后才拿整段回复合成、且要等整个 mp3 下完才开播；语音轮回复很短（当前约束 ≤60 汉字），
  用户 3 秒读完，再干等 1~2 秒才听到第一句——「都看完了才开始念」。
- 修法（后端不改，复用现有 `POST /api/voice/tts`）：
  - 正文 delta 一进来就切句：句末 `。！？!?；;…\n`；**第一句额外在 12 字后的 `，,、` 处先切一刀**，
    让首包音频尽早响。切出的句子立刻并发请求 TTS，按序播放；未以句末标点结尾的尾巴只显示、不合成，
    `done` 时把尾巴整句合成。两端规则逐项一致：macOS `VoiceChatService.appendReplyDelta/endReplyStream`，
    PWA `useVoiceChat` 的 `beginReplyStream/appendReplyDelta/endReplyStream`。
  - 播报期间麦克风门跨句保持关闭（句间不解除）；打断清空整条队列。
  - 纯代码/表格回复（每句都念不出来）收尾补播「这条回复主要是代码或表格……」，与一次性朗读一致。
- 显示（字幕跟随）：正在念的那条消息按句上色——**已播正常 / 当前句加粗 + accent / 未播 45% 透明**。
  macOS 在 `ChatBubble` 用 `Text` 拼接（流式气泡正文比流水线慢 100ms 节流，以流水线文本为准）；
  PWA 在 `MessageBubble` 用 `<span>`。句子拼回原文不一致时退回 markdown，避免切分规则漂移时丢字。
- 停止/出错/空回复都显式收掉流水线（macOS `onAssistantReplyAborted`、PWA handler 的 `ok=false`），
  避免永远等下半句。
- `shortcut:` 没有逐句时间戳；高亮粒度 = 一次 TTS 请求的句子。豆包以后若给词级时间戳，可细化到「当前词」。

### C3a 回声消除与打断（macOS）

- `Shared/Services/VoiceAudioEngine.swift`：采集与播放共用一个 `AVAudioEngine`；
  TTS 走挂在其上的 `AVAudioPlayerNode`，VPIO 才有回声参考信号。
- 初始化顺序固定：接播放图 → `setVoiceProcessingEnabled(true)`（input + output）→ 装采集 tap → start。
  顺序反了 AEC 会静默失效，不报错。
- 麦克风门：播报开始即 `setGated(true)`，分片不发 ASR，只留最近 600ms 到 pre-roll；
  播报结束再压 600ms 收尾音。
- 打断：门控期间 `onLevel` 仍工作；阈值 **跟着本机残响走**——取最近约 2 秒（30 帧）门控电平的
  **p20 当残响本底**，阈值 `max(0.03, p20 × 2.2)`，连续 3 帧（约 200ms）超过才算用户开口
  → 冲回 pre-roll → 停朗读 → `onBargeIn`（只在 `isStreaming` 时才 `stopStreaming()`）。
  门刚打开 250ms 内不判定（播放起始爆音），但这 250ms **照样采样**本底，否则开头 1 秒阈值只剩硬地板。
- 为什么不再用固定阈值（051 的第二轮教训）：定死 0.10 时，**正常坐姿离内置麦 0.5~1m** 说话只有
  0.04~0.08，永远够不着，得把头凑到屏幕前才能打断。p20 让「远场说话/近场说话/漏音大的音箱」
  用同一套余量：说话只要明显高于本机残响就能打断，漏音大时本底被抬高、阈值自动跟着抬。
- 代价：AEC 只是信号相减，耳机场景最稳；开口第一声太轻（不足本底 2.2 倍）会漏掉一次，
  说清楚一点就断。已知下限：远场支持到约 1m（内置麦 + 外放），更远需要外接麦或更大音量。
- p20 的已知失效模式：一直说、电平又贴着阈值时，超过 20% 的帧是本人声音 → p20 被抬高、
  阈值再 ×2.2，本次播报内就再也打不断（约 1.6 秒静音后回落）。典型场景是「远场小声碎碎念」，
  正常音量开口不受影响。
- 真机若出现「自己打断自己」（`raw` 与 `threshold` 都 ≈0.03），先把 `bargeInFloor` 抬到 0.04：
  门的头 250ms 窗口里只有几帧，残响本底几乎等于这几帧的最小值，地板是这时唯一的余量。
- iOS 若将来接语音，audio session 必须用 `.voiceChat`（`.spokenAudio` 下系统不给回声参考）。

### C3b PWA 点击打断

- 浏览器里不做自动 barge-in：Chrome 的 AEC 默认只覆盖 WebRTC 通路，`<audio>` 播放的漏音
  会直接把识别带偏；Safari 参数不生效，更没法可靠判断「用户在说」。
- 改用**播报期门控**：`speak()` 一开始就把上行换成等长静音（保活，防 45000081），
  `<audio>` 播完再压 600ms 才恢复真实音频——AI 自己的声音根本进不了 ASR。
  代价是播报期间开口不算数，打断只能手点（这正是「不做 auto barge-in」的落点）。
- 全屏环在「说」态按钮是「打断」：点一下 = `stopSpeaking()`（停朗读），且**只在还在生成时**才走
  `handleStop()`（断 SSE + `/api/chat/stop`）——播报时本轮通常已流完，白发一次 stop 会误杀下一轮；
  **会话与麦克风保留**。其余状态是「结束对话」→ `stopVoice()`（发 `end`，等连接正常收尾）。

## 客户端

### macOS

- 输入栏麦克风按钮。
- `AVAudioEngine` 采集 → `AVAudioConverter` 转 16k/16bit/mono。
- `URLSessionWebSocketTask` 连自家后端，二进制发 PCM，文本发 `{"type":"end"}`。
- WS 一打开就开始采集（不等 `ready`），早于 `ready` 的帧由 socket 缓冲；`max_seconds-2` 自动收口。
- 识别文本填输入框（不自动发送）。
- 回复正文 delta 边到边切句请求 TTS（C2b），`VoiceAudioEngine`（`AVAudioPlayerNode` + VPIO）按序播放；
  停止按钮同时停朗读。
- 补 `NSMicrophoneUsageDescription`。

### PWA（Vue）

- `getUserMedia` + AudioWorklet 采集，重采样 16k/Int16。
- WebSocket 连自家后端。
- WS 打开即采集（不等 `ready`），`max_seconds-2` 自动收口；`limit` 后停止采集等 `final`。
- 识别文本填输入框（不自动发送）。
- 回复正文 delta 边到边切句请求 TTS（C2b），`<audio>` 按序播放；首次点击麦克风时解锁音频元素。
- 不支持麦克风的浏览器隐藏按钮。

## 语音回复约束

新增 `voice_mode` 请求参数（照 `brief_answer` 路径）：

- 服务端在 system prompt **末尾**（Layer 5，位置贴近用户消息、遵循度最高）注入：口语化、不要
  markdown/列表/代码块、同语言、1–3 句 / 最多 60 个汉字；显式声明与「回复形态」的篇幅分档冲突时
  以本段为准、「调用了工具只回结论一句」。
- 仅语音轮注入；接受前缀缓存分叉（低频动作）。
- 工具轮额外补救：最后一次工具返回后，`mkLlmChatReq` 在**本次请求的消息副本**尾部补一句
  「只回 1–3 句结论，不复述工具返回细节」（`state.msgs` 与落库内容不动，见文末 2026-11-11 条目）。
- 客户端只朗读最终回复；剥离 markdown/代码块；纯代码/表格回复改播一句提示。

## 后续 C（连续对话）

按 C0 → C1 → C2 → C3a → C3b 顺序做，一个阶段一个提交。

| 阶段 | 内容 | 范围 |
|---|---|---|
| **C0** | 后端 `mode=continuous` → `result_type=single` + `sentence` 分句下发 | 后端 |
| **C1** | 连续流 + `sentence` 自动提交 + 会话上限（10 分钟硬上限 + 30 秒空闲自动收口） | macOS |
| **C2** | 输入框音波线（听 / 想 / 说 三态 + 振幅呼吸） | macOS + PWA ✅ |
| **C3a** | TTS 搬进 `AVAudioEngine`（`AVAudioPlayerNode`）+ VPIO + 播放后麦克风门 + 说话打断 | macOS ✅ |
| **C3b** | 点击打断（不做 auto barge-in，浏览器 AEC 不可靠） | PWA ✅ |
| **C4** | PWA 连续对话（麦克风按钮 = 开关）+ 播报期静音门控 + 空闲/上限收口 | PWA ✅ |

关键事实（`cmd/voice-smoke` 实测）：

- VAD 判停由**服务端**做：`enable_nonstream=true` + `end_window_size`，句末静音 ~800ms 后约 1.1s 出 `definite`；客户端不写 RMS 判停。
- `result_type=single` 每条结果只带当前分句；`final` 只含最后一句（退出时需补提交残句，服务端已代为下发 `sentence`）。
- 一条连接连续跑 83 秒 / 18 分句未断，**不需要**每句重连。
- `end_window_size=600` 比 800 快约 500ms，代价是自然停顿更易切句；A 保持 800，C 可降到 600。
- 打断顺序必须严格：停本地播放 → `/api/chat/stop`（真取消生成）→ 新建 ASR 连接 → 冲入 600ms pre-roll。
- AEC 只影响 C3：VPIO 需先接播放图再 `setVoiceProcessingEnabled(true)`，input/output 都开；TTS 必须走同一 engine 的 `AVAudioPlayerNode`；播放结束后仍要压 500–800ms 麦克风门。
- C 会话计费按音频时长（大模型流式 ASR ≈ 4.4 元/小时，静音也计时），所以必须有会话上限与空闲收口。

## 验收

- 后端：`go build ./... && go test ./...`，新增 TTS/ASR 单测。
- macOS：`swiftc -typecheck` 通过，真机麦克风权限、中文识别、TTS 播放。
- PWA：`vite build` 通过，Chrome/Safari 真机麦克风与播放；iOS PWA 单独验证。
- 未配置 Key 时：客户端隐藏麦克风，接口返回 503。
- **服务端**：base config `voice.enabled=false`，dev / prod 打开后缺 Key 会**启动即失败**（fail-fast）；
  CI / `go test` 不启用语音，不需要 Key。

## 部署注意

云端 PWA 经 nginx 访问时，`/api/voice/asr` 必须单独配 WebSocket 升级头，不能沿用
`location /api/` 的 `proxy_set_header Connection ""`：

```nginx
location = /api/voice/asr {
    proxy_pass http://xhan_helper_backend;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_read_timeout 300s;
    proxy_buffering off;
}
```

仓库模板已更新 `server/nginx-helper.conf` 与 `docs/front-end/plan.md`；上线前需同步到
`/etc/nginx/sites-enabled/` 并 reload。

## 后续变更

- 2026-11-10（macOS 换 tab 后语音仍绑旧会话）：
  - macOS 只有一个 `ChatView` 被复用（换 tab 只换 `viewModel`，`onAppear` 不重跑），
    语音回调闭包捕获旧 `ChatViewModel`，分句被提交进上一个会话。
  - 接线抽成 `ChatView.wireVoice()`，`onChange(of: ObjectIdentifier(viewModel))` 里
    `stopSpeaking` + `cancel` + 重接；重接前摘掉旧 VM 的 `onAssistantReply*` 回调。
  - 详见 `docs/bugs/056-macos-voice-submits-to-previous-conversation.md`；
    影响文件：`client/xHelper/xHelper/Shared/Views/ChatView.swift`（`wireVoice` / `unwireVoice`）、
    `client/xHelper/xHelper/Shared/ViewModels/ChatViewModel.swift`（新增 `replyOwner`）。
  - **取舍**：换 tab 视为结束本轮语音 —— `voice.cancel()` 会拆掉连续档会话，录音发送档里正在录的
    那段直接丢弃、语音条关闭（回来要重新点麦克风）。不这么做就得把「谁的语音会话」跟着 tab 走，代价更大。

- 2026-11-08：API Key 环境变量由 `DOUBAO_API_KEY` 改为 `DOUBAO_VOICE_API_KEY`（避免与其它豆包用途撞名）；
  `voice.enabled` 默认开启；新增 `cmd/voice-smoke` 联调工具。真实 key 下 TTS 与 ASR 已跑通。
- 2026-11-08（第二轮复审后）：
  - `/api/voice/config` 增加 `max_seconds`；客户端按 `max_seconds-2` 自动收口。
  - 采集起点改回「WS 打开即录」，不再等 `ready`（等会吞掉开头几百毫秒）；`ready` 仅作提示。
  - 新增下行 `{"type":"limit"}`：到上限先提示、服务端 `Finish` 取回结果，客户端停止采集并 arm 兜底计时器。
  - `voice.asr.timeout_seconds` 语义明确为 WS 握手超时。
  - 纯表格回复与纯代码一样改播「放在屏幕上了」提示。
  - 语音标记（`voice_mode` / `pendingVoiceReply`）在清空输入、新会话时复位，避免手打被当语音轮。
- 2026-11-08（C0）：
  - `/api/voice/asr` 新增 `mode=continuous`：服务端强制 `result_type=single`，只下发 `sentence`（`{text}`），
    不再下发 `result`。`sentence` 由 `voice.SentenceCommitter` 去重，连接结束时的残句也会补出。
  - `voice.asr` 新增 `result_type` / `end_window_size` 配置（默认 `full` / `800`），`cmd/voice-smoke`
    加 `-result-type` / `-max-seconds` / `-end-window` 三个 flag 用于参数实验。
  - 执行计划定为 C0 → C1（macOS 连续流）→ C2（全屏环两端）→ C3a（macOS 真打断）→ C3b（PWA 点击打断）。
- 2026-11-08（C1，仅 macOS）：
  - `voice.asr.session_seconds`（默认 600）/ `/api/voice/config.session_seconds`：C 模式单次会话上限；
    `mode=continuous` 的连接上限直接取它，会话内不再因 `max_seconds` 断连。
  - 客户端新增「连续对话」按钮：常开麦，每收到 `sentence` 直接提交一轮并朗读回复；
    30 秒无新分句自动收口；到会话上限提示后收起。
  - 连续模式提交前若上一轮仍在流式，先停生成再发新一句（与 C3 的 barge-in 不是同一件事）。
- 2026-11-08（C2）：
  - macOS 新增 `VoiceOrbView`：连续会话期间覆盖会话区，听/想/说三态 + 音量波纹，点「结束」收口。
  - PWA 新增 `components/VoiceOrb.vue`：录音/识别/朗读期间全屏浮层；`useVoiceChat` 暴露 `speaking`。
  - `VoiceAudioCapture` 新增 `onLevel`（RMS×4 归一化）供波纹使用，不影响 PCM 上行。
- 2026-11-08（C3a，仅 macOS）：
  - 新增 `VoiceAudioEngine`：采集与 TTS 播放共用一个 `AVAudioEngine`，VPIO（input+output）+
    固定初始化顺序；替换 `VoiceAudioCapture` 与 `AVAudioPlayer`。
  - 播报期间麦克风门 + 600ms 尾音门；门控期只攒 600ms pre-roll，打断时冲回 ASR，开头不丢。
  - 打断判定：音量 > max(0.32, 残响本底×1.8) 连续 3 帧 → 停朗读 + `onBargeIn` 停生成。
  - 门控期间不向 ASR 发送音频（不补静音）：流式 ASR 允许中途静默，避免把回声送进去。
    > **已推翻**：豆包 8 秒收不到音频包会断会话（45000081），现改为按原节拍发等长静音保活，
    > 见 2026-11-08「修复：8 秒等包超时 + 麦克风不释放」。
- 2026-11-08（C3b，PWA）：
  - 不做自动 barge-in（浏览器 AEC 不可靠）；「说」态的全屏环按钮改为「打断」，
    点击 = 停朗读 + 断 SSE + `/api/chat/stop` + `cancelVoice()`。
  - 播报期间点麦克风同样先 `handleStop()`，防止把 TTS 声音录进识别。
- 2026-11-08（第三轮复审）：
  - 配置分层：base `voice.enabled=false`，dev / prod 各自打开；CI 与 `go test` 不再需要 Key。
    `scripts/control-xhan-helper.sh` 增加启动前预检，缺 `DOUBAO_VOICE_API_KEY` 时拒绝重启（不杀旧进程）。
  - `make deploy*` 统一执行 `scripts/control-xhan-helper.sh`（此前 deploy 跑根目录那份，改脚本容易漏）。
  - 新会话首轮未 `done` 时插话：`sendMessage` 用 `effectiveConversationID`（meta 帧的 pending id）落回同一会话，
    不再另起一个会话；插话路径 `stopStreaming(notifyServer: false)`，避免迟到的 `/api/chat/stop` 误杀新轮。
  - C 模式空闲/会话计时器在「正在回答 / 正在朗读」时顺延（会话上限只给一次 15 秒宽限）。
  - 结束/到上限后的残句不再提交、不再朗读（`sentence` 要求 `state == .recording`）。
  - 分句去重改用 `utterances[].end_time`（文本判重会吞掉连着说的两句一样的话）。
  - `mode=continuous` 按用户限制一条并发连接。
  - `voice/config.session_seconds` 在客户端改为可选（灰度回滚连旧后端不会解码失败）。
- 2026-11-08（第四轮复审）：
  - `resetForNewChat()` / `loadConversation()` 清 `pendingConversationID`：否则「新对话」第一条消息
    会因 `effectiveConversationID` 回退而发进上一个会话。
  - 部署预检改用 `xhan-helper -check-config`（真正加载配置），并在 `mv` 之前校验 tmp 二进制；
    控制脚本在 `APP_ENV=prod` 下校验（此前会用默认 dev 环境误判）。
  - `SentenceCommitter` 处理「先到无 end_time、后到带 end_time」的同文重复。
  - 连续会话在 `.requestingPermission` 阶段点结束直接收口，不等 15 秒兜底。
- 2026-11-08（C3a 修复：VPIO 多声道）：
  - VPIO 会把输入节点格式变成输出设备的格式（本机 48k/**5ch**），`AVAudioConverter` 5ch→mono
    **不报错但产出全零** → A/C 两种模式都「识别不到内容」，语音环不动且无日志。
  - 采集改为：多声道先 `downmixToMono()` 混成原始采样率的 mono，再由转换器做 mono→16k/16bit。
  - 诊断工具：`client/xHelper/scripts/voice-capture-probe.swift`（`vpio` / `plain` / `vpio-raw` 三档），
    参见 `docs/bugs/048-voice-vpio-multichannel-silence.md`。
  - 日志：客户端 `[voice]` 前缀覆盖开始/授权/ticket/WS 帧/上行字节/音量峰值/门控/TTS/计时器；
    服务端 `[voice]` 覆盖 ws 打开、首帧音频、每 5s 累计时长、definite/final 结果（只记长度与时间戳，
    不记识别文本）、sentence 下发、end/退出原因。
  - `stopSpeaking()` 在 `micGated` 为真时一律解门控（TTS 尾音门 600ms 窗口内启动会永久卡死）。
- 2026-11-08（PWA 修复）：
  - 全屏语音环的按钮按状态分流：录音中 =「说完了」→ `stopVoice()`（发 `end` 等 `final`）；
    想/说 =「打断」→ `interruptVoice()`。此前录音中也走 `cancelVoice()`，已识别文本会被整段丢掉
    （见 `docs/bugs/049-pwa-voice-orb-cancel-discards-transcript.md`）。
  - PWA 补 `[voice]` 控制台日志（start/mic/worklet/ticket/ws ready/result/up 字节/final/stop/cancel/tts）。
  - PWA 音量 ×4 与 macOS 对齐，浮层「听」态的圆环才会跟着声音动。
  - 服务端 `decodeFrame` 失败时带上 ws 消息类型与帧头十六进制（区分文本帧/事件帧/字段错位）。
    用 `voice-smoke -result-type single` 连发 81 秒未复现 20:39 那次 `payload size ... exceeds frame`。
- 2026-11-08（修复：8 秒等包超时 + 麦克风不释放）：
  - **推翻** C3a 那条「门控期间不向 ASR 发送音频（不补静音）」：豆包对「8 秒内没收到下一个音频包」
    会主动断会话（错误码 45000081），长回复播到一半必现「识别失败」。现在门控期按 200ms 节拍
    补等长全 0 静音，pre-roll 逻辑不变（打断时仍冲回真实音频）。
  - `decodeFrame` 的错误帧按官方布局解析：`header(4) + 错误码(4) + payload size(4) + JSON`。
    此前把错误码（如 45000081）读成 payload size，真实错误被掩盖成「语音识别失败」。
  - `VoiceAudioEngine` 用 `playbackActive` 判断播放状态（`player.isPlaying` 播完仍为 true），
    播放结束即释放 engine，修掉退出语音模式后菜单栏麦克风占用不消失。
  - `cmd/voice-smoke` 新增 `-gap-at / -gap-seconds / -gap-silence`，可直接复现与验证这个问题。
- 2026-11-08（第六轮复审修复 + PWA 连续对话）：
  - 服务端错误帧按 compression 位解 gzip（错误码与 payload size 是明文，只有 JSON payload 压缩）。
  - 客户端 `[voice]` 诊断全部改走 `LogBuffer.diag`：`info` 整段在 `#if DEBUG` 里，Release 构建会一片空白。
  - `VoiceAudioEngine` 的播放完成回调绑定 `playGeneration`：`player.stop()` 触发的旧回调
    不会再清掉新一轮的 `playbackActive`、把 TTS 掐断。
  - 采集诊断计数改用 `statsLock`（音频线程写、主线程读/清零），`downmixToMono` 失败也计入 `empty`。
  - macOS 输入栏：删掉「语音输入（麦克风）」按钮，「连续对话」挪到「更多操作」左侧；
    退出连续会话统一走 `endContinuousVoice()`（停采集 + 停生成 + 停朗读），
    修掉「从输入栏结束、回复继续念完、麦克风占用到播完才释放」。
  - **PWA 改为连续对话**（原 A 模式移除）：麦克风按钮 = 会话开关，`mode=continuous`、
    按 `sentence` 逐轮提交、回复自动朗读；播报期间上行改发等长静音保活；
    30 秒无分句收口、`session_seconds` 到点收口（回答/朗读中给一次 15 秒宽限）；
    浮层「打断」只停朗读与生成，「结束对话」才收会话。
  - PWA 打字发送不再带 `voice_mode`（`voiceReplyPending` 一并删除）：只有语音轮会朗读回复。
- 2026-11-08（第七轮复核，6 条 P3）：
  - macOS `endContinuousVoice()` 只在 `viewModel.isStreaming` 时才 `stopStreaming()`：否则会白 POST
    一次 `/api/chat/stop` 并 `ShellProcessRegistry.killAll()`，刚结束语音马上发下一条时迟到的 stop 会误杀新轮。
  - 两端「新分句到来时」都**无条件**停朗读（原来只在流式中停）：上一轮已流完但 TTS 还在念时，
    门还关着，不停就会把这句也吞掉。
  - PWA：WS 还没 OPEN 就点「结束」时直接 `cleanup()`，不再发不出去的 `end` + 白等 15 秒兜底。
  - PWA / macOS 都忽略收尾阶段的 `error` 帧（已点结束或到上限后，服务端补发的 45000081 等包超时不再弹错）。
  - PWA `cleanup()` 一并 `stopSpeaking()`：出错/硬收口时朗读不会继续念、`speaking` 不会挂成 true。
  - 全屏环的免手取舍写进 C2（见上）。
  - macOS `VoiceChatService.start()`（A 模式）加注释说明已无入口，仅保留代码。
- 2026-11-08（修 macOS 打断失效 + 空闲收口误杀）：
  - 打断阈值 0.32 → 0.10（`max(0.10, 残响本底 × 1.8)`），并加「门打开后 250ms 内不判定」。
    真机日志：播报期间用户说话电平 0.08~0.28，原阈值高于它 → 门控期说的话全被吞、会话看着像卡死。
  - 空闲收口看「最近一次听到用户出声」（`lastVoiceAt`，电平 > 0.08 即刷新），
    不再只看 `sentence`：长句没有判停时不会被 30 秒计时器收掉。
- 2026-11-08（第八轮复核 + 远场打不断）：
  - 打断阈值改成自校准：最近 30 帧（约 2s）门控电平的 **p20 当残响本底**，阈值
    `max(0.03, p20 × 2.2)`，连续 3 帧（约 200ms）触发；250ms 预热期照样采样本底。
    固定 0.10 只对「凑近说话」有效——正常坐姿 0.5~1m 的说话电平只有 0.04~0.08，永远够不着。
  - 打断/`onBargeIn` 只在 `isStreaming` 时才 `stopStreaming()`（macOS 的 `onBargeIn`、PWA 浮层「打断」）：
    打断的主场景是本轮已流完、TTS 正在念，这时发 stop 会关掉下一轮刚注册的 stop channel。
  - 空闲收口改由服务端 `{"type":"voice"}` 事件驱动（ASR 识别结果非空才发，不含文本）：
    去掉「麦克风电平 > 0.08 就算有人说话」的猜测——环境噪声会一直续命，远场长句又会被误收。
    PWA 同步接这个事件。
  - macOS 浮层「说」态提示改为「直接开口就能打断，点「结束」是收会话」；
    `session limit` / `idle limit` / `ws receive ended` / `config unavailable` 四处日志改走 `diag`。
- 2026-11-08（C2 重做：全屏环 → 输入框音波线）：
  - 用户明确不要全屏浮层（聊天消息要一直可见），也不要横向流动的波形；
    改成**一根正弦曲线原地呼吸**，只有振幅随声音变。淘汰两版中间形态（单线波浪、
    镜像尖峰波形带），调参页留在 `docs/design/voice-chat/mockup.html`。
  - 定稿参数：幅度 0.8 / 响应 2.0（起 15ms、落 60ms）/ 波包 1.15；「说」态用 `accentSecondary` 青。
  - 修状态机：新增 `isPreparingSpeech`（macOS）/ `preparing`（PWA），把「回复已生成完、TTS 还在合成」
    并进「想」，消掉 听→想→**听**→说 里那个多余的「听」。
  - macOS：删 `ChatView` 的 `VoiceOrbView` overlay，改由 `InputBar.voiceStrip` 顶替文本行
    （`AnyView` 槽，避免再加泛型把调用表达式顶爆）；胶囊的「点空白聚焦」手势在语音期间关闭。
  - PWA：删 `VoiceOrb.vue` 全屏浮层，新增 `VoiceWaveLine.vue`（canvas + rAF），
    会话期间顶替 `<NInput>`；`useVoiceChat` 暴露 `preparing`。
- 2026-11-08（音波线跟手感）：真机反馈「震颤太慢，一个字应该抖一次」。
  - 根因是 `handleLevel` 里的 EMA（`level = level*0.6 + value*0.4`，时间常数 ≈130ms），
    把 4~5Hz 的音节抹平了。波形改用独立的 `waveLevel`（**不做 EMA**、**非 @Published**，
    由 Canvas 每帧自取），15Hz 的采集帧直接驱动振幅。
  - 振幅映射拉开动态范围：听态 `(0.8 + 13·√(level/0.22)) × 0.8`（原来有 2pt 的地板，
    安静和说话只差 40%）；静息呼吸降到 ±0.4pt。
  - 试过把 tap 子块拆成 ~16ms 发 60Hz 电平，**放弃**：主线程任务按 66ms 一批涌进来，
    Canvas 只取到最后一块，等于白拆；要真提高采样率得改 tap buffer 尺寸，
    会同时改掉打断判定的帧时长（3 帧≈200ms），不值当。

- 2026-11-09（音波线定稿：正弦 → 刺状 + 慢速横向）：
  - 对比后选刺状（真实录音那种稀疏尖峰），正弦线退场；调参页接上真麦克风（`?mic=1` / 「接麦克风」）。
  - 参数定稿：幅度 1.00 / 响应 2.00 / 波包 1.15 / 静息 1.00 / 横向流动 0.05；想态幅度 ×0.8。
  - 修「说话时整条线飞快横移」：噪声的时间项原来直接加在空间坐标里，大尺度起伏（空间频率 6）
    每秒扫约 0.84 屏；统一乘 0.05 后约 10~50 秒一屏，只剩振幅随音节跳。
  - 去掉状态字「听/想/说」与「点一下打断」：状态由颜色/波形表达，点整条仍是打断。
  - 两端实现同步为镜像填充的波形带（`VoiceOrbView.swift` 的 `VoiceWaveLine` /
    `components/VoiceWaveLine.vue`）；`mockup.html` 换成带麦克风与滑杆的调参页。

- 2026-11-09（朗读节奏：句级流水线 + 字幕跟随）：
  - 原路径是 `done` → 整段 TTS → 等整个 mp3 → 播，用户读完还要干等 1~2 秒。
  - 改为正文 delta 边到边切句（首句在 12 字后的逗号处先切），每句单独请求现有 `/api/voice/tts`，
    客户端按序播放；后端接口与协议不变。
  - 正在念的消息按句上色：已播正常、当前句加粗 + accent、未播 45% 透明；流式气泡以流水线文本为准。
  - 停止/出错/空回复显式停流水线（`onAssistantReplyAborted` / handler `ok=false`）。
  - 影响文件：`VoiceChatService.swift`、`ChatViewModel.swift`、`ChatView.swift`、`MessageListView.swift`、
    `ChatBubble.swift`；`useVoiceChat.js`、`useChatSSE.js`、`ChatView.vue`、`MessageBubble.vue`、
    `utils/speech.js`（新增，含单测）。

- 2026-11-09（按住说：显式声明「没说完」）：
  - 判停固定是豆包的 800ms 静默（`end_window_size`），半句会被提前提交；`enable_punc` 补的句号
    不能当「说完」的证据，`vad_segment_duration` 也不改变 definite 位置。因此不再尝试猜，
    改为用户按住空格/按钮显式声明「还没说完」，松手后整段提交一轮。
  - 方案、交互与 UI 见 `docs/design/voice-hold/design.md`；文档与预览先落，代码在同一分支实现。

- 2026-11-09（音波噪声相位修复：绝对时间 → 累加器）：
  - 旧实现把 `t · sp(t)` 直接当噪声坐标，瞬时频率是 `sp + t·sp'`；macOS 的 `t` 是
    `timeIntervalSinceReferenceDate`（约 8×10⁸），从第一帧起纹理就飞速跳变，PWA 开久了同样会变快。
  - 改为 `flow += speed(phase) · dt` 后代入噪声，三端（macOS / PWA / 调参页）同步；参数与振幅调制不变。
  - 详见 `docs/bugs/053-voice-wave-time-warp.md`。

- 2026-11-09（音色可切换：服务端白名单 + 客户端本地偏好）：
  - 音色从「yaml 唯一来源」改为「服务端下发白名单 + 每次合成带 speaker」；`voice.tts.speaker`
    变成默认值，请求里的非法 id 记日志后回落默认，不中断朗读。
  - 设置页（PWA / macOS）可选 12 个精选 2.0 音色并就地试听；选择存客户端本地，下一句生效。
  - 方案、接口与 UI 见 `docs/design/voice-timbre/design.md`；已按该方案实现后端 / PWA / macOS。

- 2026-11-09（手机语音不可用：线上 nginx 缺 WS 升级）：
  - 根因：生产 `/etc/nginx/sites-available/helper.xhanx.cn` 缺 `location = /api/voice/asr`，
    请求落到 `location /api/` 的 `proxy_set_header Connection ""`，`Upgrade` 被抹掉，
    Go 报 `upgrade token not found in 'Connection' header`。与浏览器无关，线上所有客户端都会失败；
    Mac Chrome 能通是因为 dev 前端 `VITE_API_BASE=http://localhost:8080` 直连后端。
  - 修法：按仓库 `server/nginx-helper.conf` 覆盖线上配置并 reload（**该文件手工安装，`make deploy` 不传**）。
  - 同批加固 iOS WebAudio（未单独验证）：`AudioContext` 在用户手势内创建 + `resume()`（不 await）；
    `AudioWorkletNode` 经 `GainNode(gain=0)` 接 destination（WebKit 不接就不跑 `process()`）。
  - 详见 `docs/bugs/054-voice-ws-upgrade-missing-nginx.md`；影响文件：`server/nginx-helper.conf`、
    `front-end/src/composables/useVoiceChat.js`。

- 2026-11-10（VPIO 空闲释放：整机音量被压）：
  - `output` 端 VPIO 是设备级的：引擎停了但 VPIO 还开着，整机输出就一直在 AEC 通道上，
    音量被压到约 1/6，要退出 app 才恢复。`ensureGraph()` 只开不关是根因。
  - `VoiceAudioEngine` 改为「按需开 VPIO」：引擎空闲 3 秒后自动关掉两端 VPIO，下次播放/采集再开；
    「停引擎后 3 秒内又开始播放」的抢窗口问题靠启动时取消待执行的释放解决。
  - 语音条开着时不释放（`VoiceChatService` 把 `voiceBarActive` 同步给引擎）：释放后再开会丢掉
    录音发送档按下瞬间的音频，短句容易被判「说话时间太短」；退出语音 3 秒后才还回去。
  - 两端分开记（`inputVPIO` / `outputVPIO`）：一端释放失败时另一端不会跟着「以为还开着」。
  - 诊断探针：`client/xHelper/scripts/vpio-release-probe.swift`；详见
    `docs/bugs/057-vpio-locks-system-output-volume.md`。

- 2026-11-10（说完话后远处有声音，整句被近场闸吞掉）：
  - 提交闸原来只看 `sentence` 到达那一刻往前 2 秒的电平窗。豆包判停要等 800ms 静音，
    背景噪声会把句子拖后数秒，到时人声早已滚出窗口，窗口里只剩远处噪声 → 整句丢弃。
  - 改为「**本次发言期间任意 2 秒窗出现过近处人声**就放行」（`nearFieldHeard` 锁存，取用即清零），
    数据不足仍走 `shouldAccept` 的 fail-open。详见 `docs/bugs/058-voice-sentence-dropped-after-background-noise.md`；
    影响文件：`NearFieldGate.swift`、`VoiceChatService.swift`、`front-end/src/utils/nearFieldGate.js`、
    `front-end/src/composables/useVoiceChat.js`。
  - 同时加观察探针（只写日志，不改上行音频）：`nearfield probe: chars=… span=… holes=…% maxHole=…`，
    用于评估「本地逐帧近场门（前处理）」是否值得做；判据与理由见 `docs/design/voice-nearfield/design.md`。

- 2026-11-10（全局按住说话）：
  - macOS 新增全局热键 `⌃⌥V`：按住本地录音、松手整段识别并提交一轮，**回复朗读**；
    app 不在前台（窗口在）也能用。用 Carbon `RegisterEventHotKey`，不需要辅助功能授权，
    且能吞键（不会漏给前台 app）。
  - 没有语音会话时走一次性采集：不置 `holdModeActive`、不弹语音条、不改用户档位偏好，
    也不 pin VPIO（录完 3 秒后整机音量自动恢复）。连续会话开着时退回现有
    `startHold` / `endHold`（按住暂存、松手提交），回复本来就会朗读。
  - 采集真正开始后才播 `Tink`（VPIO 重建那几百毫秒里说的话进不了 tap）；短按、近场闸拒绝、
    识别为空、松开时还没开录 → beep（后台看不到 Toast）。
  - 60 秒自动发送兜底保留：松开事件丢失（例如按住时 ⌘-Tab 切走）不至于一直录。
  - 诊断探针：`client/xHelper/scripts/hotkey-release-probe.swift`（验 Carbon 是否派发
    `kEventHotKeyReleased`）；影响文件：`AppShortcutBridge.swift`（`GlobalHotKey`）、
    `xHelperMac/App.swift`、`VoiceChatService.swift`（`startPushToTalk` / `finishPushToTalk`）、
    `ChatView.swift`、`ShortcutGuideView.swift`。

- 2026-11-11（网页端语音提交丢图）：
  - 用户反馈「手机网页端语音模式长按录音发送，带图时图发不出去」。根因：`submitHoldSentence` /
    `submitVoiceSentence` 写死 `imageIds: []`、不传 `attachments`，草稿图托盘却在语音条打开时
    照常显示。详见 `docs/bugs/061-voice-submit-drops-images.md`。
  - 两条语音提交改为 `takeVoiceImages()` 取托盘里已上传完成的图，写进本地气泡 `imageIds`
    并传 `attachments`。长按档 `wait: true`：松手后等图传完再发（15 秒兜底，超时/失败提示后跳过），
    等图期间用 `holdSubmitting` 挡掉新的按下（识别收尾后 `holdTranscribing` 已清，挡不住并发提交）；
    连续档不等待，未传完的图留给下一句，避免拖慢分句对话。失败提示同一张草稿只弹一次。
  - 影响文件：`front-end/src/views/ChatView.vue`、`front-end/src/utils/voiceImages.js`。

- 2026-11-11（引擎单例化 / 按住说话后半句丢失）：VPIO 是设备级资源，但 `VoiceAudioEngine`
  原来每个语音服务一份（视图每 tab 一份 + 双 ⌘ ptt 一份）。视图实例播完 TTS 后 8 秒关 VPIO，
  会把正在采集的 ptt 实例一起停掉 → 按住超过约 7.5 秒，后半句静默丢失（`docs/bugs/063`）。
  引擎收成进程级单例，采集 / 播放 / 语音条 pin 登记显式 owner（采集独占、播放抢占、pin 多 owner），
  第二个采集 owner 被拒并提示「正在语音输入，请先松手」。
  详见 `docs/design/voice-audio-engine/design.md`。影响文件：`VoiceAudioEngine.swift`、
  `VoiceAudioLease.swift`（新）、`VoiceChatService.swift`。

- 2026-11-11（语音回答还是偏长）：
  - 反馈「有时候还是会说一大堆」。链路已确认没问题（两端都带 `voice_mode`、服务端已解析），
    是约束被稀释：原约束排在 system prompt 的 Layer 1，后面压着角色定义 + 工具目录 +
    稳定上下文；且与常驻 `reply_form` 的 300/500 字档并列，模型偶尔挑宽松那条；
    工具轮的大段 tool 结果在上下文末尾，最容易诱发复述。
  - `voiceModeNotice` 收紧为「1–3 句 / 最多 60 个汉字」，显式声明「与回复形态冲突时以本段为准」、
    「调用了工具只回结论一句」，并从 Layer 1 挪到 Build 末尾（Layer 5，位置贴近用户消息）。
    非语音轮输出逐字节不变，前缀缓存不受影响。
  - `mkLlmChatReq`：语音轮且最后一条消息是 `tool` 时，往**本次请求的消息副本**尾部补一句
    「只回 1–3 句结论，不复述工具返回细节」；`state.msgs` 与落库内容不动，消息序列不变。
  - `[orch] round … response` 日志加 `voice=` 标记，便于定位长回复出现在哪类轮次。
  - 影响文件：`server/internal/agent/promptbuilder/{static.go,builder.go,builder_test.go}`、
    `server/internal/agent/{orchestrator.go,orchestrator_test.go}`。
