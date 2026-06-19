#!/usr/bin/env python3
"""
analyze_6000_range.py — 研究普通轮中 6000-7000 范围的 cache_hit 值分布原因。

================================================================================
背景
================================================================================

从全量 cache_hit 分布看，普通轮（非 tool 轮）在 6000-6784 之间形成了一个密集簇：

    cache_hit 值    出现次数   对应 chunk 数 (@256 token/chunk)
   ─────────────  ─────────  ──────────────────────────────────
    6016           56 次      23.5 个 chunk
    6272           16 次      24.5 个 chunk
    6400           33 次      25.0 个 chunk ← 精确整除
    6656           23 次      26.0 个 chunk ← 精确整除
    6784            1 次      26.5 个 chunk

这些值代表跨请求缓存的「真正常态高命中」——不是 ReAct 循环内部红利，
而是滑动窗口下两个独立请求间的公共前缀被 DeepSeek 部分命中的结果。

================================================================================
要回答的问题
================================================================================

1. 为什么同是 6000+ 却有 5 种不同值？
   → 假设：不同请求的公共前缀长度不同，导致命中 chunk 数不同。

2. 6016 和 6656 的区别是什么？
   → 假设：6016 是「系统 prompt + 部分历史」命中，6656 是「系统 prompt + 全部窗口重叠部分」命中。

3. 为什么 6400 和 6656 都是 256 的整数倍（25×256, 26×256）？
   → 假设：这两个值对应 chunk 边界恰好对齐的完美情况。

================================================================================
方法
================================================================================

对每条 6000 < cache_hit < 7000 的普通轮消息：

  1. 找到它在同一会话中紧邻的前一条 assistant 消息。
  2. 逐条比较两个请求的 messages 数组：
     - count_common_prefix(): 从头部开始数相同的消息数 → 公共前缀长度
     - count_common_suffix(): 从尾部倒数相同的消息数 → 公共后缀长度
  3. 观察第一条不同消息出现在什么位置、是什么角色。
  4. 按 cache_hit 值分组，对比各组的公共前缀/后缀模式差异。

================================================================================
数据来源
================================================================================

MySQL xhan_helper_test，表 messages，字段：
  - request_json: {"messages": [{"role": "system", "content": "..."}, ...]}
  - extra:         {"providers": [...], "usage": {"prompt_tokens": ..., "prompt_cache_hit_tokens": ...}}
  - role:          'assistant'（我们只分析 assistant 消息，因为它们是 LLM 请求的产出）
  - status:        1（正常消息，排除已删除）
"""

import json
import yaml
import pymysql
from collections import defaultdict


# ================================================================================
# 工具函数
# ================================================================================


def load_db_config():
    """从 conf/config.dev.yaml 读取 MySQL 连接参数。

    Returns:
      dict: {host, port, user, password, database, charset}
    """
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
    """从 messages.extra 字段提取 token 用量。

    extra 的 JSON 结构（注意所有 key 都是小写）:
      {
        "providers": [{"name": "core_principles", ...}, ...],
        "usage": {                              ← 部分消息没有这个字段！
          "prompt_tokens": 15000,
          "completion_tokens": 500,
          "total_tokens": 15500,
          "prompt_cache_hit_tokens": 6656,     ← 命中的 token 数
          "prompt_cache_miss_tokens": 8344     ← 未命中的 token 数
        }
      }

    Returns:
      (prompt_tokens, cache_hit_tokens, miss_tokens)，无法解析时返回 (0, 0, 0)
    """
    if not extra_str:
        return 0, 0, 0
    try:
        extra = json.loads(extra_str)
        # 注意：key 是小写 'usage'，不是 'Usage'
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


