from datetime import datetime
from pydantic import BaseModel, field_serializer


class DeviceRegisterRequest(BaseModel):
    device_name: str
    platform: str
    secret: str = ""


class DeviceRegisterResponse(BaseModel):
    device_token: str
    device_id: str


class ChatRequest(BaseModel):
    message: str
    conversation_id: str | None = None


class ConversationOut(BaseModel):
    id: str
    device_id: str
    title: str
    created_at: datetime | None = None
    updated_at: datetime | None = None

    model_config = {"from_attributes": True}

    @field_serializer("created_at", "updated_at")
    def _serialize_dt(self, dt: datetime | None) -> str | None:
        return dt.isoformat() if dt else None


class MessageOut(BaseModel):
    id: str
    conversation_id: str
    role: str
    content: str
    tool_calls_json: str | None = None
    tool_call_id: str | None = None
    created_at: datetime | None = None

    model_config = {"from_attributes": True}

    @field_serializer("created_at")
    def _serialize_created(self, dt: datetime | None) -> str | None:
        return dt.isoformat() if dt else None


class BillOut(BaseModel):
    id: str
    item: str
    amount: float
    category1: str
    category2: str
    consume_type: str
    date: str
    pay_type: str
    owner: str
    remark: str
    status: int
    created_at: datetime | None = None

    model_config = {"from_attributes": True}

    @field_serializer("created_at")
    def _serialize_created(self, dt: datetime | None) -> str | None:
        return dt.isoformat() if dt else None


class BillImportResult(BaseModel):
    imported: int
    skipped: int
    total: int
    errors: list[str] = []
