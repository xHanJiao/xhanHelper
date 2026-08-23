import Foundation
import WebKit

/// 页面媒体扫描（用户手动触发，供 MediaPickerPanelView 使用）。
/// 分层提取（优先级从高到低，URL 去重保留高优先级）：
/// 1. 站点 state 直读：小红书 `__INITIAL_STATE__` 的 masterUrl；抖音 RENDER_DATA 的 play_addr
/// 2. perf 层：performance resource entries 里已加载的 .mp4 直链（排除抖音 DASH 分轨）
/// 3. DOM 层：<video>/<img> currentSrc（blob:/data: 等不可直下的协议过滤）
/// 4. 抖音专属兜底：同步 JS 收集 perf 里的 /aweme/ 数据接口候选（detail/feed/搜索通吃），
///    Swift 原生 URLSession 逐条重放（页面真实 UA——a_bogus 签名混入 UA 必须一致、Referer、
///    域匹配 Cookie；不受 CORS 约束），在响应 JSON 里递归深查 play_addr —— 治 MSE 分轨拼流
///    拿不到整段直链的问题。
/// 注意：evaluateJavaScript 不解析 Promise（async JS 会报 WKErrorJavaScriptResultTypeIsUnsupported），
/// 所有注入 JS 必须同步返回受支持的结果类型（字符串/数字等）。
enum MediaExtractor {

    struct Item: Identifiable, Equatable {
        enum Kind: String { case video, image }
        let kind: Kind
        let url: URL
        /// 视频封面（图片项为 nil）
        let poster: URL?
        let duration: Int?
        let width: Int?
        let height: Int?
        let title: String
        let playing: Bool
        var id: String { "\(kind.rawValue)|\(url.absoluteString)" }
    }

    struct ScanResult {
        let pageURL: String
        var videos: [Item]
        let images: [Item]
        /// 各提取层命中统计（日志用）
        let statsNote: String
        var isEmpty: Bool { videos.isEmpty && images.isEmpty }
    }

    /// 扫描来源页。须在 MainActor 调用（面板 .task 即 MainActor）；
    /// 来源 webView 可能已脱离视图层级（面板 tab 激活时），但实例存活即可执行 JS。
    @MainActor
    static func scan(webView: WKWebView) async -> ScanResult? {
        guard let pageURL = webView.url?.absoluteString,
              pageURL.hasPrefix("http://") || pageURL.hasPrefix("https://") else { return nil }

        // 第 1 步：基础扫描（state + perf + DOM）
        guard var result = await scanBase(webView: webView, pageURL: pageURL) else { return nil }

        // 第 2 步：抖音 API 原生重放（仅当基础扫描没有可用直链时；DASH 分轨页必然触发）。
        // 注意：不能用 evaluateJavaScript 执行 async JS——它不解析 Promise
        // （WKErrorJavaScriptResultTypeIsUnsupported「执行JavaScript返回结果的类型不受支持」），
        // 所以拆成：同步 JS 定位候选数据接口 URL → Swift 原生 URLSession 重放（免 CORS，签名参数内嵌在 URL 里）。
        let host = URL(string: pageURL)?.host?.lowercased() ?? ""
        if host.contains("douyin."), result.videos.isEmpty {
            if let item = await replayDouyinDetailAPI(webView: webView) {
                diagLog("douyin 数据接口原生重放成功")
                result.videos.append(item)
            } else {
                diagLog("douyin 数据接口原生重放未取得直链 page=\(pageURL)")
            }
        }

        // 可观测性：视频为空时打一条带统计的日志，失效时可定位是哪层没拿到
        if result.videos.isEmpty {
            diagLog("扫描结果无视频 page=\(pageURL) \(result.statsNote)")
        }
        return result
    }

    /// 诊断日志：LogBuffer（应用内日志页可见，恒编译）+ print（Xcode 控制台可见）
    private static func diagLog(_ msg: String) {
        LogBuffer.shared.diag(msg)
        print("[MediaExtractor] \(msg)")
    }

    // MARK: - 第 1 步：基础扫描

