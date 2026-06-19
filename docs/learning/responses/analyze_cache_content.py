#!/usr/bin/env python3
"""
analyze_cache_content.py — 通过对比正例/反例的原始请求文本，推测 DeepSeek 缓存的具体内容。

================================================================================
核心思路
================================================================================

之前用 count_common_prefix() 逐条比较消息的 role+content，发现 6000+ 命中
的请求对之间只有 1 条消息相同（system prompt），无法解释 23-26 个 chunk 为何命中。

新方法：不比较消息，而是直接比较 DeepSeek 收到的原始文本。

DeepSeek 的 API 请求是 JSON 格式。request_json 字段中存储的就是发送给
DeepSeek 的完整 JSON 字符串。两个请求即使消息内容不同，JSON 结构化的
部分（如 `{"role":"user","content":"` 这些框架文本）在 token 层面可能是
相同的 → 这些「结构性 token」形成了跨请求的公共前缀。

================================================================================
方法步骤
================================================================================

1. 从同一会话中取时间相邻的一对请求：
   - A（前一条）：cache_hit=768（反例，仅 system prompt 命中）
   - B（后一条）：cache_hit=6656（正例，大量命中）

2. 取两个请求的 request_json 原始字符串。

3. 以 ~256 字符为窗口（≈ 256 token，因为实测 chars/token ≈ 1.0），
   将两个请求分别切成 chunk 序列。

4. 从首个 chunk 开始，逐个比较 A 和 B 的对应 chunk：
   - 相同 → 标记为「缓存命中候选」
   - 不同 → 标记为「缓存断裂点」，停止

5. 展示：
   - 命中 chunk 的数量和内容摘要
   - 断裂点附近的原始文本（了解为什么会断）
   - cache_hit 与 命中 chunk 数 × 256 的对应关系

================================================================================
数据来源
================================================================================

MySQL messages 表：
  - request_json: DeepSeek API 请求的完整 JSON 字符串
  - extra.usage.prompt_cache_hit_tokens: DeepSeek 返回的命中 token 数
"""

import json
import yaml
import pymysql
from collections import defaultdict


# ==========================================================================
# 工具函数
# ==========================================================================

def load_db_config():
    """从 conf/config.dev.yaml 读取 MySQL 连接参数。"""
    with open('conf/config.dev.yaml') as f:
        cfg = yaml.safe_load(f)
    db = cfg['db']
    return {
        'host': db['host'], 'port': db['port'],
        'user': db['user'], 'password': db['password'],
        'database': db['database'], 'charset': 'utf8mb4',
    }


def parse_extra(extra_str):
    """从 extra JSON 提取 usage 字段。"""
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


def chunk_text(text, chunk_size=256):
    """将文本按 ~chunk_size 字符切成块。

    使用字符数模拟 token 数（实测 chars/token ≈ 1.0）。

    Returns:
      [(chunk_idx, chunk_text, char_start, char_end), ...]
    """
    chunks = []
    pos = 0
    idx = 0
    while pos < len(text):
        end = min(pos + chunk_size, len(text))
        chunks.append((idx, text[pos:end], pos, end))
        pos = end
        idx += 1
    return chunks


def compare_chunks(chunks_a, chunks_b):
    """逐个比较两个 chunk 序列，返回前缀相同的 chunk 数。

    Returns:
      same_count: 前缀相同的 chunk 数
      first_diff: 第一个不同的位置（从 0 开始），全部相同则返回 -1
    """
    n = min(len(chunks_a), len(chunks_b))
    for i in range(n):
        _, txt_a, _, _ = chunks_a[i]
        _, txt_b, _, _ = chunks_b[i]
        if txt_a != txt_b:
            return i, i
    return n, -1


# ==========================================================================
# 主流程
# ==========================================================================

