#!/usr/bin/env python3
"""从 opencode 本地库测一票的工时 —— 「面 / 缝 / 往返」成本模型的数据源。

为什么不用 git 提交时间：提交是「用户说 commit」的批量动作（同一分钟能出好几个），
而且每个提交里混着设计文档 / mockup / 代码，切不开。openCode 的库里每条消息都带
time_created，才是能算的口径。

测量口径（**长期固定**，改口径等于破坏历史可比性）：
- 活跃时长：相邻消息间隔封顶 CAP=300s 后求和（排除走开 / 挂机）
- 只计顶层会话（parent_id 为 NULL）；子会话（subagent）单独计数、不叠加时长（并行会双算）
- turns = role='user' 的消息数（人工轮次）；min_per_turn = active 分钟 / turns
- 人类延迟 = 用户消息 − 它前面最近一条 assistant 消息（P50 / P90）
- AI 生成 = 用户消息 → 下一条用户消息之前的最后一条消息（P50 / P90）

用法：
    python3 estimate.py --since '2026-09-17 18:13' --until '2026-09-18 00:34'
    python3 estimate.py ... --record --ticket 'xxx' --pre-designed yes --faces 8 --seams 4
    python3 estimate.py report
    python3 estimate.py --selftest
"""

import argparse
import datetime as dt
import json
import os
import sqlite3
import statistics
import sys

CAP = 300  # 秒；间隔超过它就认为是离开，不计入活跃时长
LOG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "estimation-log.jsonl")
DEFAULT_DB = os.path.expanduser("~/.local/share/opencode/opencode.db")

MANUAL_FIELDS = ("faces", "seams", "seams_need_probe", "roundtrips", "decisions", "notes")


def parse_time(s):
    for fmt in ("%Y-%m-%d %H:%M:%S", "%Y-%m-%d %H:%M", "%Y-%m-%d"):
        try:
            return int(dt.datetime.strptime(s, fmt).timestamp() * 1000)
        except ValueError:
            continue
    raise SystemExit(f"时间格式不认识: {s}（用 'YYYY-MM-DD HH:MM'）")


def find_sessions(con, repo, since_ms, until_ms):
    rows = con.execute(
        "select id, parent_id from session "
        "where directory = ? and time_created <= ? and time_updated >= ?",
        (repo, until_ms, since_ms),
    ).fetchall()
    top = [r[0] for r in rows if not r[1]]
    kids = [r[0] for r in rows if r[1]]
    return top, kids


def load_events(con, session_ids, since_ms, until_ms):
    """返回 [(t_ms, role), ...]，按时间排序。"""
    if not session_ids:
        return []
    q = ",".join("?" * len(session_ids))
    rows = con.execute(
        f"select time_created, json_extract(data,'$.role') from message "
        f"where session_id in ({q}) and time_created between ? and ? order by time_created",
        (*session_ids, since_ms, until_ms),
    ).fetchall()
    return [(t, role or "other") for t, role in rows]


def pct(vals, p):
    if not vals:
        return None
    vals = sorted(vals)
    i = min(len(vals) - 1, int(len(vals) * p))
    return round(vals[i] / 60, 1)  # 分钟


def metrics(events, cap=CAP):
    """纯函数：events = [(t_ms, role)] 时间有序。返回口径里的各项。"""
    if not events:
        return None
    ts = [t for t, _ in events]
    span_s = (ts[-1] - ts[0]) / 1000
    active_s = sum(min((ts[i] - ts[i - 1]) / 1000, cap) for i in range(1, len(ts)))
    users = [i for i, (_, r) in enumerate(events) if r == "user"]

    human, gen = [], []
    for k, i in enumerate(users):
        prev = next((j for j in range(i - 1, -1, -1) if events[j][1] == "assistant"), None)
        if prev is not None:
            human.append((ts[i] - ts[prev]) / 1000)
        end = users[k + 1] - 1 if k + 1 < len(users) else len(events) - 1
        gen.append((ts[end] - ts[i]) / 1000)

    turns = len(users)
    return {
        "span_h": round(span_s / 3600, 2),
        "active_h": round(active_s / 3600, 2),
        "messages": len(events),
        "turns": turns,
        "min_per_turn": round(active_s / 60 / turns, 1) if turns else None,
        "human_lat_p50": pct(human, 0.5),
        "human_lat_p90": pct(human, 0.9),
        "gen_p50": pct(gen, 0.5),
        "gen_p90": pct(gen, 0.9),
    }


