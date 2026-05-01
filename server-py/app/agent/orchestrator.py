import json
import re
import time
import logging
from typing import AsyncIterator

from app.tools.registry import Registry
from app.tools.base import ExecuteContext
from app.agent.llm_client import call_llm, SYSTEM_PROMPT

logger = logging.getLogger("xhan.agent")


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
        if iteration == 0:
            call_msgs = [{"role": "system", "content": sys_prompt}] + msgs
        else:
            call_msgs = [{"role": "system", "content": sys_prompt}] + msgs

        t0 = time.time()
        logger.info(f"[iter={iteration}] 调用 LLM, msg_count={len(call_msgs)}")
        try:
            resp_text = await call_llm(call_msgs)
        except Exception as e:
            logger.error(f"[iter={iteration}] LLM 失败: {e}")
            yield {"type": "error", "error": str(e)}
            return
        logger.info(f"[iter={iteration}] LLM 返回, 耗时={time.time()-t0:.1f}s, len={len(resp_text)}")

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
            logger.info(f"[iter={iteration}] 纯文本/聊天, reply_len={len(reply)}, 输出→done")
            for ch in reply:
                yield {"type": "text_delta", "content": ch}
                full_reply += ch
            yield {"type": "done"}
            return

        # 有 tool_calls → 执行
        logger.info(f"[iter={iteration}] 检测到 {len(tool_calls)} 个 tool_call")
        msgs.append({"role": "assistant", "content": resp_text})

        for tc in tool_calls:
            logger.info(f"[iter={iteration}] 执行工具: {tc['tool']}.{tc['function']}")
            yield {"type": "tool_call", "content": json.dumps(tc, ensure_ascii=False)}

            t_tool = time.time()
            ctx = ExecuteContext(device_id=device_id)
            result = await registry.execute(ctx, tc["tool"], tc["function"], tc.get("args", {}))
            result_json = result.model_dump_json()
            logger.info(f"[iter={iteration}] 工具结果: success={result.success}, 耗时={time.time()-t_tool:.1f}s")

            yield {"type": "tool_result", "content": result_json}

            msgs.append({"role": "user", "content": f"[工具执行结果] {result_json}"})

        # 继续下一轮迭代，等 LLM 拿到 tool results 后的最终回复

    yield {"type": "error", "error": "max iterations reached"}
    yield {"type": "done"}
