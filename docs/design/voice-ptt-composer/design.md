# 按住说话 · 输入卡一等入口（手机端）

> UI 预览：`docs/design/voice-ptt-composer/mockup.html`（展开 / 录音 / 上滑取消 / 识别中 / 收起态 × 浅深 × 麦克风 A/B × 40–44px 可切）
> 关联：`docs/design/voice-hold-mode/design.md`（语音条里的长按档）、`docs/design/chat-prompt-queue/design.md`、
> `docs/bugs/069-pwa-composer-focus-lost-on-queue.md`（发完收起）

## 目标

1. 手机端（浏览器 + PWA）把「按住说话」从语音条里提出来，做成主输入卡常驻入口：按住录音、松手上滑取消、
   松手识别后发一条。
2. 保留输入框位置的声音纹路（`VoiceWaveLine`）与上滑 60px 取消手势。
3. 收起态（发完自动收起）也要一步可达，否则按钮在卡里等于没用。

**不做**：不动桌面交互；不动语音条（连续档）本身；不动排队/停止逻辑；不改 ASR 后端；不改 `voice.mode` 偏好。

## 现状与问题

- 长按说话目前是语音条里的一个档：先点左排麦克风进语音条，再从 `[连续对话 | 长按说话]` 里选 hold，才出现录音键。
  语音档偏好默认是 `continuous`，所以左排麦克风默认进的是连续对话。
- 手机端发完自动收起输入卡后，整张卡不可见；要说话得先点「发消息」展开。

## 交互定稿

### 布局

- 左排去掉「语音对话」入口（仅手机端；桌面保留）。左排剩：图片、计划模式、无痕、收起。
- 右排：`(🎤 按住说话) (↑ 发送)`；流式中 `(🎤) (■) (↑)`。发送键位置不变，麦克风落在它左边。
- 麦克风 40×40 圆，`--color-primary-soft` 底 + `--color-border` 描边 + 20px 话筒图标（mockup 候选 A）。
- `voiceAvailable=false` 时不渲染。

### 录音

| 时机 | 行为 |
|------|------|
| 按下 | 开始本地采集；`VoiceWaveLine` 覆盖输入框位置（textarea 不卸载，键盘与布局不跳） |
| 按住 | 麦克风实心绿 + 呼吸圈；左排图标降到 40% 且禁点；发送键置灰 |
| 上滑 > 60px | 麦克风变红，提示条红底「松开取消」；拖回恢复 |
| 松手 | 整段灌入流式 ASR；麦克风 spinner、波线切 thinking |
| 识别成功 | 输入框为空 → 发一条语音轮（带草稿图），随后收起输入卡；已有草稿 → 识别结果接在草稿后，**不自动发**；若当时是收起态，则自动展开让用户看到 |
| 取消 / 太短 / 没听清 / 近场拒绝 | 不发、不清草稿、不收起，沿用现有 toast 文案 |
| 流式中按下 | 松手后停当前轮再发（沿用 `submitHoldSentence` 的语音轮语义） |
| 切后台 / 失焦 / Esc | 取消本次，不发 |

- 录音上限 60 秒自动发送；最短 300ms；近场闸与语音条共用。
- 提示条：录音中「松手发送 · 上滑取消」，取消区「松开取消」。
- 首次进聊天页浮一次「按住说话，松手发送」，按一次或 4 秒后消失（`localStorage` 记一次）。

### 收起态

- `[🎤] [＋ 发消息]`；流式中 `[■] [🎤] [＋ 发消息]`。
- 按住麦克风直接录，录音胶囊（波线 + 上滑取消）浮在按钮上方，不展开输入卡、不跳布局。
- 松手识别完直接发，发完仍回收起态。

## 实现落点