    @MainActor
    private static func scanBase(webView: WKWebView, pageURL: String) async -> ScanResult? {
        let raw: Any?
        do {
            raw = try await webView.evaluateJavaScript(baseScanJS)
        } catch {
            diagLog("base scan JS error: \(error.localizedDescription)")
            return nil
        }
        guard let json = raw as? String,
              let data = json.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            diagLog("base scan 返回非预期结果（页面可能未加载完成）")
            return nil
        }

        func parse(_ kind: Item.Kind, list: [[String: Any]]) -> [Item] {
            list.compactMap { d in
                guard let src = d["src"] as? String,
                      let url = URL(string: src),
                      url.scheme == "http" || url.scheme == "https" else { return nil }
                return Item(
                    kind: kind,
                    url: url,
                    poster: (d["poster"] as? String).flatMap(URL.init(string:)),
                    duration: d["duration"] as? Int,
                    width: d["width"] as? Int,
                    height: d["height"] as? Int,
                    title: (d["title"] as? String) ?? "",
                    playing: (d["playing"] as? Bool) ?? false
                )
            }
        }
        let videos = parse(.video, list: obj["videos"] as? [[String: Any]] ?? [])
        let images = parse(.image, list: obj["images"] as? [[String: Any]] ?? [])
        let stats = obj["stats"] as? [String: Any] ?? [:]
        return ScanResult(
            pageURL: pageURL,
            videos: videos,
            images: images,
            statsNote: "stats=\(stats)"
        )
    }

    // MARK: - 第 2 步：抖音数据接口泛化重放

    /// 泛化重放：perf 里的 /aweme/ 数据接口可能是 detail（详情页）、tab/feed（信息流）、
    /// search（搜索）等任意形态，逐条重放并在响应 JSON 里递归深查 play_addr，命中即停。
    @MainActor
    private static func replayDouyinDetailAPI(webView: WKWebView) async -> Item? {
        // 2a. 同步 JS 定位候选接口 URL + 目标视频 id（字符串结果是 evaluateJavaScript 支持的类型）
        let raw: Any?
        do {
            raw = try await webView.evaluateJavaScript(douyinApiLocateJS)
        } catch {
            diagLog("douyin locate JS error: \(error.localizedDescription)")
            return nil
        }
        guard let json = raw as? String,
              let data = json.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            diagLog("douyin locate 返回非预期（raw=\(raw.map { "\($0)" } ?? "nil")）")
            return nil
        }
        if let diag = obj["diag"] {
            diagLog("douyin 重放诊断(定位): \(diag)")
            return nil
        }
        // 无 modal_id 的纯 feed 页 targetId 为 null → nil，深查退化为取第一个可用视频
        let targetId = obj["targetId"] as? String
        guard let apiUrls = obj["apiUrls"] as? [String], !apiUrls.isEmpty else {
            diagLog("douyin 重放诊断(api_not_in_perf id=\(targetId ?? "-"))")
            return nil
        }

        // 2b. 逐条原生重放（最多 3 条），命中即停
        for (idx, apiUrl) in apiUrls.prefix(3).enumerated() {
            let hint = URL(string: apiUrl).map { String($0.path.suffix(80)) } ?? apiUrl
            let (respData, status, err) = await BrowserToolProvider.shared
                .fetchPageResourceJSON(urlString: apiUrl, sourceWebView: webView)
            if let err {
                diagLog("douyin 重放#\(idx + 1) native_fetch_error: \(err) [\(hint)]")
                continue
            }
            guard let respData, (200...299).contains(status) else {
                diagLog("douyin 重放#\(idx + 1) http_\(status) [\(hint)]")
                continue
            }
            if let item = findAwemeVideoInJSON(respData, targetId: targetId) {
                diagLog("douyin 重放#\(idx + 1) 命中 [\(hint)]")
                return item
            }
            diagLog("douyin 重放#\(idx + 1) 响应无可用 play_addr [\(hint)]")
        }
        return nil
    }

    /// 在任意 JSON 结构里递归找「带 video.play_addr.url_list 的 aweme 对象」。
    /// aweme_id/group_id 匹配 targetId 的优先返回；否则返回遇到的第一个可用项。
    private static func findAwemeVideoInJSON(_ data: Data, targetId: String?) -> Item? {
        guard let root = try? JSONSerialization.jsonObject(with: data) else {
            diagLog("douyin 重放诊断(non_json)")
            return nil
        }
        var fallback: Item?
        if let hit = walkAweme(node: root, targetId: targetId, fallback: &fallback) { return hit }
        return fallback
    }

    private static func walkAweme(node: Any, targetId: String?, fallback: inout Item?) -> Item? {
        if let dict = node as? [String: Any] {
            if let video = dict["video"] as? [String: Any],
               let item = itemFromAweme(dict, video) {
                if let tid = targetId, !tid.isEmpty, awemeMatches(dict, tid) {
                    return item
                }
                if fallback == nil { fallback = item }
            }
            for (_, value) in dict {
                if let hit = walkAweme(node: value, targetId: targetId, fallback: &fallback) { return hit }
            }
        } else if let arr = node as? [Any] {
            for value in arr {
                if let hit = walkAweme(node: value, targetId: targetId, fallback: &fallback) { return hit }
            }
        }
        return nil
    }

    private static func awemeMatches(_ dict: [String: Any], _ tid: String) -> Bool {
        for key in ["aweme_id", "group_id"] {
            if let v = dict[key] as? String, v == tid { return true }
            if let n = dict[key] as? NSNumber, n.stringValue == tid { return true }
        }
        return false
    }

    /// 从单个 aweme 对象取直链：play_addr.url_list[0]（失败降级 bit_rate 最高码率项）。
    private static func itemFromAweme(_ dict: [String: Any], _ video: [String: Any]) -> Item? {
        func pickURL(_ playAddr: Any?) -> URL? {
            guard let pa = playAddr as? [String: Any],
                  let urls = pa["url_list"] as? [String] else { return nil }
            for u in urls {
                if let url = URL(string: u), url.scheme == "http" || url.scheme == "https" { return url }
            }
            return nil
        }
        var outURL = pickURL(video["play_addr"])
        if outURL == nil, let rates = video["bit_rate"] as? [[String: Any]], let best = rates.last {
            outURL = pickURL(best["play_addr"])
        }
        guard let url = outURL else { return nil }
        // duration 单位 ms（aweme 层优先）；个别场景可能已是秒，按量级换算
        var durationSec: Int?
        for holder in [dict, video] {
            if let n = holder["duration"] as? NSNumber, n.doubleValue > 0 {
                durationSec = Int(n.doubleValue / (n.doubleValue > 1000 ? 1000 : 1))
                break
            }
        }
        let title = ((dict["desc"] as? String) ?? "").prefix(60)
        return Item(kind: .video, url: url, poster: nil,
                    duration: durationSec, width: nil, height: nil,
                    title: String(title), playing: false)
    }

    // MARK: - 基础扫描 JS

    private static let baseScanJS = """
    (function(){
      function ctx(el, max) {
        var node = el;
        for (var i = 0; i < 4 && node; i++) {
          node = node.parentElement;
          if (!node) break;
          var tag = node.tagName;
          if (tag === 'FIGURE' || tag === 'ARTICLE' || tag === 'SECTION' || tag === 'DIV') {
            var t = (node.innerText || '').replace(/\\s+/g, ' ').trim();
            if (t) return t.length > max ? t.slice(0, max) + '…' : t;
          }
        }
        return '';
      }
      function ok(u) { return typeof u === 'string' && /^https?:\\/\\//.test(u); }
      var enhanced = [], domVideos = [], perfVideos = [], images = [];
      Array.prototype.forEach.call(document.querySelectorAll('video'), function(v) {
        var src = v.currentSrc || v.src || '';
        if (!ok(src)) return;
        domVideos.push({src: src, playing: !v.paused && !v.ended, poster: ok(v.poster) ? v.poster : '',
                        duration: isFinite(v.duration) && v.duration > 0 ? Math.round(v.duration) : null,
                        width: v.videoWidth || null, height: v.videoHeight || null,
                        title: ctx(v, 60) || v.getAttribute('aria-label') || ''});
      });
      Array.prototype.forEach.call(document.querySelectorAll('img'), function(im) {
        var src = im.currentSrc || im.src || '';
        if (!ok(src)) return;
        images.push({src: src, title: ctx(im, 60) || im.alt || '',
                     width: im.naturalWidth || null, height: im.naturalHeight || null});
      });
      var stats = {domVideos: domVideos.length, perfHits: 0, stateHit: false};
      try {
        var host = location.hostname;
        if (/xiaohongshu\\.com$/.test(host)) {
          var m = location.pathname.match(/\\/(?:explore|search_result|discovery\\/item)\\/([0-9a-fA-F]+)/)
               || location.pathname.match(/\\/([0-9a-f]{24})(?:\\/|$)/);
          var s = window.__INITIAL_STATE__;
          var map = s && s.note && s.note.noteDetailMap;
          if (m && map && map[m[1]]) {
            var note = map[m[1]].note || {};
            var media = (note.video && note.video.media) || {};
            var stream = media.stream || {};
            var first = function(a) { return a && a[0] && a[0].masterUrl; };
            var u = first(stream.h264) || first(stream.h265) || (media.legacy && media.legacy.videoUrl);
            if (ok(u)) {
              enhanced.push({src: u, playing: false, poster: '', duration: null, width: null, height: null,
                             title: ((note.title || note.desc || '') + '').slice(0, 60)});
              stats.stateHit = true;
            }
          }
        } else if (/douyin\\.com$/.test(host)) {
          var el = document.getElementById('RENDER_DATA');
          if (el) {
            var found = '';
            var walk = function(o) {
              if (found || !o || typeof o !== 'object') return;
              if (o.play_addr && o.play_addr.url_list && o.play_addr.url_list.length && ok(o.play_addr.url_list[0])) {
                found = o.play_addr.url_list[0]; return;
              }
              for (var k in o) { if (found) return; walk(o[k]); }
            };
            try { walk(JSON.parse(decodeURIComponent(el.textContent))); } catch (e) {}
            if (found) {
              enhanced.push({src: found, playing: false, poster: '', duration: null, width: null, height: null,
                             title: document.title.slice(0, 60)});
              stats.stateHit = true;
            }
          }
        }
        // perf 层：已加载的 .mp4 直链；排除抖音 DASH 分轨（纯视频/纯音频，下载了没法看）
        var seenP = {};
        Array.prototype.forEach.call(performance.getEntriesByType('resource'), function(en) {
          var u = en.name;
          if (!ok(u) || !/\\.mp4([?#]|$)/.test(u)) return;
          if (/douyinvod|media-video-|media-audio-/.test(u)) return;
          if (seenP[u]) return;
          seenP[u] = 1;
          perfVideos.push({src: u, playing: false, poster: '', duration: null, width: null, height: null, title: ''});
        });
        stats.perfHits = perfVideos.length;
      } catch (e) {}
      var videos = enhanced.concat(perfVideos, domVideos);
      var seen = {};
      videos = videos.filter(function(v) { if (seen[v.src]) return false; seen[v.src] = 1; return true; });
      return JSON.stringify({videos: videos, images: images, stats: stats});
    })()
    """

    // MARK: - 抖音数据接口定位 JS（同步，返回受支持的结果类型）

    /// 收集 perf 里所有 /aweme/ 数据接口候选 URL（新→旧最多 5 条，排除 emoji/comment 等非数据接口），
    /// 附带目标视频 id（/video/{id} 或 modal_id=，纯 feed 页为 null）。
    /// 只负责定位；重放由 Swift 原生 URLSession 完成（evaluateJavaScript 不支持 async/Promise 结果）。
    private static let douyinApiLocateJS = """
    (function(){
      var D = function(o) { try { return JSON.stringify(o); } catch (e) { return null; } };
      try {
        var m = location.pathname.match(/\\/video\\/(\\d+)/) || location.search.match(/modal_id=(\\d+)/);
        var targetId = m ? m[1] : null;
        var urls = [];
        var seen = {};
        var entries = performance.getEntriesByType('resource');
        for (var i = entries.length - 1; i >= 0 && urls.length < 5; i--) {
          var u = entries[i].name;
          if (!/^https?:\\/\\//.test(u)) continue;
          if (u.indexOf('/aweme/') < 0) continue;
          if (/emoji|dashboard|qrcode|passport|comment|reply|notice|log|report|config|constants|category/.test(u)) continue;
          if (seen[u]) continue;
          seen[u] = 1;
          urls.push(u);
        }
        return D({targetId: targetId, apiUrls: urls});
      } catch (e) {
        return D({diag:{step:'exception', err:String((e && e.message) || e).slice(0, 80)}});
      }
    })()
    """
}
