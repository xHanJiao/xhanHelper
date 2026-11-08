# 语音聊天（豆包 TTS + 流式 ASR）设计

> 状态：首版 A 已实现，待配置豆包 Key 后真机联调
> 分支：`feat/voice-chat`
> 实现：后端 `internal/voice` + `controller/voice.go`；macOS `VoiceAudioCapture` / `VoiceChatService`；PWA `useVoiceChat` + `public/voice-pcm-worklet.js`

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

`GET /api/voice/config` 返回 `enabled/ready/speaker/language`；未配置时客户端隐藏麦克风按钮。

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

新增 `controller/voice.go`：

| 路由 | 说明 |
|---|---|
| `GET /api/voice/config` | 能力探测 |
| `POST /api/voice/asr-ticket` | 一次性 ticket |
| `POST /api/voice/tts` | `{text}` → `audio/mpeg` |
| `GET /api/voice/asr?ticket=` | WebSocket 音频流 |

限制：

- TTS 单次 ≤ `voice.tts.max_chars`（默认 2000 字符），音频 ≤ 20MB。
- ASR 单轮 ≤ `voice.asr.max_seconds`（默认 60 秒），按 PCM 字节数折算。
- 不记录音频、识别文本；日志只记长度/字节数/错误。

## 配置

```yaml
voice:
  enabled: false
  api_key: ${DOUBAO_API_KEY}
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
    max_seconds: 60
    timeout_seconds: 30
```

- 新版火山控制台只需 API Key，不需要 AppID / AccessToken / Cluster。
- 开通产品：豆包语音合成模型 2.0、豆包流式语音识别模型 2.0（小时版）。

## 客户端

### macOS

- 输入栏麦克风按钮。
- `AVAudioEngine` 采集 → `AVAudioConverter` 转 16k/16bit/mono。
- `URLSessionWebSocketTask` 连自家后端，二进制发 PCM，文本发 `{"type":"end"}`。
- 识别文本填输入框（不自动发送）。
- 回复 `done` 后请求 TTS，`AVAudioPlayer` 播放；停止按钮同时停朗读。
- 补 `NSMicrophoneUsageDescription`。

### PWA（Vue）

- `getUserMedia` + AudioWorklet 采集，重采样 16k/Int16。
- WebSocket 连自家后端。
- 识别文本填输入框（不自动发送）。
- 回复完成后 `<audio>` 播放 mp3；首次点击麦克风时解锁音频元素。
- 不支持麦克风的浏览器隐藏按钮。

## 语音回复约束

新增 `voice_mode` 请求参数（照 `brief_answer` 路径）：

- 服务端在 system prompt 注入：口语化、不要 markdown/列表/代码块、同语言、中文约 80–120 字。
- 仅语音轮注入；接受前缀缓存分叉（低频动作）。
- 客户端只朗读最终回复；剥离 markdown/代码块；纯代码/表格回复改播一句提示。

## 后续 C

- AudioWorklet 已计算 RMS，接自适应噪声本底 + 1.2s 静音判停。
- 600ms pre-roll 环形缓冲，打断时补进新录音。
- 打断复用现有 `/api/chat/stop`（真取消）。
- 全屏环用 SwiftUI / CSS 实现，不改传输层。

## 验收

- 后端：`go build ./... && go test ./...`，新增 TTS/ASR 单测。
- macOS：`swiftc -typecheck` 通过，真机麦克风权限、中文识别、TTS 播放。
- PWA：`vite build` 通过，Chrome/Safari 真机麦克风与播放；iOS PWA 单独验证。
- 未配置 Key 时：客户端隐藏麦克风，接口返回 503，不影响文字聊天。

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
