import Foundation
import WebKit

/// 页面媒体扫描（用户手动触发，供 MediaPickerPanelView 使用）。
/// 分层提取（优先级从高到低，URL 去重保留高优先级）：
/// 1. 站点 state 直读：小红书 `__INITIAL_STATE__` 的 masterUrl；抖音 RENDER_DATA 的 play_addr
/// 2. perf 层：performance resource entries 里已加载的 .mp4 直链（排除抖音 DASH 分轨）
/// 3. DOM 层：<video>/<img> currentSrc（blob:/data: 等不可直下的协议过滤）
/// 4. 抖音专属兜底：页面上下文 fetch 重放 /aweme/v1/web/aweme/detail/（签名参数内嵌在 URL，
///    credentials 自动带 cookie），取响应里的 play_addr —— 治 MSE 分轨拼流拿不到整段直链的问题。
/// 全部在真实登录态页面上下文里读现成数据，不构造新的签名请求 → 不触碰风控体系。
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

        // 第 2 步：抖音 API 重放（仅当基础扫描没有可用直链时；DASH 分轨页必然触发）
        let host = URL(string: pageURL)?.host?.lowercased() ?? ""
        if host.contains("douyin."), result.videos.isEmpty {
            if let item = await replayDouyinDetailAPI(webView: webView) {
                LogBuffer.shared.info("[MediaExtractor] douyin detail API 重放成功")
                result.videos.append(item)
            } else {
                LogBuffer.shared.info("[MediaExtractor] douyin detail API 重放未取得直链 page=\(pageURL)")
            }
        }

        // 可观测性：视频为空时打一条带统计的日志，失效时可定位是哪层没拿到
        if result.videos.isEmpty {
            LogBuffer.shared.info("[MediaExtractor] 扫描结果无视频 page=\(pageURL) \(result.statsNote)")
        }
        return result
    }

    // MARK: - 第 1 步：基础扫描

    @MainActor
    private static func scanBase(webView: WKWebView, pageURL: String) async -> ScanResult? {
        let raw: Any?
        do {
            raw = try await webView.evaluateJavaScript(baseScanJS)
        } catch {
            LogBuffer.shared.error("[MediaExtractor] base scan JS error: \(error.localizedDescription)")
            return nil
        }
        guard let json = raw as? String,
              let data = json.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            LogBuffer.shared.error("[MediaExtractor] base scan 返回非预期结果（页面可能未加载完成）")
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

    // MARK: - 第 2 步：抖音 detail API 重放

    @MainActor
    private static func replayDouyinDetailAPI(webView: WKWebView) async -> Item? {
        let raw: Any?
        do {
            raw = try await webView.evaluateJavaScript(douyinReplayJS)
        } catch {
            LogBuffer.shared.error("[MediaExtractor] douyin replay JS error: \(error.localizedDescription)")
            return nil
        }
        guard let json = raw as? String,
              let data = json.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let urlStr = obj["url"] as? String,
              let url = URL(string: urlStr),
              url.scheme == "http" || url.scheme == "https" else { return nil }
        return Item(
            kind: .video,
            url: url,
            poster: nil,
            duration: obj["duration"] as? Int,
            width: nil,
            height: nil,
            title: (obj["title"] as? String) ?? "",
            playing: false
        )
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

    // MARK: - 抖音 detail API 重放 JS

    /// 从 performance entries 里找当前 aweme_id 的 detail API URL（签名参数内嵌），
    /// 页面上下文 fetch 重放，取 play_addr 直链。async IIFE 由 evaluateJavaScript 自动解包 Promise。
    private static let douyinReplayJS = """
    (async function(){
      try {
        var m = location.pathname.match(/\\/video\\/(\\d+)/) || location.search.match(/modal_id=(\\d+)/);
        var id = m ? m[1] : null;
        if (!id) return null;
        var api = null;
        var entries = performance.getEntriesByType('resource');
        for (var i = entries.length - 1; i >= 0; i--) {
          var u = entries[i].name;
          if (u.indexOf('/aweme/v1/web/aweme/detail/') >= 0 && u.indexOf('aweme_id=' + id) >= 0) { api = u; break; }
        }
        if (!api) return null;
        var r;
        try {
          r = await fetch(api, {credentials: 'include'});
        } catch (e1) {
          // 捕获的 API 可能在非常规子域（www-hj 等），跨域被拦时换主域重放一次
          r = await fetch(api.replace('//www-hj.douyin.com', '//www.douyin.com'), {credentials: 'include'});
        }
        if (!r.ok) return null;
        var j = await r.json();
        var d = j.aweme_detail || (j.item_list && j.item_list[0]) || null;
        var v = d && d.video;
        if (!v) return null;
        var pick = function(pa) {
          if (!pa || !pa.url_list) return null;
          for (var k = 0; k < pa.url_list.length; k++) if (/^https?:\\/\\//.test(pa.url_list[k])) return pa.url_list[k];
          return null;
        };
        var out = pick(v.play_addr);
        if (!out && v.bit_rate && v.bit_rate.length) out = pick(v.bit_rate[v.bit_rate.length - 1].play_addr);
        if (!out) return null;
        return JSON.stringify({url: out, duration: isFinite(d.duration) && d.duration > 0 ? Math.round(d.duration / 1000) : null,
                               title: (d.desc || document.title || '').slice(0, 60)});
      } catch (e) { return null; }
    })()
    """
}
