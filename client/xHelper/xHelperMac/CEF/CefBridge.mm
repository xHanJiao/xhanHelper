#import "CefBridge.h"

#include "CefDisguise.h"

#include <objc/runtime.h>

#include <cmath>
#include <map>
#include <memory>
#include <string>
#include <vector>

#include "include/cef_app.h"
#include "include/cef_browser.h"
#include "include/cef_client.h"
#include "include/cef_browser_process_handler.h"
#include "include/cef_context_menu_handler.h"
#include "include/cef_download_handler.h"
#include "include/cef_menu_model.h"
#include "include/cef_display_handler.h"
#include "include/cef_find_handler.h"
#include "include/cef_jsdialog_handler.h"
#include "include/cef_life_span_handler.h"
#include "include/cef_request.h"
#include "include/cef_parser.h"
#include "include/cef_resource_request_handler.h"
#include "include/cef_values.h"
#include "include/cef_load_handler.h"
#include "include/wrapper/cef_library_loader.h"

// MARK: - 进程级状态

static bool g_cef_ready = false;
static NSTimer *g_pump = nil;

// libcef_dll_wrapper 是「运行时加载」形态（C API 符号由 wrapper 里的一张函数指针表转发），
// 所以 framework 必须先 dlopen 再碰任何 cef_* —— 也就是绝不能直接链 framework（会两份）。
static std::unique_ptr<CefScopedLibraryLoader> g_loader;

// argv 指针要活到 CefExecuteProcess / CefInitialize 用完，放静态区最省心。
static std::vector<std::string> g_arg_store;
static std::vector<char *> g_arg_ptrs;

static CefMainArgs MakeMainArgs() {
  NSArray<NSString *> *args = NSProcessInfo.processInfo.arguments;
  g_arg_store.clear();
  g_arg_ptrs.clear();
  g_arg_store.reserve(args.count);
  for (NSString *arg in args) {
    g_arg_store.emplace_back(arg.UTF8String ?: "");
  }
  g_arg_ptrs.reserve(g_arg_store.size());
  for (std::string &s : g_arg_store) {
    g_arg_ptrs.push_back(const_cast<char *>(s.c_str()));
  }
  return CefMainArgs(static_cast<int>(g_arg_ptrs.size()), g_arg_ptrs.data());
}

// CEF 要求 NSApplication 实现 CefAppProtocol（见 include/cef_application_mac.h）：
// 它用 isHandlingSendEvent 判断"此刻是不是在 sendEvent: 栈里"，不实现就抛
// `-[NSApplication isHandlingSendEvent]: unrecognized selector`（实测在这条上崩过）。
// SwiftUI 不能给 NSApplication 建子类，所以用 category 补方法 + 换掉 sendEvent: 的实现。
static BOOL g_handlingSendEvent = NO;
static IMP g_originalSendEvent = NULL;

@interface NSApplication (XHCefSendingEvent)
- (BOOL)isHandlingSendEvent;
- (void)setHandlingSendEvent:(BOOL)value;
@end

@implementation NSApplication (XHCefSendingEvent)
- (BOOL)isHandlingSendEvent {
  return g_handlingSendEvent;
}
- (void)setHandlingSendEvent:(BOOL)value {
  g_handlingSendEvent = value;
}
@end

static void XHCefSendEvent(NSApplication *self, SEL _cmd, NSEvent *event) {
  BOOL previous = g_handlingSendEvent;
  g_handlingSendEvent = YES;
  ((void (*)(id, SEL, NSEvent *))g_originalSendEvent)(self, _cmd, event);
  g_handlingSendEvent = previous;
}

static void InstallSendEventSwizzleIfNeeded() {
  if (g_originalSendEvent) return;
  Method method = class_getInstanceMethod(NSApplication.class, @selector(sendEvent:));
  g_originalSendEvent = method_getImplementation(method);
  method_setImplementation(method, (IMP)XHCefSendEvent);
}

// CEF 的回调在主线程；万一不是，就挪过去再碰 AppKit。
static void RunOnMain(dispatch_block_t block) {
  if (NSThread.isMainThread) {
    block();
  } else {
    dispatch_async(dispatch_get_main_queue(), block);
  }
}

static NSString *CefStringToNSString(const CefString &value) {
  return [NSString stringWithUTF8String:value.ToString().c_str()] ?: @"";
}

// 初始化失败的原因只打一次日志（Swift 侧只看 BOOL，细节在 Console 里）
static void LogInitFailure(NSInteger code, NSString *message) {
  NSLog(@"[cef] 初始化失败(%ld)：%@", (long)code, message);
}

@interface XHCefRuntime ()
+ (void)schedulePumpWorkAfterMs:(int64_t)delayMs;
@end

namespace {

// external_message_pump 的正确用法：CEF 通过 OnScheduleMessagePumpWork 告诉"delay 毫秒后需要跑一次
// CefDoMessageLoopWork"。别用固定 60Hz 定时器空转（本 app 关窗驻留、常开，白烧电）。
class XHCefBrowserProcessHandler : public CefBrowserProcessHandler {
 public:
  void OnScheduleMessagePumpWork(int64_t delay_ms) override {
    // 这个回调可能在任意 CEF 线程，定时器必须在主线程装
    dispatch_async(dispatch_get_main_queue(), ^{
      [XHCefRuntime schedulePumpWorkAfterMs:delay_ms];
    });
  }

 private:
  IMPLEMENT_REFCOUNTING(XHCefBrowserProcessHandler);
};

class XHCefApp : public CefApp {
 public:
  CefRefPtr<CefBrowserProcessHandler> GetBrowserProcessHandler() override {
    if (!browser_process_handler_) {
      browser_process_handler_ = new XHCefBrowserProcessHandler();
    }
    return browser_process_handler_;
  }

