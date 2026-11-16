#!/usr/bin/env python3
"""生成 EPUB 阅读器的测试书（纯标准库，不依赖 PIL/zip 外部命令）。

用法：
    python3 client/xHelper/scripts/make-test-epubs.py [--out 目录]

默认输出到 ~/Downloads/xhan-epub-tests/。四本书分别覆盖：
  reader-test-main.epub              脚注 / 链接 / 内联图 / ruby / 宽表 / 隐藏内容
  reader-test-cover-only.epub        只有 cover.xhtml（无 manifest cover-image）
  reader-test-font-obfuscation.epub  字体混淆 encryption.xml（当前应被拒，批 17 后放行）
  reader-test-parser-edge.epub       nav UTF-16 + 三层 wrapper + 前缀标签 + script

注意：仓库不提交生成的 .epub；这个脚本是唯一来源，改了内容重跑即可。
"""

import argparse
import pathlib
import struct
import zipfile
import zlib

EPS = 220  # 每个大段落的近似字数（中文按 1 字算）


# ---------------------------------------------------------------- PNG

def png(width, height, pixel):
    """pixel(x, y) -> (r, g, b, a)，返回 PNG 字节（RGBA）。"""
    raw = bytearray()
    for y in range(height):
        raw.append(0)  # filter: none
        for x in range(width):
            raw.extend(pixel(x, y))

    def chunk(tag, data):
        return (struct.pack(">I", len(data)) + tag + data
                + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF))

    ihdr = struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0)
    return (b"\x89PNG\r\n\x1a\n"
            + chunk(b"IHDR", ihdr)
            + chunk(b"IDAT", zlib.compress(bytes(raw), 9))
            + chunk(b"IEND", b""))


def cover_png(w=600, h=900):
    def pix(x, y):
        # 暖色渐变底
        t = y / max(1, h - 1)
        r = int(250 - 30 * t)
        g = int(242 - 34 * t)
        b = int(226 - 30 * t)
        # 顶部主题绿圆
        cx, cy, rad = w // 2, int(h * 0.30), int(w * 0.28)
        if (x - cx) ** 2 + (y - cy) ** 2 < rad * rad:
            return (47, 125, 99, 255)
        # 底部纸色带
        if y > h * 0.80:
            return (247, 238, 221, 255)
        # 两条书脊装饰线
        if 0.76 <= y / h <= 0.765 or 0.80 <= y / h <= 0.805:
            return (59, 46, 31, 120)
        return (r, g, b, 255)
    return png(w, h, pix)


def note_png(size=64):
    def pix(x, y):
        d = (x - size / 2) ** 2 + (y - size / 2) ** 2
        if d < (size * 0.42) ** 2:
            return (47, 125, 99, 255)
        if d < (size * 0.48) ** 2:
            return (255, 255, 255, 255)
        return (0, 0, 0, 0)
    return png(size, size, pix)


def icon_png(size=24):
    def pix(x, y):
        if 3 <= x < size - 3 and 3 <= y < size - 3:
            if size * 0.42 <= x <= size * 0.58 and size * 0.42 <= y <= size * 0.58:
                return (255, 255, 255, 255)
            return (65, 201, 226, 255)
        return (0, 0, 0, 0)
    return png(size, size, pix)


def figure_png(w=480, h=280):
    def pix(x, y):
        t = x / max(1, w - 1)
        r = int(120 + 100 * t)
        g = int(160 + 70 * t)
        b = int(190 - 40 * t)
        if (x - w * 0.32) ** 2 + (y - h * 0.42) ** 2 < 60 ** 2:
            return (247, 238, 221, 255)
        return (r, g, b, 255)
    return png(w, h, pix)


# ---------------------------------------------------------------- EPUB 骨架

CONTAINER = """<?xml version="1.0" encoding="utf-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
"""

BOOK_CSS = """/* 出版社样式：第二轮只用来观察「书 CSS 被丢弃」的影响 */
body { font-family: serif; }
p { text-indent: 2em; margin: 0.3em 0; }
p.hidden-by-css { display: none; }
.duokan-footnote-content { display: none; }
sup { vertical-align: super; font-size: 0.75em; }
.center { text-align: center; }
"""


