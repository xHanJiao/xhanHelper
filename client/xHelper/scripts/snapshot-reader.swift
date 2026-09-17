// 在真引擎（WKWebView）里加载阅读版模板、合成选区并截图。
//
// 为什么需要它：选区的**绘制**是引擎行为，Blink 与 WebKit 差别很大 —— WebKit 对「跨段整块
// 选中」会画成一个横跨整个视图的大矩形（段间空隙、两侧留白全填上），Chrome 只在合成的块
// 边界 Range 下才这样。所以凡是想验证「真实渲染 / 选区长什么样」，Chrome 都测不准，得来这儿。
//
// 由 snapshot-reader.sh 调用：snapshot-reader <html> <outDir> [light|dark]
// 按 jobs 逐步合成选区 / 折叠，每步截一张 PNG 并打印探针，然后退出（有失败则退出码 1）。
import AppKit
import WebKit

final class Driver: NSObject, WKNavigationDelegate, WKScriptMessageHandler {
    private let web: WKWebView
    private let outDir: String
    private let bg: NSColor
    /// 每一步做完各截一张（中间不滚动）：大选区 → 折叠 → 容器 li 选区 → 折叠 → 单行选区。
    /// 「折叠后」那两张是查**残留像素**：簿记已清（probe 里 hasSel=false）但图里还有颜色，
    /// 就是引擎没刷新高亮层，不是我们的逻辑错。
    private let jobs: [(sel: String, file: String)] = [
        ("blocks", "selection-blocks.png"),
        ("collapse", "after-collapse.png"),
        ("nested", "selection-nested.png"),
        ("collapse", "after-collapse-nested.png"),
        ("toend", "selection-toend.png"),
        ("partial", "selection-partial.png"),
    ]
    private var i = 0
    private var failed = false

    /// 截图是透明的（页面本身透明，宿主底色在外面），直接存 PNG 会在看图器里被衬成白色 ——
    /// 深色下就会看成「白底白字」。这里把宿主底色先铺上，让存出来的图自成一体。
    static func composite(_ img: NSImage, on bg: NSColor) -> Data? {
        let out = NSImage(size: img.size)
        out.lockFocus()
        bg.setFill()
        NSRect(origin: .zero, size: img.size).fill()
        img.draw(at: .zero, from: .zero, operation: .sourceOver, fraction: 1)
        out.unlockFocus()
        return out.tiffRepresentation
    }

    init(web: WKWebView, outDir: String, bg: NSColor) {
        self.web = web
        self.outDir = outDir
        self.bg = bg
        // 目录不存在时 png.write 会静默失败（try? 吞掉），别让调用方一头雾水
        try? FileManager.default.createDirectory(atPath: outDir, withIntermediateDirectories: true)
    }

    func userContentController(_ c: WKUserContentController, didReceive m: WKScriptMessage) {}

