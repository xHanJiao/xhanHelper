# 037 · 孤儿概念行永不修复：topic 复用/命名静默跳过，日志还写着 `error: <nil>`

## 问题表现

决策循环的日志里反复出现这一行（本地 dev 实测一晚 6 次，几乎每次写入决策一次）：

```
[record_loop] embed existing topic "房产投资" error: <nil> (skip)
```

两个反常：`error: <nil>`（不是错误却按错误打），以及**同一个 topic 每次都中招**——说明它永远不会被修好。

## 根因

`agent/concept_embedder.go` 的 `GetOrEmbed` 有两段独立逻辑：

1. **概念行已存在** → 查它的向量行；查不到就 `return nil, nil`（提前返回）；
2. **概念行不存在** → 建行 + 嵌入 + 写向量。

于是「概念行在、向量行不在」这个状态落进了空档：它走第 1 段返回空向量，
**永远走不到第 2 段的嵌入逻辑**，也就永远补不上向量。

这个状态是真实存在的——`ensureEmbedded`（写侧）是先建概念行、再嵌入，
嵌入失败时只打日志 `return`，**留下一条没有向量的概念行**；而它下次进来先看 `Exists`，
为真就直接返回，自己也不会补。两边都不修，孤儿就永久留在库里（孤儿概念行的清理见 `docs/bugs/017`）。

**后果**：这条 topic 在三个消费点全部失效——

| 消费点 | 影响 |
|---|---|
| `record_loop_persist.go` 的 `reuseTopic` | 少一个复用候选（新 topic 不会被并到它上面） |
| `topic_namer.go` 的相似度召回 | 少一个可选主题 |
| `theme_discover.go` 的 `embedConcept` | 退化成直接调 embedder |

量级不大（一条 topic 失效），但**每次决策循环都白查一次 DB**，而且日志文案在撒谎
（`%v` 把 nil 打成 `<nil>`，看着像 embed API 报错，实际根本不是）。

> 顺带记一条排查成本：日志把「err != nil」和「返回空向量」塞进同一个 `if` 分支，
> 这正是这次误读的原因。已在 `record_loop_persist.go` 拆开。

## 最终方案

**`GetOrEmbed` 改成自愈**：向量行缺失、为空、或解析失败，一律复用已有概念行**补嵌 + Upsert 写回**
（`embedAndSave`），不再返回空向量。抽出的 `embedAndSave` 同时消掉了原来两段重复的嵌入代码。

**日志拆开**（`record_loop_persist.go`）：`err != nil` 与 `len(vec) == 0` 分别打，
不再出现 `error: <nil>`。

**没有动** `ensureEmbedded`：它的职责是建行，读侧的 `GetOrEmbed` 现在会把孤儿修好；
写侧若失败仍会留孤儿，但那已经无害（下次读就被补上）。

## 验证

- 新增 `TestGetOrEmbed_SelfHealsMissingVector`（`agent/concept_embedder_test.go`）：
  造一条「有概念行、无向量行」的孤儿 → `GetOrEmbed` 必须补嵌并写回 → 第二次调用命中缓存
  （把 embedder 换成必失败的实现，仍应拿到向量）。
- `go build ./... && go test ./...` 全绿。
- 真机：`make dev` 跑一次记忆写入，日志里不再出现 `error: <nil>`；
  若再出现同 topic，应能看到 `有概念行但无向量行，补嵌入`（一次性），此后不再出现。

## 防复发

- **接口类型从 `*llm.Embedder` 改成 `app.Embedder`**：不然注入不了 mock，这条自愈路径没法写单测
  （项目里其它服务早就用 `app.Embedder` 了）。
- 凡是「缓存未命中」的分支，缺失态要么自愈、要么显式报错，**不要返回零值让调用方猜**。