def xhtml(title, body, lang="zh", extra_xmlns=""):
    return f"""<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="{lang}"{extra_xmlns}>
<head>
  <meta charset="utf-8"/>
  <title>{title}</title>
  <link rel="stylesheet" type="text/css" href="../Styles/book.css"/>
</head>
<body>
{body}
</body>
</html>
"""


def nav_xhtml(entries, encoding="utf-8"):
    """entries: [(label, href, children)]"""
    def lis(items, indent):
        out = []
        pad = "  " * indent
        for label, href, kids in items:
            out.append(f'{pad}<li><a href="{href}">{label}</a>')
            if kids:
                out.append(f"{pad}  <ol>")
                out.extend(lis(kids, indent + 2))
                out.append(f"{pad}  </ol>")
            out.append(f"{pad}</li>")
        return out

    lines = [
        f'<?xml version="1.0" encoding="{encoding}"?>',
        '<!DOCTYPE html>',
        '<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">',
        '<head><meta charset="utf-8"/><title>目录</title></head>',
        '<body>',
        '  <nav epub:type="toc" id="toc">',
        '    <h1>目录</h1>',
        '    <ol>',
    ]
    lines.extend(lis(entries, 3))
    lines.extend(['    </ol>', '  </nav>', '</body>', '</html>'])
    return "\n".join(lines) + "\n"


def opf(title, manifest, spine, lang="zh", creator="测试书生成器"):
    man = "\n".join(
        f'    <item id="{iid}" href="{href}" media-type="{media}"'
        + (f' properties="{props}"' if props else "")
        + "/>"
        for iid, href, media, props in manifest
    )
    sp = "\n".join(
        f'    <itemref idref="{iid}"' + (' linear="no"' if linear == "no" else "") + "/>"
        for iid, linear in spine
    )
    return f"""<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid" xml:lang="{lang}">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:uuid:{zlib.crc32(title.encode('utf-8')):08x}-0000-4000-8000-000000000000</dc:identifier>
    <dc:title>{title}</dc:title>
    <dc:creator>{creator}</dc:creator>
    <dc:language>{lang}</dc:language>
    <meta property="dcterms:modified">2026-11-15T00:00:00Z</meta>
  </metadata>
  <manifest>
{man}
  </manifest>
  <spine>
{sp}
  </spine>
</package>
"""


def write_epub(path, files):
    """files: {相对路径: bytes|str}；mimetype 必须是第一个且不压缩。"""
    path.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(path, "w") as z:
        info = zipfile.ZipInfo("mimetype")
        info.compress_type = zipfile.ZIP_STORED
        z.writestr(info, b"application/epub+zip")
        for name, data in files.items():
            if isinstance(data, str):
                data = data.encode("utf-8")
            z.writestr(name, data, compress_type=zipfile.ZIP_DEFLATED)


# ---------------------------------------------------------------- 正文素材

LONG = ("山里的冬天来得早。清晨推开门，院子里的水缸结了一层薄冰，"
        "远处的富士山被雪压得很低，像一张摊开的纸。屋里炉火烧得旺，"
        "水壶咕嘟咕嘟响，猫蜷在炉边不肯挪窝。今天要把腌菜的石板搬进屋，"
        "再把晒了一冬的萝卜收起来。午后也许会下山，去村里买些豆腐和酒。")


def duplex_paragraph(i, anchor=None):
    text = LONG * 2
    aid = f' id="{anchor}"' if anchor else ""
    return f'<p{aid}>{text}（第 {i} 段）</p>'


def big_chapter_body():
    """三层 wrapper + 锚点 + 靠后的注释 + script 里的假标签。"""
    parts = []
    for i in range(1, 161):
        if i in (1, 21, 41, 61, 81, 101, 121, 141):
            n = (i - 1) // 20 + 1
            parts.append(f'<h2 id="s{n}">第 {n} 节</h2>')
        parts.append(duplex_paragraph(i, "big-top" if i == 2 else None))
        if i == 3:
            parts.append(
                '<p>这一句有一个指向本章末尾的注释'
                '<sup><a epub:type="noteref" href="#big-end-note">*</a></sup>。</p>'
            )
        if i == 60:
            parts.append(
                '<script type="text/javascript"><![CDATA['
                'if (1 < 2) { var fake = "<p>这是脚本字符串，不是正文</p>"; }'
                ']]></script>'
            )
        if i == 150:
            parts.append(
                '<aside epub:type="footnote" id="big-end-note">'
                '<p>这条注释放文件靠后，离引用处很远；大章被切成多节后，'
                '它应该落在后面的节里（用来测同文件跨节注释）。</p>'
                '</aside>'
            )
    inner = "\n".join(parts)
    return f'<h1>第三章 大章与跨节注释</h1>\n<div class="w1">\n<div class="w2">\n<div class="w3">\n{inner}\n</div>\n</div>\n</div>'


