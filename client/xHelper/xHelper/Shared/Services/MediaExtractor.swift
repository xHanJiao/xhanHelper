import Foundation
import WebKit

/// 页面媒体提取引擎（用户手动触发，供 MediaPickerPanelView 使用）。
///
/// 分层提取（优先级从高到低，URL 去重保留高优先级）：
/// ① 站点捷径（策略提供，可无）：页内状态直读，如小红书 __INITIAL_STATE__、抖音 RENDER_DATA
/// ② perf 层：performance resource entries 里已加载的 .mp4 直链（排除 HLS/DASH 分轨）
/// ③ DOM 层：<video>/<img> currentSrc（blob:/data: 等不可直下的协议过滤）
/// ④ 接口重放挖掘（通用兜底，所有站点）：perf 里收集数据接口 URL → Swift 原生 URLSession 重放
///    （页面真实 UA/Referer/域匹配 Cookie，不受 CORS 约束）→ 响应 JSON 递归深挖视频直链。
///
/// 注意：evaluateJavaScript 不解析 Promise（async JS 报 WKErrorJavaScriptResultTypeIsUnsupported），
/// 所有注入 JS 必须同步返回受支持的结果类型；需要异步时同步 JS 取参数 + Swift 原生请求完成。
/// 站点特化逻辑见 MediaSiteStrategies.swift（策略模式，新站点 = 一个 struct + 一行注册）。
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
        /// 各层命中统计（日志用）
        let statsNote: String
        var isEmpty: Bool { videos.isEmpty && images.isEmpty }
    }

    // MARK: - 入口

    /// 扫描来源页。须在 MainActor 调用（面板 .task 即 MainActor）；
    /// 来源 webView 可能已脱离视图层级（面板 tab 激活时），但实例存活即可执行 JS。
    @MainActor
    static func scan(webView: WKWebView) async -> ScanResult? {
        guard let pageURL = webView.url?.absoluteString,
              pageURL.hasPrefix("http://") || pageURL.hasPrefix("https://") else { return nil }
        let host = URL(string: pageURL)?.host?.lowercased() ?? ""
        let strategy = MediaSiteStrategies.strategy(forHost: host)
        var stats: [String] = []
        if let strategy {
            stats.append("site=\(strategy.id)")
        } else {
            stats.append("site=generic")
        }

        // ① 站点捷径（策略提供）
        var videos: [Item] = []
        var images: [Item] = []
        if let js = strategy?.stateReadJS {
            let raw: Any?
            do {
                raw = try await webView.evaluateJavaScript(js)
            } catch {
                diagLog("site[\(strategy?.id ?? "?")] state JS error: \(error.localizedDescription)")
                raw = nil
            }
            if let obj = normalizedState(raw) {
                let v = parseItems(.video, obj["videos"] as? [[String: Any]] ?? [])
                videos += v
                images += parseItems(.image, obj["images"] as? [[String: Any]] ?? [])
                stats.append("stateHit=\(v.isEmpty ? 0 : v.count)")
            }
        }

        // ②③ perf + DOM 通用扫描
        let base = await scanBase(webView: webView)
        videos += base.videos
        images += base.images
        stats.append(base.stats)

        videos = dedupe(videos)
        images = dedupe(images)

        // ④ 接口重放挖掘（通用兜底：所有站点，前三层无视频时触发）
        if videos.isEmpty {
            let targetId = strategy?.targetId(in: URL(string: pageURL) ?? URL(fileURLWithPath: "/"))
            let words = strategy?.replaySignalWords ?? MediaSiteStrategies.defaultSignalWords
            let mined = await mineByReplay(webView: webView, signalWords: words, targetId: targetId)
            videos += mined
        }

        if videos.isEmpty {
            diagLog("扫描结果无视频 page=\(pageURL) \(stats.joined(separator: " "))")
        }
        return ScanResult(pageURL: pageURL, videos: videos, images: images,
                          statsNote: stats.joined(separator: " "))
    }

    /// 诊断日志：LogBuffer（应用内日志页可见，恒编译）+ print（Xcode 控制台可见）
    private static func diagLog(_ msg: String) {
        LogBuffer.shared.diag(msg)
        print("[MediaExtractor] \(msg)")
    }

    // MARK: - ① 站点捷径解析

    /// 策略 stateReadJS 输出的归一化 JSON 字符串 → 字典
    private static func normalizedState(_ raw: Any?) -> [String: Any]? {
        guard let json = raw as? String,
              let data = json.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return nil }
        return obj
    }

    /// 归一化条目数组 → Item（非法/非 http(s) 条目剔除）
    private static func parseItems(_ kind: Item.Kind, _ list: [[String: Any]]) -> [Item] {
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

    // MARK: - ②③ perf + DOM 通用扫描

    @MainActor
    private static func scanBase(webView: WKWebView) async -> (videos: [Item], images: [Item], stats: String) {
        let raw: Any?
        do {
            raw = try await webView.evaluateJavaScript(baseScanJS)
        } catch {
            diagLog("base scan JS error: \(error.localizedDescription)")
            return ([], [], "baseErr")
        }
        guard let obj = normalizedState(raw) else {
            diagLog("base scan 返回非预期结果（页面可能未加载完成）")
            return ([], [], "baseErr")
        }
        let videos = parseItems(.video, obj["videos"] as? [[String: Any]] ?? [])
        let images = parseItems(.image, obj["images"] as? [[String: Any]] ?? [])
        let perfHits = (obj["stats"] as? [String: Any])?["perfHits"] as? Int ?? 0
        return (videos, images, "perf=\(perfHits),dom=\(videos.count)")
    }

    /// 通用扫描脚本：DOM video/img + performance 资源清单里的 .mp4 直链。
    /// 站点特化已抽离到策略层（stateReadJS），此处保持纯通用。
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
      var domVideos = [], perfVideos = [], images = [];
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
      // perf 层：已加载的 .mp4 直链；排除 HLS/DASH 分轨（douyinvod 整族不可直下）
      var seenP = {};
      Array.prototype.forEach.call(performance.getEntriesByType('resource'), function(en) {
        var u = en.name;
        if (!ok(u) || !/\\.mp4([?#]|$)/.test(u)) return;
        if (/m3u8|\\.mpd|douyinvod|media-audio-|media-video-/.test(u)) return;
        if (seenP[u]) return;
        seenP[u] = 1;
        perfVideos.push({src: u, playing: false, poster: '', duration: null, width: null, height: null, title: ''});
      });
      var videos = perfVideos.concat(domVideos);
      var seen = {};
      videos = videos.filter(function(v) { if (seen[v.src]) return false; seen[v.src] = 1; return true; });
      return JSON.stringify({videos: videos, images: images, stats: {perfHits: perfVideos.length}});
    })()
    """

    // MARK: - ④ 接口重放挖掘（通用）

    /// 泛化重放：收集 perf 里的数据接口候选 → 原生逐条重放（≤4）→ 响应 JSON 深挖视频直链。
    /// 返回挖到的条目（目标 id 关联的排前，最多 5 条）。
    @MainActor
    private static func mineByReplay(webView: WKWebView, signalWords: [String], targetId: String?) async -> [Item] {
        // 4a. 同步 JS 收集候选接口 URL（字符串结果是 evaluateJavaScript 支持的类型）
        let raw: Any?
        do {
            raw = try await webView.evaluateJavaScript(replayLocateJS(signalWords: signalWords, targetId: targetId))
        } catch {
            diagLog("replay locate JS error: \(error.localizedDescription)")
            return []
        }
        guard let json = raw as? String,
              let data = json.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            diagLog("replay locate 返回非预期（raw=\(raw.map { "\($0)" } ?? "nil")）")
            return []
        }
        if obj["diag"] != nil {
            diagLog("replay 定位诊断: \(obj["diag"]!)")
            return []
        }
        guard let apiUrls = obj["apiUrls"] as? [String] else {
            diagLog("replay locate 无 apiUrls 字段")
            return []
        }

        // 4b. 逐条原生重放（页面真实 UA/Referer/域匹配 Cookie，不受 CORS 约束），命中即停
        for (idx, apiUrl) in apiUrls.prefix(4).enumerated() {
            let hint = URL(string: apiUrl).map { String($0.path.suffix(80)) } ?? apiUrl
            let (respData, status, err) = await BrowserToolProvider.shared
                .fetchPageResourceJSON(urlString: apiUrl, sourceWebView: webView)
            if let err {
                diagLog("replay#\(idx + 1) native_fetch_error: \(err) [\(hint)]")
                continue
            }
            guard let respData, (200...299).contains(status) else {
                diagLog("replay#\(idx + 1) http_\(status) [\(hint)]")
                continue
            }
            guard respData.count <= 8_000_000 else {
                diagLog("replay#\(idx + 1) 响应过大跳过 (\(respData.count)B) [\(hint)]")
                continue
            }
            let items = mineVideoLinks(respData, targetId: targetId)
            if !items.isEmpty {
                diagLog("replay#\(idx + 1) 命中 \(items.count) 条 [\(hint)]")
                return items
            }
            diagLog("replay#\(idx + 1) 响应无可挖视频直链 [\(hint)]")
        }
        return []
    }

    /// 重放候选收集 JS：全量扫描 perf 资源条目（不设收集中断——真数据接口往往在加载早期，
    /// 从新往旧扫几百条才到），按路径去重（忽略查询参数，保留最新一次调用），
    /// 三桶优先级：hit（URL 含目标视频 id）→ pri（信号词）→ rest，最终截断 10 条。
    /// 信号词/目标 id 由策略注入（compile-time 常量，无注入风险）。
    private static func replayLocateJS(signalWords: [String], targetId: String?) -> String {
        let signalTest = signalWords.map { "u.indexOf('\($0)') >= 0" }.joined(separator: " || ")
        // 目标 id 只保留字母数字，防拼接破坏 JS 字符串
        let tid = (targetId ?? "").filter { $0.isNumber || $0.isLetter }
        return """
        (function(){
          var D = function(o) { try { return JSON.stringify(o); } catch (e) { return null; } };
          // 媒体文件归第②层；HLS/DASH 分轨不可直下（douyinvod 整族都是分轨或签名播放端点）
          function isMediaTrack(x) {
            var l = x.toLowerCase();
            return l.indexOf('m3u8') >= 0 || l.indexOf('.mpd') >= 0 || l.indexOf('douyinvod') >= 0
                || l.indexOf('media-audio-') >= 0 || l.indexOf('media-video-') >= 0;
          }
          try {
            // 黑名单只作用于 path（?之前）：数据接口的查询参数里必然带
            // device_platform/verifyFp 等，对全 URL 匹配会把真接口全部误杀
            var deny = /emoji|dashboard|qrcode|passport|comment|reply|notice|log|report|track|collect|analytics|monitor|beat|config|constants|category|query\\/|account|settings|custom\\/|user\\/|sug|abtest|experiment|switch|policy|webid|register|verify|device|captcha|risk|security|check/;
            var TID = '\(tid)';
            var hit = [], pri = [], rest = [];
            var seen = {};
            var entries = performance.getEntriesByType('resource');
            for (var i = entries.length - 1; i >= 0; i--) {
              var u = entries[i].name;
              if (!/^https?:\\/\\//.test(u)) continue;
              var qi = u.indexOf('?');
              var p = qi >= 0 ? u.slice(0, qi) : u;
              if (deny.test(p)) continue;
              if (/\\.(css|js|mjs|png|jpe?g|webp|gif|svg|ico|woff2?|ttf|eot)$/i.test(p)) continue;
              if (/\\.mp4([?#]|$)/.test(u) || isMediaTrack(u)) continue;
              // 按来源+路径去重：同接口不同签名参数的多次调用只留最新一次
              var key = p.replace(/^https?:\\/\\//, '');
              if (seen[key]) continue;
              seen[key] = 1;
              if (TID && u.indexOf(TID) >= 0) hit.push(u);
              else if (\(signalTest)) pri.push(u);
              else rest.push(u);
            }
            return D({apiUrls: hit.concat(pri).concat(rest).slice(0, 10)});
          } catch (e) {
            return D({diag:{step:'exception', err:String((e && e.message) || e).slice(0, 80)}});
          }
        })()
        """
    }

    /// 在响应 JSON 里递归深挖视频直链（不绑定站点数据形态）：
    /// - 结构化捷径：dict 的 url_list 数组（抖音 play_addr 形态）、键名含 url/src/play/link 且值为媒体字符串
    /// - 通用形态：任何字符串值匹配强特征（路径 .mp4/.webm/.mov 结尾）或弱特征（含 /play、/stream、media-video 等）
    /// - 排除 HLS/DASH（m3u8/mpd/media-audio-）
    /// - targetId：dict 的 id 类字段匹配时该子树挖到的链接排前
    /// - 只挖视频；图片仍走 DOM 层（避免把全页头像缩略图都挖出来）
    private static func mineVideoLinks(_ data: Data, targetId: String?, limit: Int = 5) -> [Item] {
        guard let root = try? JSONSerialization.jsonObject(with: data) else {
            diagLog("replay 响应非 JSON，跳过")
            return []
        }

        var preferred: [String] = []
        var others: [String] = []

        func collectStrings(_ node: Any, into bucket: inout [String]) {
            switch node {
            case let s as String: bucket.append(s)
            case let dict as [String: Any]:
                for value in dict.values { collectStrings(value, into: &bucket) }
            case let arr as [Any]:
                for value in arr { collectStrings(value, into: &bucket) }
            default: break
            }
        }

        // 目标对象子树优先收集
        if let tid = targetId {
            var matchedDicts: [[String: Any]] = []
            findTargetDicts(root, tid, &matchedDicts)
            for dict in matchedDicts.prefix(3) {
                collectStrings(dict, into: &preferred)
            }
        }
        collectStrings(root, into: &others)

        var seen = Set<String>()
        var items: [Item] = []
        for s in preferred + others where items.count < limit {
            guard let item = classifyMediaLink(s) else { continue }
            let key = item.url.absoluteString
            guard !seen.contains(key) else { continue }
            seen.insert(key)
            items.append(item)
        }
        return items
    }

    /// 深度优先找 id 字段匹配 targetId 的 dict（最多记录若干个）
    private static func findTargetDicts(_ node: Any, _ tid: String, _ out: inout [[String: Any]]) {
        guard out.count < 3 else { return }
        if let dict = node as? [String: Any] {
            for key in ["aweme_id", "group_id", "note_id", "item_id", "object_id", "id", "video_id"] {
                if let v = dict[key] as? String, v == tid {
                    out.append(dict)
                    break
                }
                if let n = dict[key] as? NSNumber, n.stringValue == tid {
                    out.append(dict)
                    break
                }
            }
            for value in dict.values { findTargetDicts(value, tid, &out) }
        } else if let arr = node as? [Any] {
            for value in arr { findTargetDicts(value, tid, &out) }
        }
    }

    /// 判断一个字符串是否为可下载的视频直链
    private static func classifyMediaLink(_ s: String) -> Item? {
        guard let url = URL(string: s),
              url.scheme == "http" || url.scheme == "https" else { return nil }
        let lower = s.lowercased()
        // 明确排除：HLS/DASH（魔数校验也过不了）、分轨路径特征。
        // 注意：douyinvod 域名本身不排除——play_addr.url_list 指向的合成 mp4 也可能在该域名；
        // 分轨的可靠特征是路径里的 media-video-/media-audio- 标记。
        if lower.contains(".m3u8") || lower.contains(".mpd") || lower.contains("media-audio-")
            || lower.contains("media-video-") { return nil }
        let path = url.path.lowercased()
        let strong = path.hasSuffix(".mp4") || path.hasSuffix(".webm") || path.hasSuffix(".mov")
        let weakSignals = ["play", "stream", "videoplay"]
        let weak = weakSignals.contains { lower.contains($0) }
        guard strong || weak else { return nil }
        return Item(kind: .video, url: url, poster: nil, duration: nil,
                    width: nil, height: nil, title: "", playing: false)
    }

    // MARK: - 工具

    /// 按 URL 去重，保序
    private static func dedupe(_ items: [Item]) -> [Item] {
        var seen = Set<String>()
        return items.filter { seen.insert($0.url.absoluteString).inserted }
    }
}
