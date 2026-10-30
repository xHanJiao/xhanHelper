# HTML 产物 · 图片保留策略备忘（内联 vs 外链 + 调 TTL）

- **状态**：**备忘，未决策**（2026-10-30）。灵阅刚上线，没有真实数据，先不动。
- **上位文档**：`design.md` §4.3（图片生成时内联）、§9.6（分享）
- **触发**：有人提出「同一张图被多个灵阅引用时，base64 内联会重复浪费；这种情况下不如把图片 TTL 调长、改成外链」。

---

## 一、结论

方向对（重复确实浪费），但 **「调长 TTL」不是「外链」的等价替代**：TTL 是时间的，产物是永久的，调长只是把丢图推迟，还换不到任何去重收益。

真要改的正确形态是 **外链 + pin（引用计数/清理豁免/解除时机）**——即 `design.md` §4.3 明确否掉的那套机制。现在不做。

## 二、账（按已实测值估算）

内联副本 = 最长边 1600 / JPEG q85 ≈ 原图 1/7，再乘 1.33 base64。3MB 手机照 → 库内约 400~670KB。

| 同一张图被 N 页引用 | 内联副本总量 | 只留一份原图 |
|---|---|---|
| 2 | 0.8~1.3MB | 3MB |
| 5 | 2~3.3MB | 3MB |
| 10 | 4~6.7MB | 3MB |

- 要 **N ≈ 5~8** 副本总量才超过一份原图；去重省的是 **(N−1) 份副本**，复用不普遍时收益接近零。
- 真正的体积主项是「**带图产物数 × 每页图数**」（100 页 × 3 图 ≈ 200MB 级），与重复率无关，换任何方案都省不掉。

## 三、换外链要偿的代价（都不在磁盘）

1. 分享页 CSP 是 `img-src data:`（`controller/artifact.go` 的 `ViewSharedArtifact`），外链要放开成 `self`/`https:`，页面不再自包含：存成文件、发邮件、断网、对方稍后再看都可能丢图。
2. 私有聊天图必须转 `public` + 永久 URL 才能在分享页显示（WKWebView 子资源带不了 Bearer）；撤销分享只失效 URL，图本身还在且无法回收。
3. 需要新增 artifact→image 引用表（HTML 是不透明长文本，不建表就得扫全表 `source_html`），外加单删/批量删/无痕物理删的级联。少算 = 丢图，多算 = 只增不减。
4. 改版链路（`fine-grained-edit.md` §318）现在是「图过期 → 整页失败并点名 id」，切外链后变成「悄悄 404」，更难查。

## 四、真要决策时先量的数

```sql
-- 体积
SELECT COUNT(*) n, ROUND(SUM(LENGTH(html))/1048576) mb FROM artifacts WHERE deleted_at IS NULL;
-- 已软删但仍占库的部分
SELECT ROUND(SUM(LENGTH(html))/1048576) mb_deleted FROM artifacts WHERE deleted_at IS NOT NULL;
```

```bash
# 重复率：同一 image id 被多少页引用
mysql -N -e "SELECT source_html FROM artifacts WHERE deleted_at IS NULL" db \
  | grep -o 'image:[0-9a-f-]\{36\}' | sort | uniq -c | sort -rn | head -20
```

**判据**：N≥5 的图占带图产物的比例 + artifact 列总字节。前者低、后者几百 MB 以内 → 不动。

## 五、更值钱的顺手项（等有量了再做）

1. **软删时清空 `html`**：产物删除是软删（`service/artifact_repo.go` 的 `Delete`/`DeleteMany`），base64 原样留在库里，只有无痕会话才 `Unscoped` 物理删。软删时把 `html` 置空、保留元数据，收益确定、改动一行。
2. **放不下就降内联尺寸**：`InlineMaxEdge` 1600→1200 或 `InlineJPEGQuality` 85→80，省约 40%，只是一处常量（注意 `inline/` 缓存没有版本键，改完要手删缓存）。
3. 都不够再考虑外链 + pin。

## 六、现状事实（供复查）

- 每次生成/改版都对内联图调 `MarkReferenced`，把源图保留期续 15 天（`tool/html_artifact.go` 的 create 尾部），所以「改版找不到图」只在间隔 > 15 天且期间无新产物时发生。
- 源图本身按 `storage.image_ttl_days`（15 天）/ `image_orphan_ttl_days`（3 天）清理；产物内联后与源图解耦。
