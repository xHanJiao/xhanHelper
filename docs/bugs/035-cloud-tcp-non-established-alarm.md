# 035 · 云端 ECS「TCP NON_ESTABLISHED」告警（nginx 无上游 keepalive）

## 问题表现

- 阿里云 **ECS 云监控（主机监控）** 反复报 `TCP 连接数 · NON_ESTABLISHED`。
- macOS 客户端**大量调用工具**时触发，其中**调用客户端工具（浏览器 / fs / shell 连做很多动作）时最明显**。
- 不像是被攻击：同一时间服务本身没有异常、请求都正常返回。

## 根因

**NON_ESTABLISHED = `ESTABLISHED` 以外所有 TCP 状态之和**（TIME_WAIT / CLOSE_WAIT /
FIN_WAIT / SYN_SENT …）。云监控靠 ECS 上的 netstat 采集，**loopback 连接也计入统计** —— 这一条是
后面所有误判的根源。

1. **真凶：nginx 没有上游连接池**。线上 `/etc/nginx/sites-enabled/helper.xhanx.cn` 是
   `proxy_pass http://127.0.0.1:8088;` 直连、**没有 `upstream { keepalive }` 块**。
   nginx 默认对**每个**被代理的请求新建一条到后端的 TCP 连接，响应完立刻关闭
   → `127.0.0.1` ↔ `127.0.0.1:8088` 这对地址持续产 TIME_WAIT（存活 60 秒），
   在 60 秒窗口里叠加。
2. **为什么"客户端工具"最明显**：客户端工具是唯一需要额外向上回传的路径
   （`POST /api/chat/tool-result`，`server/internal/controller/chat.go`），
   服务端工具没有这条上行链路。浏览器自动化一秒钟连做几十个动作 = 几十条 POST
   = 几十条新建/关闭的 loopback 连接。
3. 与 SSE **无关**（排查时先怀疑过，已排除，见下）。

## 排查时排除的项（记下来，下次别重查）

- **SSE 长连接不是元凶**：一个会话只有一条 `/api/chat` 长连，整轮跑完才关，全程 ESTABLISHED，
  不贡献 NON_ESTABLISHED。
- **不是"SSE 静默被代理掐断"**：客户端工具等待期有 15 秒心跳
  （`server/internal/agent/client_tools.go:62` `clientToolHeartbeatInterval`），
  长工具执行不会静默；SSEClient 侧 `timeoutIntervalForRequest = 300`，nginx 侧
  `proxy_read_timeout 600s`，都留了余量。
- **CLOSE_WAIT 残留是次要因素**：`chat.go` 在客户端断开后（`clientGone`）**不结束 handler**，
  继续把整轮事件消费完以保证消息完整入库，这段 socket 停在 CLOSE_WAIT 直到本轮结束；
  客户端工具预算最长 240s，所以残留能挂几分钟。但本次现象是"连做动作时触发"，
  不是"点过停止之后触发"，故**本次不动**（改动会牵连 SSE 收尾逻辑，风险不对等）。

## 最终方案

（2026-10-12）

线上 nginx（`/etc/nginx/sites-enabled/helper.xhanx.cn`）三处改动：

1. 文件顶层（`server {}` 之外，因为本文件被 `http {}` 包含）新增：

   ```nginx
   upstream xhan_helper_backend {
       server 127.0.0.1:8088;
       keepalive 32;
       keepalive_timeout 60s;
   }
   ```

   `keepalive 32` 是**每个 worker 各自的空闲连接缓存上限**（不是连接数上限，并发高时照样按需多开）；
   `keepalive_timeout 60s` 让空闲连接定期退休，避开"后端重启后 nginx 拿死连接发请求 → 偶发 502"。

2. `location /api/`：`proxy_pass` 改指 `http://xhan_helper_backend`，并新增
   `proxy_set_header Connection "";`。**这一行是上游 keepalive 的硬性前提，漏了整件事白改**
   （nginx 只要还向上游发 `Connection: close`，就不会复用连接）。

3. `location /images/`：同样改指 `http://xhan_helper_backend`（顺手，图片读取也不必每次新建连接）。