def wide_table():
    rows = []
    for r in range(1, 5):
        cells = "".join(
            f'<td nowrap="nowrap">第 {c} 列 · 一段比较长的单元格文字 {r}</td>'
            for c in range(1, 11)
        )
        rows.append(f"<tr>{cells}</tr>")
    return (
        "<table><thead><tr>"
        + "".join(f'<th nowrap="nowrap">列 {c} 标题</th>' for c in range(1, 11))
        + "</tr></thead><tbody>"
        + "".join(rows)
        + "</tbody></table>"
    )


# ---------------------------------------------------------------- 四本书

def main_book():
    files = {}
    files["META-INF/container.xml"] = CONTAINER
    files["OEBPS/Styles/book.css"] = BOOK_CSS
    files["OEBPS/Images/cover.png"] = cover_png()
    files["OEBPS/Images/note.png"] = note_png()
    files["OEBPS/Images/icon.png"] = icon_png()
    files["OEBPS/Images/figure.png"] = figure_png()

    files["OEBPS/Text/cover.xhtml"] = xhtml("封面", """
<div class="center">
  <img src="../Images/cover.png" alt="测试书封面"/>
</div>
""")

    files["OEBPS/Text/ch1.xhtml"] = xhtml("第一章 行内注释", """
<h1>第一章 行内注释</h1>

<p>这一段有图片注标：正文<sup><a epub:type="noteref" href="#fn-duokan-1"><img src="../Images/note.png" alt="这是多看式脚注的完整文本，会被塞进图片 alt 里" class="epub-footnote"/></a></sup>，后面还有字。</p>
<aside epub:type="footnote" id="fn-duokan-1"><ol class="duokan-footnote-content"><li class="duokan-footnote-item">这是多看式脚注，应该弹底部卡片，而不是排在正文里。</li></ol></aside>

<p>这一段有文字注标：正文<sup><a epub:type="noteref" href="#fn-text-2">2</a></sup>。</p>
<aside epub:type="footnote" id="fn-text-2"><p>这是标准 EPUB3 脚注，带 <strong>加粗</strong> 与一个注内图片 <img src="../Images/icon.png" alt="注内图"/>。</p></aside>

<p>跨文件尾注：正文<sup><a epub:type="noteref" href="notes.xhtml#en-1">3</a></sup>，尾注文件在 spine 里是 linear="no"。</p>

<p>无类型 sup 引用（应该跳到下面的普通段落，不弹注释）：<sup><a href="#plain-target">4</a></sup>。</p>

<p id="plain-target">这个段落是普通正文，不是注释，不应该弹注释卡片。</p>

<p>外链：<a href="https://example.com/x?y=1#z">example.com</a>；书内：<a href="ch2.xhtml#sec2">跳到第二章</a>；页内：<a href="#local-anchor">页内锚点</a>。</p>

<p id="local-anchor">页内锚点到达处。</p>

<p>行内图：<img src="../Images/icon.png" alt="行内图标"/> 后面继续文字，图片应该和文字同一行。</p>

<p><img src="../Images/figure.png" alt="独占图"/></p>

<figure>
  <img src="../Images/figure.png" alt="figure 图"/>
  <figcaption>这是一张独占图，图注应在下方。</figcaption>
</figure>

<p>破图：<img src="../Images/missing.png" alt="这张图故意不存在"/>。</p>

<p>日文注音：<ruby>東京<rt>とうきょう</rt></ruby> と <ruby>富士山<rt>ふじさん</rt></ruby>。</p>

<p class="hidden-by-css">这段在出版社 CSS 里是 display:none；按现状会露出来（已知取舍）。</p>

<p>音视频：<audio src="../Audio/demo.mp3">音频</audio>（应该被移除，不留空壳）。</p>

<pre><code>code block
line 2</code></pre>

<blockquote>引用块：应该保持左边线。</blockquote>
""")

    files["OEBPS/Text/ch2.xhtml"] = xhtml("第二章 引用与链接", """
<h1 id="sec2">第二章 引用与链接</h1>

<p>role 注标：<span role="doc-noteref"><a href="#role-note">注</a></span>。</p>
<aside role="doc-footnote" id="role-note"><p>用 ARIA role 写的注释。</p></aside>

<p>返回第一章：<a href="ch1.xhtml">ch1</a>。</p>

<p>同文件锚点：<a href="#sec2-end">到本章末尾</a>。</p>

<p>一条带多段的注释：<sup><a epub:type="noteref" href="#multi">5</a></sup></p>
<aside epub:type="footnote" id="multi">
  <p>第一段：注释可以有多个段落。</p>
  <p>第二段：也可以有 <em>斜体</em>。</p>
</aside>

<p id="sec2-end">本章末尾。</p>
""")

    files["OEBPS/Text/big.xhtml"] = xhtml("第三章 大章与跨节注释", big_chapter_body())

    files["OEBPS/Text/appendix.xhtml"] = xhtml("附录 宽表与基础标签", f"""
<h1>附录 宽表与基础标签</h1>
<p>下面这张表有 10 列，每列都很宽；翻页模式应该能横向滚动（或按降级方案缩字号）。</p>
{wide_table()}
<dl>
  <dt>面</dt><dd>我自己能闭环验证的接线。</dd>
  <dt>缝</dt><dd>必须真跑才知道对错的地方。</dd>
</dl>
<p>行内代码 <code>reader.noteFile</code> 与 <a href="https://example.org/">外链</a>。</p>
""")

    files["OEBPS/Text/notes.xhtml"] = xhtml("尾注", """
<h1>尾注</h1>
<section epub:type="endnotes">
  <ol>
    <li id="en-1"><p>这是独立文件里的尾注 1，带 <em>斜体</em>。</p></li>
    <li id="en-2"><p>这是尾注 2。</p></li>
    <li id="en-3"><p>这是尾注 3。</p></li>
  </ol>
</section>
""")

    nav = nav_xhtml([
        ("封面", "Text/cover.xhtml", []),
        ("第一章 行内注释", "Text/ch1.xhtml", []),
        ("第二章 引用与链接", "Text/ch2.xhtml", []),
        ("第三章 大章与跨节注释", "Text/big.xhtml", [
            ("第 1 节", "Text/big.xhtml#s1", []),
            ("第 2 节", "Text/big.xhtml#s2", []),
            ("第 3 节", "Text/big.xhtml#s3", []),
            ("第 4 节", "Text/big.xhtml#s4", []),
            ("第 5 节", "Text/big.xhtml#s5", []),
            ("第 6 节", "Text/big.xhtml#s6", []),
            ("第 7 节", "Text/big.xhtml#s7", []),
            ("第 8 节", "Text/big.xhtml#s8", []),
        ]),
        ("附录 宽表与基础标签", "Text/appendix.xhtml", []),
    ])
    files["OEBPS/nav.xhtml"] = nav

    files["OEBPS/content.opf"] = opf(
        "EPUB 阅读器测试书",
        [
            ("nav", "nav.xhtml", "application/xhtml+xml", "nav"),
            ("css", "Styles/book.css", "text/css", ""),
            ("cover-img", "Images/cover.png", "image/png", "cover-image"),
            ("cover", "Text/cover.xhtml", "application/xhtml+xml", ""),
            ("ch1", "Text/ch1.xhtml", "application/xhtml+xml", ""),
            ("ch2", "Text/ch2.xhtml", "application/xhtml+xml", ""),
            ("big", "Text/big.xhtml", "application/xhtml+xml", ""),
            ("appendix", "Text/appendix.xhtml", "application/xhtml+xml", ""),
            ("notes", "Text/notes.xhtml", "application/xhtml+xml", ""),
        ],
        [("cover", ""), ("ch1", ""), ("ch2", ""), ("big", ""), ("appendix", ""), ("notes", "no")],
    )
    return files


