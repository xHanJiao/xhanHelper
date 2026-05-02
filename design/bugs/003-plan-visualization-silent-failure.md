# Bug #003 — Plan 流程图不显示的 2.5 小时排查

> 日期: 2026-05-02 | 严重程度: P1 | 耗时: ~2.5h

---

## 问题表现

后端正确发送了 `event: plan` SSE 事件（curl 验证通过），但客户端始终不显示流程图。橙色"执行计划"框要么完全不出现，要么只出现一个孤点。

## 根因

**直接原因**：`parsePlanNodes` 用 `JSONDecoder().decode([String: [PlanNodeJSON]].self, from: d)` 一键解码整个 JSON。但 plan JSON 里混合了两种类型的对象：

```json
{
  "edges": [{"from": "n0", "to": "n1"}, ...],   // ← 字段是 from/to
  "nodes": [{"id": "n0", "label": "...", ...}]  // ← 字段是 id/label/status
}
```

`[String: [PlanNodeJSON]]` 要求字典的**所有值**都是 `[PlanNodeJSON]` 类型。`edges` 数组中的对象只有 `from`/`to`，没有 `PlanNodeJSON` 要求的 `id`/`label`/`status`，JSONDecoder 抛出类型不匹配错误。

但调用方用的是 `try?`——**静默吞掉了错误**，返回空数组 `[]`。客户端看到 `planNodes.isEmpty == true`，不渲染流程图。

**深层原因**：
1. `try?` 静默失败——解码出错不报任何日志，排查全靠猜
2. 客户端调试工具不足——没有在 Xcode 控制台打印 SSE 事件或解析状态

## 排查过程

1. 检查后端是否发了 plan 事件 → curl 验证 ✅
2. 检查 SSE 事件格式 → JSON 格式正确 ✅
3. 检查 Grape 库是否正常集成 → 编译多次失败后成功 ✅
4. 检查 ChatView 是否渲染 PlanGraphView → 代码看上去没问题
5. 检查 ChatViewModel 是否处理 plan 事件 → 代码看上去没问题
6. 用 DEBUG 标签验证 planNodes 状态 → 始终为 0 ❌
7. 怀疑 `try?` 静默失败 → 拆开 nodes 和 edges 各自解析 → ✅ 解决

## 修复

1. **不混合解码**：用 `JSONSerialization.jsonObject` 拿到原始字典，`nodes` 和 `edges` 各自解析
2. **打印错误**：解析失败时 `print("⚠️ parsePlanNodes FAILED")`，不再静默吞掉

## 教训

1. **禁止 `try?` 静默失败**。任何 `try?` 失败时必须有日志——哪怕一行 `print`
2. **混合类型 JSON 不要用强类型 Decoder 一把梭**。先拆字段，再各自解码
3. **客户端加 SSE 事件日志**在调试阶段。每个 `handleEvent` case 至少打一条 `print`
4. **排查时先验证数据是否到达 ViewModel**——一个 DEBUG 标签就省了 2 小时
