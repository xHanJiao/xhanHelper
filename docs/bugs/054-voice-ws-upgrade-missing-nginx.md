# 054 · 手机 Safari 语音对话无法识别（根因：线上 nginx 没转发 WebSocket 升级）

## 问题表现

- PWA 语音对话在 Mac Chrome 正常；iPhone Safari 点麦克风后 Safari 菜单显示「停止使用麦克风」
  （采集确实开了），但识别不到任何内容，前端红条「语音识别连接失败」。
- 起初误判为 iOS WebAudio 问题（AudioContext 手势、AudioWorklet 拉取），见文末「次要发现」。

## 根因（线上日志实锤）

服务端日志在用户操作时刻反复出现：

```
[voice] asr upgrade failed: websocket: the client is not using the websocket protocol:
        'upgrade' token not found in 'Connection' header
```

生产 `/etc/nginx/sites-available/helper.xhanx.cn`（2026-10-12 版本）**缺 `location = /api/voice/asr`**，
请求落到 `location /api/`，而那里有 `proxy_set_header Connection ""`（为了上游 keepalive 复用），
`Upgrade` 头被抹掉 → Go 收到普通 GET → 升级失败。与 Safari 无关：**线上任何浏览器都会失败**。

Mac Chrome 能通是因为 dev 前端 `VITE_API_BASE=http://localhost:8080` 直连 Go，绕过了 nginx。

## 最终方案

1. 把仓库 `server/nginx-helper.conf` 覆盖到服务器（差异只有 voice 的 WS location，其余是注释/空行），
   备份旧文件后 `sudo nginx -t && sudo systemctl reload nginx`。
2. 验证：外部重放带 `Connection: Upgrade` 的请求，返回 Go 的 `401 invalid or expired ticket`
   （而不是升级失败），即说明升级头已透传；真机随后日志出现
   `asr ws opened` → `asr ready sent` → `asr first audio frame` → `asr result`。

## 教训

`server/nginx-helper.conf` 是**手工安装**的版本化副本（`make deploy` 不会上传，安装需 root）。
新增路由（尤其 WebSocket）后必须同步 `/etc/nginx/sites-enabled/` 并 reload，否则只在线上复现、
本地 dev 直连后端永远看不到问题。

## 次要发现（同批修掉，但未单独验证）

iOS Safari 上 WebAudio 采集还有两处 WebKit 行为差异，已顺手加固（`front-end/src/composables/useVoiceChat.js`）：

1. `AudioContext` 必须在用户手势的同一任务里创建/`resume()`；原代码在 `await getUserMedia` 之后才创建，
   Safari 会让 context 一直 `suspended`，且 `resume()` 的 Promise 可能永不 settle。
   现改为 `start()` 里先同步建 context + `resume()`（不 await）再取麦克风。
2. WebKit 从 destination 反向拉取音频图：输出不接任何节点的 `AudioWorkletNode` 不执行 `process()`（Chrome 会）。
   现接一条 `GainNode(gain=0)` → destination 保活。

因为与 nginx 修复同批上线，无法单独确认这两条是否也在本次故障中起过作用；保留即可，不影响 Chrome。