def cover_only_book():
    files = {
        "META-INF/container.xml": CONTAINER,
        "OEBPS/Styles/book.css": BOOK_CSS,
        "OEBPS/Images/cover.png": cover_png(),
        "OEBPS/Text/cover.xhtml": xhtml("封面", """
<div class="center">
  <svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"
       width="100%" height="100%" viewBox="0 0 600 900" preserveAspectRatio="xMidYMid meet">
    <image width="600" height="900" xlink:href="../Images/cover.png"/>
  </svg>
</div>
"""),
        "OEBPS/Text/ch1.xhtml": xhtml("第一章", "<h1>第一章</h1><p>这是一本只有 cover.xhtml、没有 manifest cover-image 的书；书架封面应该从封面页里提取。</p>"),
        "OEBPS/nav.xhtml": nav_xhtml([
            ("封面", "Text/cover.xhtml", []),
            ("第一章", "Text/ch1.xhtml", []),
        ]),
    }
    files["OEBPS/content.opf"] = opf(
        "封面页测试书",
        [
            ("nav", "nav.xhtml", "application/xhtml+xml", "nav"),
            ("css", "Styles/book.css", "text/css", ""),
            ("coverimg", "Images/cover.png", "image/png", ""),
            ("cover", "Text/cover.xhtml", "application/xhtml+xml", ""),
            ("ch1", "Text/ch1.xhtml", "application/xhtml+xml", ""),
        ],
        [("cover", ""), ("ch1", "")],
    )
    return files


