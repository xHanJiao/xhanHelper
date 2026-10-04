#!/usr/bin/env python3
"""仓库开发节奏统计：提交节奏 / 代码量 / 按周 / 按区域 / 热点文件 / 类型分布 / 时段 / 体量 / 大 commit。

用法：
    python3 scripts/dev-stats.py                        # 全历史
    python3 scripts/dev-stats.py --months 3             # 近 3 个月
    python3 scripts/dev-stats.py --since 2026-07-01     # 指定起点（也接受 3.months.ago 这类相对写法）
    python3 scripts/dev-stats.py --out docs/stats/dev-stats.md   # 写文件（不传则打印到 stdout）
    python3 scripts/dev-stats.py --selftest             # 解析器自检
"""

import argparse
import calendar
import re
import subprocess
import sys
from collections import Counter, defaultdict
from datetime import date, datetime

# 不计入「手写代码」的路径，改判定只改这几处。
#  - vendored：第三方源码整包导入
#  - generated：提交进仓库的运行时产物 / 锁文件（不是人写的）
VENDOR_PREFIXES = ("client/packages/", "client/xHelper/xHelperMac/Resources/Readability.js")
GENERATED_PREFIXES = ("server/data/",)
GENERATED_SUFFIXES = ("package-lock.json", ".lock")
VENDOR_LABEL = "vendored (第三方)"
GENERATED_LABEL = "生成物 / 锁文件"
AREAS = ["server", "client", "front-end", "docs"]

# git log 的单次扫描格式。刻意不用 -z：-z 遇到 rename 会把路径拆成两个 NUL 分片
# （"<ins>\t<del>\t" + 前像 + 后像），普通输出则是天然单行的 "<ins>\t<del>\t<path>"，
# split("\t", 2) 一次搞定。quotePath=false 让中文路径保持可读（否则是八进制转义）。
LOG_FORMAT = "C:%H|%ad|%P|%at|%an|%s"
LOG_ARGS = ["-c", "core.quotePath=false", "log", "--numstat",
            "--date=format:%Y-%m-%d", "--pretty=format:" + LOG_FORMAT]

_RENAME_RE = re.compile(r"\{[^{}]*=>\s*([^{}]*)\}")


def normalize_path(path):
    """git 的 rename 路径写作 `prefix{旧 => 新}suffix`，归一到目标路径再分类，否则 `{a => b}` 会被当成一个目录名。"""
    return _RENAME_RE.sub(r"\1", path)


def die(msg, code=1):
    sys.stderr.write("dev-stats: %s\n" % msg)
    sys.exit(code)


