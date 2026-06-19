#!/usr/bin/env python3
"""
analyze_cache_patterns.py — 深入分析缓存命中的模式规律。

================================================================================
要回答的问题
================================================================================

Q2: 为什么 6000-6700 范围有 6016/6272/6400/6656 等不同值？
    → 分析一对 6016 和 6656 的请求，比较它们在 token 序列层面的差异

Q3: 连续命中、断崖上升/下降、连续不命中有什么规律？
    → 找出同一会话中的缓存模式序列，观察触发条件

================================================================================
方法
================================================================================

1. Token 估算改进：
   - 不再用 256 字符固定窗口
   - 用每个请求的 prompt_tokens / req_json_len 计算该请求的 chars/token 比
   - 据此将 request_json 按 token 切分（而非按字符）

2. 6016 vs 6656 对比：
   - 取一对同会话内 cache_hit=6016 和 6656 的请求
   - 找出它们在 request_json 层面的差异位置
   - 计算差异是否落在 256-token chunk 边界

3. 模式序列分析：
   - 对每个会话，列出 cache_hit 的时间序列
   - 标注连续命中（>1000）、连续不命中（≤1024）、断崖跳变
   - 观察触发条件：是否和消息长度剧变、工具调用频率相关
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
    print(f"加载消息: {len(rows)}")

    # ---- 解析 ----
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

        # 计算该请求的精确 chars/token 比
        req_len = len(req_json)
        chars_per_token = req_len / prompt_tok if prompt_tok > 0 else 1.0

        all_msgs.append({
            'id': msg_id, 'conv_id': conv_id, 'time': str(created_at),
            'req_json': req_json, 'req_len': req_len,
            'rounds': rounds, 'last_role': last_role,
            'prompt_tok': prompt_tok, 'cache_hit': cache_hit, 'miss_tok': miss_tok,
            'hit_pct': cache_hit / prompt_tok * 100 if prompt_tok else 0,
            'chars_per_token': chars_per_token,
        })

    print(f"解析成功: {len(all_msgs)}")

    # =========================================================================
    # Part 1: 同一会话内的缓存模式序列
    # =========================================================================

    print("\n" + "="*80)
    print("Part 1: 各会话的缓存命中模式序列")
    print("="*80)

    # 按会话分组
    convs = defaultdict(list)
    for m in all_msgs:
        if m['last_role'] != 'tool':
            convs[m['conv_id']].append(m)

    # 对每个会话，分析序列模式
    pattern_counts = defaultdict(int)  # 统计各模式出现次数
    jump_events = []  # 记录断崖跳变事件

    for conv_id, msgs in sorted(convs.items()):
        if len(msgs) < 5:
            continue

        # 生成模式序列标签
        # 分类: LOW(≤1024) / MID(1025-5000) / HIGH(>5000)
        def classify(hit):
            if hit <= 1024: return 'L'
            if hit <= 5000: return 'M'
            return 'H'

        labels = [classify(m['cache_hit']) for m in msgs]

        # 简化输出：只显示有模式变化的会话
        has_pattern = len(set(labels)) > 1
        if not has_pattern:
            continue

        # 找连续段
        segments = []
        seg_start = 0
        for i in range(1, len(labels)):
            if labels[i] != labels[i-1]:
                segments.append((seg_start, i-1, labels[seg_start]))
                seg_start = i
        segments.append((seg_start, len(labels)-1, labels[seg_start]))

        # 找断崖跳变 (L→H 或 H→L)
        for i in range(1, len(msgs)):
            if (labels[i-1] == 'L' and labels[i] == 'H') or \
               (labels[i-1] == 'H' and labels[i] == 'L'):
                jump_events.append({
                    'conv_id': conv_id,
                    'from': msgs[i-1],
                    'to': msgs[i],
                    'direction': 'L→H' if labels[i] == 'H' else 'H→L',
                })

        # 只输出有断崖跳变的会话
        has_jump = any(abs(msgs[i]['cache_hit'] - msgs[i-1]['cache_hit']) > 4000
                       for i in range(1, len(msgs)))
        if not has_jump:
            continue

        print(f"\n会话 {conv_id[:8]} ({len(msgs)}条)")
        # 显示时间序列，标注跳变
        for i, m in enumerate(msgs):
            marker = ''
            if i > 0:
                diff = m['cache_hit'] - msgs[i-1]['cache_hit']
                if diff > 4000:
                    marker = f' ← ↑+{diff}'
                elif diff < -4000:
                    marker = f' ← ↓{diff}'
            print(f"  {m['time'][-12:]} R={m['rounds']:2d} hit={m['cache_hit']:5d} "
                  f"({m['hit_pct']:5.1f}%) prompt={m['prompt_tok']:5d}{marker}")

    # =========================================================================
    # Part 2: 6016 和 6656 的精确差异对比
    # =========================================================================

    print("\n" + "="*80)
    print("Part 2: 6016 vs 6656 —— 差异到底是什么")
    print("="*80)

    # 找一个有 6016 且同会话也有 6656 的会话
    conv_has_both = []
    for conv_id, msgs in convs.items():
        hits = {m['cache_hit'] for m in msgs}
        if 6016 in hits and 6656 in hits:
            conv_has_both.append((conv_id, msgs))

    if conv_has_both:
        # 取第一个会话中的两条
        conv_id, msgs = conv_has_both[0]
        m6016 = next(m for m in msgs if m['cache_hit'] == 6016)
        m6656 = next(m for m in msgs if m['cache_hit'] == 6656)

        print(f"\n会话 {conv_id[:8]}")
        print(f"\n6016 消息: id={m6016['id'][:8]} time={m6016['time'][-12:]} "
              f"rounds={m6016['rounds']} prompt={m6016['prompt_tok']} "
              f"req_len={m6016['req_len']} chars/tok={m6016['chars_per_token']:.2f}")
        print(f"6656 消息: id={m6656['id'][:8]} time={m6656['time'][-12:]} "
              f"rounds={m6656['rounds']} prompt={m6656['prompt_tok']} "
              f"req_len={m6656['req_len']} chars/tok={m6656['chars_per_token']:.2f}")

        # 使用各自的 chars/token 比将 request_json 切分为 token 序列
        def token_split(req_json, chars_per_token):
            """将文本按 token 切分，返回每个 token 的字符位置。"""
            pos = 0
            tokens = []
            while pos < len(req_json):
                # 估算下一个 token 的字符宽度
                chunk = int(chars_per_token)
                if chunk < 1:
                    chunk = 1
                tokens.append((pos, min(pos + chunk, len(req_json))))
                pos += chunk
            return tokens

        tokens_6016 = token_split(m6016['req_json'], m6016['chars_per_token'])
        tokens_6656 = token_split(m6656['req_json'], m6656['chars_per_token'])

        # 比较两个 token 序列的前缀
        same_tokens = 0
        for i in range(min(len(tokens_6016), len(tokens_6656))):
            start_a, end_a = tokens_6016[i]
            start_b, end_b = tokens_6656[i]
            text_a = m6016['req_json'][start_a:end_a]
            text_b = m6656['req_json'][start_b:end_b]
            if text_a == text_b:
                same_tokens += 1
            else:
                break

        print(f"\n前缀相同 token 数: {same_tokens}")
        print(f"  6016 的第一个不同 token 位置: [{tokens_6016[same_tokens][0]}-{tokens_6016[same_tokens][1]}] "
              f"/ {len(m6016['req_json'])}")
        print(f"  6656 的第一个不同 token 位置: [{tokens_6656[same_tokens][0]}-{tokens_6656[same_tokens][1]}] "
              f"/ {len(m6656['req_json'])}")

        # 展示第一个不同 token 的内容
        if same_tokens < len(tokens_6656):
            start, end = tokens_6656[same_tokens]
            text = m6656['req_json'][start:min(start+200, end)]
            print(f"\n6656 的第一个不同 token 内容:")
            print(f"  位置 {start}-{end}")
            print(f"  内容: {text[:200]}")

        # 计算差异在 chunk(256 token) 层面的表现
        delta = m6656['cache_hit'] - m6016['cache_hit']
        print(f"\ncache_hit 差值: {delta} tokens = {delta/256:.1f} chunks")
        print(f"  6016 = {6016/256:.1f} chunks, 6656 = {6656/256:.1f} chunks")

        # 两个请求的角色组成差异
        req_6016 = json.loads(m6016['req_json'])
        req_6656 = json.loads(m6656['req_json'])
        msgs_6016 = req_6016.get('messages', [])
        msgs_6656 = req_6656.get('messages', [])

        print(f"\n消息组成:")
        print(f"  6016: {len(msgs_6016)}条, rounds={m6016['rounds']}")
        print(f"  6656: {len(msgs_6656)}条, rounds={m6656['rounds']}")
        print(f"  差异: {len(msgs_6656) - len(msgs_6016)}")

    # =========================================================================
    # Part 3: 跳变事件总结
    # =========================================================================

    print("\n" + "="*80)
    print("Part 3: 断崖跳变事件总结")
    print("="*80)

    # 分类统计
    l_to_h = [e for e in jump_events if e['direction'] == 'L→H']
    h_to_l = [e for e in jump_events if e['direction'] == 'H→L']

    print(f"\nL→H (上升): {len(l_to_h)} 次")
    for e in l_to_h[:10]:
        fr = e['from']
        to = e['to']
        print(f"  {fr['time'][-12:]} → {to['time'][-12:]}   "
              f"cache_hit: {fr['cache_hit']}→{to['cache_hit']}  "
              f"rounds: {fr['rounds']}→{to['rounds']}  "
              f"prompt: {fr['prompt_tok']}→{to['prompt_tok']}")

    print(f"\nH→L (下降): {len(h_to_l)} 次")
    for e in h_to_l[:10]:
        fr = e['from']
        to = e['to']
        print(f"  {fr['time'][-12:]} → {to['time'][-12:]}   "
              f"cache_hit: {fr['cache_hit']}→{to['cache_hit']}  "
              f"rounds: {fr['rounds']}→{to['rounds']}  "
              f"prompt: {fr['prompt_tok']}→{to['prompt_tok']}")

    # 统计上升触发条件
    print(f"\n上升触发条件分析:")
    for e in l_to_h:
        fr = e['from']
        to = e['to']
        # 检查是否同一轮数
        same_rounds = fr['rounds'] == to['rounds']
        print(f"  same_rounds={same_rounds}  "
              f"prompt_delta={to['prompt_tok'] - fr['prompt_tok']:+d}")

    cur.close()
    conn.close()


if __name__ == '__main__':
    main()