  /// 后台标签（agent 工作区停进隐藏窗口）的 renderer 会被 Chromium 当"不可见"降频：
  /// 定时器冻结、加载停在半路 —— 实测表现是标题正确但 `document.readyState` 永远不是 complete。
  /// 这三个开关就是关掉这层降频；只改浏览器进程，子进程参数由 CEF 自己往下传。
  void OnBeforeCommandLineProcessing(const CefString &process_type,
                                    CefRefPtr<CefCommandLine> command_line) override {
    if (!process_type.empty() || !command_line) return;
    command_line->AppendSwitch("disable-background-timer-throttling");
    command_line->AppendSwitch("disable-renderer-backgrounding");
    command_line->AppendSwitch("disable-backgrounding-occluded-windows");
  }

 private:
  CefRefPtr<XHCefBrowserProcessHandler> browser_process_handler_;
  IMPLEMENT_REFCOUNTING(XHCefApp);
};

}  // namespace

@class XHCefBrowserView;

@interface XHCefBrowserView () <NSMenuDelegate>
- (void)cefBrowserCreated:(CefRefPtr<CefBrowser>)browser;
/// 下载进行中：留下 CefDownloadItemCallback 的取消句柄（CEF 不允许把 item 留存到回调之外）
- (void)cefDownloadRegister:(int64_t)downloadId callback:(CefRefPtr<CefDownloadItemCallback>)callback;
- (void)cefDownloadDrop:(int64_t)downloadId;
/// 把 CefMenuModel 摊成 NSMenu 显示（Alloy 模式没有默认右键菜单，必须自己弹）
- (void)cefShowMenu:(CefRefPtr<CefMenuModel>)model
           callback:(CefRefPtr<CefRunContextMenuCallback>)callback;
/// 渲染进程返回的 JS 结果（`xh_eval_result`）
- (void)cefEvalResult:(int32_t)requestId json:(NSString *)json error:(NSString *)error;
- (void)cefBrowserDidClose;
- (void)cefLoadingStateChanged:(BOOL)isLoading
                     canGoBack:(BOOL)canGoBack
                  canGoForward:(BOOL)canGoForward;
@end

namespace {

// 第 2 层伪装：CEF 默认的 sec-ch-ua 只报 Chromium（UA 被覆盖后甚至可能不发），服务端一眼看出不是 Chrome。
// 真 Chrome 只在安全传输上加这三个低熵提示（http 上不发，乱发反而是异常信号）。
class XHCefResourceRequestHandler : public CefResourceRequestHandler {
 public:
  ReturnValue OnBeforeResourceLoad(CefRefPtr<CefBrowser> browser,
                                   CefRefPtr<CefFrame> frame,
                                   CefRefPtr<CefRequest> request,
                                   CefRefPtr<CefCallback> callback) override {
    if (IsSecureForClientHints(request->GetURL())) {
      request->SetHeaderByName("sec-ch-ua", xhdisguise::SecChUa(), true);
      request->SetHeaderByName("sec-ch-ua-mobile", "?0", true);
      request->SetHeaderByName("sec-ch-ua-platform", "\"macOS\"", true);
    }
    return RV_CONTINUE;
  }

 private:
  // 低熵提示只走安全来源（https，以及浏览器当"可信"的 localhost）。逐个比 host 前缀不够：
  // `http://localhost.evil.com` 也会命中，那才是不该注入的地方。
  static bool IsSecureForClientHints(const CefString &url) {
    CefURLParts parts;
    if (!CefParseURL(url, parts)) return false;
    const std::string scheme = CefString(&parts.scheme).ToString();
    if (scheme == "https") return true;
    if (scheme != "http") return false;
    const std::string host = CefString(&parts.host).ToString();
    return host == "localhost" || host == "127.0.0.1" || host == "[::1]";
  }

