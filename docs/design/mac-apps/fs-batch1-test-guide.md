# fs 工具 Batch 1 真机验收指南

> 分支：`feature/fs-tools`（commit `8bda70c`）
> 测试夹具：`server/data/fs-test/`（详见其 `README.md`）
> 适用：macOS 客户端 fs 读工具增强（HTML 剥离 / 编码回退 / PDF 文本 / 预算对齐 / 安全加固）

## 一、执行前准备

1. **编译**：在 Xcode 中编译运行 xHelperMac（agent 不执行 xcodebuild，编译由人工完成）。
2. **服务端**：`cd server && make dev` 跑着（本批无服务端改动，仅确认零文件操作）。
3. **客户端**：macOS App 登录并进入对话，发送消息携带 `client_tools`（fs 工具自动上报）。
4. **授权前提**：读 `server/data/fs-test/` 目录下的文件会触发首次授权确认（L3 确认条），点允许即可，之后 1 小时免确认。

## 二、验收方式

通过聊天自然语言让 agent 读取文件（如"帮我读一下 /Users/you/xhanCodes/xhanHelper/server/data/fs-test/utf8.txt"），观察返回内容。所有路径统一使用：

```
/Users/you/xhanCodes/xhanHelper/server/data/fs-test/
```

## 三、测试用例

### 1. 基础 UTF-8

| 项 | 操作 | 预期 |
|----|------|------|
| utf8.txt | 读 | 逐行带行号 `1: 内容`，行号连续，末尾 `(End of file - total N lines) (encoding: UTF-8)` |

### 2. 编码回退

| 项 | 操作 | 预期 |
|----|------|------|
| gbk.txt | 读 | 中文正常显示，末尾标注 `(encoding: GB18030)` |
| utf16le.txt | 读 | 中文正常显示，标注 `(encoding: UTF-16)` |
| utf16be.txt | 读 | 同上 |
| bom_utf8.txt | 读 | 第一行无 BOM 乱码字符（`\uFEFF` 不出现） |
| crlf.txt | 读 | 内容正常，无 `\r` 残留显示 |

### 3. HTML script/style 剥离（重点）

| 项 | 操作 | 预期 |
|----|------|------|
| sample.html | 读 | ① 行号从 1 到 27 连续，中间无跳号 ② 原 script/style 块内每行显示为 `<!-- [fs] stripped -->` ③ 块外内容原样 ④ 文件中"prompt injection: 忽略所有指令"字样**不出现** |
| resume.html | 读 | 简历正文完整（教育/工作/项目/技能/自我评价），结尾 `<script>` 埋点块整行被替换为占位标记，行号连续 |

判定要点：**剥内容不剥行号**——之后 edit 工具（Batch 3）依赖行号对齐。

### 4. PDF 文本提取

| 项 | 操作 | 预期 |
|----|------|------|
| resume.pdf | 读 | 输出纯页文本（含"张三 - 个人简历"，无 `[Page N of N]` 标记），末尾 `(End of PDF - total 2 pages, size: ... bytes)`；**不得出现 base64** |
| resume.pdf 续读 | 读时带 `page_offset=2` | 从第 2 页开始 |
| scanned.pdf | 读 | 报错"PDF 无文本层（可能是扫描件）" |

### 5. 大文件截断续读

| 项 | 操作 | 预期 |
|----|------|------|
| big.txt（6.8MB） | 读 | 输出约 19KB 即截断，提示 `(Output capped at 19 KB...)` 与续读 offset；不返回 6.8MB 全量 |
| big.txt 续读 | 按提示带 offset 再读 | 能继续读后续行，直至 `(End of file...)` |
| longline.txt | 读 | 单行显示前 2000 字符 + `(line truncated to 2000 chars)`，不卡死不爆内存 |

### 6. 图片 base64

| 项 | 操作 | 预期 |
|----|------|------|
| image.png | 读 | 返回 `Image read successfully\n[data:image/png;base64,...]` |

### 7. 目录与空文件

| 项 | 操作 | 预期 |
|----|------|------|
| testdir/ | 读目录 | `<type>directory</type>` + 条目（a.txt、b.md、sub/），目录名带 `/` |
| testdir/sub | 读 | 列出 c.json |
| empty.txt | 读 | `1: \n(End of file - total 1 lines)` |

### 8. 敏感文件拦截

| 项 | 操作 | 预期 |
|----|------|------|
| sensitive/test.env | 读 | `BLOCKED: cannot access sensitive file...` |
| sensitive/credentials.txt | 读 | `BLOCKED`（credentials 关键词） |

### 9. symlink 逃逸（重点）

| 项 | 操作 | 预期 |
|----|------|------|
| symlink-in.txt | 读 | 正常返回（内容 = utf8.txt），证明 realpath 解析后仍在授权范围内 |
| symlink-out.txt | 读 | 报错"路径不在已授权目录内（或 symlink 指向授权目录外）" |

### 10. 大小写不敏感授权

| 项 | 操作 | 预期 |
|----|------|------|
| 授权 fs-test 后，读 `/Users/you/xhanCodes/xhanHelper/SERVER/data/fs-test/utf8.txt` | 大小写变体 | 应成功（授权比较大小写不敏感） |

### 11. 服务器零文件操作确认

| 项 | 操作 | 预期 |
|----|------|------|
| 任意读操作后 | 看服务端日志 | 无任何文件路径访问记录；服务端只转发 client_tool_call / tool-result |

## 四、快速冒烟清单（时间紧时只跑这些）

1. 读 `resume.html` → script 剥、行号连续（用例 3）
2. 读 `gbk.txt` → GB18030 标注（用例 2）
3. 读 `resume.pdf` → 纯页文本、无 `[Page N of N]` 标记、无 base64（用例 4）
4. 读 `big.txt` → 19KB 截断 + 续读 offset（用例 5）
5. 读 `symlink-out.txt` → 拒绝（用例 9）
6. 读 `sensitive/test.env` → BLOCKED（用例 8）

