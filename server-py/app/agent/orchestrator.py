import json
import re
import asyncio
from typing import AsyncIterator

from app.tools.registry import Registry
from app.tools.base import ExecuteContext
from app.agent.llm_client import call_llm, SYSTEM_PROMPT


def _parse_llm_response(text: str) -> dict | None:
    """解析 LLM 回复中的 JSON，返回 {reply, tool_calls} 或 None"""
    cleaned = text.strip()
    cleaned = re.sub(r"^```(?:json)?\s*", "", cleaned)
    cleaned = re.sub(r"\s*```$", "", cleaned).strip()

    start = cleaned.find("{")
    if start < 0:
        return None

    try:
        data = json.loads(cleaned[start:])
        if "tool_calls" not in data:
            data["tool_calls"] = []
        return data
    except json.JSONDecodeError:
        return None


async def run_agent(
    messages: list[dict],
    device_id: str,
    registry: Registry,
) -> AsyncIterator[dict]:
    """
    ReAct 循环 — 返回 SSE 事件 dict 的 async generator

    事件类型:
      {"type": "text_delta", "content": "x"}
      {"type": "tool_call", "content": "{...}"}
      {"type": "tool_result", "content": "{...}"}
      {"type": "done"}
      {"type": "error", "error": "..."}
    """
    msgs = list(messages)  # 可变的拷贝
    sys_prompt = SYSTEM_PROMPT + registry.generate_prompt()
    full_reply = ""

    for iteration in range(5):
        # 构建消息列表，第一轮注入 system prompt
        if iteration == 0:
            call_msgs = [{"role": "system", "content": sys_prompt}] + msgs
        else:
            call_msgs = [{"role": "system", "content": sys_prompt}] + msgs

        try:
            resp_text = await call_llm(call_msgs)
        except Exception as e:
            yield {"type": "error", "error": str(e)}
            return

        parsed = _parse_llm_response(resp_text)

        if parsed is None:
            # 纯文本 → 逐字输出 → done
            for ch in resp_text:
                yield {"type": "text_delta", "content": ch}
                full_reply += ch
            yield {"type": "done"}
            return

        # 有 JSON → 检查 tool_calls
        tool_calls = parsed.get("tool_calls", [])
        reply = parsed.get("reply", "")

        if not tool_calls:
            # 纯聊天 JSON → 输出 reply → done
            for ch in reply:
                yield {"type": "text_delta", "content": ch}
                full_reply += ch
            yield {"type": "done"}
            return

        # 有 tool_calls → 执行
        msgs.append({"role": "assistant", "content": resp_text})

        for tc in tool_calls:
            yield {"type": "tool_call", "content": json.dumps(tc, ensure_ascii=False)}

            ctx = ExecuteContext(device_id=device_id)
            result = await registry.execute(ctx, tc["tool"], tc["function"], tc.get("args", {}))
            result_json = result.model_dump_json()

            yield {"type": "tool_result", "content": result_json}

            msgs.append({"role": "user", "content": f"[工具执行结果] {result_json}"})

        # 继续下一轮迭代，等 LLM 拿到 tool results 后的最终回复

    yield {"type": "error", "error": "max iterations reached"}
    yield {"type": "done"}
