import json
import uuid
import time
import logging

from fastapi import APIRouter, Depends
from fastapi.responses import StreamingResponse
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy import select

from app.db.session import async_session
from app.api.deps import get_device
from app.models.device import Device
from app.models.conversation import Conversation
from app.models.message import Message
from app.schemas import ChatRequest
from app.tools.registry import Registry
from app.agent.orchestrator import run_agent

logger = logging.getLogger("xhan.chat")
router = APIRouter()

_registry: Registry | None = None


def init_chat(registry: Registry):
    global _registry
    _registry = registry


def _build_history(messages: list[Message]) -> list[dict]:
    return [{"role": m.role, "content": m.content} for m in messages]


@router.post("/chat")
async def chat(req: ChatRequest, device: Device = Depends(get_device)):
    t0 = time.time()

    # 1. DB: 获取/创建会话 + 保存用户消息 + 加载历史
    t_db = time.time()
    async with async_session() as db:
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

        user_msg = Message(conversation_id=conv.id, role="user", content=req.message)
        db.add(user_msg)
        await db.commit()

        result = await db.execute(
            select(Message)
            .where(Message.conversation_id == conv.id)
            .order_by(Message.created_at.asc())
            .limit(20)
        )
        history = result.scalars().all()
    logger.info(f"[chat] db done, {time.time()-t_db:.1f}s, history_len={len(history)}")

    conversation_id = conv.id

    async def event_stream():
        t_start = time.time()
        full_content = ""
        event_count = 0
        try:
            async for event in run_agent(
                messages=_build_history(history),
                device_id=device.id,
                registry=_registry,
            ):
                event_count += 1
                if event["type"] == "text_delta":
                    full_content += event["content"]
                    data = json.dumps({"delta": event["content"]}, ensure_ascii=False)
                    yield f"event: message\ndata: {data}\n\n"
                elif event["type"] == "tool_call":
                    yield f"event: tool_call\ndata: {event['content']}\n\n"
                elif event["type"] == "tool_result":
                    yield f"event: tool_result\ndata: {event['content']}\n\n"
                elif event["type"] == "done":
                    if full_content.strip():
                        msg_id = str(uuid.uuid4())
                        t_save = time.time()
                        async with async_session() as db:
                            assistant_msg = Message(id=msg_id, conversation_id=conversation_id, role="assistant", content=full_content)
                            db.add(assistant_msg)
                            await db.commit()
                        logger.info(f"[chat] save assistant {time.time()-t_save:.1f}s")
                        data = json.dumps({"conversation_id": conversation_id, "message_id": msg_id})
                        yield f"event: done\ndata: {data}\n\n"
                    else:
                        yield f"event: done\ndata: {json.dumps({'conversation_id': conversation_id})}\n\n"
                    logger.info(f"[chat] done total={time.time()-t_start:.1f}s events={event_count} reply_len={len(full_content)}")
                elif event["type"] == "error":
                    logger.error(f"[chat] error: {event['error']}")
                    yield f"event: error\ndata: {event['error']}\n\n"
        except Exception as e:
            logger.error(f"[chat] exception: {e}", exc_info=True)

    logger.info(f"[chat] returning SSE stream, setup={time.time()-t0:.1f}s")
    return StreamingResponse(
        event_stream(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "Connection": "keep-alive"},
    )