  IMPLEMENT_REFCOUNTING(XHCefResourceRequestHandler);
};

class XHCefClient : public CefClient,
                    public CefLifeSpanHandler,
                    public CefLoadHandler,
                    public CefDisplayHandler,
                    public CefFindHandler,
                    public CefJSDialogHandler,
                    public CefDownloadHandler,
                    public CefContextMenuHandler,
                    public CefRequestHandler {
 public:
  explicit XHCefClient(XHCefBrowserView *view) : view_(view) {}

  CefRefPtr<CefLifeSpanHandler> GetLifeSpanHandler() override { return this; }
  CefRefPtr<CefLoadHandler> GetLoadHandler() override { return this; }
  CefRefPtr<CefDisplayHandler> GetDisplayHandler() override { return this; }
  CefRefPtr<CefFindHandler> GetFindHandler() override { return this; }
  CefRefPtr<CefJSDialogHandler> GetJSDialogHandler() override { return this; }
  CefRefPtr<CefDownloadHandler> GetDownloadHandler() override { return this; }
  CefRefPtr<CefContextMenuHandler> GetContextMenuHandler() override { return this; }
  CefRefPtr<CefRequestHandler> GetRequestHandler() override { return this; }

  // JS 求值回包（渲染进程的 XHCefRenderProcessHandler 跑完脚本后回传）
  bool OnProcessMessageReceived(CefRefPtr<CefBrowser> browser,
                                CefRefPtr<CefFrame> frame,
                                CefProcessId source_process,
                                CefRefPtr<CefProcessMessage> message) override {
    if (!message || message->GetName() != "xh_eval_result") return false;
    CefRefPtr<CefListValue> args = message->GetArgumentList();
    if (!args || args->GetSize() < 3) return false;
    const int32_t requestId = args->GetInt(0);
    NSString *json = CefStringToNSString(args->GetString(1));
    NSString *error = CefStringToNSString(args->GetString(2));
    XHCefBrowserView *view = view_;
    RunOnMain(^{
      [view cefEvalResult:requestId json:json error:error];
    });
    return true;
  }

  // 下载：Alloy 模式下不接这条就是"点了没反应"（CEF 默认取消，见 cef_download_handler.h）
  bool OnBeforeDownload(CefRefPtr<CefBrowser> browser,
                        CefRefPtr<CefDownloadItem> download_item,
                        const CefString &suggested_name,
                        CefRefPtr<CefBeforeDownloadCallback> callback) override {
    XHCefBrowserView *view = view_;
    NSString *name = CefStringToNSString(suggested_name);
    NSString *url = CefStringToNSString(download_item->GetURL());
    // 回调在主线程，这里同步取路径（RunOnMain 在当前线程是直接调用）
    __block NSString *path = nil;
    RunOnMain(^{
      if (view.downloadPathRequested) path = view.downloadPathRequested(name, url);
    });
    if (path.length == 0) {
      // 不执行 callback → CEF 按取消处理（头文件明确写了这条语义）
      return true;
    }
    callback->Continue(path.UTF8String, false);  // false = 不弹 CEF 自带的另存为框
    return true;
  }

  void OnDownloadUpdated(CefRefPtr<CefBrowser> browser,
                         CefRefPtr<CefDownloadItem> download_item,
                         CefRefPtr<CefDownloadItemCallback> callback) override {
    NSInteger state;
    if (download_item->IsComplete()) {
      state = 1;
    } else if (download_item->IsCanceled()) {
      state = 2;
    } else if (download_item->IsInProgress()) {
      state = 0;
    } else {
      return;   // 中间态（待开始时）不算进度
    }
    XHCefBrowserView *view = view_;
    const int64_t downloadId = download_item->GetId();
    const int64_t received = download_item->GetReceivedBytes();
    const int64_t total = download_item->GetTotalBytes();
    NSString *fullPath = CefStringToNSString(download_item->GetFullPath());
    NSString *url = CefStringToNSString(download_item->GetURL());
    RunOnMain(^{
      if (state == 0) {
        [view cefDownloadRegister:downloadId callback:callback];
      } else {
        [view cefDownloadDrop:downloadId];
      }
      if (view.downloadUpdated) {
        view.downloadUpdated(downloadId, state, received, total, fullPath, url);
      }
    });
  }

  // 右键菜单：Alloy（子视图窗口）没有默认菜单实现，OnBeforeContextMenu 只改模型，显示归 RunContextMenu
  void OnBeforeContextMenu(CefRefPtr<CefBrowser> browser,
                           CefRefPtr<CefFrame> frame,
                           CefRefPtr<CefContextMenuParams> params,
                           CefRefPtr<CefMenuModel> model) override {
    NSString *selection =
        [CefStringToNSString(params->GetSelectionText())
            stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceAndNewlineCharacterSet]];
    if (selection.length > 0) {
      NSString *label = selection.length > 24 ? [selection substringToIndex:24] : selection;
      NSString *title = [NSString stringWithFormat:@"用百度搜索「%@」", label];
      model->AddSeparator();
      model->AddItem(MENU_ID_USER_FIRST + 1, title.UTF8String);
    }
    // DevTools：CEF 的默认模型在 Alloy 下不带这一项，自己加（cefclient 也是这么干的）
    model->AddSeparator();
    model->AddItem(MENU_ID_USER_FIRST + 2, "检查元素");
  }

  bool RunContextMenu(CefRefPtr<CefBrowser> browser,
                      CefRefPtr<CefFrame> frame,
                      CefRefPtr<CefContextMenuParams> params,
                      CefRefPtr<CefMenuModel> model,
                      CefRefPtr<CefRunContextMenuCallback> callback) override {
    XHCefBrowserView *view = view_;
    RunOnMain(^{
      [view cefShowMenu:model callback:callback];
    });
    return true;
  }

  bool OnContextMenuCommand(CefRefPtr<CefBrowser> browser,
                            CefRefPtr<CefFrame> frame,
                            CefRefPtr<CefContextMenuParams> params,
                            int command_id,
                            EventFlags event_flags) override {
    if (command_id == MENU_ID_USER_FIRST + 1) {
      XHCefBrowserView *view = view_;
      NSString *text = CefStringToNSString(params->GetSelectionText());
      RunOnMain(^{
        if (view.contextSearchRequested) view.contextSearchRequested(text);
      });
      return true;
    }
    if (command_id == MENU_ID_USER_FIRST + 2) {
      // 空 windowInfo = CEF 自己开一个独立原生窗（macOS 上 CefWindowInfo 只有 SetAsChild，
      // 没有 SetAsPopup，cefclient 也是传默认值）
      CefWindowInfo windowInfo;
      CefBrowserSettings settings;
      browser->GetHost()->ShowDevTools(windowInfo, nullptr, settings,
                                       CefPoint(params->GetXCoord(), params->GetYCoord()));
      NSLog(@"[cef] ShowDevTools at (%d,%d)", params->GetXCoord(), params->GetYCoord());
      return true;
    }
    return false;   // 其余交给 CEF 默认实现
  }

  // 页内查找：CEF 只在 finalUpdate 时给确定结果（中途的 count 是"当前累计"）
  void OnFindResult(CefRefPtr<CefBrowser> browser,
                    int identifier,
                    int count,
                    const CefRect &selectionRect,
                    int activeMatchOrdinal,
                    bool finalUpdate) override {
    if (!finalUpdate) return;
    XHCefBrowserView *view = view_;
    RunOnMain(^{
      if (view.findResultCount) view.findResultCount(count);
    });
  }

  // JS 对话框：不接的话 CEF 会自己弹平台原生框（阻塞式），观感和我们的 sheet 也不一致
  bool OnJSDialog(CefRefPtr<CefBrowser> browser,
                  const CefString &origin_url,
                  JSDialogType dialog_type,
                  const CefString &message_text,
                  const CefString &default_prompt_text,
                  CefRefPtr<CefJSDialogCallback> callback,
                  bool &suppress_message) override {
    XHCefBrowserView *view = view_;
    const NSInteger kind = static_cast<NSInteger>(dialog_type);
    NSString *message = CefStringToNSString(message_text);
    NSString *defaultText = CefStringToNSString(default_prompt_text);
    __block BOOL replied = NO;
    void (^reply)(BOOL, NSString *) = ^(BOOL accepted, NSString *text) {
      if (replied) return;   // callback 只能调一次，页面还在等它
      replied = YES;
      callback->Continue(accepted, CefString((text ?: @"").UTF8String));
    };
    RunOnMain(^{
      if (view.jsDialogRequested) {
        view.jsDialogRequested(kind, message, defaultText, reply);
      } else {
        reply(NO, nil);   // 没接线也别让页面卡住
      }
    });
    suppress_message = false;
    return true;
  }