def main():
    db_cfg = load_db_config()
    conn = pymysql.connect(**db_cfg)
    cur = conn.cursor()

    # 加载全部 assistant 消息
    cur.execute("""
        SELECT id, conversation_id, created_at, request_json, extra
        FROM messages
        WHERE role = 'assistant' AND status = 1
          AND request_json IS NOT NULL
          AND extra IS NOT NULL
        ORDER BY conversation_id, created_at ASC
    """)
    rows = cur.fetchall()
    print(f"加载消息: {len(rows)} 条")

    # 解析
    all_msgs = []
    for row in rows:
        msg_id, conv_id, created_at, req_json, extra_str = row
        prompt_tok, cache_hit, miss_tok = parse_extra(extra_str)
        if prompt_tok == 0:
            continue
        # 解析 request_json 获取 messages（用于 rounds 统计）
        try:
            req = json.loads(req_json)
            msgs = req.get('messages', [])
            rounds = sum(1 for m in msgs if m.get('role') == 'user')
            last_role = msgs[-1]['role'] if msgs else ''
        except (json.JSONDecodeError, TypeError):
            rounds, last_role = 0, ''

        all_msgs.append({
            'id': msg_id,
            'conv_id': conv_id,
            'time': str(created_at),
            'req_json': req_json,        # ← 保留原始 JSON 字符串！
            'rounds': rounds,
            'last_role': last_role,
            'prompt_tok': prompt_tok,
            'cache_hit': cache_hit,
            'miss_tok': miss_tok,
            'hit_pct': cache_hit / prompt_tok * 100 if prompt_tok else 0,
        })

    print(f"解析成功: {len(all_msgs)}")

    # ---- 找正例/反例对 ----
    # 条件：
    #   1. 同一会话
    #   2. 时间相邻（B 紧接在 A 之后）
    #   3. A = cache_hit=768（反例，仅 system prompt）
    #   4. B = cache_hit=6656（正例，大量命中） 或 6016/6400
    #   5. 都不是 tool 轮

    pairs = []  # [(msg_a, msg_b), ...]
    for i in range(1, len(all_msgs)):
        a = all_msgs[i - 1]
        b = all_msgs[i]

        # 同会话 + 时间相邻
        if a['conv_id'] != b['conv_id']:
            continue

        # 排除 tool 轮
        if a['last_role'] == 'tool' or b['last_role'] == 'tool':
            continue

        # A 是反例（768）且 B 是正例（>6000）
        if a['cache_hit'] == 768 and b['cache_hit'] > 6000:
            pairs.append((a, b))

    print(f"\n找到 {len(pairs)} 对 正例/反例")

    # ---- 对每对做 chunk 级对比 ----
    for pair_idx, (a, b) in enumerate(pairs[:5]):  # 最多分析 5 对
        print(f"\n{'='*80}")
        print(f"正例/反例对 #{pair_idx+1}")
        print(f"{'='*80}")

        print(f"\n反例 A (cache_hit=768):")
        print(f"  msg={a['id'][:8]}  time={a['time']}  rounds={a['rounds']}")
        print(f"  prompt={a['prompt_tok']} tokens  cache_hit={a['cache_hit']}  "
              f"hit_pct={a['hit_pct']:.1f}%  req_json_len={len(a['req_json'])}")

        print(f"\n正例 B (cache_hit={b['cache_hit']}):")
        print(f"  msg={b['id'][:8]}  time={b['time']}  rounds={b['rounds']}")
        print(f"  prompt={b['prompt_tok']} tokens  cache_hit={b['cache_hit']}  "
              f"hit_pct={b['hit_pct']:.1f}%  req_json_len={len(b['req_json'])}")

        # ---- Chunk 级对比 ----
        chunks_a = chunk_text(a['req_json'], 256)
        chunks_b = chunk_text(b['req_json'], 256)

        same_cnt, break_at = compare_chunks(chunks_a, chunks_b)
        estimated_hit = same_cnt * 256  # 用 chunk 数估算命中 token

        print(f"\nChunk 对比 (chunk_size=256 chars):")
        print(f"  A 共 {len(chunks_a)} 个 chunk  B 共 {len(chunks_b)} 个 chunk")
        print(f"  前缀相同 chunk 数: {same_cnt}")
        print(f"  估算命中 token: {same_cnt} × 256 = {estimated_hit}")
        print(f"  DeepSeek 实际命中: {b['cache_hit']} tokens")
        print(f"  估算/实际 比值: {estimated_hit / max(1, b['cache_hit']):.2f}")

        if b['cache_hit'] > 0:
            # 反推：DeepSeek 实际用了多少个 chunk = cache_hit / 256
            actual_chunks = b['cache_hit'] / 256
            print(f"  实际命中 chunk 数(按256): {actual_chunks:.1f}")
            # 如果相同 chunk 数远大于实际命中，说明 chunk_size 估计需要调
            # 如果相同 chunk 数 ≈ 实际命中，说明 256 字符 ≈ 256 token 成立

        # ---- 展示断裂点附近的内容 ----
        if break_at >= 0 and break_at < len(chunks_b):
            print(f"\n断裂点位置: chunk[{break_at}]")
            print(f"  (前 {break_at} 个 chunk 相同，第 {break_at} 个 chunk 开始不同)")

            # 展示断裂点附近的 chunk 内容（截取前 120 字符）
            if break_at > 0:
                _, prev_text, _, _ = chunks_b[break_at - 1]
                prev_preview = prev_text[-80:]  # 前一个 chunk 的末尾
                print(f"\n  chunk[{break_at-1}] 末尾: ...{prev_preview}")

            _, break_text, char_start, char_end = chunks_b[break_at]
            break_preview = break_text[:200]
            print(f"\n  chunk[{break_at}] (断裂点) 内容预览:")
            print(f"  ┌{'─'*78}")
            # 用不同行展示，限制每行 78 字符
            for line_offset in range(0, len(break_preview), 78):
                print(f"  │ {break_preview[line_offset:line_offset+78]}")
            print(f"  └{'─'*78}")
            print(f"  断裂点字符位置: {char_start}-{char_end} / {len(b['req_json'])}")

            # 如果是 JSON，尝试展示断裂点在哪个消息附近
            # 数一下到断裂点为止有多少个 "role" 关键字 → 推测在哪个消息附近
            role_count_before = b['req_json'][:char_start].count('"role"')
            print(f"  断裂点之前有 {role_count_before} 个 \"role\" → 大约在第 {role_count_before} 条消息附近")

        # ---- 展示 B 中命中部分的内容摘要 ----
        print(f"\n  B 的请求中，命中的 {same_cnt} 个 chunk 覆盖了前 {same_cnt * 256} 字符")
        # 这些命中 chunk 对应的 JSON 内容
        hit_text = b['req_json'][:same_cnt * 256]
        # 数 role 出现次数 → 大约覆盖了几条消息
        hit_role_count = hit_text.count('"role"')
        print(f"  命中部分约有 {hit_role_count} 个 \"role\" 关键字 → 覆盖约 {hit_role_count} 条消息")

        # ---- 展示 A 和 B 请求中 messages 的简要 diff ----
        # 只是为了方便理解两个请求的区别
        try:
            req_a = json.loads(a['req_json'])
            req_b = json.loads(b['req_json'])
            msgs_a = req_a.get('messages', [])
            msgs_b = req_b.get('messages', [])

            # 数消息数差异
            role_diff_a = defaultdict(int)
            role_diff_b = defaultdict(int)
            for m in msgs_a:
                role_diff_a[m['role']] += 1
            for m in msgs_b:
                role_diff_b[m['role']] += 1

            print(f"\n  两个请求的消息组成对比:")
            all_roles = set(list(role_diff_a.keys()) + list(role_diff_b.keys()))
            for role in sorted(all_roles):
                ca, cb = role_diff_a.get(role, 0), role_diff_b.get(role, 0)
                marker = ' ←' if ca != cb else '  '
                print(f"    {role}: {ca} vs {cb}{marker}")
        except (json.JSONDecodeError, KeyError):
            pass

        print()

    cur.close()
    conn.close()


if __name__ == '__main__':
    main()
