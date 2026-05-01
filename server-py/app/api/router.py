from fastapi import APIRouter
from app.api import devices, conversations, bills

router = APIRouter(prefix="/api")
router.include_router(devices.router, tags=["devices"])
router.include_router(conversations.router, tags=["conversations"])
router.include_router(bills.router, tags=["bills"])
