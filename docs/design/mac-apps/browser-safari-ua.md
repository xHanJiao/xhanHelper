# 浏览器 UA & 指纹策略切换：Chrome 伪装 → Safari 原生

> 日期：2026-08-10
> 分支：feat/browser-safari-ua
> 前提：WKWebView 内核 = Safari（WebKit + JSC + Apple GPU），不做 Chrome 伪装，比伪装更多破绽更少

## 一、动机

当前浏览器使用 Chrome UA + 指纹脚本（fake plugins/WebGL/languages 等）。百度持续弹验证码，说明指纹仍有破绽。

**核心洞察**：WKWebView 底层就是 Safari 的 WebKit 引擎——它的 `navigator.plugins`（空 PluginArray）、`navigator.vendor`（Apple Computer, Inc.）、WebGL renderer（Apple GPU）、`window.chrome`（不存在）等，天然和 Safari 一致。假装 Chrome 需要覆盖 7+ 个属性，且每覆盖一个都可能在别处与底层不一致而自相矛盾。假装 Safari 只需要补 **一个破绽**：`navigator.webdriver`。

## 二、改动清单

### 2.1 UA 字符串

| 位置 | 旧（Chrome 131） | 新（Safari 18） |
|------|-----------------|----------------|
| `makeWebView` L2128 | `...Chrome/131.0.0.0 Safari/537.36` | `...Version/18.0 Safari/605.1.15` |
| `defaultChromeUA` L991 | 同上 | 同上 + 改名为 `defaultBrowserUA` |
| L852 引用 | `Self.defaultChromeUA` | `Self.defaultBrowserUA` |

完整 Safari 18 UA：
```
Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15
```

### 2.2 指纹脚本：删除 `fingerprintJS`

整个 `static let fingerprintJS`（L2952-2987）删除，连同 `makeWebView` 中注入该脚本的代码块（L2105-2110）。具体删除内容：

- `navigator.plugins` 覆盖（手造 4 个 Chrome 插件）→ **原生空 PluginArray 与 Safari 一致**，不需要覆盖。
- `navigator.languages` / `platform` / `hardwareConcurrency` / `deviceMemory` / `maxTouchPoints` 覆盖 → **底层真实值和 Safari 一致**，不需要覆盖。
- WebGL1 `getParameter` hook（返回 Apple GPU/Apple Inc.）→ **原生返回就是 Apple GPU**，跨 Safari 一致。WebGL2 路径之前未覆盖，现在也不需补。

### 2.3 保留：Safari 伪装脚本（webdriver + window.safari）

WKWebView 与 Safari.app 仅存两处 JS 差异，合一脚本注入（documentStart）：

1. `navigator.webdriver`：WKWebView 恒为 `true`，Safari.app 为 `false`。覆盖 **原型层**（`Navigator.prototype`）而非实例层——实例层会在 `navigator` 上残留 own property，`Object.getOwnPropertyDescriptor(navigator,'webdriver')` 一查就穿帮；原型层覆盖后实例无残留、原型返回 `false`，与 Safari 一致。
2. `window.safari`：Safari.app 有该全局对象（`pushNotification` 等），WKWebView 完全缺失。UA 声称 Safari 但该对象 `undefined` 是最大的明面破绽，注入最小壳子 `{pushNotification: function(){}}` 补上。

JS 脚本：
```js
(function(){
  try{Object.defineProperty(Navigator.prototype,'webdriver',{get:function(){return false;},configurable:true});}
  catch(e){try{Object.defineProperty(navigator,'webdriver',{get:function(){return false;}});}catch(e2){}}
  try{if(!window.safari){window.safari={pushNotification:function(){}};}}catch(e){}
})()
```

### 2.4 不受影响的部分

- `a11yRegistryScript`（L2113-2118）：agent 工具链需要，保留。
- `ContentBlocker`（L2121-2123）：广告拦截，与 UA 无关，保留。
- Favicon 下载（L1763）：使用 `session.webView.customUserAgent`，会自动跟随新 UA。
- Agent 工具下载（L852）：有 `defaultBrowserUA` 兜底，UA 更换后自动生效。
- 收藏夹/历史：不涉及 UA 字符串。

## 三、验证

- Xcode 编译运行 → 打开 https://www.browserling.com/user-agent（或任意 UA 检测页），确认显示 Safari 18。
- 百度首页：验证码出现频率应明显下降（减少或不再出现）。
- 小红书/抖音：浏览行为不变，性能不受影响（脚本减少反而更快）。
- Agent 工具链（snapshot/click/download/fill）不受影响。
