#!/usr/bin/env python3
"""
plot_cache_hit_after_1546.py — 绘制 15:46 (BJT) 之后的缓存命中率 + 请求重复程度曲线。

三条曲线：
  1. cache_hit_pct — DeepSeek 返回的缓存命中率
  2. text_overlap_pct — 相邻请求的 request_json 逐字符公共前缀占比
  3. msg_overlap_pct — 相邻请求的 messages 位置级重叠占比（同位置同内容消息数/总消息数）

用法: cd server && python3 ../docs/learning/responses/plot_cache_hit_after_1546.py
"""

import json
import yaml
import pymysql
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
from datetime import datetime, timezone, timedelta


def load_db_config():
    with open('conf/config.dev.yaml') as f:
        cfg = yaml.safe_load(f)
    db = cfg['db']
    return {
        'host': db['host'], 'port': db['port'],
        'user': db['user'], 'password': db['password'],
        'database': db['database'], 'charset': 'utf8mb4',
    }


def main():
    BJ = timezone(timedelta(hours=8))
    db_cfg = load_db_config()
    conn = pymysql.connect(**db_cfg)
    cur = conn.cursor()

    # 16:00 BJT = 08:00 UTC
    cutoff = '2026-06-19 08:00:00'
    cur.execute("""
        SELECT id, conversation_id, created_at, request_json, extra
        FROM messages
        WHERE role = 'assistant' AND status = 1
          AND created_at >= %s
          AND request_json IS NOT NULL AND extra IS NOT NULL
        ORDER BY conversation_id, created_at ASC
    """, (cutoff,))
    rows = cur.fetchall()
    print(f"加载消息: {len(rows)} 条 (after {cutoff} UTC)")

    # 解析
    all_msgs = []
    for row in rows:
        try:
            req = json.loads(row[3])
            extra = json.loads(row[4])
        except Exception:
            continue
        u = extra.get('usage', {})
        pt = u.get('prompt_tokens', 0) or 0
        ch = u.get('prompt_cache_hit_tokens', 0) or 0
        if not pt:
            continue
        all_msgs.append({
            'id': row[0][:8],
            'conv': row[1][:8],
            'time': row[2].replace(tzinfo=timezone.utc).astimezone(BJ),
            'msgs': req.get('messages', []),
            'req_json': row[3],
            'prompt': pt,
            'cache_hit': ch,
            'hit_pct': ch / pt * 100,
        })

    print(f"解析成功: {len(all_msgs)}")

    # 只取默认会话
    default_conv = [m for m in all_msgs if m['conv'].startswith('3b3f6e2f')]
    print(f"默认会话: {len(default_conv)} 条")

    if not default_conv:
        print("没有默认会话数据，使用所有会话")
        default_conv = all_msgs

    # 计算每条与前一条的重复程度
    times = []
    hit_pcts = []
    text_overlaps = []   # request_json 逐字符前缀重复 %
    msg_overlaps = []    # messages 位置级重复 %
    hit_tokens = []

    for i in range(len(default_conv)):
        curr = default_conv[i]
        times.append(curr['time'])
        hit_pcts.append(curr['hit_pct'])
        hit_tokens.append(curr['cache_hit'])

        if i == 0:
            text_overlaps.append(None)
            msg_overlaps.append(None)
            continue

        prev = default_conv[i - 1]
        if prev['conv'] != curr['conv']:
            text_overlaps.append(None)
            msg_overlaps.append(None)
            continue

        # 1. Text prefix overlap: request_json 字符前缀重复
        prev_text = prev['req_json']
        curr_text = curr['req_json']
        same_chars = 0
        for j in range(min(len(prev_text), len(curr_text))):
            if prev_text[j] == curr_text[j]:
                same_chars += 1
            else:
                break
        text_overlaps.append(same_chars / len(curr_text) * 100)

        # 2. Message position overlap: 同位置同内容消息占比
        prev_msgs = prev['msgs']
        curr_msgs = curr['msgs']
        same_msgs = 0
        for j in range(min(len(prev_msgs), len(curr_msgs))):
            if (prev_msgs[j].get('role') == curr_msgs[j].get('role') and
                    prev_msgs[j].get('content') == curr_msgs[j].get('content')):
                same_msgs += 1
            else:
                break
        msg_overlaps.append(same_msgs / max(len(curr_msgs), 1) * 100)

    # 检查是否有数据
    valid = [i for i in range(len(times)) if text_overlaps[i] is not None]
    if not valid:
        print("没有相邻对话对！")
        cur.close()
        conn.close()
        return

    # ======== 绘图 ========
    fig, axes = plt.subplots(3, 1, figsize=(16, 10), sharex=True)

    # 颜色
    colors = {'hit': '#e74c3c', 'text': '#3498db', 'msg': '#2ecc71'}

    # 子图1: 缓存命中率
    ax1 = axes[0]
    ax1.plot(times, hit_pcts, color=colors['hit'], linewidth=1.5, marker='o', markersize=10, label='cache hit %')
    ax1.axhline(y=15, color='gray', linestyle='--', alpha=0.5, label='15% baseline')
    ax1.set_ylabel('Cache Hit %', color=colors['hit'])
    ax1.tick_params(axis='y', labelcolor=colors['hit'])
    ax1.legend(loc='upper right', fontsize=8)
    ax1.grid(True, alpha=0.3)
    ax1.set_ylim(-10, 110)
    # 标注每个点的值
    for t, v in zip(times, hit_pcts):
        ax1.annotate(f'{v:.0f}%', (t, v), textcoords='offset points', xytext=(0, 8),
                     fontsize=9, color=colors['hit'], ha='center')

    # 子图2: 文本前缀重叠 (request_json)
    ax2 = axes[1]
    ax2.plot(times, text_overlaps, color=colors['text'], linewidth=1.5, marker='o', markersize=10)
    ax2.set_ylabel('Text Prefix Overlap %', color=colors['text'])
    ax2.tick_params(axis='y', labelcolor=colors['text'])
    ax2.grid(True, alpha=0.3)
    ax2.set_ylim(-10, 110)
    for t, v in zip(times, text_overlaps):
        if v is not None:
            ax2.annotate(f'{v:.0f}%', (t, v), textcoords='offset points', xytext=(0, 8),
                         fontsize=9, color=colors['text'], ha='center')

    # 子图3: 消息位置重叠
    ax3 = axes[2]
    ax3.plot(times, msg_overlaps, color=colors['msg'], linewidth=1.5, marker='o', markersize=10)
    ax3.set_ylabel('Msg Position Overlap %', color=colors['msg'])
    ax3.tick_params(axis='y', labelcolor=colors['msg'])
    ax3.grid(True, alpha=0.3)
    ax3.set_ylim(-10, 110)
    for t, v in zip(times, msg_overlaps):
        if v is not None:
            ax3.annotate(f'{v:.0f}%', (t, v), textcoords='offset points', xytext=(0, 8),
                         fontsize=9, color=colors['msg'], ha='center')

    # X轴格式
    ax3.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M'))
    ax3.xaxis.set_major_locator(mdates.MinuteLocator(interval=10))
    plt.setp(ax3.xaxis.get_majorticklabels(), rotation=45, ha='right')
    t0_str = times[0].strftime('%H:%M')
    t1_str = times[-1].strftime('%H:%M')
    ax3.set_xlabel('Time (BJT) -- {} messages, {}~{}'.format(len(times), t0_str, t1_str))

    fig.suptitle(f'Cache Hit vs Request Overlap (after 16:00 BJT, N=10 start, N={len(times)})',
                 fontsize=13, fontweight='bold')
    plt.tight_layout()

    outpath = '../docs/learning/responses/cache_hit_after_1600_N10.png'
    plt.savefig(outpath, dpi=150)
    print(f"\n图表已保存: {outpath}")
    print(f"时间范围: {times[0]} ~ {times[-1]}")
    print(f"消息数: {len(times)}")
    print(f"缓存命中率: min={min(hit_pcts):.1f}% max={max(hit_pcts):.1f}% avg={sum(hit_pcts)/len(hit_pcts):.1f}%")
    print(f"文本前缀重叠: avg={sum(x for x in text_overlaps if x is not None)/len(valid):.1f}%")
    print(f"消息位置重叠: avg={sum(x for x in msg_overlaps if x is not None)/len(valid):.1f}%")

    cur.close()
    conn.close()


if __name__ == '__main__':
    main()
