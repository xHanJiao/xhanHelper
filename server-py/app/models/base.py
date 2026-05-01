from sqlalchemy.orm import DeclarativeBase
import uuid as _uuid


def uuid():
    return str(_uuid.uuid4())


class Base(DeclarativeBase):
    pass
