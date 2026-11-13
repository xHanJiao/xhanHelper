#import <Cocoa/Cocoa.h>

NS_ASSUME_NONNULL_BEGIN

/// CEF 运行时（进程级）。只在 macOS target 里编（xHelperMac/ 不被 iOS 同步组收）。
///
/// 两个调用时机是硬约束：
/// 1. `executeSubprocessIfNeeded` —— 必须在 `main()` 第一行、NSApplication 起来之前；
/// 2. `initializeIn:` —— 必须在主线程、NSApplication 已起来之后（它会起消息泵定时器）。
@interface XHCefRuntime : NSObject

/// 命中 CEF 子进程分支时直接 `exit()`，不会返回；普通进程返回 NO。
+ (BOOL)executeSubprocessIfNeeded;

/// 成功返回 YES；失败返回 NO（缺 framework / 缺 Helper / CefInitialize 失败），原因打在 Console 的 `[cef]` 日志里。
+ (BOOL)initializeIn:(NSString *)dataRoot;

+ (BOOL)isReady;

@end

/// 一个 CEF 页面视图。父视图只是容器，真正的 Chromium 子视图由 CEF 自己挂进来。
@interface XHCefBrowserView : NSView

- (instancetype)initWithFrame:(NSRect)frame URLString:(NSString *)urlString;

/// 弹窗宿主（`window.open` / `target=_blank`）：浏览器由 CEF 在 `OnBeforePopup` 返回 false 后
/// **自己创建**——只有这样弹窗页才有真的 `window.opener`（`window.open()` 不再返回 null）。
/// 本视图只准备落脚视图和 client，等浏览器创建完再把 CEF 的渲染视图搬进来。
- (instancetype)initAsPopupHostWithURLString:(NSString *)urlString;

@property (nonatomic, copy, nullable) void (^titleDidChange)(NSString *title);
@property (nonatomic, copy, nullable) void (^urlDidChange)(NSString *url);
@property (nonatomic, copy, nullable) void (^loadDidFinish)(BOOL ok);
/// 加载状态 / 前进后退可用性变化（任一变化时调一次，宿主据此刷新地址栏）
@property (nonatomic, copy, nullable) void (^stateDidChange)(void);
/// `target=_blank` / `window.open` 的真弹窗：CEF 自己创建 popup browser（**保留 `window.opener`**），
/// 回调交来还没绑定 browser 的宿主视图。宿主必须**立刻**（同步）把它装进一个新 tab ——
/// 挪到下一拍的话，视图在创建期间没人持有，浏览器就成了孤儿。
@property (nonatomic, copy, nullable) void (^popupPageRequested)(XHCefBrowserView *popupView, NSString *url);
/// 页面自己关了窗口（`window.close()`）：宿主把这个 tab 关掉，否则留下的是一张不会再响应的死页面。
@property (nonatomic, copy, nullable) void (^pageClosedExternally)(void);
/// 页内 `console.log("XHPROBE:<json>")` 的内容（回归探针的回传通道：CEF 执行 JS 没有返回值）
@property (nonatomic, copy, nullable) void (^probeReported)(NSString *payload);
/// 页内查找命中数（CEF 的 Find 是异步的，结果从这里回；finalUpdate 时才回调）
@property (nonatomic, copy, nullable) void (^findResultCount)(int count);
/// JS 对话框：kind 0=alert / 1=confirm / 2=prompt。`reply` **必须调一次**，否则页面一直挂着。
@property (nonatomic, copy, nullable) void (^jsDialogRequested)(NSInteger kind, NSString *message,
                                                               NSString *defaultText,
                                                               void (^reply)(BOOL accepted, NSString * _Nullable text));
/// 用户点击下载：宿主决定落盘路径。**必须同步返回**（nil = 取消这次下载）。
/// 不接这条 CEF 会走 Alloy 默认行为——直接取消，文件不会落盘。
@property (nonatomic, copy, nullable) NSString * _Nullable (^downloadPathRequested)(NSString *suggestedName,
                                                                               NSString *url);
/// 下载状态：state 0=下载中 / 1=完成 / 2=已取消。`fullPath` 是 CEF 实际写入的路径。
@property (nonatomic, copy, nullable) void (^downloadUpdated)(int64_t downloadId,
                                                          NSInteger state,
                                                          int64_t receivedBytes,
                                                          int64_t totalBytes,
                                                          NSString *fullPath,
                                                          NSString *url);
/// 右键菜单里的「用百度搜索选中文字」：宿主开新 tab
@property (nonatomic, copy, nullable) void (^contextSearchRequested)(NSString *selectedText);
/// 媒体检测第 2/3 层回报（页内 JS 的 `console.error("XHMEDIA:<原因>")`）：
/// `no-h264` = 内核不支持 H.264 / `media-src-unsupported` = 源格式不支持。宿主据此提示换兼容引擎。
@property (nonatomic, copy, nullable) void (^mediaIssueReported)(NSString *reason);

@property (nonatomic, readonly) BOOL isLoading;
@property (nonatomic, readonly) BOOL canGoBack;
@property (nonatomic, readonly) BOOL canGoForward;

- (void)loadURLString:(NSString *)urlString;
/// CEF 没有 loadHTMLString，用 data: URL 顶（新标签页那种本地 HTML）
- (void)loadHTMLString:(NSString *)html;
/// 执行 JS（无返回值；要拿结果就让页面 `console.log("XHPROBE:…")`，见 probeReported）
- (void)evaluateJavaScript:(NSString *)script;
/// 执行 JS 并取结果（异步）：value 是 JSON 解析后的对象，失败给 error。
/// 走渲染进程代跑（CEF 没有"执行并返回"的 API），10s 无回包按超时失败、关页立刻失败。
- (void)evaluateJavaScript:(NSString *)script
                completion:(void (^)(id _Nullable value, NSString * _Nullable error))completion;
- (void)reload;
- (void)stopLoading;
- (void)goBack;
- (void)goForward;

/// 页面缩放（1.0 = 100%）。CEF 内部是对数级 zoom level，换算在 .mm 里做。
@property (nonatomic, readonly) double zoomScale;
- (void)setZoomScale:(double)scale;

/// 页内查找（结果经 findResultCount 回报；空串 = 停止查找）
- (void)findString:(NSString *)text forward:(BOOL)forward matchCase:(BOOL)matchCase;
- (void)stopFinding:(BOOL)clearSelection;

/// 取消进行中的下载（CEF 侧真正停掉）
- (void)cancelDownload:(int64_t)downloadId;

/// 强回收：等 OnBeforeClose 才放掉 browser 引用（泄漏先例见 docs/bugs/2026-08-browser-webview-leak）
- (void)closeBrowser;

@end

NS_ASSUME_NONNULL_END
