#!/usr/bin/env python3
"""从 DocumentReaderView.swift 里抠出阅读版 HTML 模板并渲染成可跑的页面，写到 stdout。

模板是嵌在 Swift 源码里的一整块字符串（`private let template = #\"\"\" ... \"\"\"#`），
两个自查脚本（check-reader-html.sh / snapshot-reader.sh）都要用它。抠取只在这里做一次 ——
抠的是源文件本身，所以模板改了这里跟着变，不存在副本漂移。

用法:
  reader-template.py <marked.js> <title> <tags-html> <md-file> <images-json> [fs]
"""
import json
import os
import re
import sys

SRC = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                   '..', 'xHelper', 'Shared', 'Views', 'DocumentReaderView.swift')


def render(marked_path, title, tags_html, md, images, fs='20.06'):
    src = open(SRC, encoding='utf-8').read()
    m = re.search(r'private let template = #"""\n(.*?)\n"""#', src, re.S)
    if not m:
        sys.exit('没能从 Swift 源码里抠出 template 字面量')
    return (m.group(1)
            .replace('__FS__', fs)
            .replace('__MARKED_JS__', open(marked_path, encoding='utf-8').read())
            .replace('__TITLE_HTML__', title)
            .replace('__TAGS_HTML__', tags_html)
            .replace('__MD_JSON__', json.dumps(md, ensure_ascii=False))
            .replace('__IMAGES_JSON__', json.dumps(images, ensure_ascii=False)))


if __name__ == '__main__':
    a = sys.argv[1:]
    if len(a) < 5:
        sys.exit(__doc__)
    sys.stdout.write(render(a[0], a[1], a[2], open(a[3], encoding='utf-8').read(),
                            json.loads(a[4]), a[5] if len(a) > 5 else '20.06'))