def font_book():
    encryption = """<?xml version="1.0" encoding="utf-8"?>
<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"
            xmlns:enc="http://www.w3.org/2001/04/xmlenc#">
  <enc:EncryptedData>
    <enc:EncryptionMethod Algorithm="http://www.idpf.org/2008/embedding"/>
    <enc:CipherData>
      <enc:CipherReference URI="OEBPS/Fonts/test.ttf"/>
    </enc:CipherData>
  </enc:EncryptedData>
</encryption>
"""
    files = {
        "META-INF/container.xml": CONTAINER,
        "META-INF/encryption.xml": encryption,
        "OEBPS/Styles/book.css": BOOK_CSS,
        "OEBPS/Fonts/test.ttf": b"\x00\x01\x00\x00" + b"fake-font" * 20,
        "OEBPS/Text/ch1.xhtml": xhtml("字体混淆", "<h1>字体混淆</h1><p>这本 EPUB 的 encryption.xml 只声明了字体混淆；批 17 之后应该能导入，字体本身会被忽略。</p>"),
        "OEBPS/nav.xhtml": nav_xhtml([("第一章", "Text/ch1.xhtml", [])]),
    }
    files["OEBPS/content.opf"] = opf(
        "字体混淆测试书",
        [
            ("nav", "nav.xhtml", "application/xhtml+xml", "nav"),
            ("css", "Styles/book.css", "text/css", ""),
            ("font", "Fonts/test.ttf", "font/ttf", ""),
            ("ch1", "Text/ch1.xhtml", "application/xhtml+xml", ""),
        ],
        [("ch1", "")],
    )
    return files


def parser_edge_book():
    parts = []
    for i in range(1, 141):
        if i in (1, 21, 41, 61, 81, 101, 121):
            n = (i - 1) // 20 + 1
            parts.append(f'<h2 id="p{n}">第 {n} 节</h2>')
        if i in (131, 132, 133):
            # 只有末尾几段用前缀标签：验证 scanBody/topLevelRanges 的去前缀，不把整章排版弄坏
            parts.append(f'<xhtml:p>{LONG}（前缀段落 {i}）<xhtml:br /></xhtml:p>')
        else:
            parts.append(f'<p>{LONG * 2}（第 {i} 段）</p>')
        if i == 30:
            parts.append(
                '<script type="text/javascript">'
                'if (1 < 2) { var fake = "<p>脚本里的假段落，不应参与切节</p>"; }'
                '</script>'
            )
    body = (
        "<h1>解析边界</h1>"
        '<div class="a"><div class="b"><div class="c">'
        + "\n".join(parts)
        + "</div></div></div>"
    )
    chapter = xhtml(
        "解析边界", body, extra_xmlns=' xmlns:xhtml="http://www.w3.org/1999/xhtml"'
    )

    nav_text = nav_xhtml([
        ("解析边界", "Text/ch1.xhtml", [
            (f"第 {n} 节", f"Text/ch1.xhtml#p{n}", []) for n in range(1, 7)
        ]),
    ], encoding="utf-16")
    # nav 故意用 UTF-16LE + BOM：当前 navTOC 只吃 UTF-8，会退回 NCX；没有 NCX → 目录为空
    nav_bytes = nav_text.encode("utf-16")

    files = {
        "META-INF/container.xml": CONTAINER,
        "OEBPS/Styles/book.css": BOOK_CSS,
        "OEBPS/Text/ch1.xhtml": chapter,
        "OEBPS/nav.xhtml": nav_bytes,
    }
    files["OEBPS/content.opf"] = opf(
        "解析边界测试书",
        [
            ("nav", "nav.xhtml", "application/xhtml+xml", "nav"),
            ("css", "Styles/book.css", "text/css", ""),
            ("ch1", "Text/ch1.xhtml", "application/xhtml+xml", ""),
        ],
        [("ch1", "")],
    )
    return files


