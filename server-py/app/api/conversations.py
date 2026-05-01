from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy import select
from app.db.session import get_db
from app.api.deps import get_device
from app.models.device import Device
from app.models.conversation import Conversation
from app.models.message import Message
from app.schemas import ConversationOut, MessageOut

router = APIRouter()


@router.get("/conversations", response_model=list[ConversationOut])
async def list_conversations(
    device: Device = Depends(get_device),
    db: AsyncSession = Depends(get_db),
):
    result = await db.execute(
        select(Conversation)
        .where(Conversation.device_id == device.id)
        .order_by(Conversation.updated_at.desc())
        .limit(50)
    )
    return result.scalars().all()


@router.get("/conversations/{conversation_id}/messages", response_model=list[MessageOut])
async def get_messages(
    conversation_id: str,
    device: Device = Depends(get_device),
    db: AsyncSession = Depends(get_db),
):
    result = await db.execute(
        select(Conversation).where(Conversation.id == conversation_id)
    )
    conv = result.scalar_one_or_none()
    if conv is None:
        raise HTTPException(status_code=404, detail="conversation not found")
    if conv.device_id != device.id:
        raise HTTPException(status_code=403, detail="access denied")

    result = await db.execute(
        select(Message)
        .where(Message.conversation_id == conversation_id)
        .order_by(Message.created_at.asc())
        .limit(100)
    )
    return result.scalars().all()