  CefRefPtr<CefResourceRequestHandler> GetResourceRequestHandler(
      CefRefPtr<CefBrowser> browser,
      CefRefPtr<CefFrame> frame,
      CefRefPtr<CefRequest> request,
      bool is_navigation,
      bool is_download,
      const CefString &request_initiator,
      bool &disable_default_handling) override {
    return new XHCefResourceRequestHandler();
  }

  // 关窗/CloseBrowser 走完进程回收才通知外部放引用
  void OnAfterCreated(CefRefPtr<CefBrowser> browser) override {
    XHCefBrowserView *view = view_;
    if (!view) {
      // 视图已经没了（tab 开出来就被关 / 配额回收）：没人接管这个 browser，
      // 不在这里兜底就是一个永远回收不掉的 renderer 进程。
      dispatch_async(dispatch_get_main_queue(), ^{
        browser->GetHost()->CloseBrowser(true);
      });
      return;
    }
    RunOnMain(^{
      [view cefBrowserCreated:browser];
    });
  }

  // target=_blank / window.open：拒绝 CEF 自己的原生窗口（见 CefBridge.h 的注释）
  bool OnBeforePopup(CefRefPtr<CefBrowser> browser,
                     CefRefPtr<CefFrame> frame,
                     int popup_id,
                     const CefString &target_url,
                     const CefString &target_frame_name,
                     WindowOpenDisposition target_disposition,
                     bool user_gesture,
                     const CefPopupFeatures &popupFeatures,
                     CefWindowInfo &windowInfo,
                     CefRefPtr<CefClient> &client,
                     CefBrowserSettings &settings,
                     CefRefPtr<CefDictionaryValue> &extra_info,
                     bool *no_javascript_access) override {
    // 无 URL 的 window.open（先开窗再 document.write）没法用 tab 承接，直接拒绝并留痕
    if (target_url.empty()) {
      NSLog(@"[cef] popup without url (frame=%@) dropped", CefStringToNSString(target_frame_name));
      return true;
    }
    NSString *url = CefStringToNSString(target_url);
    XHCefBrowserView *view = view_;
    RunOnMain(^{
      if (view.popupRequested) view.popupRequested(url);
    });
    return true;
  }

  void OnBeforeClose(CefRefPtr<CefBrowser> browser) override {
    XHCefBrowserView *view = view_;
    RunOnMain(^{
      [view cefBrowserDidClose];
    });
  }

  void OnLoadEnd(CefRefPtr<CefBrowser> browser,
                 CefRefPtr<CefFrame> frame,
                 int httpStatusCode) override {
    if (!frame->IsMain()) return;
    XHCefBrowserView *view = view_;
    // 4xx/5xx 也算加载完（页面已经渲染出来了），失败由 OnLoadError 报
    RunOnMain(^{
      if (view.loadDidFinish) view.loadDidFinish(YES);
    });
  }

  void OnLoadingStateChange(CefRefPtr<CefBrowser> browser,
                            bool isLoading,
                            bool canGoBack,
                            bool canGoForward) override {
    XHCefBrowserView *view = view_;
    RunOnMain(^{
      [view cefLoadingStateChanged:isLoading canGoBack:canGoBack canGoForward:canGoForward];
    });
  }

  void OnLoadError(CefRefPtr<CefBrowser> browser,
                   CefRefPtr<CefFrame> frame,
                   ErrorCode errorCode,
                   const CefString &errorText,
                   const CefString &failedUrl) override {
    if (!frame->IsMain()) return;
    // ERR_ABORTED 是正常中断（重定向、用户点走），不当失败报
    if (errorCode == ERR_ABORTED) return;
    XHCefBrowserView *view = view_;
    RunOnMain(^{
      if (view.loadDidFinish) view.loadDidFinish(NO);
    });
  }

  void OnTitleChange(CefRefPtr<CefBrowser> browser,
                     const CefString &title) override {
    NSString *text = CefStringToNSString(title);
    XHCefBrowserView *view = view_;
    RunOnMain(^{
      if (view.titleDidChange) view.titleDidChange(text);
    });
  }

  void OnAddressChange(CefRefPtr<CefBrowser> browser,
                       CefRefPtr<CefFrame> frame,
                       const CefString &url) override {
    if (!frame->IsMain()) return;
    NSString *text = CefStringToNSString(url);
    XHCefBrowserView *view = view_;
    RunOnMain(^{
      if (view.urlDidChange) view.urlDidChange(text);
    });
  }

  // 页面 → 原生的回传通道（回归探针用）：CEF 的 ExecuteJavaScript 没有返回值，页面里只能靠
  // console.log 把结果交出来。前缀 "XHPROBE:" 的才截走，其余照常进控制台。
  bool OnConsoleMessage(CefRefPtr<CefBrowser> browser,
                        cef_log_severity_t level,
                        const CefString &message,
                        const CefString &source,
                        int line) override {
    const std::string text = message.ToString();
    const std::string prefix = "XHPROBE:";
    if (text.rfind(prefix, 0) != 0) return false;
    NSString *payload = [NSString stringWithUTF8String:text.substr(prefix.size()).c_str()] ?: @"";
    XHCefBrowserView *view = view_;
    RunOnMain(^{
      if (view.probeReported) view.probeReported(payload);
    });
    return true;  // 已消费，别再往控制台刷一坨 JSON
  }

