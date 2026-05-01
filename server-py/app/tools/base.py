import json
from abc import ABC, abstractmethod
from pydantic import BaseModel


class ToolResult(BaseModel):
    success: bool
    data: dict | None = None
    error: str | None = None


class ExecuteContext(BaseModel):
    device_id: str

    model_config = {"arbitrary_types_allowed": True}


class BaseTool(ABC):
    @property
    @abstractmethod
    def name(self) -> str: ...

    @property
    @abstractmethod
    def description(self) -> str: ...

    @abstractmethod
    def functions(self) -> list[dict]: ...

    @abstractmethod
    async def execute(self, ctx: ExecuteContext, func_name: str, args: dict) -> ToolResult: ...