def parse_request_json(req_str):
    """从 messages.request_json 字段提取 messages 数组及相关统计。

    request_json 的 JSON 结构:
      {
        "messages": [
          {"role": "system", "content": "你是 xhanHelper..."},
          {"role": "user", "content": "用户发言1"},
          {"role": "assistant", "content": "助手回复1"},
          ...（按时间正序排列）
        ],
        "system_prompt": "你是 xhanHelper...",  ← 可能不存在（DeepSeek 用 messages[0] 传 system）
        ...
      }

    Returns:
      (messages_list, rounds, last_role, total_msgs)
      - messages_list: [{'role': str, 'content': str}, ...]
      - rounds: role='user' 的数量，即 LLM 收到的对话轮数
      - last_role: 最后一条消息的角色，用于判断是否为 tool 轮
      - total_msgs: 消息数组长度
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


def count_common_prefix(msgs_a, msgs_b):
    """逐条比较两个消息数组的前缀，返回连续相同的第一条消息数。

    比较规则:
      同时比较 role 和 content 字段。两者必须完全一致才算「相同」。
      一旦遇到第一条不同的消息，立即停止——因为 DeepSeek 缓存要求前缀完整连续匹配。

    示例:
      msgs_a = [sys, user1, asst1, user2, asst2]
      msgs_b = [sys, user1, asst1, user2, asst2_new]  ← asst2 变了
      → 返回 4（前 4 条相同）

    Returns:
      int: 前缀连续相同的消息数
    """
    n = min(len(msgs_a), len(msgs_b))
    for i in range(n):
        a, b = msgs_a[i], msgs_b[i]
        # 两条消息必须 role 和 content 都完全一致
        if a.get('role') != b.get('role') or a.get('content') != b.get('content'):
            return i
    return n


def count_common_suffix(msgs_a, msgs_b):
    """从末尾倒着比较两个消息数组的后缀，返回连续相同的消息数。

    这和 count_common_prefix 互补——用于观察「哪些消息在新请求中保持不变」。
    在滑动窗口场景中，后缀不变通常意味着最新几轮消息还在窗口内（没被推出）。

    示例:
      msgs_a = [sys, user1, asst1, user2, asst2]
      msgs_b = [sys, user1, asst1, user3, asst3, user2, asst2]
      → 返回 2（最后 2 条 user2+asst2 相同）

    Returns:
      int: 后缀连续相同的消息数
    """
    n = min(len(msgs_a), len(msgs_b))
    for i in range(n):
        a, b = msgs_a[-(i + 1)], msgs_b[-(i + 1)]
        if a.get('role') != b.get('role') or a.get('content') != b.get('content'):
            return i
    return n


def estimate_tokens(text):
    """粗略估算文本的 token 数。

    基于实测数据，request_json 的整体 chars/token ≈ 1.0（因混有大量
    英文 JSON 和 XML 标签）。单条中文消息的 chars/token ≈ 1.2~1.5。
    这里使用 1:1 作为保守估算。
    """
    return len(text)


def show_msg_sequence(msgs, start, end):
    """展示消息数组中指定范围的角色序列（用于快速判断组成）。

    Returns:
      str: 如 "[0]sys [1]user [2]assistant [3]user ..."
    """
    parts = []
    for i in range(start, min(end, len(msgs))):
        role_short = msgs[i]['role'][:4]  # 取前 4 个字符：syst/user/assi/tool
        parts.append(f"[{i}]{role_short}")
    return ' '.join(parts)


# ================================================================================
# 主流程
# ================================================================================

def main():
    # ---- 连接数据库 ----
    db_cfg = load_db_config()
    conn = pymysql.connect(**db_cfg)
    cur = conn.cursor()

    # ---- 加载全部 assistant 消息 ----
    # 按 conversation_id + created_at 排序，确保「前一条消息」的查找正确
    cur.execute("""
        SELECT id, conversation_id, created_at, request_json, extra
        FROM messages
        WHERE role = 'assistant' AND status = 1
        ORDER BY conversation_id, created_at ASC
    """)
    rows = cur.fetchall()
    print(f"加载消息: {len(rows)} 条")

    # ---- 解析每条消息为结构化数据 ----
    all_msgs = []
    skipped_no_req = 0
    skipped_no_extra = 0
    skipped_no_usage = 0

    for row in rows:
        msg_id, conv_id, created_at, req_json, extra_str = row

        # 解析 request 端
        msgs, rounds, last_role, total = parse_request_json(req_json)
        if total == 0:
            skipped_no_req += 1
            continue

        # 解析 usage 端
        prompt_tok, cache_hit, miss_tok = parse_extra(extra_str)
        if prompt_tok == 0:
            # 可能是没有 usage 字段的旧消息
            skipped_no_usage += 1
            continue

        all_msgs.append({
            'id': msg_id,
            'conv_id': conv_id,
            'time': str(created_at),
            'messages': msgs,          # 完整的 messages 数组
            'rounds': rounds,          # user 消息数
            'last_role': last_role,    # 最后一条消息的角色
            'prompt_tok': prompt_tok,  # 总 prompt token
            'cache_hit': cache_hit,    # 命中 token
            'miss_tok': miss_tok,      # 未命中 token
            'hit_pct': cache_hit / prompt_tok * 100 if prompt_tok else 0,
        })

    print(f"解析成功: {len(all_msgs)} 条")
    print(f"  跳过(无request_json): {skipped_no_req}")
    print(f"  跳过(无usage字段): {skipped_no_usage}")

    # ---- 筛选 6000-7000 范围的普通轮 ----
    target = [
        m for m in all_msgs
        if m['last_role'] != 'tool'          # 排除 ReAct 内部工具轮
        and 6000 < m['cache_hit'] < 7000     # 聚焦 6000-7000 范围
    ]
    target.sort(key=lambda x: x['cache_hit'])

    # 按 cache_hit 值分组
    groups = defaultdict(list)
    for m in target:
        groups[m['cache_hit']].append(m)

    print(f"\n{'='*80}")
    print(f"6000-7000 范围普通轮: {len(target)} 条")
    print(f"不同命中值: {sorted(groups.keys())}")
    print(f"  hit=6016: {len(groups[6016])}条")
    print(f"  hit=6272: {len(groups[6272])}条")
    print(f"  hit=6400: {len(groups[6400])}条")
    print(f"  hit=6656: {len(groups[6656])}条")
    print(f"  hit=6784: {len(groups[6784])}条")
    print(f"{'='*80}")

    # ---- 对每组进行深度分析 ----
    for hit_val in sorted(groups.keys()):
        samples = groups[hit_val]
        print(f"\n{'─'*80}")
        print(f"cache_hit={hit_val}  (= {hit_val}/256 = {hit_val/256:.1f} 个 chunk)  共 {len(samples)} 条")
        print(f"{'─'*80}")

        # 分析前 5 条样本，找公共前缀模式
        prefix_counts = []
        suffix_counts = []
        round_list = []
        prompt_list = []

        for curr in samples[:5]:
            # 找前一条同会话消息
            curr_idx = all_msgs.index(curr)
            prev = None
            for j in range(curr_idx - 1, -1, -1):
                if all_msgs[j]['conv_id'] == curr['conv_id']:
                    prev = all_msgs[j]
                    break

            if prev:
                # 统计前缀和后缀相同的消息数
                common_pref = count_common_prefix(prev['messages'], curr['messages'])
                common_suff = count_common_suffix(prev['messages'], curr['messages'])
                prefix_counts.append(common_pref)
                suffix_counts.append(common_suff)

            round_list.append(curr['rounds'])
            prompt_list.append(curr['prompt_tok'])

        # 汇总统计
        if prefix_counts:
            print(f"  公共前缀消息数: {prefix_counts}  平均={sum(prefix_counts)/len(prefix_counts):.0f}")
        if suffix_counts:
            print(f"  公共后缀消息数: {suffix_counts}  平均={sum(suffix_counts)/len(suffix_counts):.0f}")
        print(f"  rounds 范围: {min(round_list)}-{max(round_list)}")
        print(f"  prompt 范围: {min(prompt_list)}-{max(prompt_list)}")

        # ---- 对第一条样本做详细展示 ----
        sample = samples[0]
        curr_idx = all_msgs.index(sample)
        prev = None
        for j in range(curr_idx - 1, -1, -1):
            if all_msgs[j]['conv_id'] == sample['conv_id']:
                prev = all_msgs[j]
                break

        # 数各角色的消息数量
        role_counts = defaultdict(int)
        for m in sample['messages']:
            role_counts[m['role']] += 1
        print(f"  当前请求角色分布: sys={role_counts.get('system',0)} user={role_counts.get('user',0)} "
              f"assistant={role_counts.get('assistant',0)} tool={role_counts.get('tool',0)}")
        print(f"  rounds={sample['rounds']}  prompt={sample['prompt_tok']}  "
              f"hit_pct={sample['hit_pct']:.1f}%  miss={sample['miss_tok']}")

        # 展示前后请求的关系
        if prev:
            # 公共前缀
            common = count_common_prefix(prev['messages'], sample['messages'])
            print(f"  前一条 msg={prev['id'][:8]}  rounds={prev['rounds']}  "
                  f"cache_hit={prev['cache_hit']}  last_role={prev['last_role']}")
            print(f"  前一条消息总数={len(prev['messages'])}  当前消息总数={len(sample['messages'])}")
            print(f"  公共前缀: {common} 条消息相同 (= 前 {common}/{len(sample['messages'])} "
                  f"即 {common/max(1,len(sample['messages']))*100:.0f}%)")

            # 展示前缀尾部：从 common-2 到 common+2 的消息角色序列
            show_start = max(0, common - 3)
            show_end = min(len(sample['messages']), common + 3)
            print(f"  前缀断裂处附近 [{show_start}:{show_end}]: {show_msg_sequence(sample['messages'], show_start, show_end)}")

            # 第一条不同的消息（= 前缀断裂点）
            if common < len(sample['messages']):
                diff_msg = sample['messages'][common]
                preview = diff_msg.get('content', '')
                if len(preview) > 200:
                    preview = preview[:200] + '...'
                print(f"  断裂点 [{common}] role={diff_msg['role']} 内容: {preview.replace(chr(10), ' ')}")

            # 公共后缀
            suff = count_common_suffix(prev['messages'], sample['messages'])
            if suff > 0:
                print(f"  公共后缀: {suff} 条消息相同（尾部不变部分）")

            # 前一条的角色分布
            prev_role_counts = defaultdict(int)
            for m in prev['messages']:
                prev_role_counts[m['role']] += 1
            role_delta = {}
            for r in set(list(role_counts.keys()) + list(prev_role_counts.keys())):
                curr_cnt = role_counts.get(r, 0)
                prev_cnt = prev_role_counts.get(r, 0)
                if curr_cnt != prev_cnt:
                    role_delta[r] = f"{prev_cnt}→{curr_cnt}"
            if role_delta:
                print(f"  角色数变化: {role_delta}")

        # ---- 估算理论公共前缀 token 数 ----
        # 用前一条的前 common 条消息的字符数累加
        if prev and common > 0:
            pref_tokens = sum(estimate_tokens(m.get('content', '')) for m in prev['messages'][:common])
            print(f"  公共前缀估算 token: ~{pref_tokens}  (cache_hit={sample['cache_hit']}, 比值={sample['cache_hit']/max(1,pref_tokens):.2f})")

    # ---- 总结 ----
    print(f"\n{'='*80}")
    print("总结")
    print(f"{'='*80}")
    print(f"{'hit值':<8} {'chunks':>8} {'样本':>5} {'均前缀':>8} {'均后缀':>8} {'均rounds':>9} {'均prompt':>9}")
    for hit_val in sorted(groups.keys()):
        samples = groups[hit_val]
        pref_lst, suff_lst, rnd_lst, prm_lst = [], [], [], []
        for curr in samples[:3]:
            curr_idx = all_msgs.index(curr)
            for j in range(curr_idx - 1, -1, -1):
                if all_msgs[j]['conv_id'] == curr['conv_id']:
                    prev = all_msgs[j]
                    pref_lst.append(count_common_prefix(prev['messages'], curr['messages']))
                    suff_lst.append(count_common_suffix(prev['messages'], curr['messages']))
                    break
            rnd_lst.append(curr['rounds'])
            prm_lst.append(curr['prompt_tok'])
        avg_pref = sum(pref_lst) / max(1, len(pref_lst))
        avg_suff = sum(suff_lst) / max(1, len(suff_lst))
        avg_rnds = sum(rnd_lst) / max(1, len(rnd_lst))
        avg_prm = sum(prm_lst) / max(1, len(prm_lst))
        print(f"{hit_val:<8} {hit_val/256:>8.1f} {len(samples):>5} {avg_pref:>8.0f} {avg_suff:>8.0f} {avg_rnds:>9.0f} {avg_prm:>9.0f}")

    cur.close()
    conn.close()


if __name__ == '__main__':
    main()
