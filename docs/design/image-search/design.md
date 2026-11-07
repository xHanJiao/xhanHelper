# 图片搜索（描述关键词 + 描述语义）— 技术方案

> 状态：**已实现**（2026-11-07）。
> 估算（开工）：9 面 / 1 缝（相似度阈值只能真实数据验证，暂无探针）/ 1 次真机往返 / 3 个待拍决定 · ~1.5–3h
> 估算（定稿）：9 面 / 1 缝（同上）/ 1 次真机往返 / 0 个待拍决定 · ~1.5–2h
> 关联：`../multimodal-images.md`（图床与描述生成）、`../document-user-images/design.md`（图片进文档）

---

## 一、一句话

让 agent 不再只能翻页遍历图片：按描述里的关键词精确搜，或按自然语言语义搜；搜索结果和 `list_images` 同构，拿到就能嵌文档/HTML。

---

## 二、范围

**做**

- `image` 工具新增 `search_images(query, mode=keyword|semantic, top_k)`
- 图片描述生成后自动建向量；启动时加载已有向量并回填老图
- 删除图片（手动 / 到期清理 / 坏记录自愈）时同步清向量

**不做**

- 不搜图片像素本身（嵌入模型支持图片嵌入，但本期只嵌描述；见 §八）
- 不改 `unified_search` 的五个域，不加 `image` 域
- 不改前端/客户端的图片管理页（纯 agent 侧能力）

---

## 三、决定

| # | 决定 | 取值 |
|---|---|---|
| D1 | 入口 | `image` 工具新增 `search_images`，不并进 `unified_search`（图片工具本就在渐进暴露里，输出结构现成） |
| D2 | 默认模式 | `keyword`；描述性查询显式传 `semantic`（关键词免费、确定） |
| D3 | 相似度阈值 | `0.5`，与 memory/note 一致，代码常量便于后调 |
| D4 | 老图回填 | 启动时对「有描述但没向量」的图补建（后台 goroutine，与 memory/note warmup 同模式） |
| D5 | 可见性 | 不变。搜索是 owner-scoped，private/public 都搜；搜索本身不改变可见性 |

---

## 四、搜索契约

```
search_images(query: string, mode?: "keyword"|"semantic" = "keyword", top_k?: int = 10, max 20)
```

返回结构与 `list_images` 一致（`images[] / total / hint`），语义模式每条多一个 `score`：

```json
{"images":[{"id":"...","description":"...","markdown":"![](/images/<id>.png)",
            "html":"<img src=\"image:<id>\" alt=\"...\">","bytes":123,
            "source":"chat","available":true,"created_at":"2026-11-07 10:00",
            "score":0.72}],
 "total":1,"hint":"..."}
```

- **关键词**：`user_id = ?` + 每个关键词一条 `INSTR(LOWER(description), ?) > 0`，OR 连接，按 `created_at DESC, id DESC`（id 做 tiebreaker，同一秒上传的图顺序稳定）。用 `INSTR` 而不是 `LIKE`，免去 `%`/`_` 转义；MySQL 与 SQLite 都支持。
- **语义**：`Embed(query)` → 内存 `VectorStore.Search`，过滤 `metadata.type = "img" && metadata.user_id = 当前用户` → 阈值 0.5 → 按分数降序 → **回 DB 用 `GetByIDsAndUser` 取资产**。
- 关键词只匹配描述，不匹配文件名（图床不存原始文件名）。

---

## 五、向量与生命周期

| 项 | 取值 |
|---|---|
| `embeddings.entity_type` | `image` |
| 内存向量 key | `img:<image_id>` |
| 元数据 | `{type: "img", user_id}` |
| 写入时机 | `ImageStore.describe()` 回写描述成功后；失败只记日志（描述已落库，下次启动 warmup 补） |
| 回填 | 启动 warmup：分页加载已有向量 + 对「有描述无向量」的图逐个 embed |
| 删除 | `ImageStore.Delete` / `Cleanup.purge` / `purgeMissingFiles` 调 `ImageIndexer.RemoveImages`（删向量行 + 内存条目） |

**「删除后不会被搜到」的保证**：不依赖向量删干净。关键词走 DB；语义向量只产候选 ID，最终一定回 `image_assets` 表取图，取不到就丢弃。向量清理失败最多留孤儿向量，不会出现在结果里。

warmup 顺带对账：图片向量里 DB 已不存在的 `entity_id` 直接删向量行，避免孤儿长期占内存。

---

## 六、改动文件

| 文件 | 改动 |
|---|---|
| `server/internal/model/embedding.go` | 新增 `EntityTypeImage` / `VectorTypeImage` / `VectorPrefixImage` |
| `server/internal/service/image_asset_repo.go` | `SearchByDescription`、`ListDescribedAfter`（回填键集分页）、`ExistingIDs`（孤儿对账） |
| `server/internal/service/image_store.go` | `ImageIndexer` 接口 + `SetIndexer`；描述后索引；三处删除路径清向量 |
| `server/internal/agent/image_indexer.go` | 新文件：`IndexImage` / `RemoveImages` |
| `server/internal/agent/warmup.go` | `WarmupImageEmbeddings`（加载 + 回填 + 孤儿对账），在 `ready` 之后调用，不绑健康检查 |
| `server/internal/tool/image.go` | 新增 `search_images`；构造函数加 embedder/vector store；`ReadOnlyFuncs` / `Catalog` / 输出结构；语义结果按 `top_k` 截断 |
| `server/internal/tool/document.go` / `html_artifact.go` | 工具描述补「找图可用 search_images」 |
| `server/internal/appinit/tools.go` / `agents.go`、`server/cmd/xhan-helper/main.go` | 接线 |
| `server/internal/tool/image_test.go` / `image_search_test.go` | 关键词 OR / 大小写 / 用户隔离 / limit；语义排序 / 阈值 / **top_k 截断**；**向量在、记录删 → 返回空** |
| `server/internal/service/image_store_test.go` | 描述成功后建索引、删图清向量 |

---

## 七、验收

- `go build ./... && go test ./...`
- 单测覆盖：关键词、语义、`top_k` 截断、删除后搜不到、用户隔离、描述为空的图搜不到
- 真机一次：上传图 → 等描述 → `search_images` 关键词与语义各一次；删图后再搜为空
- 不在 prod 库做任何写操作

---

## 八、未来：图片本身的嵌入

搜索只认向量元数据 `type=img`，不关心向量从哪来。以后嵌入模型支持图片嵌入时，可把图片向量写进同一套 key/元数据（或新增 `image_vec` 类型后调整过滤条件），`search_images` 的契约与输出不用改。

---

## 九、已知限制

- 描述未生成的图（上传后不久 / 描述失败）搜不到，`hint` 会提示改用 `list_images`。
- SHA 去重路径不会补建索引：重复上传返回老记录，只有 `DescriptionAt == nil` 才重新生成描述；「有描述但索引失败」的图要等下次启动 warmup 补。
- warmup 就绪前语义搜索可能缺内存向量（与 memory/note 相同的既有取舍）。
- `image` 工具不在任何 subagent 白名单里，搜索只在主循环可用。
- 搜索返回的 `markdown` 是 public 路径；只有写进文档才触发 `PromoteToDoc`，写进笔记/聊天正文时 private 图打不开。`html` 字段由服务端按 owner 内联，private 可用。
