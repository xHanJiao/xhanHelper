import Foundation
@testable import EpubCore

/// 用系统 `zip` 现造 EPUB fixture —— 不往仓库里塞二进制书，改 fixture 就是改这段文本。
enum EpubFixture {

    static func epub(_ files: [String: String], named: String = "book.epub") throws -> URL {
        try epub(files.mapValues { Data($0.utf8) }, named: named)
    }

    static func epub(_ files: [String: Data], named: String = "book.epub") throws -> URL {
        let root = tempDir()
        let src = root.appendingPathComponent("src", isDirectory: true)
        let fm = FileManager.default
        try fm.createDirectory(at: src, withIntermediateDirectories: true)
        for (path, data) in files {
            let url = src.appendingPathComponent(path)
            try fm.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try data.write(to: url)
        }
        let dest = root.appendingPathComponent(named)
        try run("/usr/bin/zip", ["-q", "-r", "-X", dest.path, "."], cwd: src)
        return dest
    }

    static func tempDir() -> URL {
        let url = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("epub-test-\(UUID().uuidString)", isDirectory: true)
        do {
            try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        } catch {
            fatalError("建临时目录失败：\(error)")
        }
        return url
    }

    @discardableResult
    static func run(_ path: String, _ args: [String], cwd: URL?) throws -> String {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: path)
        p.arguments = args
        if let cwd { p.currentDirectoryURL = cwd }
        let out = Pipe(), err = Pipe()
        p.standardOutput = out
        p.standardError = err
        try p.run()
        let outData = out.fileHandleForReading.readDataToEndOfFile()
        let errData = err.fileHandleForReading.readDataToEndOfFile()
        p.waitUntilExit()
        guard p.terminationStatus == 0 else {
            throw NSError(domain: "fixture", code: Int(p.terminationStatus), userInfo: [
                NSLocalizedDescriptionKey: String(decoding: errData, as: UTF8.self),
            ])
        }
        return String(decoding: outData, as: UTF8.self)
    }
}

/// 三份有代表性的书：EPUB2（NCX）、EPUB3（nav + 具名实体）、无章节单文件。
enum SampleBook {

    static let containerXML = """
    <?xml version="1.0" encoding="UTF-8"?>
    <container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
      <rootfiles>
        <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
      </rootfiles>
    </container>
    """

    static let chapter1 = """
    <?xml version="1.0" encoding="UTF-8"?>
    <html xmlns="http://www.w3.org/1999/xhtml"><head><title>c1</title></head>
    <body><h1>第一章 起</h1><p>第一段，带<em>强调</em>与&nbsp;空白。</p><p>第二段。</p></body></html>
    """

    static let chapter2 = """
    <?xml version="1.0" encoding="UTF-8"?>
    <html xmlns="http://www.w3.org/1999/xhtml"><head><title>c2</title></head>
    <body><h1>内文标题二</h1><p>第二章正文。</p></body></html>
    """

    /// EPUB2：NCX 目录（嵌套一层）+ `<meta name="cover">` 封面
    static func epub2() throws -> URL {
        let opf = """
        <?xml version="1.0" encoding="UTF-8"?>
        <package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
          <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
            <dc:title>旧格式 &amp; 目录</dc:title>
            <dc:creator>某作者</dc:creator>
            <dc:language>zh-CN</dc:language>
            <meta name="cover" content="cover-img"/>
          </metadata>
          <manifest>
            <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
            <item id="ch1" href="Text/ch1.xhtml" media-type="application/xhtml+xml"/>
            <item id="ch2" href="Text/ch2.xhtml" media-type="application/xhtml+xml"/>
            <item id="cover-img" href="Images/cover.jpg" media-type="image/jpeg"/>
          </manifest>
          <spine toc="ncx">
            <itemref idref="ch1"/>
            <itemref idref="ch2"/>
          </spine>
        </package>
        """
        let ncx = """
        <?xml version="1.0" encoding="UTF-8"?>
        <ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
          <navMap>
            <navPoint id="p1" playOrder="1">
              <navLabel><text>第一章 起</text></navLabel>
              <content src="Text/ch1.xhtml"/>
            </navPoint>
            <navPoint id="p2" playOrder="2">
              <navLabel><text>第二章 承</text></navLabel>
              <content src="Text/ch2.xhtml"/>
              <navPoint id="p3" playOrder="3">
                <navLabel><text>2.1 小节</text></navLabel>
                <content src="Text/ch2.xhtml#sec"/>
              </navPoint>
            </navPoint>
          </navMap>
        </ncx>
        """
        return try EpubFixture.epub([
            "mimetype": Data("application/epub+zip".utf8),
            "META-INF/container.xml": Data(containerXML.utf8),
            "OEBPS/content.opf": Data(opf.utf8),
            "OEBPS/toc.ncx": Data(ncx.utf8),
            "OEBPS/Text/ch1.xhtml": Data(chapter1.utf8),
            "OEBPS/Text/ch2.xhtml": Data(chapter2.utf8),
            "OEBPS/Images/cover.jpg": Data([0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10]),
        ])
    }