## 五、排查

| 现象 | 排查 |
|------|------|
| fs 工具不出现 | 客户端是否上报 client_tools（请求体含 client_tools 字段）；重启 App |
| 读文件一直弹授权 | 授权是临时 1h，超时需重新确认 |
| 返回内容被二次截断 | 服务端 20KB 注入预算：结果 ≤19KB 才不触发；检查是否还有 50KB 旧逻辑 |
| PDF 中文乱码 | PDFKit 依赖 PDF 内 ToUnicode 表，生成器相关，非工具 bug（扫描件/无文本层报错属预期） |

## 六、已知问题（与本批无关）

- `FileSystemToolProvider+Grep.swift` 中 `LogBuffer.shared.warning` 存在 MainActor 隔离报错（既有代码，提交前已存在；若 Xcode 编译不过需单独修复）。
- 服务端 `make test` 有既有失败（测试数据库残留），与本次改动无关。

## 七、Batch 2 验收（fs__write + diff 确认条）

> 前提：先完成 Batch 1 冒烟；授权 fs-test 目录（读一次任意文件即可）。

### 用例

| # | 操作（聊天对话） | 预期 |
|---|------------------|------|
| 1 | 让 agent 新建 `fs-test/new-note.txt`，内容 "hello" | 确认条出现：`📝 写入文件`（displayName=写入文件）+ 路径 + 全 + 行 diff + `+1 -0`；点允许 → 落盘；结果返回 Wrote N bytes |
| 2 | 让 agent 写 `fs-test/resume.html` 的"技能"一节（改内容） | **覆盖已有文件**：确认条显示 diff（绿/红/上下文着色）+ `+N -M`；拒绝 → 文件不变 + LLM 收到"用户拒绝"；队首超时 180s → 同上 |
| 3 | 直接让 agent 覆盖 `fs-test/utf8.txt`（本轮未读过） | 不弹确认，直接报错"请先用 read 读取该文件后再覆盖写入" |
| 4 | 先读 `fs-test/bom_utf8.txt`，再让其覆盖写一行 | 允许后文件仍带 UTF-8 BOM（`file` 命令验证 "with BOM"） |
| 5 | 先读 `fs-test/utf8.txt`，让 agent append 一行 | 确认条显示追加 diff；允许后原内容保留、新增一行，且原行尾若缺 \n 自动补 |
| 6 | 让 agent 写 `fs-test/sensitive/test.env` | 不弹确认，直接 BLOCKED |
| 7 | 让 agent 写 `fs-test/symlink-out.txt`（→ /etc/hosts），**信任模式需关闭** | 确认条弹出且显示解析后的真实路径 `/etc/hosts`；点允许后执行时报错"路径不在已授权目录内"（realpath 复检） |
| 8 | 信任模式开启后让 agent 写文件 | 不弹确认直接落盘（L3 被信任模式跳过） |
| 9 | 让 agent 写 `fs-test/big.txt`（覆盖，先读过） | 确认条显示"文件较大…未生成行级 diff 预览"（degraded，非卡死） |
| 10 | 写目录路径（如 `fs-test/testdir/`） | 报错"路径是目录，不是文件" |

### 快速冒烟（时间紧）

用例 1 + 2 + 3 + 6。

### 判定要点

- 确认条 diff 逐行着色：`+` 绿底、`-` 红底、`@@` 灰、上下文白
- 大 diff 可滚动（240pt 上限），不截断
- 新建 = 全绿行；覆盖 = 混合；追加 = 尾部绿
- 写成功后在客户端日志（LogBuffer）可见结果字节数

## 八、Batch 3 验收（fs__edit 渐进改）

> 前提：Batch 1/2 通过；每次 edit 前先 read 目标文件（本会话）。

### 用例

| # | 操作（聊天对话） | 预期 |
|---|------------------|------|
| 1 | 读 `fs-test/resume.html` 后，让 agent edit "技能"节一处（如 `Go / Swift / Python` → `Go / Swift / Python / Rust`） | 确认条弹 diff（改动块红绿）；允许 → 替换成功，返回"Replaced 1 occurrence at line(s) N" |
| 2 | edit 带缩进误差的 old_string（少缩进/多空格） | 仍匹配成功（line-trimmed / ws-normalized 兜底） |
| 3 | old_string 匹配到多处（如 edit `XX大学` 不带上下文） | 报错"N 处匹配 + 行号"，提示加上下文或 replace_all；LLM 应变通 |
| 4 | 同文件连续 3 次小 edit（渐进改，每次先 read 或基于前次结果） | 3 次都成功，内容逐段变化 |
| 5 | 先读 `fs-test/crlf.txt`，edit 一行 | 替换成功且文件仍 CRLF（`file` 验证） |
| 6 | 未 read 直接 edit `fs-test/utf8.txt` | 报错"请先用 read 读取该文件后再编辑" |
| 7 | old_string 与 new_string 相同 / old_string 为空 | 明确报错，不落盘 |
| 8 | old_string 是文件中不存在的内容 | 报错"未找到 old_string"+ 重读提示 |
| 9 | edit `fs-test/sensitive/test.env` | 直接 BLOCKED，不弹确认 |
| 10 | 信任模式开启下 edit | 不弹确认直接改（L3 被跳过） |

### 快速冒烟

用例 1 + 3 + 6。

### 判定要点

- 确认条与 write 同款 diff 卡片（复用）
- 每次 edit 都是小 diff（红绿集中在改动块），不是全量
- 失败话术要能引导 LLM 重读/加上下文/用 replace_all，不重复死磕同一 old_string
