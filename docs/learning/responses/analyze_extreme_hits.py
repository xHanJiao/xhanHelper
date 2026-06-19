#!/usr/bin/env python3
"""
analyze_extreme_hits.py — 研究普通轮（非 tool 轮）中极端高缓存命中的原因。

问题:
  全量数据中，普通轮（request 末尾不是 tool）的最大 cache_hit 是 18816，其次 18688、
  11136、11648。这些值远高于 6656 的常见天花板,但各只出现一次。
  为什么会出现这些孤立高值？

方法:
  1. 从 MySQL 加载所有 assistant 消息，解析 request_json 和 extra。
  2. 筛选「普通轮」（请求最后一条消息不是 tool role）且 cache_hit > 10000 的。
  3. 对每个极端高命中消息，找到它在同一会话中紧邻的前一条 assistant 消息。
  4. 逐条对比两个请求的 messages 数组，统计前缀相同（role + content 完全一致）的消息数。
  5. 计算公共前缀的 token 估算值，与 cache_hit 对比，判断是否为「前缀匹配」场景。

数据来源:
  - MySQL xhan_helper_test (config.dev.yaml)
  - messages.request_json → messages[].role, messages[].content
  - messages.extra → Usage.prompt_cache_hit_tokens

依赖: pip install pymysql pyyaml
"""

import json
import sys
import yaml
import pymysql

# ---- 1. 连接数据库 ----

def load_db_config():
    """从 config.dev.yaml 读取数据库连接参数。"""
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


def parse_extra(extra_str):
    """从 extra JSON 中提取 usage.prompt_cache_hit_tokens 和 prompt_tokens。

    extra 结构示例（注意 key 是小写）:
      {"providers": [...], "usage": {"prompt_tokens": 15000, "prompt_cache_hit_tokens": 6656, ...}}
    部分消息没有 usage 字段（只有 providers），此时返回 (0,0,0)。
    返回 (prompt_tokens, cache_hit_tokens, prompt_cache_miss_tokens) 或 (0,0,0)。
    """
    if not extra_str:
        return 0, 0, 0
    try:
        extra = json.loads(extra_str)
        usage = extra.get('usage', {})  # 小写 key
        if not usage:
            return 0, 0, 0
        return (
            usage.get('prompt_tokens', 0),
            usage.get('prompt_cache_hit_tokens', 0),
            usage.get('prompt_cache_miss_tokens', 0),
        )
    except (json.JSONDecodeError, TypeError):
        return 0, 0, 0


def parse_request_json(req_str):
    """从 request_json 中提取 messages 数组。

    返回:
      messages: [{'role': str, 'content': str}, ...] 或空列表
      rounds: user 消息数量
      last_role: 最后一条消息的角色
      total_msgs: 消息总数
    """
    if not req_str:
        return [], 0, '', 0
    try:
        req = json.loads(req_str)
        msgs = req.get('messages', [])
        rounds = sum(1 for m in msgs if m.get('role') == 'user')
        last_role = msgs[-1]['role'] if msgs else ''
        return msgs, rounds, last_role, len(msgs)
    except (json.JSONDecodeError, TypeError):
        return [], 0, '', 0


# ---- 2. 前缀匹配分析 ----

def count_common_prefix_msgs(msgs_a, msgs_b):
    """逐条比较两个消息数组，返回前缀连续相同的消息数。

    只比较 role + content 是否完全一致。一旦发现不同，立即停止——
    因为缓存命中要求前缀完整连续，一条不同则后续全部不同。

    返回: (common_count, common_until_idx)
    """
    n = min(len(msgs_a), len(msgs_b))
    for i in range(n):
        a, b = msgs_a[i], msgs_b[i]
        if a.get('role') != b.get('role') or a.get('content') != b.get('content'):
            return i, i
    return n, n


def estimate_tokens(text):
    """粗略估算文本的 token 数。

    中英文混排场景，我们使用「字符数 ≈ token 数」的 1:1 估算。
    从实测数据看，request_json 的 chars/token ≈ 1.0（因大量英文工具 JSON 和 XML）。
    单条中文 user 消息的 chars/token ≈ 1.2-1.5。
    这里保守使用 1:1 估算，误差在可接受范围。
    """
    return len(text)


# ---- 3. 主流程 ----

