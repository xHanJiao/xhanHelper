#!/usr/bin/env python3
"""从 DocumentReaderView.swift 里抠出阅读版 HTML 模板并渲染成可跑的页面，写到 stdout。

模板是嵌在 Swift 源码里的一整块字符串（`private let template = #\"\"\" ... \"\"\"#`），
三个自查脚本（check-reader-html.sh / snapshot-reader.sh / book-reader-probe.sh）都要用它。
抠取只在这里做一次 —— 抠的是源文件本身，所以模板改了这里跟着变，不存在副本漂移。

两种模式：
  doc （默认）渲染 markdown 正文
  book（首参 --book）不渲染正文，由探针脚本调用 reader.setChapter 灌章节

用法:
  reader-template.py [--book] <marked.js> <title> <tags-html> <md-file> <images-json> [fs]
"""
import html
import json
import os
import re
import sys

SRC = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                   '..', 'xHelper', 'Shared', 'Views', 'DocumentReaderView.swift')

BOOK_JSON = {'fs': 20, 'line': 1.78, 'para': 0.55, 'measure': 728,
             'font': '-apple-system, "PingFang SC", sans-serif', 'mode': 'scroll'}


def render(marked_path, title, tags_html, md, images, fs='20.06', mode='doc'):
    src = open(SRC, encoding='utf-8').read()
    m = re.search(r'private let template = #"""\n(.*?)\n"""#', src, re.S)
    if not m:
        sys.exit('没能从 Swift 源码里抠出 template 字面量')
    book = mode == 'book'
    header = '' if book else (
        '<div class="doc-title">%s</div>\n'
        '<div class="doc-tags">%s</div>\n'
        '<hr class="doc-rule">' % (html.escape(title), tags_html)
    )
    return (m.group(1)
            .replace('__NONCE__', 'testnonce')
            .replace('__MODE__', 'book' if book else 'doc')
            .replace('__HEADER_HTML__', header)
            .replace('__BOOK_JSON__', json.dumps(BOOK_JSON, ensure_ascii=False) if book else 'null')
            .replace('__FS__', fs)
            .replace('__MARKED_JS__', '' if book else open(marked_path, encoding='utf-8').read())
            .replace('__MD_JSON__', 'null' if book else
                     json.dumps(md, ensure_ascii=False).replace('</', '<\\/'))
            .replace('__IMAGES_JSON__', '[]' if book else
                     json.dumps(images, ensure_ascii=False).replace('</', '<\\/')))


if __name__ == '__main__':
    args = sys.argv[1:]
    mode = 'doc'
    if args and args[0] == '--book':
        mode = 'book'
        args = args[1:]
    if len(args) < 5:
        sys.exit(__doc__)
    sys.stdout.write(render(args[0], args[1], args[2], open(args[3], encoding='utf-8').read(),
                            json.loads(args[4]), args[5] if len(args) > 5 else '20.06',
                            mode))