    /// EPUB3：nav 目录（嵌套 + `&nbsp;`/`&mdash;`），无 NCX。
    /// `navEncoding` 用来造「nav 不是 UTF-8」的老书（UTF-16 带 BOM + 声明写 utf-16）。
    static func epub3(extraMetadata: String = "",
                      navEncoding: String.Encoding? = nil) throws -> URL {
        let opf = """
        <?xml version="1.0" encoding="UTF-8"?>
        <package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
          <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
            <dc:title>新格式书</dc:title>
            <dc:creator>作者乙</dc:creator>
            <dc:language>zh</dc:language>
            <meta property="dcterms:modified">2026-01-01T00:00:00Z</meta>
            \(extraMetadata)
          </metadata>
          <manifest>
            <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
            <item id="c1" href="text/c1.xhtml" media-type="application/xhtml+xml"/>
            <item id="c2" href="text/c2.xhtml" media-type="application/xhtml+xml"/>
          </manifest>
          <spine>
            <itemref idref="c1"/>
            <itemref idref="c2"/>
          </spine>
        </package>
        """
        let nav = """
        <?xml version="1.0" encoding="UTF-8"?>
        <html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
        <body>
        <nav epub:type="landmarks"><ol><li><a href="text/c1.xhtml">不该进目录</a></li></ol></nav>
        <nav epub:type="toc">
          <ol>
            <li><a href="text/c1.xhtml">第一章&nbsp;开端 &amp; 续</a></li>
            <li><a href="text/c2.xhtml">第二章&mdash;继续</a>
              <ol><li><a href="text/c2.xhtml#s1">2.1 细节</a></li></ol>
            </li>
          </ol>
        </nav>
        </body></html>
        """
        var navData = Data(nav.utf8)
        if let navEncoding {
            let adjusted = nav.replacingOccurrences(of: "encoding=\"UTF-8\"",
                                                    with: "encoding=\"UTF-16\"")
            guard let encoded = adjusted.data(using: navEncoding) else {
                throw NSError(domain: "fixture", code: 1, userInfo: [
                    NSLocalizedDescriptionKey: "nav 编码失败",
                ])
            }
            navData = encoded
        }
        return try EpubFixture.epub([
            "mimetype": Data("application/epub+zip".utf8),
            "META-INF/container.xml": Data(containerXML.utf8),
            "OEBPS/content.opf": Data(opf.utf8),
            "OEBPS/nav.xhtml": navData,
            "OEBPS/text/c1.xhtml": Data(chapter1.utf8),
            "OEBPS/text/c2.xhtml": Data(chapter2.utf8),
        ])
    }

    /// 一个 spine 文件装整本书、目录全靠 `#锚点`（很常见的排版书），
    /// 且目录里有一层「空标题的分组包装」（`<li><ol>…</ol></li>`）。
    static func singleFileRichTOC() throws -> URL {
        let opf = """
        <?xml version="1.0" encoding="UTF-8"?>
        <package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
          <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
            <dc:title>整本一个文件</dc:title>
          </metadata>
          <manifest>
            <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
            <item id="all" href="all.xhtml" media-type="application/xhtml+xml"/>
          </manifest>
          <spine><itemref idref="all"/></spine>
        </package>
        """
        let nav = """
        <?xml version="1.0" encoding="UTF-8"?>
        <html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
        <body><nav epub:type="toc"><ol>
          <li><ol>
            <li><a href="all.xhtml#c1">第 1 章 开头</a></li>
            <li><a href="all.xhtml#c2">第 2 章 中间</a></li>
            <li><a href="all.xhtml#c3">第 3 章 结尾</a></li>
          </ol></li>
        </ol></nav></body></html>
        """
        let all = """
        <?xml version="1.0" encoding="UTF-8"?>
        <html xmlns="http://www.w3.org/1999/xhtml"><head><title>全书</title></head><body>
        <h1 id="c1">第 1 章 开头</h1><p>第一段。</p>
        <h2 id="c2">第 2 章 中间</h2><p>第二段。</p>
        <h2 id="c3">第 3 章 结尾</h2><p>第三段。</p>
        </body></html>
        """
        return try EpubFixture.epub([
            "mimetype": "application/epub+zip",
            "META-INF/container.xml": containerXML,
            "OEBPS/content.opf": opf,
            "OEBPS/nav.xhtml": nav,
            "OEBPS/all.xhtml": all,
        ])
    }

