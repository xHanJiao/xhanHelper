# Bug #002 — LLM 分类未严格按照枚举值选择

> 发现日期: 2026-05-01 | 严重程度: P1（数据质量问题） | 状态: 已修复

---

## 问题表现

记账时 LLM 自创分类——比如用户说"今天剪头发花了80"，LLM 可能用 `category1: "美容美发"` 而非 `"美容"`；或者用 `category2: "理发"` 而非已有的 `"漂染头发"` 等。

自创分类导致：账单数据无法按标准分类统计、同一类消费被分散到多个类目下。

---

## 根因分析

**Prompt 缺失枚举值列表。** JSON Schema 参数只写了类型和一句描述：

```json
{
  "category1": {
    "type": "string",
    "description": "一级分类"
  }
}
```

LLM 只看到 `type: string`，不知道可选值范围。它根据语义自行推断分类名，而这些推断可能和系统定义的枚举不完全匹配。

相比之下，原 bill-assistant-server 的 prompt 明确列出了所有可选值：
```
- category1 只能从以下选择：餐饮、服装、交通、居住、旅游、美容、人情、日用、数码、学习、医疗、娱乐
```

**第二，缺少"不确定时反问"的约束规则。** 即使 LLM 知道分类列表，遇到模糊场景（如"买了个包"——是服装/日用/娱乐？）仍可能强行猜测。

---

## 修复方案

### 修复 1：JSON Schema 加 `enum` 字段

`tool/bookkeeping.go` 中 category1/category2/consume_type/payType 参数增加 `"enum": [...]`：

```go
// 修复前
"category1": {"type": "string", "description": "一级分类"}

// 修复后
"category1": {
    "type": "string",
    "enum": ["餐饮","服装","交通","居住","旅游","美容","人情","日用","数码","学习","医疗","娱乐"],
    "description": "一级分类，必须从列表中选"
}
```

### 修复 2：`registry.GeneratePrompt()` 显示枚举值

```go
// 遍历 JSON Schema 参数时，发现有 enum 就展示
if enumVals, ok := prop["enum"].([]interface{}); ok && len(enumVals) > 0 {
    buf.WriteString(fmt.Sprintf("      可选值: %s\n", strings.Join(vals, "/")))
}
```

LLM 最终看到的 prompt 变为：

```
函数:
  - create_bill: 新增一笔账单
    - item (string): 项目名称
    - amount (number, 必填): 金额
    - category1 (string, 必填): 一级分类，必须从列表中选
      可选值: 餐饮/服装/交通/居住/旅游/美容/人情/日用/数码/学习/医疗/娱乐
    - category2 (string, 必填): 二级分类，必须从列表中选
      可选值: 保险/餐饮/厨房用具/...
    ...
```

### 修复 3：加强约束规则

在 prompt 末尾追加：

```
===== 重要规则 =====

1. 带枚举值的字段必须严格从可选值中选择，不得自创分类！
2. 如果用户描述模糊无法确定分类，必须用纯文本回复询问用户，不要猜测！
   示例回复: "这个消费是餐饮还是购物呢？"
3. 只有用户明确或根据上下文99%确定时，才调用工具。
```

---

## 影响范围

| 文件 | 改动 |
|------|------|
| `server/internal/tool/bookkeeping.go` | JSON Schema 增加 enum 字段 |
| `server/internal/tool/registry.go` | GeneratePrompt 展示 enum 值 + 强约束规则 |
| `server/scripts/api-test.sh` | 测试消息改为明确分类（"午饭35块，餐饮-堂食"） |
