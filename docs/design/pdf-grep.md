# PDF 精确检索（fs__grep 支持 PDF）

## 背景

`fs__read` 已能按页提取 PDF 文本层（`FileSystemToolProvider+Read.swift`），但 `fs__grep`
把路径直接交给 `rg`/`grep`，PDF 是二进制会被跳过，导致 177 页这类大 PDF 只能靠
`page_offset` 逐页翻，无法"先定位再精读"。

## 目标

- 对**有文本层**的 PDF 支持 grep 式正则精确匹配，命中返回页码，再用
  `read(filePath, page_offset=N)` 读上下文。
- 不做语义检索、不做 OCR、不做缓存（后置）。

## 范围

- 仅单个 PDF 文件（path 指向 .pdf）；目录内 PDF 批量搜索后置。
- 匹配粒度：按行（与 grep 语义一致，能报页内行号）。

## 设计

### 分层

纯逻辑进 `FsCore`（SwiftPM，`swift test` 可自验）；PDFKit 提取留在 app target。

```
fs__grep
  ├─ 授权/敏感检查（复用现有）
  ├─ 是否 PDF？（ext == pdf 或魔数 %PDF）
  │    ├─ 否 → 原 rg/grep 路径不变
  │    └─ 是 → PDFTextExtractor.pageTexts(path)          [app, PDFKit]
  │            → PDFGrepMatcher.search(...)              [FsCore, 纯逻辑]
  │            → GrepParser.formatGrepOutput(...)        [FsCore, 复用]
  │            → OutputSanitizer.sanitize(...)
```

### 数据结构

- `GrepMatch` 增加 `let page: Int? = nil`（memberwise init 带默认值，现有调用点不破）。
  - `page != nil` 表示 PDF 命中，`line` 为页内行号。
- `GrepParser.formatGrepOutput` 标签：`page != nil ? "Page \(page), line \(line)" : "Line \(line)"`。

### PDFGrepMatcher（FsCore）

```swift
enum PDFGrepMatcher {
    static func search(pageTexts: [String], pattern: String, file: String,
                       maxLineChars: Int, maxResults: Int, skip: Int) throws -> GrepCollectResult
}
```

- `NSRegularExpression(pattern:)` 构造失败 → throw（调用方转 `serverError`）。
- 逐页 `split(separator: "\n", omittingEmptySubsequences: false)`，逐行 `firstMatch`。
- 命中交给 `GrepCollector.ingest`，返回 false（收满 `maxResults`）立即停止。
- `skip` 翻页语义、`moreAvailable` 停止语义全部复用 `GrepCollector`。

### PDFTextExtractor（app target）

```swift
enum PDFTextExtractor {
    static func pageTexts(path: String) throws -> [String]
}
enum PDFExtractError: LocalizedError { case cannotOpen, encrypted, noTextLayer }
```

- `PDFDocument(url:)` 失败 → `cannotOpen`；`isLocked` → `encrypted`。
- 逐页 `page(at:)?.string ?? ""`。
- 所有页文本去空白后均为空 → `noTextLayer`（扫描件）。

### 错误映射（handleGrep）

| 错误 | serverError |
|------|-------------|
| `cannotOpen` | 无法打开 PDF: {path} |
| `encrypted` | PDF 已加密，无法提取文本: {path} |
| `noTextLayer` | PDF 无文本层（可能是扫描件），无法检索: {path} |
| 正则构造失败 | 无效正则表达式: {pattern} |

## 涉及文件

- 新增 `client/xHelper/xHelper/Shared/Tools/PDFTextExtractor.swift`
- 改 `client/xHelper/xHelper/Shared/Tools/GrepParser.swift`
- 改 `client/xHelper/xHelper/Shared/Tools/FileSystemToolProvider+Grep.swift`
- 改 `client/xHelper/xHelper/Shared/Tools/FileSystemToolProvider.swift`（工具描述）
- 改 `server/internal/agent/subagent.go`（fileExplorePrompt）
- 改 `client/xHelper/Tests/FsCoreTests/FsCoreTests.swift`（用例）

## 后续迭代（177 页财报实测反馈）

- **页内续读**：`read` 增 `page_char_offset`（仅作用于起始页）。`PDFPageWindower`
  按字节预算切片，单页超预算时至少返回该页一部分，续读提示精确到
  `page_offset=P, page_char_offset=C`；修复原 `readPDFText` 用计划末页当续读点导致的跳页 bug。
- **页眉页脚清洗**（位置无关）：`PDFPageCleaner` 删除两类——
  ①**行尾数字 token == 物理页码**（`其他应付款69`→`其他应付款`；数字前为 `.`/`,` 视为数值保留）；
  ②归一化后出现在 `>= max(3, ceil(页数*80%))` 个不同页的行（不看位置，长度≥8 且含字母/CJK 才参与统计）：
  整行命中则删整行；被拼进正文行（行首/行中/行尾）则按**忽略空白差异的行内子串剔除**。
  `read`/`grep` 共享。启发式天花板：不足 80% 页重复的页眉会漏；`- 69 -`/`第69页` 及印刷页码偏移不处理；
  阈值内重复的正文行、以及恰好内嵌完整页眉串的正文行会被摘除。
- **阴性对照**：验证清洗请用只在页眉出现的串（如 `2026年半年度报告全文`），修完应 0 命中；
  不要用公司名（正文合法出现多次，非 0 是正常的）。
- **无页码标记**：`PDFPageWindower` 输出纯页文本（页间换行分隔），不注入 `[Page N of N]`，
  避免切片/embedding 污染；页码仅通过 grep 命中的 `Page N, line M` 或 `read` 的入参体现。
- **grep 行号**：命中标签为 `Page N, line M`（页内行号，展示用；`read` 仍按页读）。
- **size**：`read` 尾注补字节大小；`glob` 每条 `path\t<bytes>`。
- **表格局限**：`page.string` 无列坐标，宽表行列关系丢失、跨行数字被切断（如
  `16,240,00` / `76,520,9`）。实测结论：本工具作**检索/定位**合格，作**数据提取**不合格。
  财报数值解析交给版面感知提取器（pdfplumber/camelot/tabula）或 XBRL，本工具不做表格重建。
- **跨行数字搜索**：line-based grep 无法命中被换行切断的数字，属版面丢失，后置不做 flow 模式。
- **raw 诊断通道**：清洗做在提取层，`read`/`grep` 共用同一份清洗后文本，导致"页眉消失"无法独立验证
  （兼容"清洗生效"与"本就没提取到"）。`read` 增 `raw=true`（默认 false）绕过清洗，返回 `page.string` 原样，
  用于对照验证（raw 应见页眉/页码，默认读应不见）。仅加在 `read`，`grep` 不加。
- **页码基准**：`page_offset`、grep 的 `Page N`、`(total N pages)` 三者统一为 **PDF 物理页、1 起、含封面**；
  打印页码可能因封面/目录偏移而不同，故与人工标注页码可能差 1~2 页。
- **误删取舍**：`stripRepeatedLines` 先按整行去空白匹配识别重复串，再对该串做行内子串剔除。
  正文"包含"重复串的长句若整行不等于该串，只有其中内嵌的串片段会被摘除，其余保留；
  恰好内嵌完整页眉串的正文行也会被摘除。范围窄，接受，靠 `raw` 审计。

## 验收

- `cd client/xHelper && swift test` 全绿（新增 PDF 匹配/翻页/页码格式化用例）。
- 集成（PDFKit 提取、路由）由用户在 Xcode 真机用大 PDF 验证；agent 不执行 xcodebuild。
