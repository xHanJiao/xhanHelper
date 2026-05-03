# UI 改进清单

> 日期: 2026-05-02 | 状态: 待处理

---

## 1. 工具函数名中文化

**问题**: 工具卡片上显示 `get_current_time`、`evaluate`、`generate` 等英文函数名，不可读。

**改法**: `ToolCallData.swift` 的 `displayName` 扩展映射表：

```swift
var displayName: String {
    switch function {
    case "create_bill": return "记账"
    case "update_bill": return "修改账单"
    case "cancel_bill":  return "作废账单"
    case "query_bills":  return "查询账单"
    case "get_current_time": return "获取时间"
    case "evaluate":     return "计算"
    case "generate":     return "生成UUID"
    default: return function
    }
}
```

---

## 2. 思考过程不显示

**问题**: 后端已发送 `event: thought`，但客户端看不到思考内容。

**排查方向**:
- `ChatViewModel` 的 `thoughtMessage` 是否正确接收
- `ChatView` 中 thought 气泡渲染条件是否正确
- SSE 事件类型解析是否遗漏

**验证**: 用 curl 看 SSE 流中是否有 `event: thought` 行

---

## 3. Markdown 渲染

**问题**: LLM 回复中的 Markdown（`**粗体**`、`- 列表`、```代码块```）显示为原始文本。

**改法**: 
- SwiftUI 原生不支持 Markdown 渲染
- 方案 A: iOS 15+ 的 `AttributedString` + `try? AttributedString(markdown:)` 内置支持
- 方案 B: 引入轻量 Markdown 库
- 推荐方案 A，零依赖，一行代码:

```swift
// ChatBubble.swift
Text(try? AttributedString(markdown: text) ?? AttributedString(text))
```