改后 `sudo nginx -t && sudo systemctl reload nginx`（`reload` 不断连接；SSE 那条长连会被切一次，重发消息即可）。

## 当次踩到的连带坑：`conflicting server name ... ignored`

reload 后 `nginx -t` 报：

```
[warn] conflicting server name "helper.xhanx.cn" on 0.0.0.0:80, ignored
[warn] conflicting server name "helper.xhanx.cn" on 0.0.0.0:443, ignored
```

**这是警告不是错误，但很危险**：`/etc/nginx/sites-enabled/` 里存在**第二份**同名 server 块
（典型来源是编辑 conf 时留下的 `.bak` / `.save` 备份文件，被 `include /etc/nginx/sites-enabled/*`
的 `*` 一并加载）。nginx 静默丢弃**后**加载的那份，**生效的是先加载的那份** ——
于是可能出现"新配置已上传但线上跑的是旧配置"的假象，让人误判成"改了没用"。

定位（`nginx -T` 会把实际生效的完整配置打出来，每段标注来源文件）：

```bash
sudo nginx -T 2>&1 | grep -E "^# configuration file|server_name helper"
ls -la /etc/nginx/sites-enabled/ /etc/nginx/conf.d/
```

确认多余的那份后移出 `sites-enabled`（移到别处而不是删）：

```bash
sudo mv /etc/nginx/sites-enabled/<多余文件> /root/
sudo nginx -t && sudo systemctl reload nginx
```

`conflicting server name` 警告消失 = 干净。

## 验证

一边跑、一边让 Mac 打一轮客户端工具密集的任务：

```bash
watch -n1 'ss -tan state time-wait | grep -c "127.0.0.1:8088"'
```

改前会冲到几十上百并随动作数增长；改后应稳定在个位数（只剩刚关闭、未满 60 秒的尾巴）。
辅助量级：`cat /proc/net/sockstat`（`TCP: ... tw N` 就是 TIME_WAIT 总数）。

> 本次**未回填实测值**（改后没跑监控确认）：判定依据是配置层面的根因已消除
> （每请求新建连接 → 复用连接池），而非观测到曲线下降。

分对端定位（下次如果有类似告警，先跑这个再决定改哪里）：

```bash
ss -tan | awk 'NR>1{print $1}' | sort | uniq -c | sort -rn
ss -tan state time-wait | awk '{print $4}' | sed 's/:[0-9]*$//' | sort | uniq -c | sort -rn | head
```

- 大头是 `127.0.0.1`（8088 那侧）→ nginx 上游连接池（本次就是这个）。
- 大头是 `127.0.0.1:3306` → MySQL 连接池（见下"仍未处理"）。
- 大头是公网 IP（DeepSeek / Ark 的地址）→ LLM 出站连接。
- 状态里 CLOSE_WAIT 占比显著 → SSE 断连残留（见上"排查时排除的项"）。

## 防复发

- 改 nginx 行为时**同一 PR 更新 `server/nginx-helper.conf`**，并记得手动装到线上（同 `docs/bugs/031`）。
- `sites-enabled/` 里**不要留 `.bak` / `.save` / `.old`**：`include .../sites-enabled/*` 会把它们一起加载。
- 看到云监控 NON_ESTABLISHED 告警，**先按"对端 + 状态"分类再动手**，不要当攻击查——
  loopback 上的 TIME_WAIT 也会计入这个指标。

## 仍未处理（留观，本次刻意不动）

- `server/internal/service/db.go:24` `gorm.Open` 后**未设置连接池**，默认 `MaxIdleConns=2` /
  `ConnMaxLifetime=0`：并发超过 2 时连接"用完即关"，`127.0.0.1:3306` 也会产 loopback TIME_WAIT。
- LLM 客户端用默认 Transport（`&http.Client{Timeout: ...}`，无自定义 Transport）
  → `MaxIdleConnsPerHost=2`，主循环 + subagent + 后台 worker 同时打同一个 host 时超出的连接会被关掉重建。

两者都与"**只**在客户端工具密集时明显"对不上（服务端工具的 DB / LLM 调用量相当），
故本次不动；若分对端统计显示它们占比显著，再补（MySQL 侧 3 行 `SetMaxIdleConns` / `SetConnMaxLifetime` 即可）。