 private:
  __weak XHCefBrowserView *view_;

  IMPLEMENT_REFCOUNTING(XHCefClient);
};

}  // namespace

// MARK: - XHCefRuntime

@implementation XHCefRuntime

static bool EnsureLibraryLoaded() {
  if (g_loader) return true;
  auto loader = std::make_unique<CefScopedLibraryLoader>();
  if (!loader->LoadInMain()) return false;
  g_loader = std::move(loader);
  return true;
}

+ (BOOL)executeSubprocessIfNeeded {
  // Helper 进程走 CEF/helper_main.mm 里的 LoadInHelper，不经过这里
  if (!EnsureLibraryLoaded()) return NO;
  CefMainArgs mainArgs = MakeMainArgs();
  // 子进程会在这里跑完整个生命周期；主进程立刻返回 -1
  int code = CefExecuteProcess(mainArgs, nullptr, nullptr);
  if (code >= 0) {
    exit(code);
  }
  return NO;
}

+ (BOOL)initializeIn:(NSString *)dataRoot {
  if (g_cef_ready) return YES;
  if (!EnsureLibraryLoaded()) {
    LogInitFailure(5, @"Chromium Embedded Framework 加载失败");
    return NO;
  }

  NSBundle *bundle = NSBundle.mainBundle;
  NSString *frameworks = bundle.privateFrameworksPath;
  NSString *framework = [frameworks stringByAppendingPathComponent:
                                          @"Chromium Embedded Framework.framework"];
  NSString *productName = bundle.infoDictionary[@"CFBundleName"] ?: @"xhanHelper";
  NSString *helper = [frameworks stringByAppendingPathComponent:
                                    [NSString stringWithFormat:@"%@ Helper.app/Contents/MacOS/%@ Helper",
                                                               productName, productName]];

  NSFileManager *fm = NSFileManager.defaultManager;
  if (![fm fileExistsAtPath:framework]) {
    LogInitFailure(1, [NSString stringWithFormat:@"缺 framework：%@（先跑 scripts/fetch-cef.sh）", framework]);
    return NO;
  }
  if (![fm fileExistsAtPath:helper]) {
    LogInitFailure(2, [NSString stringWithFormat:@"缺 Helper：%@", helper]);
    return NO;
  }

  InstallSendEventSwizzleIfNeeded();

  NSString *cachePath = [dataRoot stringByAppendingPathComponent:@"CEF"];
  NSError *dirError = nil;
  if (![fm createDirectoryAtPath:cachePath
     withIntermediateDirectories:YES
                      attributes:nil
                           error:&dirError]) {
    LogInitFailure(3, [NSString stringWithFormat:@"建缓存目录失败：%@（%@）",
                                                    cachePath, dirError.localizedDescription]);
    return NO;
  }

  CefMainArgs mainArgs = MakeMainArgs();
  CefSettings settings;
  settings.no_sandbox = true;              // 本机未开沙盒，用 CEF sandbox 还要 entitlement
  settings.external_message_pump = true;   // 消息泵由 OnScheduleMessagePumpWork 指导调度，见 schedulePumpWorkAfterMs
  settings.windowless_rendering_enabled = false;
  settings.persist_session_cookies = true;
  settings.background_color = 0xFFFFFFFF;  // 首帧前白底，避免黑闪
  settings.log_severity = LOGSEVERITY_WARNING;

  CefString(&settings.framework_dir_path) = framework.UTF8String;
  CefString(&settings.browser_subprocess_path) = helper.UTF8String;
  CefString(&settings.main_bundle_path) = bundle.bundlePath.UTF8String;
  CefString(&settings.resources_dir_path) =
      [framework stringByAppendingPathComponent:@"Resources"].UTF8String;
  CefString(&settings.locales_dir_path) =
      [framework stringByAppendingPathComponent:@"Resources"].UTF8String;
  CefString(&settings.root_cache_path) = cachePath.UTF8String;
  // 不设 cache_path = incognito（cookie/localStorage 只在内存），登录态全靠它
  CefString(&settings.cache_path) = cachePath.UTF8String;

  // 第 1 层伪装：同一条串同时管 User-Agent 请求头和 navigator.userAgent
  CefString(&settings.user_agent) = xhdisguise::UserAgent();

  if (!CefInitialize(mainArgs, settings, new XHCefApp(), nullptr)) {
    LogInitFailure(4, @"CefInitialize 失败（CEF 自己的日志见 Console）");
    return NO;
  }
  g_cef_ready = true;
  return YES;
}

// shortcut: 退出时不做 CefShutdown（进程退出即回收，省掉 OnBeforeClose → Shutdown 的时序坑），
// B2 接正式引擎、需要"关掉浏览器子系统"时再补这条路径。

+ (BOOL)isReady {
  return g_cef_ready;
}

+ (void)schedulePumpWorkAfterMs:(int64_t)delayMs {
  if (!g_cef_ready) return;

  NSDate *target = [NSDate dateWithTimeIntervalSinceNow:delayMs <= 0 ? 0 : delayMs / 1000.0];
  // 已经有更早的排程就别动（合并请求，只保留最早的一次）
  if (g_pump && [g_pump.fireDate compare:target] != NSOrderedDescending) return;
  [g_pump invalidate];

  g_pump = [NSTimer timerWithTimeInterval:MAX(0, [target timeIntervalSinceNow])
                                  repeats:NO
                                    block:^(__unused NSTimer *timer) {
                                      g_pump = nil;
                                      if (!g_cef_ready) return;
                                      // 单次 CefDoMessageLoopWork 只处理一小撮任务，一次不够把导航推下去：
                                      // 实测只调 1 次时 renderer 活着、页面永不 commit（History 空、network
                                      // service 不起），调 100 次稳定 5/5 通过。CEF 没有"是否还有活"的查询口，
                                      // 只能给个上限。
                                      // shortcut: 100 是实测够用的经验值，B2 做性能时再按真实负载收窄
                                      for (int i = 0; i < 100; i++) {
                                        CefDoMessageLoopWork();
                                      }
                                    }];
  [NSRunLoop.mainRunLoop addTimer:g_pump forMode:NSRunLoopCommonModes];
}

