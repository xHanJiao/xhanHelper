import json
import httpx
from app.config import settings

API_URL = f"{settings.llm_base_url}/responses"

SYSTEM_PROMPT = """你是 xhanHelper，一个个人 AI 助手。你的职责是帮助用户高效完成任务。

核心原则：
1. 简洁回复：用最少的字把事说清楚
2. 诚实：不知道就说不知道，不要编造
3. 主动：如果用户意图不明确，追问关键信息"""


async def call_llm(messages: list[dict]) -> str:
    """调用豆包 /responses API，返回完整文本。
    messages 应已包含 system prompt（由 orchestrator 注入）"""
    async with httpx.AsyncClient(timeout=120) as client:
        resp = await client.post(
            API_URL,
            headers={
                "Content-Type": "application/json",
                "Authorization": f"Bearer {settings.llm_api_key}",
            },
            json={
                "model": settings.llm_model,
                "input": messages,
                "store": True,
                "max_output_tokens": 2048,
            },
        )

    if resp.status_code != 200:
        raise RuntimeError(f"LLM error {resp.status_code}: {resp.text}")

    data = resp.json()
    if data.get("error"):
        raise RuntimeError(f"LLM error: {data['error']}")

    if data.get("status") != "completed":
        raise RuntimeError(f"LLM not completed: {data.get('status')}")

    content = _extract_content(data)
    if not content:
        raise RuntimeError("LLM returned no content")
    return content


def _extract_content(data: dict) -> str:
    for output in data.get("output", []):
        if output.get("type") == "message" and output.get("role") == "assistant":
            for item in output.get("content", []):
                if item.get("type") == "output_text" and item.get("text"):
                    return item["text"]
    return ""
