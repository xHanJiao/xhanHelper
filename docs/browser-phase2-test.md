# Phase 2 浏览器工具测试用例

## 测试环境

- 站点：https://www.baidu.com
- 右侧栏打开后即可开始

---

## Case 1: 基础打开 + 读取

**工具**: `open` → `get_title` + `get_url`

**Prompt:**
```
用浏览器打开 https://www.baidu.com ，告诉我标题和当前URL
```

**预期结果:**
- 右侧栏出现浏览器 tab，标题「百度一下，你就知道」
- LLM 回复 URL 和标题，且 title 不为空

---

## Case 2: 读取页面文字

**工具**: `read` 或 `get_text`

**Prompt:**
```
读取这个页面的所有文字内容，给我一个简短摘要（50字以内）
```

**预期结果:**
- LLM 返回百度首页的文字摘要
- 内容包含百度首页特征文字（如「新闻」「hao123」等）
- 至少返回了百字以上的有效文本

---

## Case 3: 滚动页面

**工具**: `scroll`

**Prompt:**
```
向下滚动 500 像素，告诉我页面上能看到什么新内容
```

**预期结果:**
- `scroll` 返回 success
- LLM 回复提到页面下半部分的内容（不应还是搜索引擎入口）

---

## Case 4: 等待选择器 + 获取快照

**工具**: `wait_for_selector` + `snapshot`

**Prompt:**
```
等待页面上 #kw 元素出现，然后用 snapshot 获取页面的可访问性快照（限制500字）
```

**预期结果:**
- `wait_for_selector(#kw)` 返回 found:true
- snapshot 返回 DOM 结构文本，包含 `<input#kw` 等标签
- 快照不是空字符串

---

## Case 5: 表单填写（fill - 覆盖写入）

**工具**: `fill`

**Prompt:**
```
用 fill 在搜索框（#kw）中填入"天气预报"
```

**预期结果:**
- `fill` 返回 success（非"未找到元素"或 JS 错误）
- 右侧栏浏览器中，百度搜索框显示「天气预报」四个字

---

## Case 6: 表单填写（type - 追加写入）

**工具**: `type`

**Prompt:**
```
用 type 在搜索框中追加输入" 北京"（前面有个空格），然后告诉我搜索框里现在的内容
```

**预期结果:**
- `type` 返回 success
- 搜索框内容变成「天气预报 北京」（追加而非覆盖）
- LLM 能确认内容包含两个词

---

## Case 7: 点击按钮

**工具**: `click` + `wait_for_load`

**Prompt:**
```
点击"百度一下"按钮（#su），等待页面加载完成后，告诉我新页面的标题
```

**预期结果:**
- `click` 返回 success
- 页面跳转到搜索结果页
- 新页面标题包含「天气预报 北京」
- `get_title` 返回非空值

---

## Case 8: 按键

**工具**: `press`

**Prompt:**
```
按 Escape 键，告诉我当前页面标题
```

**预期结果:**
- `press` 返回 success（非 JS 错误）
- 页面标题不变
- 如果有搜索建议弹出，应该关闭了

---

## Case 9: 多 Tab 管理

**工具**: `list_tabs` + `open`（新 tab）

**Prompt:**
```
别忘了刚才的天气预报结果页，现在：
1. 用 list_tabs 列出所有打开的 tab
2. 打开一个新 tab 访问 https://www.baidu.com/s?wd=机器学习
3. 用 list_tabs 看看现在有几个 tab
```

**预期结果:**
- 第一次 `list_tabs` 显示 1 个 tab
- `open` 正常返回（新 tab 出现）
- 第二次 `list_tabs` 显示 2 个 tab（id 不同）
- 右侧栏有 2 个 tab 按钮

---

## Case 10: Tab 切换

**工具**: `switch_tab` + `get_title`

**Prompt:**
```
现在有两个 tab 了。切换到第一个 tab（天气预报那个），告诉我标题。
再切换回第二个 tab（机器学习的），告诉我标题。
```

**预期结果:**
- 两次 `switch_tab` 都成功
- 第一个 tab 标题包含「天气预报」
- 第二个 tab 标题包含「机器学习」
- 两个标题不同，证明确实切换了

---

## Case 11: 关闭所有 tab

**工具**: `close` + `list_tabs`

**Prompt:**
```
关闭所有浏览器 tab，用 list_tabs 确认一下
```

**预期结果:**
- `close` 返回 success，count ≥ 1
- `list_tabs` 返回空列表
- 右侧栏面板收起（isOpen = false）

---

## 无法在百度测试的工具

| 工具 | 原因 | 后续方案 |
|------|------|---------|
| `select` | 百度无 `<select>` 元素 | 找含下拉框页面 |
| `check` / `uncheck` | 百度无复选框 | 找含复选框页面 |
| `wait_for_load` | 在 Case 7 中间接覆盖 | 已测 |

---

## 状态记录

| Case | 日期 | 结果 | 备注 |
|------|------|------|------|
| 1 | | | |
| 2 | | | |
| 3 | | | |
| 4 | | | |
| 5 | | | |
| 6 | | | |
| 7 | | | |
| 8 | | | |
| 9 | | | |
| 10 | | | |
| 11 | | | |