| 文件 | 改动 |
|------|------|
| `front-end/src/composables/useVoiceChat.js` | 新增与会话无关的 PTT：`pttRecording` / `pttTranscribing` / `startPushToTalk` / `finishPushToTalk` / `cancelPushToTalk` / `onPushToTalk`。复用 `holdWorklet/holdStream/holdChunks/holdFrames`、近场闸、300ms/60s 判定、`injectAudio`；**不置 `active`、不建连续 WS、不改 `voice.mode`**。采集图建图与 getUserMedia 抽成 `beginHoldCapture` 给长按档共用 |
| `front-end/src/views/ChatView.vue` | 右排麦克风 + 指针手势（`setPointerCapture`、`preventDefault` 保焦点、上滑 60px）；波线覆盖层；`.hold-hint` 接入；收起态麦克风 + 录音胶囊；手机端隐藏连续档入口；首用提示；`onPushToTalk` 落草稿规则 |
| `front-end/src/utils/voicePref.js` | 首用提示只显示一次的读写（含测试） |
| `front-end/src/components/VoiceWaveLine.vue` | 不改，复用 |

### 关键约束

- **textarea 不能卸载**：录音时用绝对定位的波线覆盖层盖住输入框，输入框（原生 `textarea`，见 `docs/bugs/073`）保持挂载，
  否则 iOS 会失焦收键盘、输入卡高度变化把按钮从手指底下抽走。
- 麦克风 `pointerdown` 必须 `preventDefault()`：既阻止焦点从 textarea 挪到按钮，也阻止 iOS 点外部收键盘。
- PTT 不依赖语音会话，松手后用一次性短 WS 识别；因此不会出现「进语音条 / 退语音条」的状态切换。

## 已知取舍

- 手机端不再有连续对话入口（用户确认不用）。恢复需要重新给入口。
- 异步识别回来后无法在 iOS 上可靠 `focus()` 输入框：草稿追加后光标不自动出现，需要手动点一下。
- 首次使用要过一次麦克风授权弹窗；弹窗期间按住手势会断，松手若发生在采集开始前会静默收掉（不报「太短」）。
  若系统把 `pointerup` 一起吞掉（手指已不在但页面没收到事件），开麦耗时超过 1.5 秒就按「手势被打断」收口并提示
  重新按住——正常开麦远小于这个值，代价是极慢设备上偶尔要多按一次。

## 验证

1. 展开态按住麦克风 → 波线出现在输入框位置、卡片不跳、提示「松手发送 · 上滑取消」→ 松手 → 识别并发出一条。
2. 上滑 60px → 变红「松开取消」→ 松手不发送。
3. 输入框先打字再按住说话 → 识别结果接在草稿后，不自动发。
4. 收起态点按麦克风按住 → 胶囊录音 → 松手发一条，仍回收起态。
5. 流式中按住 → 松手停当前轮并发出。
6. 桌面（≥1100）不受影响：左排语音入口、语音条、发送键焦点行为都不变。
7. 首次安装/清 localStorage 后进聊天页 → 只浮一次「按住说话，松手发送」。

## 后续变更

- 2026-11-19 · 收起态那组浮动按钮重做视觉重心（用户：麦克风与「＋发消息」都是同级入口，不能一小一大）：
  两者统一 44px 高、`primary-soft` 叠 `surface-strong`（半透明软底会让滚上来的正文透字）、
  `primary 30%` 描边、文字 `primary-hover`（软底上 `primary` 只有约 4.1:1）；麦克风图标 20→24px、
  胶囊文字 12.5→16px/500、＋图标 14→19px，停止键 38→44。展开态那套 40px 控件不动。
  影响文件：`front-end/src/views/ChatView.vue`、`docs/design/voice-ptt-composer/mockup.html`。

- 2026-11-21 · 输入框从 naive `NInput` 换回原生 `<textarea>`（高度自己量、组合输入期间不量高，
  修「打拼音丢光标」），「录音时 textarea 不卸载」这条约束不变。见 `docs/bugs/073`。
  影响文件：`front-end/src/views/ChatView.vue`。

- 2026-11-21 · 粘贴文字后重写一次输入框 value（重建 WebKit 编辑会话），修「粘贴后拼音不组词、数字落成圈码」。
  代价：该次粘贴不能撤销。见 `docs/bugs/074`。影响文件：`front-end/src/views/ChatView.vue`。
