import Foundation

/// 站点媒体提取策略：对特定站点的捷径与特化配置。
///
/// 设计约定：
/// - 引擎（MediaExtractor）持有全部通用逻辑（perf 清单 / DOM 扫描 / 接口重放挖掘）；
///   策略只提供「站点捷径 JS」与少量调优参数。
/// - 新增站点支持 = 写一个 struct（通常只需一段 stateReadJS）+ 注册进 `MediaSiteStrategies.all`。
/// - 所有注入 JS 必须同步返回受支持的结果类型——evaluateJavaScript 不解析 Promise
///   （async JS 报 WKErrorJavaScriptResultTypeIsUnsupported）。
protocol MediaSiteStrategy {
    var id: String { get }

    /// 是否适用当前页（host 已转小写；注册表按顺序取第一个命中者）
    func matches(host: String) -> Bool

    /// 第①层捷径：同步 JS 直读页内状态。
    /// 输出统一形状的 JSON 字符串（引擎共用解析器）：
    /// {"videos":[{"src":"https://...","duration":32,"title":"..."}],"images":[]}
    /// duration 单位秒。nil = 该站点无捷径。
    var stateReadJS: String? { get }

    /// 第④层重放候选排序信号词（URL 含任一词排前）。有默认词表，可覆盖。
    var replaySignalWords: [String] { get }

    /// 当前页目标对象 id（重放深挖结果优先关联该对象的子树），无则返回 nil。
    func targetId(in url: URL) -> String?
}

extension MediaSiteStrategy {
    var replaySignalWords: [String] { MediaSiteStrategies.defaultSignalWords }
    func targetId(in url: URL) -> String? { nil }
}

// MARK: - 小红书

struct XiaohongshuMediaStrategy: MediaSiteStrategy {
    let id = "xiaohongshu"

    func matches(host: String) -> Bool { host.contains("xiaohongshu.") }

    /// 笔记视频直链在 SSR 状态里：noteDetailMap[noteId].note.video.media.stream.h264[0].masterUrl
    /// （实测验证过；未播放时 <video> 只有 blob:，此捷径是唯一高清来源）
    var stateReadJS: String? {
        """
        (function(){
          function ok(u){ return typeof u === 'string' && /^https?:\\/\\//.test(u); }
          var videos = [];
          try {
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
              if (ok(u)) videos.push({src: u, duration: null,
                                      title: ((note.title || note.desc || '') + '').slice(0, 60)});
            }
          } catch (e) {}
          return JSON.stringify({videos: videos, images: []});
        })()
        """
    }
}

// MARK: - 抖音

struct DouyinMediaStrategy: MediaSiteStrategy {
    let id = "douyin"

    func matches(host: String) -> Bool { host.contains("douyin.") }

    /// 旧版页面把路由数据放在 #RENDER_DATA（URL 编码 JSON）；新版多已移除，
    /// 此捷径保留一行成本——没有时由第④层通用重放兜底。
    var stateReadJS: String? {
        """
        (function(){
          function ok(u){ return typeof u === 'string' && /^https?:\\/\\//.test(u); }
          var videos = [];
          try {
            var el = document.getElementById('RENDER_DATA');
            if (el) {
              var found = '';
              var walk = function(o) {
                if (found || !o || typeof o !== 'object') return;
                if (o.play_addr && o.play_addr.url_list && o.play_addr.url_list.length && ok(o.play_addr.url_list[0])) {
                  found = o.play_addr.url_list[0];
                  return;
                }
                for (var k in o) { if (found) return; walk(o[k]); }
              };
              try { walk(JSON.parse(decodeURIComponent(el.textContent))); } catch (e) {}
              if (found) videos.push({src: found, duration: null, title: document.title.slice(0, 60)});
            }
          } catch (e) {}
          return JSON.stringify({videos: videos, images: []});
        })()
        """
    }

    /// 目标视频 id：详情页 /video/{id}，信息流 ?modal_id={id}
    func targetId(in url: URL) -> String? {
        let parts = url.path.split(separator: "/").map(String.init)
        if let i = parts.firstIndex(of: "video"), parts.indices.contains(i + 1) {
            return parts[i + 1]
        }
        if let query = url.query {
            for pair in query.split(separator: "&") {
                let kv = pair.split(separator: "=", maxSplits: 1)
                if kv.count == 2, kv[0] == "modal_id" { return String(kv[1]) }
            }
        }
        return nil
    }

    var replaySignalWords: [String] {
        MediaSiteStrategies.defaultSignalWords + ["aweme"]
    }
}

// MARK: - 注册表

enum MediaSiteStrategies {
    /// 新增站点：实现 MediaSiteStrategy 后追加到此处
    static let all: [MediaSiteStrategy] = [
        XiaohongshuMediaStrategy(),
        DouyinMediaStrategy(),
    ]

    /// 通用重放候选排序信号词
    static let defaultSignalWords = [
        "detail", "feed", "item", "watch", "note", "status", "play", "stream", "video",
    ]

    static func strategy(forHost host: String?) -> MediaSiteStrategy? {
        guard let host, !host.isEmpty else { return nil }
        let h = host.lowercased()
        return all.first { $0.matches(host: h) }
    }
}
