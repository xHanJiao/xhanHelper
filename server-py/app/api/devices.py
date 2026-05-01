from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy.ext.asyncio import AsyncSession
from app.db.session import get_db
from app.models.device import Device
from app.schemas import DeviceRegisterRequest, DeviceRegisterResponse
from app.config import settings

router = APIRouter()


@router.post("/devices/register", response_model=DeviceRegisterResponse)
async def register_device(req: DeviceRegisterRequest, db: AsyncSession = Depends(get_db)):
    if settings.registration_secret and req.secret != settings.registration_secret:
        raise HTTPException(status_code=403, detail="invalid secret")

    device = Device(
        name=req.device_name,
        platform=req.platform,
    )
    db.add(device)
    await db.commit()
    await db.refresh(device)
    return DeviceRegisterResponse(device_token=device.token, device_id=device.id)
