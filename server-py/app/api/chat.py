import json
import uuid
import asyncio

from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy import select
from sse_starlette.sse import EventSourceResponse

from app.db.session import get_db, async_session
from app.api.deps import get_device
from app.models.device import Device
from app.models.conversation import Conversation
from app.models.message import Message
from app.schemas import ChatRequest, MessageOut
from app.tools.registry import Registry
from app.agent.orchestrator import run_agent

router = APIRouter()


# 全局 registry，由 main.py 组装时注入
_registry: Registry | None = None


def init_chat(registry: Registry):
    global _registry
    _registry = registry


def _build_history(messages: list[Message]) -> list[dict]:
    return [{"role": m.role, "content": m.content} for m in messages]


@router.post("/chat")
async def chat(req: ChatRequest, device: Device = Depends(get_device)):
    async with async_session() as db:
        # 获取或创建会话
        if req.conversation_id:
            result = await db.execute(
                select(Conversation).where(Conversation.id == req.conversation_id)
            )
            conv = result.scalar_one_or_none()
        else:
            conv = None

        if conv is None:
            conv = Conversation(device_id=device.id)
            db.add(conv)
            await db.commit()
            await db.refresh(conv)

        # 保存用户消息
        user_msg = Message(conversation_id=conv.id, role="user", content=req.message)
        db.add(user_msg)
        await db.commit()

        # 加载历史
        result = await db.execute(
            select(Message)
            .where(Message.conversation_id == conv.id)
            .order_by(Message.created_at.asc())
            .limit(20)
        )
        history = result.scalars().all()

    conversation_id = conv.id

    async def event_stream():
        full_content = ""
        try:
            async for event in run_agent(
                messages=_build_history(history),
                device_id=device.id,
                registry=_registry,
            ):
                if event["type"] == "text_delta":
                    full_content += event["content"]
                    yield {
                        "event": "message",
                        "data": json.dumps({"delta": event["content"]}, ensure_ascii=False),
                    }
                elif event["type"] == "tool_call":
                    yield {"event": "tool_call", "data": event["content"]}
                elif event["type"] == "tool_result":
                    yield {"event": "tool_result", "data": event["content"]}
                elif event["type"] == "done":
                    if full_content.strip():
                        msg_id = str(uuid.uuid4())
                        async with async_session() as db:
                            assistant_msg = Message(
                                id=msg_id,
                                conversation_id=conversation_id,
                                role="assistant",
                                content=full_content,
                            )
                            db.add(assistant_msg)
                            await db.commit()
                        yield {
                            "event": "done",
                            "data": json.dumps({
                                "conversation_id": conversation_id,
                                "message_id": msg_id,
                            }),
                        }
                    else:
                        yield {
                            "event": "done",
                            "data": json.dumps({"conversation_id": conversation_id}),
                        }
                elif event["type"] == "error":
                    yield {"event": "error", "data": event["error"]}
        except asyncio.CancelledError:
            pass  # 客户端断开

    return EventSourceResponse(event_stream())