# ---------------------------------------------------------------- 校验 / 入口

def verify(path):
    with zipfile.ZipFile(path) as z:
        bad = z.testzip()
        if bad:
            raise SystemExit(f"{path.name}: 坏条目 {bad}")
        names = z.namelist()
        if names[0] != "mimetype" or z.getinfo("mimetype").compress_type != zipfile.ZIP_STORED:
            raise SystemExit(f"{path.name}: mimetype 不是第一个/未压缩条目")
        if z.read("mimetype") != b"application/epub+zip":
            raise SystemExit(f"{path.name}: mimetype 内容不对")
        return len(names), path.stat().st_size


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default=str(pathlib.Path.home() / "Downloads" / "xhan-epub-tests"))
    args = ap.parse_args()
    out = pathlib.Path(args.out)
    out.mkdir(parents=True, exist_ok=True)

    books = {
        "reader-test-main.epub": main_book(),
        "reader-test-cover-only.epub": cover_only_book(),
        "reader-test-font-obfuscation.epub": font_book(),
        "reader-test-parser-edge.epub": parser_edge_book(),
    }
    readme = """# EPUB 阅读器测试书

由 `client/xHelper/scripts/make-test-epubs.py` 生成，仓库不提交这些 .epub。

| 文件 | 看什么 |
|---|---|
| `reader-test-main.epub` | 第一章：多看式图片注标、标准 aside、文字注标、跨文件尾注（linear=no）、无类型 sup（不应弹注释）、外链、书内/页内链接；行内图 vs 独占图；破图；ruby；出版社 CSS 隐藏段；audio 空壳。第二章：role 注标、多段注释。第三章：三层 wrapper 大章、同文件跨节注释。附录：10 列宽表、dl。目录：8 个节锚点（批 17 前大章可能只有一节） |
| `reader-test-cover-only.epub` | 只有 cover.xhtml（SVG image），没有 manifest cover-image。批 17 前书架没封面，批 17 后应该从封面页提取 |
| `reader-test-font-obfuscation.epub` | encryption.xml 只声明字体混淆。批 17 前会被当成 DRM 拒收；批 17 后应能导入（字体忽略） |
| `reader-test-parser-edge.epub` | nav 是 UTF-16（声明也写 utf-16）；正文三层 wrapper + `<xhtml:p>` 前缀标签 + 大章切节；script 里故意有裸 `<`（不完全合法的 XHTML，用来压 topLevelRanges）。批 17 前目录为空/大章不切；批 17 后目录出现、按 8 个锚点切节 |

## 建议顺序

1. 先导 `main`：看正文里注释是否被排成「1. 1. 1.」、注标是否被顶成单独一行、图片是否全变块级。
2. 再导 `cover-only` / `font-obfuscation` / `parser-edge`，分别确认「没封面 / DRM 拒收 / 目录空」三个基线。
3. 批 16、17 合并后重新导入（或打开）对照设计文档 §七 的验收清单。
"""
    (out / "README.md").write_text(readme, encoding="utf-8")
    for name, files in books.items():
        path = out / name
        write_epub(path, files)
        count, size = verify(path)
        print(f"{path}  ({count} 个条目, {size / 1024:.1f} KB)")
    print("\n说明书: " + str(out / "README.md"))
    print("导入后按 docs/design/epub-reader/round-2-compat.md §七 的清单看。")


if __name__ == "__main__":
    main()
