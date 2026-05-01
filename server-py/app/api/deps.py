from fastapi import Depends, HTTPException, Header
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy import select
from app.db.session import get_db
from app.models.device import Device


async def get_device(
    authorization: str = Header(default=""),
    db: AsyncSession = Depends(get_db),
) -> Device:
    if not authorization.startswith("Bearer "):
        raise HTTPException(status_code=401, detail="missing token")
    token = authorization[7:]
    result = await db.execute(select(Device).where(Device.token == token))
    device = result.scalar_one_or_none()
    if device is None:
        raise HTTPException(status_code=401, detail="invalid token")
    return device
