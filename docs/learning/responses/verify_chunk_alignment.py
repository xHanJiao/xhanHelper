#!/usr/bin/env python3
"""
verify_chunk_alignment.py — 用数据验证「chunk 边界对齐导致 L/H 交替」猜想。

================================================================================
猜想
================================================================================

DeepSeek 以 ~256 token 为间隔切分 chunk。两个相邻请求如果 prompt_tokens 的
差值恰好是 256 的整数倍，chunk 边界对齐 → 缓存命中高（H）；否则不对齐 → 低（L）。

由于滑动窗口每轮推入 1 条新消息、推出 1 条旧消息，相邻请求的 prompt_tokens
增量大致等于「新消息长度 - 旧消息长度」。这个 delta 决定了 chunk 边界的偏移量。

================================================================================
验证方法
================================================================================

对同一会话内的每对相邻请求 (A, B)：
  1. delta = B.prompt_tokens - A.prompt_tokens
  2. alignment = |delta % 256 - 128|  （0=完美对齐, 128=最差, 越小越好）
  3. 检查 B.cache_hit 是否高于阈值

如果猜想成立：
  - alignment 小的请求，cache_hit 应该偏高
  - alignment 大的请求，cache_hit 应该偏低
  - 对于严格的 L/H 交替，alignment 应该也交替

================================================================================
"""

import json
import yaml
import pymysql
from collections import defaultdict


def load_db_config():
    with open('conf/config.dev.yaml') as f:
        cfg = yaml.safe_load(f)
    db = cfg['db']
    return {
        'host': db['host'], 'port': db['port'],
        'user': db['user'], 'password': db['password'],
        'database': db['database'], 'charset': 'utf8mb4',
    }


def parse_extra(extra_str):
    if not extra_str:
        return 0, 0, 0
    try:
        extra = json.loads(extra_str)
        usage = extra.get('usage', {})
        if not usage:
            return 0, 0, 0
        return (
            usage.get('prompt_tokens', 0),
            usage.get('prompt_cache_hit_tokens', 0),
            usage.get('prompt_cache_miss_tokens', 0),
        )
    except (json.JSONDecodeError, TypeError):
        return 0, 0, 0


