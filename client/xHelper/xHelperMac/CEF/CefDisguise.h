// CEF 四层伪装（docs/design/mac-apps/browser-chromium-migration.md 第三章）。
//
// 第 1 层（UA）和第 2 层（sec-ch-ua 请求头）在浏览器进程用；第 3/4 层（navigator.userAgentData、
// window.chrome）必须在渲染进程的 OnContextCreated 里注，所以本头被 `xHelperMac/CEF/CefBridge.mm`
// 和 `CEF/helper_main.mm` **两个二进制**共用 —— 只在 .mm 里 include（用到 Foundation / ObjC++）。
//
// 版本号一律取自 cef_version.h：升级内核后 UA 与 brands 自动跟上，不会出现「UA 写死 156、内核已 160」
// 这种最隐蔽的错（10.1 的 A 档检查因此不需要再单独 grep UA 常量）。
#ifndef XH_CEF_DISGUISE_H_
#define XH_CEF_DISGUISE_H_

#import <Foundation/Foundation.h>

#include <string>

#include "include/cef_render_process_handler.h"
#include "include/cef_v8.h"
#include "include/cef_version.h"

namespace xhdisguise {

/// 把 UTF-8 源码包成 JS 字符串字面量（agent 传的脚本可以含引号/换行/反斜杠，不能直接拼）。
inline std::string JsStringLiteral(const std::string &utf8) {
  std::string out;
  out.reserve(utf8.size() + 2);
  out.push_back('"');
  for (unsigned char c : utf8) {
    switch (c) {
      case '"': out += "\\\""; break;
      case '\\': out += "\\\\"; break;
      case '\n': out += "\\n"; break;
      case '\r': out += "\\r"; break;
      case '\t': out += "\\t"; break;
      default:
        if (c < 0x20) {
          char buf[8];
          snprintf(buf, sizeof(buf), "\\u%04x", c);
          out += buf;
        } else {
          out.push_back(static_cast<char>(c));  // UTF-8 多字节直接透传
        }
    }
  }
  out.push_back('"');
  return out;
}

/// 包一层 eval：既能跑表达式也能跑语句（取完成值），并把结果序列化成 JSON。
/// 返回值形态固定为 `{ok:true, json:"…"}` 或 `{ok:false, error:"…"}`。
inline std::string EvalWrapper(const std::string &script) {
  return "(function(){try{var __r=(0,eval)(" + JsStringLiteral(script) +
         ");return {ok:true,json:JSON.stringify(__r===undefined?null:__r)};}"
         "catch(e){return {ok:false,error:String((e&&e.message)||e)};}})()";
}

inline std::string ChromeVersion() {
  return std::to_string(CHROME_VERSION_MAJOR) + "." + std::to_string(CHROME_VERSION_MINOR) + "." +
         std::to_string(CHROME_VERSION_BUILD) + "." + std::to_string(CHROME_VERSION_PATCH);
}

inline std::string Major() { return std::to_string(CHROME_VERSION_MAJOR); }

// GREASE 陪跑品牌：真 Chrome 每几代换一次这个"假品牌"，就是为了抓硬编码品牌表的机器人。
// 156 era（Chrome 14x 起）是 Not:A-Brand/99，更早是 Not_A Brand/24。
// shortcut: 这两行是跟着内核代次手写的；升级内核后如果抓包发现形态变了，同步改这里。
inline constexpr char kGreaseBrand[] = "Not:A-Brand";
inline constexpr char kGreaseVersion[] = "99";

// macOS 的 UA 里 OS 版本永远是冻结的 10_15_7（Apple 的 UA 精简规则），真实系统版本只出现在
// UA-CH 的 high-entropy 里 —— 别"修正"成 Mac OS X 26。
inline std::string UserAgent() {
  return "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) "
         "Chrome/" +
         ChromeVersion() + " Safari/537.36";
}

inline std::string SecChUa() {
  return "\"" + std::string(kGreaseBrand) + "\";v=\"" + kGreaseVersion + "\", \"Chromium\";v=\"" +
         Major() + "\", \"Google Chrome\";v=\"" + Major() + "\"";
}

// Chrome 报的 platformVersion 是 major.minor.0（末位恒 0）
inline std::string PlatformVersion() {
  NSOperatingSystemVersion v = NSProcessInfo.processInfo.operatingSystemVersion;
  return std::to_string(v.majorVersion) + "." + std::to_string(v.minorVersion) + ".0";
}

// 第 3/4 层：document-start 注入的 JS。
//
// 只做"名义层"（内核本来就是 Chromium），不碰 plugins/WebGL 这类深层指纹 —— 那是 WK 时代的路子，
// 在真 Chromium 上只会制造更多不一致。
inline std::string JavaScript() {
  return R"JS((function () {
  // 每层单独兜：任何一层炸了都要在 Console 里喊一声（注入失败不许静默），且不影响其它层。
  function fail(layer, e) {
    try { console.error("[cef-disguise] " + layer + " 注入失败: " + ((e && e.stack) || e)); } catch (_) {}
  }
  var major = ")JS" + Major() + R"JS(", full = ")JS" + ChromeVersion() + R"JS(";
  var grease = ")JS" + kGreaseBrand + R"JS(", greaseVer = ")JS" + kGreaseVersion + R"JS(";

  try {
    var brands = [
      { brand: grease, version: greaseVer },
      { brand: "Chromium", version: major },
      { brand: "Google Chrome", version: major }
    ];
    var extra = {
      architecture: "arm", bitness: "64", model: "",
      platformVersion: ")JS" + PlatformVersion() + R"JS(", uaFullVersion: full,
      fullVersionList: [
        { brand: grease, version: greaseVer + ".0.0.0" },
        { brand: "Chromium", version: full },
        { brand: "Google Chrome", version: full }
      ],
      wow64: false
    };
    var uad = {
      brands: brands, mobile: false, platform: "macOS",
      getHighEntropyValues: function (hints) {
        var out = {};
        (hints || []).forEach(function (h) {
          if (h === "brands") out.brands = brands;
          else if (h === "mobile") out.mobile = false;
          else if (h === "platform") out.platform = "macOS";
          else if (Object.prototype.hasOwnProperty.call(extra, h)) out[h] = extra[h];
        });
        return Promise.resolve(out);
      },
      toJSON: function () { return { brands: brands, mobile: false, platform: "macOS" }; }
    };
    Object.defineProperty(uad, Symbol.toStringTag, { value: "NavigatorUAData" });
    Object.defineProperty(Navigator.prototype, "userAgentData", {
      get: function () { return uad; }, configurable: true, enumerable: true
    });
  } catch (e) { fail("navigator.userAgentData", e); }

  try {
    // CEF 会在 chrome 上塞 setSyncEncryptionKeys / setClientEncryptionKeys /
    // addTrustedSyncEncryptionRecoveryMethod 三个真 Chrome 没有的函数，带着它们 UA-CH 再对也被拦。
    var timing = (window.performance && performance.timing) || {};
    var chromeObj = {
      app: {
        isInstalled: false, runningState: "cannot_run",
        InstallState: { DISABLED: "disabled", INSTALLED: "installed", NOT_INSTALLED: "not_installed" },
        RunningState: { CANNOT_RUN: "cannot_run", READY_TO_RUN: "ready_to_run", RUNNING: "running" },
        getDetails: function () { return null; },
        getIsInstalled: function () { return false; }
      },
      csi: function () {
        return { onloadT: timing.domContentLoadedEventEnd || 0, startE: timing.navigationStart || 0,
                 pageT: performance.now(), tran: 15 };
      },
      loadTimes: function () {
        var nav = timing.navigationStart || 0;
        return { requestTime: nav / 1000, startLoadTime: nav / 1000,
                 commitLoadTime: (timing.responseStart || 0) / 1000,
                 finishDocumentLoadTime: (timing.domContentLoadedEventEnd || 0) / 1000,
                 finishLoadTime: (timing.loadEventEnd || 0) / 1000,
                 firstPaintTime: (timing.responseStart || 0) / 1000, firstPaintAfterLoadTime: 0,
                 navigationType: "Other", wasFetchedViaSpdy: true, wasNpnNegotiated: true,
                 npnNegotiatedProtocol: "h2", wasAlternateProtocolAvailable: false,
                 connectionInfo: "h2" };
      },
      runtime: {}
    };
    try {
      Object.defineProperty(window, "chrome",
                            { value: chromeObj, writable: true, configurable: true, enumerable: true });
    } catch (e) {
      // CEF 若把它定义成不可配置，至少把三个 CEF 专属函数抹掉
      var c = window.chrome || {};
      ["setSyncEncryptionKeys", "setClientEncryptionKeys",
       "addTrustedSyncEncryptionRecoveryMethod"].forEach(function (k) {
        try { delete c[k]; } catch (_) {}
      });
      window.chrome = c;
      fail("window.chrome 整体替换（已退化为删三个函数）", e);
    }
  } catch (e) { fail("window.chrome", e); }
})();)JS";
}

