import json
import logging
import httpx
from app.config import settings

logger = logging.getLogger("xhan.llm")
API_URL = f"{settings.llm_base_url}/chat/completions"

SYSTEM_PROMPT = """你是 xhanHelper，一个个人 AI 助手。你的职责是帮助用户高效完成任务。

核心原则：
1. 简洁回复：用最少的字把事说清楚
2. 诚实：不知道就说不知道，不要编造
3. 主动：如果用户意图不明确，追问关键信息"""


async def call_llm(messages: list[dict]) -> str:
    """调用豆包 /chat/completions API (OpenAI 兼容)，返回完整文本"""
    body = {
        "model": settings.llm_model,
        "messages": messages,
    }
    logger.info(f"LLM 请求: model={settings.llm_model}, msg_count={len(messages)}")

    async with httpx.AsyncClient(timeout=120) as client:
        resp = await client.post(
            API_URL,
            headers={
                "Content-Type": "application/json",
                "Authorization": f"Bearer {settings.llm_api_key}",
            },
            json=body,
        )

    if resp.status_code != 200:
        logger.error(f"LLM 返回 {resp.status_code}: {resp.text[:300]}")
        raise RuntimeError(f"LLM error {resp.status_code}: {resp.text[:300]}")

    data = resp.json()
    choices = data.get("choices", [])
    if not choices:
        raise RuntimeError("LLM returned no choices")
    content = choices[0].get("message", {}).get("content", "")
    if not content:
        raise RuntimeError("LLM returned empty content")
    return content