@end

// MARK: - XHCefBrowserView

/// 后台标签（agent 工作区）不会进任何窗口，但 CEF 的 `CreateBrowser` 要父视图落在一个窗口里才有渲染宿主。
/// 给它们一个进程内共享的隐藏窗口当"停车位"：视图真正挂进侧栏时只是换父窗口，CEF 的子视图跟着走。
static NSWindow *XHCefParkingWindow(void) {
  static NSWindow *window = nil;
  static dispatch_once_t once;
  dispatch_once(&once, ^{
    window = [[NSWindow alloc] initWithContentRect:NSMakeRect(-20000, -20000, 1280, 800)
                                         styleMask:NSWindowStyleMaskBorderless
                                           backing:NSBackingStoreBuffered
                                             defer:NO];
    window.releasedWhenClosed = NO;
    window.alphaValue = 0;   // 永不 orderFront，只当父窗口用
  });
  return window;
}

@implementation XHCefBrowserView {
  CefRefPtr<CefBrowser> _browser;
  CefRefPtr<XHCefClient> _client;
  NSString *_startURL;
  NSString *_pendingURL;      // browser 还没 OnAfterCreated 时来的导航，等 created 再补
  BOOL _browserRequested;
  BOOL _closeRequested;       // 创建窗口期内就被要求关闭（tab 秒关），created 后立刻回收
  BOOL _isLoading;
  BOOL _canGoBack;
  BOOL _canGoForward;
  /// 进行中下载的取消句柄（CefDownloadItemCallback 只能在 OnDownloadUpdated 里用，必须留一份）
  std::map<int64_t, CefRefPtr<CefDownloadItemCallback>> _downloadCallbacks;
  /// 右键菜单：显示期间持有 callback（菜单是同步模态的，关闭后清掉）
  CefRefPtr<CefRunContextMenuCallback> _menuCallback;
  BOOL _menuPicked;
  NSMenu *_menu;
  /// JS 求值：request id → 待回包（回包/超时/关页三条路径都要把它取走，只走一次）
  int32_t _evalRequestCounter;
  NSMutableDictionary<NSNumber *, id> *_evalCompletions;
}

- (instancetype)initWithFrame:(NSRect)frame URLString:(NSString *)urlString {
  self = [super initWithFrame:frame];
  if (self) {
    _startURL = [urlString copy] ?: @"about:blank";
    _evalCompletions = [NSMutableDictionary dictionary];
  }
  return self;
}

- (BOOL)isFlipped {
  return YES;
}

- (void)viewDidMoveToWindow {
  [super viewDidMoveToWindow];
  if (self.window) {
    [self createBrowserIfNeeded];
  }
}

- (void)createBrowserIfNeeded {
  // 未就绪不置标记，等下次进窗口再试；已要求关闭就不再造（省一个白造的 renderer）
  if (_browserRequested || _closeRequested || !XHCefRuntime.isReady) return;
  // 后台标签没有窗口：先停进隐藏窗口，否则 CEF 建不出渲染宿主（页面永远不 commit）
  if (!self.window) {
    NSView *parking = XHCefParkingWindow().contentView;
    if (parking) {
      self.frame = parking.bounds;
      [parking addSubview:self];
    }
  }
  _browserRequested = YES;

  _client = new XHCefClient(self);

  CefWindowInfo windowInfo;
  CefWindowHandle parent = (CefWindowHandle)(__bridge void *)self;
  windowInfo.SetAsChild(parent, CefRect(0, 0, (int)NSWidth(self.bounds), (int)NSHeight(self.bounds)));
  CefBrowserSettings browserSettings;
  browserSettings.background_color = 0xFFFFFFFF;

  if (!CefBrowserHost::CreateBrowser(windowInfo, _client, _startURL.UTF8String,
                                     browserSettings, nullptr, nullptr)) {
    NSLog(@"[cef] CreateBrowser 失败（父视图或浏览器设置有问题），允许下次重试");
    _browserRequested = NO;
    _client = nullptr;
  }
}

- (void)layout {
  [super layout];
  [self syncBrowserFrame];
}

- (void)setFrameSize:(NSSize)newSize {
  [super setFrameSize:newSize];
  [self syncBrowserFrame];
}

- (void)syncBrowserFrame {
  NSRect bounds = self.bounds;
  // CEF 会把自己的渲染子视图挂进来，它不跟随父视图尺寸，得我们同步。
  // 窗口模式（非 OSR）下 WasResized/WasHidden 是空操作，别调（会让人以为遮挡/尺寸问题已处理）。
  for (NSView *subview in self.subviews) {
    subview.frame = bounds;
  }
}

- (void)loadURLString:(NSString *)urlString {
  if (_browser) {
    _isLoading = YES;
    if (self.stateDidChange) self.stateDidChange();
    _browser->GetMainFrame()->LoadURL(urlString.UTF8String);
  } else {
    _startURL = [urlString copy] ?: @"about:blank";
    _pendingURL = [_startURL copy];
    // 后台标签（agent 工作区）从来没进过窗口：就地建，CEF 不要求窗口可见，只要求父视图有窗口
    [self createBrowserIfNeeded];
  }
}

- (void)loadHTMLString:(NSString *)html {
  NSString *base64 = [[html dataUsingEncoding:NSUTF8StringEncoding] base64EncodedStringWithOptions:0];
  [self loadURLString:[@"data:text/html;charset=utf-8;base64," stringByAppendingString:base64]];
}

- (void)evaluateJavaScript:(NSString *)script {
  if (!_browser) return;
  CefRefPtr<CefFrame> frame = _browser->GetMainFrame();
  if (frame) frame->ExecuteJavaScript(script.UTF8String, frame->GetURL(), 0);
}