/// 渲染进程的注入钩子。Helper 进程在 `CefExecuteProcess` 里拿到本类（见 CEF/helper_main.mm），
/// 每个 frame（含 iframe —— 谷歌登录判定跑在 iframe 里）在上下文创建时注一次。
class XHCefRenderProcessHandler : public CefRenderProcessHandler {
 public:
  /// 浏览器进程发来的 `xh_eval`：代跑一段 JS，把结果 JSON 回给 `xh_eval_result`。
  /// CEF 的 `CefFrame::ExecuteJavaScript` 没有返回值，agent 工具（快照/点击/读正文）全靠这条通道。
  bool OnProcessMessageReceived(CefRefPtr<CefBrowser> browser,
                                CefRefPtr<CefFrame> frame,
                                CefProcessId source_process,
                                CefRefPtr<CefProcessMessage> message) override {
    if (!message || message->GetName() != "xh_eval" || !frame) return false;
    CefRefPtr<CefListValue> args = message->GetArgumentList();
    if (!args || args->GetSize() < 2) return false;
    const int requestId = args->GetInt(0);
    const std::string script = args->GetString(1).ToString();

    std::string json;
    std::string error;
    CefRefPtr<CefV8Context> context = frame->GetV8Context();
    if (!context) {
      error = "渲染进程上下文不可用（页面正在导航或已崩溃）";
    } else if (!context->Enter()) {
      error = "进入 V8 上下文失败";
    } else {
      CefRefPtr<CefV8Value> result;
      CefRefPtr<CefV8Exception> exception;
      const bool ok = context->Eval(EvalWrapper(script), frame->GetURL(), 0, result, exception);
      context->Exit();
      if (!ok) {
        error = exception ? exception->GetMessage().ToString() : "JS 执行失败（无异常信息）";
      } else if (!result || !result->IsObject()) {
        error = "JS 结果形态异常（不是包装对象）";
      } else {
        CefRefPtr<CefV8Value> okValue = result->GetValue("ok");
        if (okValue && okValue->IsBool() && okValue->GetBoolValue()) {
          CefRefPtr<CefV8Value> jsonValue = result->GetValue("json");
          json = (jsonValue && jsonValue->IsString()) ? jsonValue->GetStringValue().ToString() : "null";
        } else {
          CefRefPtr<CefV8Value> errValue = result->GetValue("error");
          error = (errValue && errValue->IsString()) ? errValue->GetStringValue().ToString() : "JS 抛异常";
        }
      }
    }

    CefRefPtr<CefProcessMessage> reply = CefProcessMessage::Create("xh_eval_result");
    CefRefPtr<CefListValue> replyArgs = reply->GetArgumentList();
    replyArgs->SetInt(0, requestId);
    replyArgs->SetString(1, json);
    replyArgs->SetString(2, error);
    frame->SendProcessMessage(PID_BROWSER, reply);
    return true;
  }