    /// 改编书典型形态：NCX 完全平（所有 navPoint 同级），层级只能从锚点所在标题
    /// （h1/h2/h3）和「第 N 部分 / 附录」这类标题文字推出来。
    static func flatTOCHierarchy() throws -> URL {
        let opf = """
        <?xml version="1.0" encoding="UTF-8"?>
        <package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
          <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
            <dc:title>平目录的书</dc:title>
          </metadata>
          <manifest>
            <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
            <item id="all" href="all.xhtml" media-type="application/xhtml+xml"/>
            <item id="b" href="b.xhtml" media-type="application/xhtml+xml"/>
          </manifest>
          <spine toc="ncx"><itemref idref="all"/><itemref idref="b"/></spine>
        </package>
        """
        let ncx = """
        <?xml version="1.0" encoding="UTF-8"?>
        <ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
          <navMap>
            <navPoint id="p1"><navLabel><text>第一部分 设计原则</text></navLabel>
              <content src="all.xhtml#p0"/></navPoint>
            <navPoint id="p2"><navLabel><text>第 1 章 引言</text></navLabel>
              <content src="all.xhtml#c1"/></navPoint>
            <navPoint id="p3"><navLabel><text>约书亚树</text></navLabel>
              <content src="all.xhtml#s1"/></navPoint>
            <navPoint id="p4"><navLabel><text>一点说明</text></navLabel>
              <content src="all.xhtml#s2"/></navPoint>
            <navPoint id="p5"><navLabel><text>附录 A：术语表</text></navLabel>
              <content src="all.xhtml#a1"/></navPoint>
            <navPoint id="p6"><navLabel><text>续篇</text></navLabel>
              <content src="b.xhtml"/></navPoint>
          </navMap>
        </ncx>
        """
        let all = """
        <?xml version="1.0" encoding="UTF-8"?>
        <html xmlns="http://www.w3.org/1999/xhtml"><head><title>全书</title></head><body>
        <h1 id="p0">第一部分 设计原则</h1><p>部分导语。</p>
        <h1 id="c1">第 1 章 引言</h1><p>第一章正文。</p>
        <h2 id="s1">约书亚树</h2><p>小节正文。</p>
        <h3 id="s2">一点说明</h3><p>更深一层。</p>
        <h1 id="a1">附录 A：术语表</h1><p>附录正文。</p>
        </body></html>
        """
        // 目录条目只指向整个文件（不带 #fragment）：层级只能看这个文件的首个标题
        let b = """
        <?xml version="1.0" encoding="UTF-8"?>
        <html xmlns="http://www.w3.org/1999/xhtml"><head><title>续篇</title></head><body>
        <h2>续篇</h2><p>另一章正文。</p>
        </body></html>
        """
        return try EpubFixture.epub([
            "mimetype": "application/epub+zip",
            "META-INF/container.xml": containerXML,
            "OEBPS/content.opf": opf,
            "OEBPS/toc.ncx": ncx,
            "OEBPS/all.xhtml": all,
            "OEBPS/b.xhtml": b,
        ])
    }

    /// 无章节的书：spine 只有一项、没有目录
    static func singleChapter() throws -> URL {
        let opf = """
        <?xml version="1.0" encoding="UTF-8"?>
        <package xmlns="http://www.idpf.org/2007/opf" version="2.0">
          <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
            <dc:title>单文件长篇</dc:title>
          </metadata>
          <manifest>
            <item id="only" href="all.xhtml" media-type="application/xhtml+xml"/>
          </manifest>
          <spine><itemref idref="only"/></spine>
        </package>
        """
        return try EpubFixture.epub([
            "mimetype": "application/epub+zip",
            "META-INF/container.xml": containerXML,
            "OEBPS/content.opf": opf,
            "OEBPS/all.xhtml": chapter1,
        ])
    }

