#!/usr/bin/env python3
"""
analyze_session_gaps.py — 分析会话中相邻 user 消息的时间间隔，
找到切分 "session burst" 的最佳阈值。

用法: cd server && python3 ../docs/learning/responses/analyze_session_gaps.py [conversation_id]

输出:
  1. 间隔基本统计（中位数、均值）
  2. 不同阈值下的切割结果（段数、段均消息数、最大段、段均时长）
  3. 大段（>100 消息）的起止时间

作者: Claude（基于 xhanHelper 的缓存实验代码）
"""

import sys
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


def main():
    conv_id = sys.argv[1] if len(sys.argv) > 1 else \
        '3b3f6e2f-0ddb-42a4-a2dc-a140cc30838c'

    db_cfg = load_db_config()
    conn = pymysql.connect(**db_cfg)
    cur = conn.cursor()

    # 查询指定会话的所有 user 消息时间
    cur.execute(
        "SELECT created_at FROM messages "
        "WHERE conversation_id=%s AND role='user' AND status=1 "
        "ORDER BY created_at ASC",
        (conv_id,)
    )
    times = [r[0] for r in cur.fetchall()]
    print(f"会话: {conv_id[:8]}...")
    print(f"user 消息总数: {len(times)}")
    print(f"时间跨度: {times[0]} ~ {times[-1]}")

    # ---- 间隔统计 ----
    gaps = [(times[i] - times[i - 1]).total_seconds() / 60
            for i in range(1, len(times))]
    sorted_gaps = sorted(gaps)
    print(f"间隔中位数: {sorted_gaps[len(gaps)//2]:.1f} 分钟")
    print(f"间隔均值:   {sum(gaps)/len(gaps):.1f} 分钟")
    print(f"间隔 P90:    {sorted_gaps[int(len(gaps)*0.9)]:.1f} 分钟")
    print(f"间隔 P95:    {sorted_gaps[int(len(gaps)*0.95)]:.1f} 分钟")

    # ---- 不同阈值切割 ----
    thresholds = [10, 20, 30, 40, 50, 60, 120, 180, 240, 300]
    print(f"\n{'阈值':<10} {'段数':>5} {'段均msg':>8} {'最大段':>6} {'段均时长':>10} {'>100msg段':>8}")
    for t in thresholds:
        # 按阈值切割: 间隔超过 t 分钟就切一段
        segments = []
        seg_start = 0
        for i in range(1, len(times)):
            gap = (times[i] - times[i - 1]).total_seconds() / 60
            if gap > t:
                segments.append(i - seg_start)
                seg_start = i
        segments.append(len(times) - seg_start)

        # 段统计
        avg_len = sum(segments) / len(segments)
        max_len = max(segments)
        big_seg_count = sum(1 for s in segments if s > 100)

        # 段均时长
        total_dur = 0.0
        s = 0
        for seg_len in segments:
            dur = (times[s + seg_len - 1] - times[s]).total_seconds() / 3600
            total_dur += dur
            s += seg_len
        avg_dur = total_dur / len(segments)

        print(f"{t:<10} {len(segments):>5} {avg_len:>8.0f} {max_len:>6} {avg_dur:>8.1f}h {big_seg_count:>8}")

    # ---- 大段详情 ----
    print(f"\n阈值=20min 的 >50msg 段:")
    t = 20
    segments = []
    seg_start = 0
    starts = [0]
    for i in range(1, len(times)):
        gap = (times[i] - times[i - 1]).total_seconds() / 60
        if gap > t:
            segments.append(i - seg_start)
            seg_start = i
            starts.append(seg_start)
    segments.append(len(times) - seg_start)
    for j in range(len(segments)):
        if segments[j] > 50:
            t0, t1 = times[starts[j]], times[starts[j] + segments[j] - 1]
            dur = (t1 - t0).total_seconds() / 60
            print(f"  {segments[j]}条 {t0} ~ {t1} ({dur:.0f}分钟)")

    cur.close()
    conn.close()


if __name__ == '__main__':
    main()