def git(args):
    p = subprocess.run(["git"] + args, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if p.returncode != 0:
        die("git %s 失败：%s" % (" ".join(args), p.stderr.decode("utf-8", "replace").strip()))
    return p.stdout


def is_vendor(path):
    return any(path.startswith(p) for p in VENDOR_PREFIXES)


def is_generated(path):
    return path.startswith(GENERATED_PREFIXES) or path.endswith(GENERATED_SUFFIXES)


def is_hand(path):
    return not is_vendor(path) and not is_generated(path)


def area_of(path):
    if is_vendor(path):
        return VENDOR_LABEL
    if is_generated(path):
        return GENERATED_LABEL
    return path.split("/")[0] if "/" in path else "(根)"


def parse_log(raw):
    """解析 `git log --numstat --pretty=format:C:...` 的输出，返回 commit dict 列表（新→旧）。

    每行要么是 `C:hash|date|parents|ts|author|subject`（commit header），
    要么是 `<ins>\\t<del>\\t<path>`（文件记录，rename 时 path 形如 `a/{ => b}/c`）。
    """
    commits = []
    cur = None
    for line in raw.decode("utf-8", "replace").splitlines():
        if line.startswith("C:"):
            parts = line[2:].split("|", 5)
            if len(parts) != 6:
                cur = None
                continue
            sha, day, parents, ts, author, subject = parts
            cur = {
                "sha": sha,
                "day": day,
                "parents": len(parents.split()) if parents else 0,
                "ts": int(ts),
                "author": author,
                "subject": subject,
                "numstat": [],
                "ins": 0,
                "del": 0,
                "binary": 0,
            }
            commits.append(cur)
            continue
        if cur is None or "\t" not in line:
            continue
        fields = line.split("\t", 2)
        if len(fields) != 3:
            continue
        path = normalize_path(fields[2])
        if not fields[0].isdigit() or not fields[1].isdigit():
            cur["binary"] += 1  # 二进制：行数无意义，不计
            continue
        ins, dele = int(fields[0]), int(fields[1])
        cur["numstat"].append((ins, dele, path))
        cur["ins"] += ins
        cur["del"] += dele
    return commits


def churn(commits, keep):
    """按 keep(path) 过滤后累加 (+ins, -del)。"""
    i = d = 0
    for c in commits:
        for ins, dele, path in c["numstat"]:
            if keep(path):
                i += ins
                d += dele
    return i, d


def hand_churn(c):
    return churn([c], is_hand)


def week_of(day):
    d = datetime.strptime(day, "%Y-%m-%d").date()
    y, w, _ = d.isocalendar()
    return "%04d-W%02d" % (y, w)


def shift_months(d, n):
    q, m0 = divmod(d.month - 1 + n, 12)
    y, m = d.year + q, m0 + 1
    return date(y, m, min(d.day, calendar.monthrange(y, m)[1]))


def resolve_start(args, first_day):
    """窗口起点：--months / 绝对日期 --since 生效；相对写法（3.months.ago）交给 git 过滤，回落到首个提交日。"""
    if args.months:
        return shift_months(date.today(), -args.months), None
    if args.since:
        try:
            return datetime.strptime(args.since, "%Y-%m-%d").date(), None
        except ValueError:
            return first_day, "「%s」是相对写法，窗口起点按 git 过滤后首个提交日计算" % args.since
    return first_day, None


def max_streak(days):
    best = cur = 0
    prev = None
    for d in sorted(days):
        cur = cur + 1 if prev and (d - prev).days == 1 else 1
        prev = d
        best = max(best, cur)
    return best


def area_lines(rev, *paths):
    """若干路径在指定 tree 上的文件数与行数（git grep 一次调用搞定，二进制自动跳过）。"""
    p = subprocess.run(["git", "grep", "-c", "", rev, "--"] + list(paths),
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    files = lines = 0
    for row in p.stdout.decode("utf-8", "replace").splitlines():
        if ":" not in row:
            continue
        try:
            lines += int(row.rsplit(":", 1)[1])
            files += 1
        except ValueError:
            continue
    return files, lines


def tracked_count(rev):
    if rev == "HEAD":
        return len(git(["ls-files", "-z"]).split(b"\0")) - 1
    return len(git(["ls-tree", "-r", "--name-only", rev]).splitlines())


def md_table(headers, rows, right_align_from=1):
    out = ["| " + " | ".join(headers) + " |",
           "|" + "|".join(("-" * 4 + ":" if i >= right_align_from else "-" * 6)
                          for i in range(len(headers))) + "|"]
    for r in rows:
        out.append("| " + " | ".join(str(x) for x in r) + " |")
    return "\n".join(out)


def block(rows):
    """等宽代码块，保证终端里列对齐（Markdown 表格在窄终端会折行）。"""
    widths = [max(len(str(r[i])) for r in rows) for i in range(len(rows[0]))]
    return "```\n" + "\n".join("  ".join(str(c).ljust(w) for c, w in zip(r, widths)).rstrip()
                               for r in rows) + "\n```"


def n(x):
    return "{:,}".format(x)


def render(commits, args):
    out = ["# 开发节奏统计", ""]
    if not commits:
        out.append("窗口内无提交。")
        return "\n".join(out) + "\n"

    real = [c for c in commits if c["parents"] < 2]
    merges = len(commits) - len(real)
    n_real = len(real) or len(commits)  # 窗口内全是 merge 时兜住除零
    days = sorted({c["day"] for c in commits})
    authors = {c["author"] for c in commits}
    first_day, last_day = days[0], days[-1]
    start, start_note = resolve_start(args, datetime.strptime(first_day, "%Y-%m-%d").date())
    end = max(date.today(), datetime.strptime(last_day, "%Y-%m-%d").date())
    span = (end - start).days + 1
    weeks = span / 7.0

    out[0] = "# 开发节奏统计 · %s ~ %s" % (start, end)
    out.append("")
    if start_note:
        out.append("> %s" % start_note)

    out.append("## 概要")
    out.append("")
    out.append(md_table(["指标", "值"], [
        ["窗口", "%s ~ %s（%d 自然日 / %.1f 周）" % (start, end, span, weeks)],
        ["提交", "%d（其中 merge %d，实质 **%d**）" % (len(commits), merges, len(real))],
        ["作者", "%d 人（%s）" % (len(authors), "、".join(sorted(authors)[:3]))],
        ["活跃天", "%d / %d（**%.0f%%**），零提交 %d 天" % (len(days), span, 100.0 * len(days) / span, span - len(days))],
        ["均 / 活跃日", "%.1f 提交" % (len(commits) / float(len(days)))],
        ["均 / 自然日", "%.1f 提交" % (len(commits) / float(span))],
        ["均 / 周", "%.1f 提交" % (len(commits) / weeks)],
        ["最长连续活跃", "%d 天" % max_streak([datetime.strptime(d, "%Y-%m-%d").date() for d in days])],
    ]))
    out.append("")

    tot_ins = sum(c["ins"] for c in commits)
    tot_del = sum(c["del"] for c in commits)
    ven_ins, ven_del = churn(commits, is_vendor)
    gen_ins, gen_del = churn(commits, is_generated)
    oi, od = churn(commits, is_hand)
    binary = sum(c["binary"] for c in commits)
    out.append("## 代码量")
    out.append("")
    out.append(md_table(["口径", "+", "-", "net", "每实质提交"], [
        ["原始", n(tot_ins), n(tot_del), "%+d" % (tot_ins - tot_del),
         "+%d / -%d" % (tot_ins / float(n_real), tot_del / float(n_real))],
        ["**手写**（剔 vendored + 生成物）", "**%s**" % n(oi), "**%s**" % n(od), "**%+d**" % (oi - od),
         "**+%d / -%d**" % (oi / float(n_real), od / float(n_real))],
    ]))
    out.append("")
    out.append("- vendored：+%s / -%s（%s）；生成物/锁文件：+%s / -%s（%s）"
               % (n(ven_ins), n(ven_del), VENDOR_LABEL, n(gen_ins), n(gen_del), GENERATED_LABEL))
    if binary:
        out.append("- 二进制文件 %d 次改动，未计入行数" % binary)
    out.append("")

    byweek = defaultdict(lambda: [0, 0, 0, 0])
    for c in commits:
        w = byweek[week_of(c["day"])]
        w[0] += 1
        if c["parents"] < 2:
            w[1] += 1
        for ins, dele, path in c["numstat"]:
            if is_hand(path):
                w[2] += ins
                w[3] += dele
    rows = [[w, byweek[w][0], byweek[w][1], n(byweek[w][2]), n(byweek[w][3]), "%+d" % (byweek[w][2] - byweek[w][3])]
            for w in sorted(byweek)]
    out.append("## 按周（手写口径）")
    out.append("")
    out.append(block([["周", "提交", "实质", "+", "-", "net"]] + rows))
    out.append("")

    area = defaultdict(lambda: [0, 0, 0])
    for c in commits:
        for ins, dele, path in c["numstat"]:
            a = area[area_of(path)]
            a[0] += 1
            a[1] += ins
            a[2] += dele
    out.append("## 按区域（顶层目录）")
    out.append("")
    out.append(md_table(["区域", "文件触碰", "+", "-", "net"],
                        [[k, n(v[0]), n(v[1]), n(v[2]), "%+d" % (v[1] - v[2])]
                         for k, v in sorted(area.items(), key=lambda kv: -kv[1][1])]))
    out.append("")

    hot = defaultdict(lambda: [0, 0])
    for c in commits:
        for ins, dele, path in c["numstat"]:
            if not is_hand(path):
                continue
            hot[path][0] += ins
            hot[path][1] += dele
    top = sorted(hot.items(), key=lambda kv: -(kv[1][0] + kv[1][1]))[:args.top]
    out.append("## 热点文件 Top %d（按 churn，已排 vendored / 生成物）" % len(top))
    out.append("")
    out.append(md_table(["#", "文件", "churn", "+", "-"],
                        [[i + 1, "`%s`" % p, n(v[0] + v[1]), n(v[0]), n(v[1])] for i, (p, v) in enumerate(top)]))
    out.append("")

    KNOWN = {"feat", "fix", "docs", "refactor", "chore", "perf", "test",
             "build", "ci", "style", "revert", "config"}
    kind = Counter()
    for c in real:  # 只统计实质提交，merge 的 subject 不是类型
        m = re.match(r"^([A-Za-z]+)(\([^)]*\))?!?:", c["subject"])
        t = m.group(1).lower() if m else ""
        kind[t if t in KNOWN else "other"] += 1
    out.append("## commit 类型分布（实质提交 %d 个）" % len(real))
    out.append("")
    out.append(block([["类型", "个数", "占比"]] +
                     [[k, str(v), "%.1f%%" % (100.0 * v / n_real)] for k, v in kind.most_common()]))
    out.append("")

    hours = Counter(datetime.fromtimestamp(c["ts"]).hour for c in commits)
    out.append("## 提交时段（按运行机时区解析作者时间戳）")
    out.append("")
    out.append(block([["时"] + ["%02d" % h for h in range(12)],
                      ["数"] + [hours.get(h, 0) for h in range(12)]]))
    out.append(block([["时"] + ["%02d" % h for h in range(12, 24)],
                      ["数"] + [hours.get(h, 0) for h in range(12, 24)]]))
    out.append("")

    base_sha = commits[-1]["sha"]
    base_label = "%s（窗口首个提交 %s）" % (base_sha[:8], first_day)
    out.append("## 仓库体量：窗口起点 vs HEAD")
    out.append("")
    rows = [["tracked 文件", str(tracked_count(base_sha)), str(tracked_count("HEAD"))]]
    vf, vl = area_lines("HEAD", *VENDOR_PREFIXES)
    for a in AREAS:
        bf, bl = area_lines(base_sha, a)
        hf, hl = area_lines("HEAD", a)
        if a == "client":
            hf, hl = hf - vf, hl - vl
            rows.append(["%s (手写)" % a, "%d 文件 / %s 行" % (bf, n(bl)),
                         "%d 文件 / %s 行" % (hf, n(hl))])
            rows.append(["%s" % VENDOR_LABEL, "（起点尚无）", "%d 文件 / %s 行" % (vf, n(vl))])
            continue
        rows.append([a, "%d 文件 / %s 行" % (bf, n(bl)), "%d 文件 / %s 行" % (hf, n(hl))])
    out.append("起点 = %s" % base_label)
    out.append("")
    out.append(md_table(["区域", "起点", "HEAD"], rows))
    out.append("")

    big = sorted((c for c in commits if c["parents"] < 2),
                 key=lambda c: -sum(hand_churn(c)))[:args.top]
    out.append("## 最大单 commit Top %d（手写口径）" % len(big))
    out.append("")
    out.append(md_table(["#", "日期", "churn", "subject"],
                        [[i + 1, c["day"], n(sum(hand_churn(c))), c["subject"][:64]]
                         for i, c in enumerate(big)]))
    out.append("")

    out.append("## 读数须知")
    out.append("")
    out.append("- **merge 不产生 numstat**：`git log --numstat`（本脚本口径）不为 merge 出 diff，所以「最大单 commit」看不到经合并进来的工作量；提交总数也会被 merge 虚高。")
    out.append("- **vendored / 生成物会虚增行数**：第三方源码整包导入 +%s 行，提交进仓库的数据/锁文件 +%s 行，两者都已在「手写」口径剔除。" % (n(ven_ins), n(gen_ins)))
    out.append("- **line ≠ 有效代码量**：含注释、文档字符串、生成物；vendor 与 docs 尤其不适合当产出指标。")
    out.append("- **按区域含历史目录**：区域按「提交当时的路径」归类，后来删除/改名的顶层目录（如早期的 `design/`、`server-py/`）单独成行，不等于当前布局。")
    out.append("- **类型分布依赖 conventional commits**：只认 feat/fix/docs/refactor/chore/perf/test/build/ci/style/revert/config，其余全归 `other`。")
    out.append("")
    return "\n".join(out)


def selftest():
    payload = (
        "C:aaa|2026-09-30|111|1757000000|hanxiao|feat(core): 一个\n"
        "10\t2\tserver/a.go\n"
        "3\t0\tclient/packages/vendored.go\n"
        "1\t1\tclient/we\"ird 中文.go\n"
        "2\t0\tdocs/design/{ => archive}/x.md\n"
        "9\t0\tserver/data/topic_vectors.json\n"
        "C:bbb|2026-10-06|222 222|1757000001|hanxiao|fix: y\n"
        "5\t5\tserver/b.go\n"
        "4\t0\tfront-end/package-lock.json\n"
        "-\t-\tclient/logo.png\n"
        "C:ccc|2026-10-06|333|1757000002|hanxiao|no prefix commit\n"
    ).encode("utf-8")
    cs = parse_log(payload)
    assert len(cs) == 3, len(cs)
    assert [c["parents"] for c in cs] == [1, 2, 1], [c["parents"] for c in cs]
    assert cs[2]["numstat"] == [] and cs[2]["ins"] == 0, "空 diff 的 commit 也要算进提交数"
    assert sum(c["ins"] for c in cs) == 34, sum(c["ins"] for c in cs)      # 二进制不计
    assert sum(c["del"] for c in cs) == 8, sum(c["del"] for c in cs)
    assert churn(cs, is_vendor) == (3, 0), churn(cs, is_vendor)
    assert churn(cs, is_generated) == (13, 0), churn(cs, is_generated)
    assert churn(cs, is_hand) == (18, 8), churn(cs, is_hand)
    assert cs[0]["binary"] == 0 and cs[1]["binary"] == 1, "二进制行要单独计数"
    assert 'client/we"ird 中文.go' in [p for _, _, p in cs[0]["numstat"]], cs[0]["numstat"]
    assert "docs/design/archive/x.md" in [p for _, _, p in cs[0]["numstat"]], "rename 要归一到目标路径"
    assert cs[0]["subject"] == "feat(core): 一个", cs[0]["subject"]
    assert cs[0]["author"] == "hanxiao"
    assert week_of("2026-09-30") == "2026-W40" and week_of("2026-10-06") == "2026-W41"
    assert max_streak([date(2026, 9, 30), date(2026, 10, 1), date(2026, 10, 6)]) == 2
    assert shift_months(date(2026, 3, 31), -1) == date(2026, 2, 28), "月末夹取"
    assert area_of("client/packages/x/y.swift") == VENDOR_LABEL
    assert area_of("server/data/x.json") == GENERATED_LABEL
    assert area_of("front-end/package-lock.json") == GENERATED_LABEL
    assert area_of("docs/design/archive/x.md") == "docs"
    assert area_of("AGENTS.md") == "(根)"
    assert "窗口内无提交" in render([], None), "空窗口不能崩（P1 回归）"
    head = git(["rev-parse", "HEAD"]).decode().strip()
    only_merge = parse_log(("C:%s|2026-10-06|111 222|1757000000|hanxiao|Merge branch 'x'\n" % head).encode())
    ns = argparse.Namespace(months=None, since=None, top=10)
    assert "开发节奏统计" in render(only_merge, ns), "全 merge 窗口不能除零（P1 回归）"
    print("selftest: ok")


def main():
    ap = argparse.ArgumentParser(description="仓库开发节奏统计")
    ap.add_argument("--since", help="起点，YYYY-MM-DD 或 3.months.ago 之类相对写法")
    ap.add_argument("--months", type=int, help="近 N 个月（与 --since 二选一）")
    ap.add_argument("--out", help="写入文件；不传则打印到 stdout")
    ap.add_argument("--top", type=int, default=10, help="热点文件 / 大 commit 条数，默认 10")
    ap.add_argument("--selftest", action="store_true", help="只跑解析器自检")
    args = ap.parse_args()

    if args.selftest:
        selftest()
        return
    if args.since and args.months:
        sys.stderr.write("dev-stats: --since 与 --months 同时给，只用 --since\n")

    since = args.since
    if not since and args.months:
        since = "%d.months.ago" % args.months
    log_args = list(LOG_ARGS)
    if since:
        log_args.append("--since=" + since)
    commits = parse_log(git(log_args))

    report = render(commits, args)
    report += "\n---\n\n生成于 %s · `%s`\n" % (datetime.now().strftime("%Y-%m-%d %H:%M"), " ".join(
        ["python3 scripts/dev-stats.py"] +
        (["--since " + args.since] if args.since else []) +
        (["--months %d" % args.months] if args.months else []) +
        (["--top %d" % args.top] if args.top != 10 else [])))
    if args.out:
        with open(args.out, "w") as f:
            f.write(report)
        sys.stderr.write("dev-stats: 已写入 %s\n" % args.out)
    else:
        sys.stdout.write(report)


if __name__ == "__main__":
    main()