def main():
    # 连接数据库
    db_cfg = load_db_config()
    conn = pymysql.connect(**db_cfg)
    cur = conn.cursor()

    # 查询所有 assistant 消息（按 created_at 排序，确保找「前一条」时顺序正确）
    cur.execute("""
        SELECT id, conversation_id, user_id, created_at, request_json, extra, tool_calls_json
        FROM messages
        WHERE role = 'assistant' AND status = 1
        ORDER BY conversation_id, created_at ASC
    """)
    rows = cur.fetchall()
    print(f"加载消息: {len(rows)} 条")

    # 解析所有消息
    all_msgs = []  # [(id, conv_id, user_id, time, messages, rounds, last_role, prompt_tok, cache_hit, miss_tok, tool_calls_json)]
    skipped = 0
    for row in rows:
        msg_id, conv_id, user_id, created_at, req_json, extra_str, tc_json = row
        msgs, rounds, last_role, total = parse_request_json(req_json)
        prompt_tok, cache_hit, miss_tok = parse_extra(extra_str)
        if prompt_tok == 0 or total == 0:
            skipped += 1
            continue
        all_msgs.append({
            'id': msg_id,
            'conv_id': conv_id,
            'user_id': user_id,
            'time': str(created_at),
            'messages': msgs,
            'rounds': rounds,
            'last_role': last_role,
            'prompt_tok': prompt_tok,
            'cache_hit': cache_hit,
            'miss_tok': miss_tok,
            'hit_pct': cache_hit / prompt_tok * 100 if prompt_tok > 0 else 0,
            'tool_calls_json': tc_json,
        })
    print(f"解析成功: {len(all_msgs)}, 跳过: {skipped}")

    # 筛选「普通轮 + cache_hit > 10000」
    extreme = [m for m in all_msgs if m['last_role'] != 'tool' and m['cache_hit'] > 10000]
    extreme.sort(key=lambda x: x['cache_hit'], reverse=True)

    print(f"\n{'='*80}")
    print(f"极端高命中普通轮（cache_hit > 10000）: {len(extreme)} 条")
    print(f"{'='*80}")

    # 对每条极端消息，找到它在同一会话中的前一条 assistant 消息
    for rank, curr in enumerate(extreme):
        curr_idx = all_msgs.index(curr)

        # 往前找到同会话的最近一条 assistant 消息
        prev = None
        for j in range(curr_idx - 1, -1, -1):
            if all_msgs[j]['conv_id'] == curr['conv_id']:
                prev = all_msgs[j]
                break

        # 当前消息的信息
        hit_val = curr['cache_hit']
        miss_val = curr['miss_tok']
        prompt = curr['prompt_tok']
        print(f"\n{'─'*80}")
        print(f"#{rank+1} msg={curr['id'][:8]}  time={curr['time']}  conv={curr['conv_id'][:8]}")
        print(f"   rounds={curr['rounds']}  last_role={curr['last_role']}")
        print(f"   prompt={prompt} tokens  cache_hit={hit_val} (={hit_val}/256={hit_val/256:.1f} chunks)  miss={miss_val}  hit_pct={curr['hit_pct']:.1f}%")
        print(f"   消息总数={len(curr['messages'])}")

        if prev is None:
            print("   ⚠ 找不到前一条消息（可能是该会话第一条 assistant 消息）")
            continue

        # 检查前一条的时间差和是否含 tool_calls
        prev_tc = prev.get('tool_calls_json', '')
        prev_has_tool = bool(prev_tc and prev_tc != '[]')
        print(f"\n   前一条: msg={prev['id'][:8]}  time={prev['time']}  last_role={prev['last_role']}")
        print(f"   前一条 rounds={prev['rounds']}  prompt={prev['prompt_tok']}  cache_hit={prev['cache_hit']} ({prev['hit_pct']:.1f}%)")
        if prev_has_tool:
            print(f"   前一条含 tool_calls")

        # 比较两个 request 的 messages，找公共前缀
        prev_msgs = prev['messages']
        curr_msgs = curr['messages']
        common_cnt, _ = count_common_prefix_msgs(prev_msgs, curr_msgs)

        # 估算公共前缀的 token 数
        common_tokens = 0
        for m in prev_msgs[:common_cnt]:
            common_tokens += estimate_tokens(m.get('content', ''))

        print(f"\n   公共前缀消息数: {common_cnt} / {len(curr_msgs)} (当前) vs {len(prev_msgs)} (前一条)")
        print(f"   公共前缀估算 token: ~{common_tokens}")
        print(f"   cache_hit={hit_val} vs 估算公共前缀={common_tokens}")
        if hit_val > 0:
            print(f"   cache_hit / 估算前缀 = {hit_val / max(1, common_tokens):.2f}")

        # 显示前缀最后一条和第一条不同的消息
        if common_cnt < len(curr_msgs):
            first_diff = curr_msgs[common_cnt]
            preview = first_diff.get('content', '')
            if len(preview) > 200:
                preview = preview[:200] + '...'
            print(f"\n   第一条不同的消息位置 [{common_cnt}] role={first_diff.get('role')}")
            print(f"   内容预览: {preview}")
        else:
            print(f"\n   消息列完全相同！全部 {common_cnt} 条匹配。")

        # 检查 prev 的最后一条消息是不是用户消息（说明两个请求之间只差一条用户输入）
        if prev['last_role'] == 'user':
            print(f"   → 前一条的最后是 user，当前请求新增了 assistant+tool 回复 → 前缀完全重合")

    cur.close()
    conn.close()


if __name__ == '__main__':
    main()
