import uuid as _uuid
from datetime import datetime
from sqlalchemy import String, func
from sqlalchemy.orm import Mapped, mapped_column, relationship
from .base import Base


class Device(Base):
    __tablename__ = "devices"

    id: Mapped[str] = mapped_column(String(36), primary_key=True, default=lambda: str(_uuid.uuid4()))
    token: Mapped[str] = mapped_column(String(64), unique=True, nullable=False, default=lambda: str(_uuid.uuid4()))
    name: Mapped[str] = mapped_column(String(100))
    platform: Mapped[str] = mapped_column(String(20), nullable=False)
    created_at: Mapped[datetime] = mapped_column(default=func.now())
    last_seen_at: Mapped[datetime] = mapped_column(default=func.now(), onupdate=func.now())

    conversations = relationship("Conversation", back_populates="device")
    bills = relationship("Bill", back_populates="device")
