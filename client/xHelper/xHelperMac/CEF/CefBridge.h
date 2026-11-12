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

@property (nonatomic, copy, nullable) void (^titleDidChange)(NSString *title);
@property (nonatomic, copy, nullable) void (^urlDidChange)(NSString *url);
@property (nonatomic, copy, nullable) void (^loadDidFinish)(BOOL ok);
/// 加载状态 / 前进后退可用性变化（任一变化时调一次，宿主据此刷新地址栏）
@property (nonatomic, copy, nullable) void (^stateDidChange)(void);
/// `target=_blank` / `window.open`：CEF 默认会自己开一个裸原生窗口（还会复用本视图的 client，
/// 把地址栏串成弹窗的 URL）。这里一律拦下，交给 Swift 开成我们自己的 tab。
@property (nonatomic, copy, nullable) void (^popupRequested)(NSString *url);

@property (nonatomic, readonly) BOOL isLoading;
@property (nonatomic, readonly) BOOL canGoBack;
@property (nonatomic, readonly) BOOL canGoForward;

- (void)loadURLString:(NSString *)urlString;
/// CEF 没有 loadHTMLString，用 data: URL 顶（新标签页那种本地 HTML）
- (void)loadHTMLString:(NSString *)html;
- (void)reload;
- (void)stopLoading;
- (void)goBack;
- (void)goForward;

/// 强回收：等 OnBeforeClose 才放掉 browser 引用（泄漏先例见 docs/bugs/2026-08-browser-webview-leak）
- (void)closeBrowser;

@end

NS_ASSUME_NONNULL_END