// JS 求值（要返回值）：CEF 没有"执行并拿结果"的 API，交给渲染进程代跑再回包。
- (void)evaluateJavaScript:(NSString *)script
                completion:(void (^)(id _Nullable value, NSString * _Nullable error))completion {
  CefRefPtr<CefFrame> frame = _browser ? _browser->GetMainFrame() : nullptr;
  if (!frame) {
    completion(nil, @"页面还没准备好（浏览器未创建）");
    return;
  }
  const int32_t requestId = ++_evalRequestCounter;
  _evalCompletions[@(requestId)] = [completion copy];

  CefRefPtr<CefProcessMessage> message = CefProcessMessage::Create("xh_eval");
  CefRefPtr<CefListValue> args = message->GetArgumentList();
  args->SetInt(0, requestId);
  args->SetString(1, script.UTF8String);
  frame->SendProcessMessage(PID_RENDERER, message);

  // 超时兜底：渲染进程崩了 / 页面在导航时回包可能永远不来，agent 工具不能挂死
  __weak XHCefBrowserView *weakSelf = self;
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(10 * NSEC_PER_SEC)),
                 dispatch_get_main_queue(), ^{
                   XHCefBrowserView *strongSelf = weakSelf;
                   if (!strongSelf) return;
                   id pending = strongSelf->_evalCompletions[@(requestId)];
                   if (!pending) return;
                   [strongSelf->_evalCompletions removeObjectForKey:@(requestId)];
                   ((void (^)(id, NSString *))pending)(nil, @"JS 执行超时（渲染进程无响应）");
                 });
}

- (void)cefEvalResult:(int32_t)requestId json:(NSString *)json error:(NSString *)error {
  id pending = _evalCompletions[@(requestId)];
  if (!pending) return;   // 已超时/已关页，回包来晚了
  [_evalCompletions removeObjectForKey:@(requestId)];
  void (^completion)(id, NSString *) = pending;
  if (error.length > 0) {
    completion(nil, error);
    return;
  }
  // 渲染进程回的是信封字符串：{"ok":true,"json":"…"} / {"ok":false,"error":"…"}
  NSDictionary *envelope = nil;
  if (json.length > 0) {
    envelope = [NSJSONSerialization JSONObjectWithData:[json dataUsingEncoding:NSUTF8StringEncoding]
                                               options:0
                                                 error:nil];
  }
  if (![envelope isKindOfClass:NSDictionary.class]) {
    completion(nil, @"JS 回包不是合法信封");
    return;
  }
  if (![(envelope[@"ok"] ?: @NO) boolValue]) {
    NSString *message = envelope[@"error"];
    completion(nil, message.length > 0 ? message : @"JS 抛异常");
    return;
  }
  NSString *valueJSON = envelope[@"json"];
  if (![valueJSON isKindOfClass:NSString.class] || valueJSON.length == 0) {
    completion(nil, nil);   // 空结果 = undefined/null
    return;
  }
  NSError *parseError = nil;
  id value = [NSJSONSerialization JSONObjectWithData:[valueJSON dataUsingEncoding:NSUTF8StringEncoding]
                                             options:NSJSONReadingFragmentsAllowed
                                               error:&parseError];
  if (!value && parseError) {
    completion(nil, [NSString stringWithFormat:@"JS 结果不是合法 JSON：%@", parseError.localizedDescription]);
    return;
  }
  completion(value, nil);
}

// zoom level ↔ 缩放比：Chromium 默认一级约 1.2 倍
- (double)zoomScale {
  if (!_browser) return 1.0;
  return pow(1.2, _browser->GetHost()->GetZoomLevel());
}

- (void)setZoomScale:(double)scale {
  if (!_browser || scale <= 0) return;
  _browser->GetHost()->SetZoomLevel(log(scale) / log(1.2));
}

- (void)findString:(NSString *)text forward:(BOOL)forward matchCase:(BOOL)matchCase {
  if (!_browser) return;
  if (text.length == 0) {
    _browser->GetHost()->StopFinding(YES);
    return;
  }
  _browser->GetHost()->Find(text.UTF8String, forward, matchCase, false);
}

- (void)stopFinding:(BOOL)clearSelection {
  if (_browser) _browser->GetHost()->StopFinding(clearSelection);
}

- (void)reload {
  if (_browser) _browser->Reload();
}

- (void)stopLoading {
  if (_browser) _browser->StopLoad();
}

- (void)goBack {
  if (_browser && _browser->CanGoBack()) _browser->GoBack();
}

- (void)goForward {
  if (_browser && _browser->CanGoForward()) _browser->GoForward();
}

- (BOOL)isLoading {
  return _isLoading;
}

- (BOOL)canGoBack {
  return _canGoBack;
}

- (BOOL)canGoForward {
  return _canGoForward;
}

- (void)cefLoadingStateChanged:(BOOL)isLoading
                     canGoBack:(BOOL)canGoBack
                  canGoForward:(BOOL)canGoForward {
  _isLoading = isLoading;
  _canGoBack = canGoBack;
  _canGoForward = canGoForward;
  if (self.stateDidChange) self.stateDidChange();
}

- (void)closeBrowser {
  _closeRequested = YES;
  if (!_browser) return;   // 还在创建中：OnAfterCreated 里看到标记会立刻关（见 cefBrowserCreated）
  // force_close=true：否则 renderer 进程会赖着（240MB/页的先例）
  _browser->GetHost()->CloseBrowser(true);
}

- (void)cefBrowserCreated:(CefRefPtr<CefBrowser>)browser {
  _browser = browser;
  if (_closeRequested) {
    // tab 在 CreateBrowser 的异步窗口期就被关了：此刻才拿到 browser，就地收掉。
    // 异步一拍再关，避免在 OnAfterCreated 回调栈里同步 CloseBrowser（CFE 没禁止，但没必要赌重入）。
    dispatch_async(dispatch_get_main_queue(), ^{
      browser->GetHost()->CloseBrowser(true);
    });
    return;
  }
  [self syncBrowserFrame];
  if (_pendingURL) {
    browser->GetMainFrame()->LoadURL(_pendingURL.UTF8String);
    _pendingURL = nil;
  }
}