    /// spine 里混进封面图：非正文项跳过，其余章节照常可读
    static func spineWithImage() throws -> URL {
        let opf = """
        <?xml version="1.0" encoding="UTF-8"?>
        <package xmlns="http://www.idpf.org/2007/opf" version="2.0">
          <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>带封面页的书</dc:title></metadata>
          <manifest>
            <item id="coverpage" href="Images/cover.jpg" media-type="image/jpeg"/>
            <item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/>
          </manifest>
          <spine>
            <itemref idref="coverpage"/>
            <itemref idref="c1"/>
            <itemref idref="missing"/>
          </spine>
        </package>
        """
        return try EpubFixture.epub([
            "mimetype": Data("application/epub+zip".utf8),
            "META-INF/container.xml": Data(containerXML.utf8),
            "OEBPS/content.opf": Data(opf.utf8),
            "OEBPS/c1.xhtml": Data(chapter1.utf8),
            "OEBPS/Images/cover.jpg": Data([0xFF, 0xD8, 0xFF, 0xE0]),
        ])
    }

    /// 空 spine（能解包，但没有可读章节）
    static func emptySpine() throws -> URL {
        let opf = """
        <?xml version="1.0" encoding="UTF-8"?>
        <package xmlns="http://www.idpf.org/2007/opf" version="2.0">
          <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>空书</dc:title></metadata>
          <manifest><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/></manifest>
          <spine></spine>
        </package>
        """
        return try EpubFixture.epub([
            "mimetype": "application/epub+zip",
            "META-INF/container.xml": containerXML,
            "OEBPS/content.opf": opf,
            "OEBPS/c1.xhtml": chapter1,
        ])
    }

    /// 带加密清单的书（正文 / 字体随便，只看 encryption.xml 怎么判）
    private static func encryptedBook(encryption: String) throws -> URL {
        let opf = """
        <?xml version="1.0" encoding="UTF-8"?>
        <package xmlns="http://www.idpf.org/2007/opf" version="2.0">
          <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>加密书</dc:title></metadata>
          <manifest><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/></manifest>
          <spine><itemref idref="c1"/></spine>
        </package>
        """
        return try EpubFixture.epub([
            "mimetype": "application/epub+zip",
            "META-INF/container.xml": containerXML,
            "META-INF/encryption.xml": encryption,
            "OEBPS/content.opf": opf,
            "OEBPS/c1.xhtml": chapter1,
        ])
    }

    /// 真 DRM：正文用 AES 加密
    static func drm() throws -> URL {
        try encryptedBook(encryption: """
        <?xml version="1.0" encoding="UTF-8"?>
        <encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"
                    xmlns:enc="http://www.w3.org/2001/04/xmlenc#">
          <enc:EncryptedData>
            <enc:EncryptionMethod Algorithm="http://www.w3.org/2001/04/xmlenc#aes256-cbc"/>
            <enc:CipherData><enc:CipherReference URI="OEBPS/c1.xhtml"/></enc:CipherData>
          </enc:EncryptedData>
        </encryption>
        """)
    }

    /// 字体混淆（IDPF / Adobe）：正文是明文，只打乱字体内码，必须能导入
    static func fontObfuscated(adobe: Bool = false) throws -> URL {
        let algorithm = adobe
            ? "http://ns.adobe.com/pdf/enc#RC"
            : "http://www.idpf.org/2008/embedding"
        return try encryptedBook(encryption: """
        <?xml version="1.0" encoding="UTF-8"?>
        <encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"
                    xmlns:enc="http://www.w3.org/2001/04/xmlenc#">
          <enc:EncryptedData>
            <enc:EncryptionMethod Algorithm="\(algorithm)"/>
            <enc:CipherData><enc:CipherReference URI="OEBPS/fonts/book.otf"/></enc:CipherData>
          </enc:EncryptedData>
        </encryption>
        """)
    }

    /// 空加密清单：有清单不等于加密
    static func emptyEncryptionList() throws -> URL {
        try encryptedBook(encryption: """
        <?xml version="1.0" encoding="UTF-8"?>
        <encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"/>
        """)
    }

    /// 坏掉的加密清单：看不懂就保守拒绝（当 DRM）
    static func brokenEncryption() throws -> URL {
        try encryptedBook(encryption: "<encryption><EncryptedData>")
    }

