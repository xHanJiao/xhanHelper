from datetime import datetime
from sqlalchemy import String, Text, ForeignKey, Boolean, func
from sqlalchemy.orm import Mapped, mapped_column
from .base import Base, uuid


class ScheduledTask(Base):
    __tablename__ = "scheduled_tasks"

    id: Mapped[str] = mapped_column(String(36), primary_key=True, default=uuid)
    device_id: Mapped[str] = mapped_column(String(36), ForeignKey("devices.id"), nullable=False)
    tool_name: Mapped[str] = mapped_column(String(50), nullable=False)
    func_name: Mapped[str] = mapped_column(String(50), nullable=False)
    params_json: Mapped[str | None] = mapped_column(Text, nullable=True)
    cron_expr: Mapped[str] = mapped_column(String(50), nullable=False)
    enabled: Mapped[bool] = mapped_column(Boolean, default=True)
    created_at: Mapped[datetime] = mapped_column(default=func.now())