- (void)cefBrowserDidClose {
  // 只放 browser；client 留到本视图 dealloc。
  // 在 OnBeforeClose 栈里把 client 引用清零，CEF 那份引用一放就是"成员函数执行中 delete this"。
  _browser = nullptr;
  // 关页时把还等着的 JS 求值一律失败掉，别让 agent 工具等到超时
  NSArray<id> *pending = _evalCompletions.allValues;
  [_evalCompletions removeAllObjects];
  for (id block in pending) {
    ((void (^)(id, NSString *))block)(nil, @"页面已关闭");
  }
}

- (void)dealloc {
  [self closeBrowser];
}

// MARK: 下载

- (void)cefDownloadRegister:(int64_t)downloadId callback:(CefRefPtr<CefDownloadItemCallback>)callback {
  _downloadCallbacks[downloadId] = callback;
}

- (void)cefDownloadDrop:(int64_t)downloadId {
  _downloadCallbacks.erase(downloadId);
}

- (void)cancelDownload:(int64_t)downloadId {
  auto it = _downloadCallbacks.find(downloadId);
  if (it == _downloadCallbacks.end()) return;
  if (it->second) it->second->Cancel();
  _downloadCallbacks.erase(it);
}

// MARK: 右键菜单

/// CEF 的菜单标签带 Windows/Linux 风格的助记符标记（`&Back` → Alt+B），macOS 原生菜单不认。
/// 按标准规则反转义：`&&` → 字面 `&`，单个 `&X` → `X`。
static NSString *XHMenuLabel(NSString *raw) {
  if ([raw rangeOfString:@"&"].location == NSNotFound) return raw;
  NSMutableString *out = [NSMutableString stringWithCapacity:raw.length];
  const NSUInteger length = raw.length;
  for (NSUInteger i = 0; i < length; i++) {
    unichar c = [raw characterAtIndex:i];
    if (c == '&' && i + 1 < length) {
      unichar next = [raw characterAtIndex:i + 1];
      if (next == '&') {
        [out appendString:@"&"];
        i++;
      }
      continue;   // 单个 & 只是助记符标记，丢掉
    }
    [out appendFormat:@"%C", c];
  }
  return out;
}

/// 把 CefMenuModel 递归摊成 NSMenu 项；tag 存 command_id，选中后原样交回 CEF。
static void XHAppendMenuItems(NSMenu *menu, CefRefPtr<CefMenuModel> model, id target) {
  const size_t count = model->GetCount();
  for (size_t i = 0; i < count; i++) {
    switch (model->GetTypeAt(i)) {
      case MENUITEMTYPE_NONE:
        break;
      case MENUITEMTYPE_SEPARATOR:
        [menu addItem:[NSMenuItem separatorItem]];
        break;
      case MENUITEMTYPE_SUBMENU: {
        NSString *title = XHMenuLabel(CefStringToNSString(model->GetLabelAt(i)));
        if (title.length == 0) break;
        NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:title action:nil keyEquivalent:@""];
        NSMenu *sub = [[NSMenu alloc] initWithTitle:title];
        sub.autoenablesItems = NO;
        XHAppendMenuItems(sub, model->GetSubMenuAt(i), target);
        item.submenu = sub;
        [menu addItem:item];
        break;
      }
      default: {
        NSString *title = XHMenuLabel(CefStringToNSString(model->GetLabelAt(i)));
        if (title.length == 0) break;
        NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:title
                                                     action:@selector(xhMenuItemPicked:)
                                              keyEquivalent:@""];
        item.target = target;
        item.tag = model->GetCommandIdAt(i);
        item.enabled = model->IsEnabledAt(i);
        if (model->GetTypeAt(i) != MENUITEMTYPE_COMMAND) {
          item.state = model->IsCheckedAt(i) ? NSControlStateValueOn : NSControlStateValueOff;
        }
        [menu addItem:item];
        break;
      }
    }
  }
}

- (void)cefShowMenu:(CefRefPtr<CefMenuModel>)model
           callback:(CefRefPtr<CefRunContextMenuCallback>)callback {
  _menuCallback = callback;
  _menuPicked = NO;
  _menu = [[NSMenu alloc] initWithTitle:@"context"];
  _menu.autoenablesItems = NO;   // 我们自己按模型设 enabled，交给 AppKit 自动判会全灰
  _menu.delegate = self;
  XHAppendMenuItems(_menu, model, self);
  if (_menu.numberOfItems == 0) {
    // 空模型：没有可显示的东西，直接 Cancel，别让渲染进程以为菜单还开着
    _menu = nil;
    if (_menuCallback) _menuCallback->Cancel();
    _menuCallback = nullptr;
    return;
  }
  if (NSEvent *event = NSApp.currentEvent) {
    [NSMenu popUpContextMenu:_menu withEvent:event forView:self];
  } else {
    NSPoint location = [self convertPoint:self.window.mouseLocationOutsideOfEventStream fromView:nil];
    [_menu popUpMenuPositioningItem:nil atLocation:location inView:self];
  }
}

- (void)xhMenuItemPicked:(NSMenuItem *)sender {
  _menuPicked = YES;
  if (_menuCallback) _menuCallback->Continue((int)sender.tag, EVENTFLAG_NONE);
  _menuCallback = nullptr;
}

- (void)menuDidClose:(NSMenu *)menu {
  // 点空白/按 Esc 关掉：告诉 CEF 菜单没选中，别让渲染进程以为还开着
  if (!_menuPicked && _menuCallback) _menuCallback->Cancel();
  _menuCallback = nullptr;
  _menu = nil;
}

@end