    /// 封面做成「封面页 XHTML 里放一张图」的书（没有 manifest `cover-image`）：
    /// guide 指向的是版权页（先有一张 logo），spine 里的 cover.xhtml 才是真封面。
    static func coverPageBook() throws -> URL {
        let opf = """
        <?xml version="1.0" encoding="UTF-8"?>
        <package xmlns="http://www.idpf.org/2007/opf" version="2.0">
          <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>封面页书</dc:title></metadata>
          <manifest>
            <item id="front" href="front.xhtml" media-type="application/xhtml+xml"/>
            <item id="cov" href="cover.xhtml" media-type="application/xhtml+xml"/>
            <item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/>
            <item id="logo" href="Images/logo.jpg" media-type="image/jpeg"/>
            <item id="cover-pic" href="Images/cover-pic.jpg" media-type="image/jpeg"/>
          </manifest>
          <spine>
            <itemref idref="front"/>
            <itemref idref="cov"/>
            <itemref idref="c1"/>
          </spine>
          <guide><reference type="cover" href="front.xhtml"/></guide>
        </package>
        """
        let front = """
        <?xml version="1.0" encoding="UTF-8"?>
        <html xmlns="http://www.w3.org/1999/xhtml"><body>
        <div class="logo"><img src="Images/logo.jpg" alt="logo"/></div>
        </body></html>
        """
        let cover = """
        <?xml version="1.0" encoding="UTF-8"?>
        <html xmlns="http://www.w3.org/1999/xhtml"><body>
        <div class="cover"><img src="Images/cover-pic.jpg" alt="封面"/></div>
        </body></html>
        """
        return try EpubFixture.epub([
            "mimetype": Data("application/epub+zip".utf8),
            "META-INF/container.xml": Data(containerXML.utf8),
            "OEBPS/content.opf": Data(opf.utf8),
            "OEBPS/front.xhtml": Data(front.utf8),
            "OEBPS/cover.xhtml": Data(cover.utf8),
            "OEBPS/c1.xhtml": Data(chapter1.utf8),
            "OEBPS/Images/logo.jpg": Data([0xFF, 0xD8, 0xFF, 0xE0]),
            "OEBPS/Images/cover-pic.jpg": Data([0xFF, 0xD8, 0xFF, 0xE1]),
        ])
    }

    /// 扉页 / 版权页这类没有标题的页：章节标题按 href 关键词兜底，不删章节
    static func frontMatterBook() throws -> URL {
        let opf = """
        <?xml version="1.0" encoding="UTF-8"?>
        <package xmlns="http://www.idpf.org/2007/opf" version="2.0">
          <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>前置页书</dc:title></metadata>
          <manifest>
            <item id="cover" href="cover.xhtml" media-type="application/xhtml+xml"/>
            <item id="title" href="titlepage.xhtml" media-type="application/xhtml+xml"/>
            <item id="copy" href="copyright.xhtml" media-type="application/xhtml+xml"/>
            <item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/>
          </manifest>
          <spine>
            <itemref idref="cover"/>
            <itemref idref="title"/>
            <itemref idref="copy"/>
            <itemref idref="c1"/>
          </spine>
        </package>
        """
        let bare = """
        <?xml version="1.0" encoding="UTF-8"?>
        <html xmlns="http://www.w3.org/1999/xhtml"><body><div><img src="x.jpg"/></div></body></html>
        """
        return try EpubFixture.epub([
            "mimetype": "application/epub+zip",
            "META-INF/container.xml": containerXML,
            "OEBPS/content.opf": opf,
            "OEBPS/cover.xhtml": bare,
            "OEBPS/titlepage.xhtml": bare,
            "OEBPS/copyright.xhtml": bare,
            "OEBPS/c1.xhtml": chapter1,
        ])
    }

    /// 固定版式（画册）
    static func fixedLayout() throws -> URL {
        try epub3(extraMetadata: #"<meta property="rendition:layout" content="pre-paginated"/>"#)
    }

    /// 目录里带越界条目（zip slip）：`../../evil.txt` 解到 `content/` 后是 `Books/evil.txt`。
    /// 注意 `unzip` 会剥掉 `../../` 前缀，所以这条只钉住「预检阶段抛错」，钉不住「解包前」。
    static func zipSlip() throws -> URL {
        let root = EpubFixture.tempDir()
        let fm = FileManager.default
        let src = root.appendingPathComponent("src", isDirectory: true)
        try fm.createDirectory(at: src, withIntermediateDirectories: true)
        // 源文件放在 src 的上两级（= fixture 根），条目名才会是 `../../evil.txt`
        let evil = root.deletingLastPathComponent().appendingPathComponent("epub-slip-evil.txt")
        try Data("evil".utf8).write(to: evil)
        defer { try? fm.removeItem(at: evil) }
        let dest = root.appendingPathComponent("slip.epub")
        try EpubFixture.run("/usr/bin/zip", ["-q", "-X", dest.path, "../../epub-slip-evil.txt"], cwd: src)
        return dest
    }
}