def main():
    db_cfg = load_db_config()
    conn = pymysql.connect(**db_cfg)
    cur = conn.cursor()

    cur.execute("""
        SELECT id, conversation_id, created_at, request_json, extra
        FROM messages
        WHERE role = 'assistant' AND status = 1
          AND request_json IS NOT NULL AND extra IS NOT NULL
        ORDER BY conversation_id, created_at ASC
    """)
    rows = cur.fetchall()
    print(f"加载: {len(rows)}")

    all_msgs = []
    for row in rows:
        msg_id, conv_id, created_at, req_json, extra_str = row
        prompt_tok, cache_hit, miss_tok = parse_extra(extra_str)
        if prompt_tok == 0:
            continue
        try:
            req = json.loads(req_json)
            msgs = req.get('messages', [])
            rounds = sum(1 for m in msgs if m.get('role') == 'user')
            last_role = msgs[-1]['role'] if msgs else ''
        except (json.JSONDecodeError, TypeError):
            rounds, last_role = 0, ''

        all_msgs.append({
            'id': msg_id, 'conv_id': conv_id, 'time': str(created_at),
            'rounds': rounds, 'last_role': last_role,
            'prompt_tok': prompt_tok, 'cache_hit': cache_hit, 'miss_tok': miss_tok,
        })

    # =========================================================================
    # Part 1: delta vs cache_hit 相关性 —— 整体统计
    # =========================================================================

    print("\n" + "="*80)
    print("Part 1: prompt_tokens 增量 vs 256整除 与 cache_hit 的关系")
    print("="*80)

    # 收集所有相邻对（同会话、非 tool 轮）
    PAIRS = []
    for i in range(1, len(all_msgs)):
        a, b = all_msgs[i-1], all_msgs[i]
        if a['conv_id'] != b['conv_id']:
            continue
        if a['last_role'] == 'tool' or b['last_role'] == 'tool':
            continue

        delta = b['prompt_tok'] - a['prompt_tok']
        # alignment: 0=完美对齐 128=完全错位
        # |delta % 256| 越接近 0 或 256，对齐越好
        # 但还要考虑方向：delta 可能是正也可能是负
        remainder = abs(delta) % 256
        # 距离最近 256 倍数的距离
        alignment_score = min(remainder, 256 - remainder)

        PAIRS.append({
            'a': a, 'b': b,
            'delta': delta,
            'remainder': remainder,
            'alignment_score': alignment_score,
        })

    # 按 alignment_score 分组，看各组 cache_hit 分布
    good_align = [p for p in PAIRS if p['alignment_score'] < 32]   # 差 <32
    ok_align   = [p for p in PAIRS if 32 <= p['alignment_score'] < 64]
    bad_align  = [p for p in PAIRS if p['alignment_score'] >= 96]  # 差 >96

    for label, group in [("对齐好(<32)", good_align), ("中等(32-64)", ok_align), ("对齐差(>96)", bad_align)]:
        if not group:
            continue
        avg_hit = sum(p['b']['cache_hit'] for p in group) / len(group)
        high_count = sum(1 for p in group if p['b']['cache_hit'] > 5000)
        low_count = sum(1 for p in group if p['b']['cache_hit'] <= 1024)
        print(f"\n{label} ({len(group)}对):")
        print(f"  平均 cache_hit: {avg_hit:.0f}")
        print(f"  高命中(>5000): {high_count} ({high_count/max(1,len(group))*100:.0f}%)")
        print(f"  低命中(≤1024): {low_count} ({low_count/max(1,len(group))*100:.0f}%)")

    # =========================================================================
    # Part 2: 展示 L/H 和 delta 的具体序列
    # =========================================================================

    print("\n" + "="*80)
    print("Part 2: L/H 序列与 delta%256 的对应关系（选一段典型序列）")
    print("="*80)

    # 找一段有最多跳变的序列（密集 L/H 交替）
    main_conv = None
    for conv_id in set(p['a']['conv_id'] for p in PAIRS):
        conv_pairs = [p for p in PAIRS if p['a']['conv_id'] == conv_id]
        jumps = sum(1 for p in conv_pairs
                    if (p['a']['cache_hit'] <= 1024 and p['b']['cache_hit'] > 5000)
                    or (p['a']['cache_hit'] > 5000 and p['b']['cache_hit'] <= 1024))
        if jumps > 10:
            main_conv = conv_id
            break

    if main_conv:
        conv_pairs = [p for p in PAIRS if p['a']['conv_id'] == main_conv]
        print(f"\n会话 {main_conv[:8]} ({len(conv_pairs)+1}条消息, {len(conv_pairs)}对)")
        print(f"{'A时间':<10} {'B时间':<10} {'A-h':>6} {'B-h':>6} {'delta':>7} {'delta%256':>9} {'align':>6} {'跳变':>4}")
        for p in conv_pairs[-40:]:  # 最后 40 对
            a, b = p['a'], p['b']
            jump = ''
            if a['cache_hit'] <= 1024 and b['cache_hit'] > 5000:
                jump = '↑H'
            elif a['cache_hit'] > 5000 and b['cache_hit'] <= 1024:
                jump = '↓L'
            print(f"{a['time'][-9:]:<10} {b['time'][-9:]:<10} "
                  f"{a['cache_hit']:>6} {b['cache_hit']:>6} "
                  f"{p['delta']:>+7} {p['remainder']:>9} {p['alignment_score']:>6} {jump:>4}")

    # =========================================================================
    # Part 3: 关键验证——看 delta 的一致性
    # =========================================================================

    print("\n" + "="*80)
    print("Part 3: 关键验证——同一会话内 delta 是否稳定")
    print("="*80)

    print("如果每轮推入的消息长度大致稳定，delta 应该在某个值附近窄幅波动。")
    print("稳定的 delta 意味着 chunk 偏移是可预测的——这就是 L/H 交替的根源。\n")

    # 取会话 3b3f6e2f（数据最多，509条）
    conv_3b = [p for p in PAIRS if p['a']['conv_id'].startswith('3b3f6e2f')]
    if conv_3b:
        deltas = [p['delta'] for p in conv_3b]
        avg_d = sum(deltas) / len(deltas)
        # 统计 delta 分布
        buckets = defaultdict(int)
        for d in deltas:
            bucket = (d // 50) * 50  # 50 token 一组
            buckets[bucket] += 1

        print(f"会话 3b3f6e2f: {len(deltas)} 对, delta 均值={avg_d:.0f}")
        print(f"delta 分布 (每 50 token):")
        for k in sorted(buckets.keys()):
            bar = '█' * buckets[k]
            print(f"  {k:>+6}: {buckets[k]:>3} {bar}")

        # 看 delta 对 256 的余数分布
        remainder_dist = defaultdict(int)
        for d in deltas:
            r = abs(d) % 256
            bucket = (r // 16) * 16
            remainder_dist[bucket] += 1
        print(f"\ndelta % 256 的分布 (每 16):")
        for k in sorted(remainder_dist.keys()):
            bar = '█' * remainder_dist[k]
            print(f"  {k:>4}: {remainder_dist[k]:>3} {bar}")

    cur.close()
    conn.close()


if __name__ == '__main__':
    main()
