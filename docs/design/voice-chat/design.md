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

1. macOS 客户端与 Vue/PWA 都能「点麦克风说话 → 识别 → 填入输入框 → 发送 → 回复自动朗读」。
2. TTS 走豆包语音合成模型 2.0，ASR 走豆包流式语音识别模型 2.0。
3. API Key 只在服务端；客户端不接触豆包凭证。
4. 为后续 C（静音自动提交 + 全屏环 + 打断）保留传输层与状态机接口。

## 非目标

- 不做 iOS 原生客户端（PWA 代替）。
- 不做逐句流式 TTS（首版整段返回 mp3；C 再评估）。
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
| `limit` | 到达单连接上限；服务端已 `Finish`，随后会吐 `final`（A 模式）或 `sentence`（C 模式）。客户端应停止采集但**不要**再发 `end`，并 arm 一个兜底计时器 |
| `error` | 识别失败 |

C 模式说明：

- `mode=continuous` 时服务端强制 `result_type=single`，**不再下发 `result`**，只下发 `sentence`，避免两套累计逻辑并存。
- `sentence` 只在 `definite=true` 时发；重复文本去重；连接结束时的 `final` 只含最后一句，也会作为 `sentence` 补出残句。
- 一条连接可连续识别多句（实测 83 秒 / 18 句不断），退出语音模式才发 `end`。

### C1 连续对话（macOS）

- 输入栏新增「连续对话」按钮（`waveform.circle`）：点一下开麦，之后一直收音频。
- 每收到一条 `sentence` 就**直接提交一轮对话**（`speakReply=true`，回复自动朗读）；
  上一轮还在跑时先 `stopStreaming()` + `stopSpeaking()`，避免两个 ReAct 循环交叉写库。
- 会话上限取 `/api/voice/config` 的 `session_seconds`（默认 600 秒）：到点提示并收口。
- 空闲收口：30 秒没有新分句（即没人说话）自动结束会话，避免占并发、白扣音频时长。
- 结束/掉线都会发 `cancel` 或等负包结果后关连接；`limit` 后立即停采集（负包之后不能再发音频）。
- `shortcut:` 连接意外断开不自动重连，10 分钟会话内掉线需用户再点一次；真机常掉再加重连。

### C2 全屏语音环

- macOS：`VoiceOrbView`（`Shared/Views/`）覆盖会话区（`.ultraThinMaterial` + 三层环），
  状态优先级「说 > 想 > 听」；listen 态用 `VoiceAudioEngine.onLevel`（RMS×4，0...1）驱动核心缩放。
- PWA：`components/VoiceOrb.vue` 全屏浮层（`position: fixed`），监听态用 AudioWorklet 已有的 `level`，
  `speaking` 由 `<audio>` 的 `ended`/`pause` 事件维护。
- 两端都能点「结束」收口：macOS 同时停生成与朗读；PWA `cancel()` + `stopSpeaking()`。
- 只是展示层，不改传输层与状态机。

### C3a 回声消除与打断（macOS）

- `Shared/Services/VoiceAudioEngine.swift`：采集与播放共用一个 `AVAudioEngine`；
  TTS 走挂在其上的 `AVAudioPlayerNode`，VPIO 才有回声参考信号。
- 初始化顺序固定：接播放图 → `setVoiceProcessingEnabled(true)`（input + output）→ 装采集 tap → start。
  顺序反了 AEC 会静默失效，不报错。
- 麦克风门：播报开始即 `setGated(true)`，分片不发 ASR，只留最近 600ms 到 pre-roll；
  播报结束再压 600ms 收尾音。
- 打断：门控期间 `onLevel` 仍工作；音量超过 `max(0.32, 残响本底 × 1.8)` 连续 3 帧（约 120ms）
  即判为用户开口 → 冲回 pre-roll → 停朗读 → `onBargeIn` → ChatView 调 `stopStreaming()`。
  残响本底用 EMA 跟随自身播放的漏音，避免「自己打断自己」。
- 代价：AEC 只是信号相减，耳机场景最稳；外放时阈值抬高，可能需要说大声一点。
- iOS 若将来接语音，audio session 必须用 `.voiceChat`（`.spokenAudio` 下系统不给回声参考）。

### C3b PWA 点击打断

- 浏览器里不做自动 barge-in：Chrome 的 AEC 默认只覆盖 WebRTC 通路，`<audio>` 播放的漏音
  会直接把识别带偏；Safari 参数不生效，更没法可靠判断「用户在说」。
- 全屏环在「说」态把按钮改成「打断」：点一下 = `handleStop()`（停朗读 + 断 SSE + `/api/chat/stop`）
  再 `cancelVoice()`。
- 播报期间绕开浮层点麦克风同样先走 `handleStop()`，避免把 AI 自己的声音录进去。

## 客户端

### macOS

- 输入栏麦克风按钮。
- `AVAudioEngine` 采集 → `AVAudioConverter` 转 16k/16bit/mono。
- `URLSessionWebSocketTask` 连自家后端，二进制发 PCM，文本发 `{"type":"end"}`。
- WS 一打开就开始采集（不等 `ready`），早于 `ready` 的帧由 socket 缓冲；`max_seconds-2` 自动收口。
- 识别文本填输入框（不自动发送）。
- 回复 `done` 后请求 TTS，由 `VoiceAudioEngine`（`AVAudioPlayerNode` + VPIO）播放；停止按钮同时停朗读。
- 补 `NSMicrophoneUsageDescription`。

### PWA（Vue）

- `getUserMedia` + AudioWorklet 采集，重采样 16k/Int16。
- WebSocket 连自家后端。
- WS 打开即采集（不等 `ready`），`max_seconds-2` 自动收口；`limit` 后停止采集等 `final`。
- 识别文本填输入框（不自动发送）。
- 回复完成后 `<audio>` 播放 mp3；首次点击麦克风时解锁音频元素。
- 不支持麦克风的浏览器隐藏按钮。

## 语音回复约束

新增 `voice_mode` 请求参数（照 `brief_answer` 路径）：

- 服务端在 system prompt 注入：口语化、不要 markdown/列表/代码块、同语言、中文约 80–120 字。
- 仅语音轮注入；接受前缀缓存分叉（低频动作）。
- 客户端只朗读最终回复；剥离 markdown/代码块；纯代码/表格回复改播一句提示。

## 后续 C（连续对话）

按 C0 → C1 → C2 → C3a → C3b 顺序做，一个阶段一个提交。

| 阶段 | 内容 | 范围 |
|---|---|---|
| **C0** | 后端 `mode=continuous` → `result_type=single` + `sentence` 分句下发 | 后端 |
| **C1** | 连续流 + `sentence` 自动提交 + 会话上限（10 分钟硬上限 + 30 秒空闲自动收口） | macOS |
| **C2** | 全屏环（听 / 想 / 说 三态 + 音量） | macOS + PWA ✅ |
| **C3a** | TTS 搬进 `AVAudioEngine`（`AVAudioPlayerNode`）+ VPIO + 播放后麦克风门 + 说话打断 | macOS ✅ |
| **C3b** | 点击打断（不做 auto barge-in，浏览器 AEC 不可靠） | PWA ✅ |

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