def cmd(args):
    repo = os.path.realpath(args.repo)
    since, until = parse_time(args.since), parse_time(args.until)
    if not os.path.exists(args.db):
        raise SystemExit(f"找不到 opencode 库：{args.db}（用 --db 指定）")
    con = sqlite3.connect(f"file:{args.db}?immutable=1", uri=True)
    top, kids = find_sessions(con, repo, since, until)
    m = metrics(load_events(con, top, since, until))
    if not m:
        raise SystemExit("这个窗口里没有消息；检查 --since/--until/--repo")

    row = {
        "date": args.date or dt.datetime.fromtimestamp(since / 1000).strftime("%Y-%m-%d"),
        "ticket": args.ticket,
        "sessions": len(top),
        "subagents": len(kids),
        "pre_designed": args.pre_designed,
        **m,
    }
    for f in MANUAL_FIELDS:
        row[f] = getattr(args, f.replace("-", "_"))

    if args.record:
        with open(os.path.realpath(LOG), "a", encoding="utf-8") as fh:
            fh.write(json.dumps(row, ensure_ascii=False) + "\n")
        print(f"已追加到 {os.path.realpath(LOG)}")
    for k, v in row.items():
        print(f"{k:16} {v}")
    if args.record is False:
        print("\n（只测不记账；要写入账本加 --record）")


def cmd_report(_args):
    path = os.path.realpath(LOG)
    rows = [json.loads(l) for l in open(path, encoding="utf-8") if l.strip()]
    if not rows:
        raise SystemExit("账本还是空的")
    print("| 日期 | 票 | 预先设计 | 活跃h | 轮次 | min/轮 | 面 | 缝 | 往返 | 决定 |")
    print("|---|---|---|---|---|---|---|---|---|---|")
    for r in rows:
        print(
            f"| {r['date']} | {r['ticket']} | {r.get('pre_designed')} | {r['active_h']} | "
            f"{r['turns']} | {r['min_per_turn']} | {r.get('faces')} | {r.get('seams')} | "
            f"{r.get('roundtrips')} | {r.get('decisions')} |"
        )
    for flag in ("yes", "no"):
        v = [r["min_per_turn"] for r in rows if r.get("pre_designed") == flag and r.get("min_per_turn")]
        if v:
            print(f"\n预先设计={flag}: n={len(v)} min/轮 中位数={statistics.median(v)}")


def selftest():
    t0 = 1_700_000_000_000
    # 4 条消息：用户 → 助手(+30s) → [离开 1h] → 用户 → 助手(+60s)
    ev = [(t0, "user"), (t0 + 30_000, "assistant"), (t0 + 3_630_000, "user"), (t0 + 3_690_000, "assistant")]
    m = metrics(ev)
    assert m["turns"] == 2, m
    # 活跃 = 30s + 300s(封顶) + 60s = 390s
    assert m["active_h"] == round(390 / 3600, 2), m
    assert m["min_per_turn"] == round(390 / 60 / 2, 1), m
    # 人类延迟：只有第 2 轮有前一条助手（相隔 3600s）→ P50 = 60.0 分钟
    assert m["human_lat_p50"] == 60.0, m
    # AI 生成：第 1 轮 30s、第 2 轮 60s → P50（取上中位）= 1.0 分钟
    assert m["gen_p50"] == 1.0, m
    empty = metrics([])
    assert empty is None
    print("selftest ok")


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--repo", default=os.getcwd(), help="仓库路径（默认当前目录）")
    p.add_argument("--since", help="起始时间 'YYYY-MM-DD HH:MM'")
    p.add_argument("--until", help="结束时间")
    p.add_argument("--db", default=DEFAULT_DB)
    p.add_argument("--record", action="store_true", help="把这一行追加进账本")
    p.add_argument("--date", help="记账日期（默认取 --since 那天）")
    p.add_argument("--ticket", help="票名，如 document-highlight-annotation(P1-P5)")
    p.add_argument("--pre-designed", choices=("yes", "no"), help="开工前决定是否已拍完 / 出过稿")
    p.add_argument("--faces", type=int)
    p.add_argument("--seams", type=int)
    p.add_argument("--seams-need-probe", type=int, dest="seams_need_probe")
    p.add_argument("--roundtrips", type=int)
    p.add_argument("--decisions", type=int)
    p.add_argument("--notes")
    p.add_argument("--selftest", action="store_true")
    p.add_argument("report", nargs="?", help="只读账本并打印汇总表")
    args = p.parse_args()

    if args.selftest:
        return selftest()
    if args.report:
        return cmd_report(args)
    if not args.since or not args.until:
        raise SystemExit("要测一个窗口就给 --since 和 --until（或用 report / --selftest）")
    cmd(args)


if __name__ == "__main__":
    main()