    func webView(_ w: WKWebView, didFinish n: WKNavigation!) {
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
            // 先报一次页面内部状态：环境不对（marked 没跑 / 缺 Highlight）时后面只会得到
            // 一堆语焉不详的 JS 异常，不如在这里一次说清
            let probe = """
            window.__errs = [];
            window.addEventListener('error', function (e) { window.__errs.push(String(e.message)) });
            JSON.stringify({
              marked: typeof marked, applySel: typeof window.__applySel,
              highlight: typeof Highlight, registry: !!(window.CSS && CSS.highlights),
              p: document.querySelectorAll('#content p').length,
              h2: document.querySelectorAll('#content h2').length
            })
            """
            w.evaluateJavaScript(probe) { v, e in
                print("页面状态: \(v as? String ?? "-")\(e.map { "  错误: \($0.localizedDescription)" } ?? "")")
                self.step()
            }
        }
    }

    func step() {
        guard i < jobs.count else {
            print(failed ? "有截图失败" : "全部完成: \(outDir)")
            exit(failed ? 1 : 0)
        }
        let job = jobs[i]
        i += 1
        // 选区是脚本合成的：手动拖拽的绘制结果相同（块边界 Range 正是 WebKit 会自然落到的形态）。
        // 包一层 try/catch 把结果回传：直接抛的话 WKWebView 只给一句「line 0」，什么都没说
        let js = """
        (function () {
          try { return 'step=' + String(__step('\(job.sel)')) } catch (e) { return 'stepErr=' + e.message }
        })()
        """
        web.evaluateJavaScript(js) { ret, err in
            print("  \(job.sel): \(ret as? String ?? "-")\(err.map { "  错误: \($0.localizedDescription)" } ?? "")")
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.7) {
                // 探针放在这一拍之后再读：selectionchange 是异步派发的，紧接着读会读到「上一态」，
                // 看着像簿记错（实测：刚设完选区 hasSel=false、刚折叠 hasSel=true）
                self.web.evaluateJavaScript("window.__probe()") { p, pe in
                    print("    \(p as? String ?? "-")\(pe.map { "  探针错误: \($0.localizedDescription)" } ?? "")")
                    self.snapshot(job)
                }
            }
        }
    }

    private func snapshot(_ job: (sel: String, file: String)) {
        web.takeSnapshot(with: WKSnapshotConfiguration()) { img, err in
            guard let img, let flat = Self.composite(img, on: self.bg),
                  let rep = NSBitmapImageRep(data: flat),
                  let png = rep.representation(using: .png, properties: [:]) else {
                print("截图失败 \(job.file): \(err?.localizedDescription ?? "图片为空")")
                self.failed = true
                self.step()
                return
            }
            // 顺手采一下左上角：页面是透明的，这里应显示宿主窗口的底色。
            // 深色下若采到白，就说明 WebView 的透明没生效（别只看图去猜）
            let corner = rep.colorAt(x: 10, y: 10)?.usingColorSpace(.deviceRGB)
            let sample = corner.map { String(format: "%.2f/%.2f/%.2f", $0.redComponent, $0.greenComponent, $0.blueComponent) } ?? "-"
            do {
                try png.write(to: URL(fileURLWithPath: "\(self.outDir)/\(job.file)"))
                print("写入 \(job.file)  左上角底色=\(sample)")
            } catch {
                print("写入失败 \(job.file): \(error.localizedDescription)")
                self.failed = true
            }
            self.step()
        }
    }
}

let args = CommandLine.arguments
guard args.count > 2 else {
    print("用法: snapshot-reader <html 路径> <输出目录> [light|dark]")
    exit(2)
}
let htmlPath = args[1]
let outDir = args[2]
let dark = args.count > 3 && args[3] == "dark"

// 不用挂 messageHandlers.reader：模板里的 post() 自带 try/catch，没有接收器时静默丢弃
let cfg = WKWebViewConfiguration()

// 窗口只是为了让 WebView 有条渲染链（没有窗口时 takeSnapshot 拿到的是空图）；
// 不 activate，不抢焦点 —— 选区由我们自己画，不依赖窗口是否为 key
NSApplication.shared.setActivationPolicy(.regular)
NSApp.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
let win = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 900, height: 820),
                   styleMask: [.titled], backing: .buffered, defer: false)
let web = WKWebView(frame: win.contentView!.bounds, configuration: cfg)
win.contentView!.addSubview(web)
win.makeKeyAndOrderFront(nil)

// 外观必须显式定：跟随系统时同一份代码在不同时刻会截出不一样的图（实测午后浅色、入夜变深色），
// 看图的人会以为代码改了。页面本身是透明的（与 app 一致，底色由宿主铺），所以窗口要铺底色，
// 否则深色下会看到 WebView 的默认白底 + 白色正文 = 就像坏了。真实宿主是 regularMaterial 卡片，
// 这里用等价的中性色近似。
// 两条都要：drawsBackground 关掉 WebView 自身底色，underPageBackgroundColor 清掉页面
// 后面那层（只关前者时，深色下仍会看到白色页面底）
web.setValue(false, forKey: "drawsBackground")
web.underPageBackgroundColor = .clear
let bg = dark ? NSColor(calibratedWhite: 0.11, alpha: 1) : NSColor.white
win.backgroundColor = bg
win.contentView!.wantsLayer = true
win.contentView!.layer?.backgroundColor = bg.cgColor

let driver = Driver(web: web, outDir: outDir, bg: bg)
web.navigationDelegate = driver
web.loadFileURL(URL(fileURLWithPath: htmlPath),
                allowingReadAccessTo: URL(fileURLWithPath: htmlPath).deletingLastPathComponent())
DispatchQueue.main.asyncAfter(deadline: .now() + 30) {
    print("超时（30s）")
    exit(2)
}
NSApplication.shared.run()
