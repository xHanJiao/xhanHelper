import logging
from contextlib import asynccontextmanager
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

logging.basicConfig(
    level=logging.DEBUG,
    format="%(asctime)s [%(name)s] %(message)s",
    datefmt="%H:%M:%S",
)
logging.getLogger("uvicorn.access").setLevel(logging.WARNING)
logging.getLogger("httpx").setLevel(logging.WARNING)
from app.config import settings
from app.db.session import engine, async_session
from app.models import Base
from app.tools.registry import Registry
from app.tools.bookkeeping import BookkeepingTool
from app.api.router import router
from app.api.chat import init_chat


@asynccontextmanager
async def lifespan(app: FastAPI):
    async with engine.begin() as conn:
        await conn.run_sync(Base.metadata.create_all)

    # 组装工具注册表
    reg = Registry()
    reg.register(BookkeepingTool(lambda: async_session()))
    init_chat(reg)

    yield
    await engine.dispose()


app = FastAPI(title="xhanHelper", lifespan=lifespan)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

app.include_router(router)


@app.get("/api/health")
async def health():
    return {"status": "ok", "env": settings.app_env}
