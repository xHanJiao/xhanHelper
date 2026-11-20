# 聊天草稿自动落盘（macOS + iOS）

> 状态：**已实现**（待真机验）
> 范围：客户端 `Shared/`（`DraftAutosaveStore.swift` 新增、`ChatViewModel.swift`、`ChatTabManager.swift`）。服务端零改动。

估算（定稿）：1 面 / 1 缝 / 1 次真机往返 / 0 个待拍决定，约 1–2 小时。

---

## 一、背景

输入框草稿（`ChatViewModel.draftText`）此前只活在内存里，全项目只有一处落盘：登录态失效（401）时的
`UnsentDraftStore`（重新登录后恢复一次）。因此这些路径会**静默**丢掉用户写的内容：

| 触发 | 原行为 |
|---|---|
| 在空白「新对话」写了字，点左侧任意会话 / 再点一次「新对话」 | `closeBlankTab()` / `resetBlank()` 直接连 VM 一起扔 |
| 切到第 6 个会话（`maxCachedTabs = 5`，LRU 淘汰） | 被淘汰 tab 的 VM 销毁，草稿没了 |
| 退出 App / 崩溃 / 强制退出 | 没有任何保存钩子 |
| 删除会话 | `close(conversationId:)` 扔 VM |

全屏输入不新增丢失路径（与普通输入栏共用同一份 `draftText`），但它把草稿从短句放大成整篇手账，
同样的丢失代价高得多。

---

## 二、决策记录

| 编号 | 决策 | 结论 |
|---|---|---|
| DA-1 | 存什么 | ✅ **只存文字**。待发图片（`DraftAttachment.data`）与排队队列（`pendingSends`）不存 |
| DA-2 | 空白「新对话」的草稿 | ✅ **保留**：切会话、重开 App 都能灌回来；点「新对话」不再抹掉已有草稿 |
| DA-3 | tab 淘汰策略 | ✅ **照旧淘汰（满 5 个丢最旧的）**，草稿靠落盘恢复；不为草稿放开缓存上限 |
| DA-4 | 何时写盘 | 输入停下 0.4s 后写（防抖）；清空即删除；tab 被淘汰前同步 flush 一次 |
| DA-5 | 存储位置 | `UserDefaults`（单个 JSON blob），与 `UnsentDraftStore` 同款做法 |

---

## 三、实现

```
DraftAutosaveStore（Services/DraftAutosaveStore.swift，纯逻辑）
  key: 会话 id；空白 tab 用 "__new__"
  value: { text, updatedAt }
  save/load/remove + 30 天过期与条数上限清理；单条 > 20 万字符拒绝保存（返回 false）
  blob 解码失败按空处理并落一条 os.Logger 错误（否则下一次写入会把旧条目无声覆盖）
        ↑
ChatViewModel
  init:  $draftText.dropFirst().map { (key: draftKey, text) }   // key 在文本产生时锁定
         .removeDuplicates().debounce(0.4s) → save（空串 = 删除）
  loadConversation: 设好 conversationId 后，若 draftText 为空则回填
  resetForNewChat: 清空后立即回填「空白 tab」草稿（即不再抹掉）
  flushDraft(): 立刻写盘（淘汰/关闭 tab 前调用）
        ↑
ChatTabManager
  closeBlankTab / enforceCacheLimit: 移除 tab 前先 flushDraft()
  close(conversationId:): 不 flush —— 它只用于「会话已删除」，那条草稿留在本地等 30 天过期
```

- **回填只填空的**：`guard draftText.isEmpty`，不会覆盖用户此刻正在写的字（scene 激活刷新、
  同会话重载都会走 `loadConversation`）。
- **为什么 `resetForNewChat` 清空后要回填**：它就是「让这个 tab 变成空白新对话」。清了不回填，
  启动时的 `newChat()` 会先把上次退出留的空白草稿抹掉；回填后「点新对话」与「启动」都表现为
  「草稿还在」，符合 DA-2。
- **key 在发射时锁定**：防抖窗口里 `conversationId` 会变（空白 tab 发完首条转正、切会话），
  写入时现算 key 会把 A 的字存进 B，或用空串删掉 B 的草稿。
- **发送同步删条目**：`sendDraft()` 在置空 `draftText` 之前先 `remove(for: draftKey)`，
  因为空白 tab 发完首条后 `conversationId` 很快变成新 id，等 0.4s 的防抖任务就会删错 key。
- **淘汰前 flush**：防抖窗口内（<0.4s）被关闭/淘汰，最后几个字来不及写，所以这两处移除 tab 前
  同步落一次盘。

---

## 四、边界与已知上限

- **不存附件**：草稿里的待发图片不落盘，重开 App 后只剩文字（图片本来也没上传过，无从恢复）。
- **不存排队队列**：tab 被淘汰时 `pendingSends` 一起丢（只在流式期间存在，概率低）。
- **崩溃窗口**：写完最后 0.4s 内进程被杀，最后几个字仍可能丢（退出/淘汰路径已 flush，只有崩溃/强退有这窗口）。
- **超长草稿**：单条 > 20 万字符不保存（保留上一次内容），日志里有一行 warning。
- 存储是明文 `UserDefaults`：与消息正文同级的本地数据，不额外加密。

---

## 五、自查与验证

- `swiftc -typecheck`（macOS 26.3 + iOS 26.0）与 base 的报错集合一致（仅既有 CEF 报错）。
- `swift test`：`DraftAutosaveStoreTests` 覆盖存取、空串删除、过期清理、超长拒存。
- 真机（用户在 Xcode 确认）：
  - 写一段字 → 切到别的会话 → 切回来（或在第 6 个 tab 把最早的挤掉后再打开）→ 草稿还在。
  - 写一段字 → 退出 App → 重开 → 草稿回来。
  - 空白「新对话」里写字 → 点左侧某个会话 → 再点「新对话」→ 字还在。
  - 发送 / ⌃C 清空后，重开 App 不应再出现旧草稿。

---

## 六、后续变更

- （暂无）