  void OnContextCreated(CefRefPtr<CefBrowser> browser,
                        CefRefPtr<CefFrame> frame,
                        CefRefPtr<CefV8Context> context) override {
    // 同步 Eval，而不是 frame->ExecuteJavaScript：后者是投任务，"谁先跑"是赌博，赌输了页面脚本
    // 看到的就是没清洗过的 navigator/window.chrome，等于没做。此刻上下文还没执行任何页面代码。
    if (!context) return;
    if (!context->Enter()) {
      NSLog(@"[cef] 伪装注入：进入 V8 上下文失败（fatal，本 frame 未伪装）");
      return;
    }
    CefRefPtr<CefV8Value> result;
    CefRefPtr<CefV8Exception> error;
    const bool ok = context->Eval(JavaScript(), frame->GetURL(), 0, result, error);
    context->Exit();
    if (!ok) {
      NSString *reason = error ? CefStringToNSStringNoThrow(error->GetMessage()) : @"unknown";
      NSLog(@"[cef] 伪装注入失败（语法/编译错）：%@", reason);
    }
  }

 private:
  static NSString *CefStringToNSStringNoThrow(const CefString &value) {
    return [NSString stringWithUTF8String:value.ToString().c_str()] ?: @"";
  }

  IMPLEMENT_REFCOUNTING(XHCefRenderProcessHandler);
};

}  // namespace xhdisguise

#endif  // XH_CEF_DISGUISE_H_
