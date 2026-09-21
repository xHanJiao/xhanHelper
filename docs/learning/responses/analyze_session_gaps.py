#!/usr/bin/env python3
"""
analyze_session_gaps.py — 分析会话中相邻 user 消息的时间间隔，为 `chat.session_gap_minutes`
（同时驱动窗口重置 + session_end 提取延迟）选阈值。

用法:
  cd server && python3 ../docs/learning/responses/analyze_session_gaps.py            # 全量
  cd server && python3 ../docs/learning/responses/analyze_session_gaps.py <conv_id>  # 单会话

输出:
  1. 分位数（全量 / 纯聊两路）
  2. 按分钟密度直方图（找分布谷底）
  3. 累计覆盖表（各阈值覆盖多少相邻间隔）
  4. 阈值扫描（段数 / 中位段长 / 单条段占比）
  5. （单会话模式）大段起止时间

纯聊定义：该会话 0 条 assistant tool_calls、且 user 消息 >= 8 条。
结论见 docs/research/session-gap-threshold.md
"""

import sys
import statistics
from collections import defaultdict, Counter

import yaml
import pymysql


def load_db_config():
    with open('conf/config.dev.yaml', 'r') as f:
        cfg = yaml.safe_load(f)
    db = cfg['db']
    return {
        'host': db['host'],
        'port': db['port'],
        'user': db['user'],
        'password': db['password'],
        'database': db['database'],
        'charset': 'utf8mb4',
    }


def fetch_conversations():
    """返回 {conv_id: [(created_at, role, has_tool), ...]}，按时间升序。"""
    conn = pymysql.connect(**load_db_config())
    try:
        cur = conn.cursor()
        cur.execute(
            "SELECT conversation_id, created_at, role, "
            "CASE WHEN tool_calls_json IS NOT NULL AND tool_calls_json<>'' THEN 1 ELSE 0 END "
            "FROM messages WHERE status=1 AND is_sub_agent=0 "
            "ORDER BY conversation_id, created_at ASC"
        )
        conv = defaultdict(list)
        for cid, ts, role, has_tool in cur.fetchall():
            conv[cid].append((ts, role, has_tool))
        return conv
    finally:
        conn.close()


def gaps_minutes(times):
    return [(times[i] - times[i - 1]).total_seconds() / 60 for i in range(1, len(times))]


def pct(v, p):
    if not v:
        return float('nan')
    v = sorted(v)
    k = (len(v) - 1) * p
    f = int(k)
    c = min(f + 1, len(v) - 1)
    return v[f] + (v[c] - v[f]) * (k - f)


def user_times(msgs):
    return [ts for ts, role, _ in msgs if role == 'user']


def segments(times, threshold):
    """按阈值把 times 切成段，返回 [(start_idx, end_idx), ...]。"""
    segs, start = [], 0
    for i in range(1, len(times)):
        if (times[i] - times[i - 1]).total_seconds() / 60 > threshold:
            segs.append((start, i - 1))
            start = i
    segs.append((start, len(times) - 1))
    return segs


EDGES = [(0, 1), (1, 2), (2, 3), (3, 4), (4, 5), (5, 7), (7, 10), (10, 13),
         (13, 16), (16, 20), (20, 25), (25, 30), (30, 40), (40, 60), (60, 120)]
THRESHOLDS = [5, 10, 15, 20, 25, 30, 45, 60]


def report_distribution(label, gaps):
    print(f"\n=== {label}：间隔分布（n={len(gaps)}） ===")
    if not gaps:
        return
    print("median %.1f  mean %.1f  p75 %.1f  p90 %.1f  p95 %.1f" % (
        statistics.median(gaps), statistics.mean(gaps),
        pct(gaps, .75), pct(gaps, .9), pct(gaps, .95)))
    total = len(gaps)
    cum = 0
    print(f"  {'区间':>8} {'数量':>6} {'占比':>7} {'累计':>7} {'密度/min':>9}")
    for a, b in EDGES:
        n = sum(1 for x in gaps if a <= x < b)
        cum += n
        print(f"  {f'{a:g}-{b:g}':>8}m {n:>6} {n/total*100:>6.1f}% {cum/total*100:>6.1f}% {n/(b-a):>9.1f}")
    tail = total - cum
    print(f"  {'>120':>8}m {tail:>6} {tail/total*100:>6.1f}% {100.0:>6.1f}% {'-':>9}")
    print("  累计覆盖:", "  ".join(
        f"<={t}m {sum(1 for x in gaps if x <= t)/total*100:.1f}%" for t in (5, 10, 15, 20, 30)))


def report_sweep(label, conv, min_user=2):
    print(f"\n=== {label}：阈值扫描 ===")
    print(f"  {'阈值':>5} {'段数':>6} {'单条段':>7} {'<=2条段':>8} {'中位段长':>9}")
    for t in THRESHOLDS:
        runs, single, short, durs = 0, 0, 0, []
        for msgs in conv.values():
            u = user_times(msgs)
            if len(u) < min_user:
                continue
            for a, b in segments(u, t):
                n = b - a + 1
                runs += 1
                single += n == 1
                short += n <= 2
                durs.append((u[b] - u[a]).total_seconds() / 60)
        print(f"  {t:>4}m {runs:>6} {single/runs*100:>6.0f}% {short/runs*100:>6.0f}% "
              f"{statistics.median(durs):>8.1f}m")


def main():
    single = len(sys.argv) > 1
    conv = fetch_conversations()

    if single:
        cid = sys.argv[1]
        msgs = conv.get(cid)
        if not msgs:
            print(f"会话不存在或无消息: {cid}")
            return
        ut = user_times(msgs)
        print(f"会话 {cid[:8]}...  user 消息 {len(ut)} 条  跨度 {ut[0]} ~ {ut[-1]}")
        report_distribution("单会话", gaps_minutes(ut))
        for t in THRESHOLDS:
            print(f"  阈值 {t}m -> {len(segments(ut, t))} 段")
        print("\n阈值=20min 的 >50 条段：")
        for a, b in segments(ut, 20):
            if b - a + 1 > 50:
                print(f"  {b-a+1} 条  {ut[a]} ~ {ut[b]}")
        return

    all_gaps = [g for msgs in conv.values() for g in gaps_minutes(user_times(msgs))]
    n_conv = sum(1 for m in conv.values() if len(user_times(m)) >= 2)
    print(f"会话数 {len(conv)}（>=2 条 user 消息的 {n_conv} 个）  user 间隔总数 {len(all_gaps)}")

    pure = {cid: m for cid, m in conv.items()
            if all(ht == 0 for _, _, ht in m) and len(user_times(m)) >= 8}
    pure_gaps = [g for m in pure.values() for g in gaps_minutes(user_times(m))]

    report_distribution("全量", all_gaps)
    report_distribution(f"纯聊（0 工具调用、>=8 条 user，{len(pure)} 个会话）", pure_gaps)
    report_sweep("全量", conv)
    report_sweep("纯聊", pure, min_user=8)


if __name__ == '__main__':
    main()
