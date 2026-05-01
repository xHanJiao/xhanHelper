from datetime import datetime
from sqlalchemy import String, ForeignKey, func, Integer
from sqlalchemy.orm import Mapped, mapped_column, relationship
from .base import Base, uuid

# 分类枚举
CATEGORY1_LIST = ["餐饮", "服装", "交通", "居住", "旅游", "美容", "人情", "日用", "数码", "学习", "医疗", "娱乐"]
CATEGORY2_LIST = ["保险", "餐饮", "厨房用具", "床上用品", "打车", "地铁", "电影", "订阅", "房租", "服务费", "服装", "机票", "纪念品", "驾照培训", "交通", "聚会", "咖啡奶茶", "老家", "零食", "买菜", "买药", "女性日用", "朋友", "啤酒", "漂染头发", "日用", "日用耗材", "日用小家电", "社会", "生活用品", "市内短途", "数码配件", "数码设备", "水电费", "堂食", "堂食（伴侣）", "堂食（请客）", "体检", "外卖", "玩具", "箱包", "休闲", "医疗", "医疗器械", "饮料", "饮用水", "长途（火车）", "住宿"]
CONSUME_TYPE_LIST = ["长期", "一次性", "周期"]
PAY_TYPE_LIST = ["现金", "公积金"]

BILL_STATUS_NORMAL = 1
BILL_STATUS_CANCEL = 2


class Bill(Base):
    __tablename__ = "bills"

    id: Mapped[str] = mapped_column(String(36), primary_key=True, default=uuid)
    device_id: Mapped[str] = mapped_column(String(36), ForeignKey("devices.id"), nullable=False)
    item: Mapped[str] = mapped_column(String(100), nullable=False)
    amount: Mapped[float] = mapped_column(nullable=False)
    category1: Mapped[str] = mapped_column(String(50), nullable=False)
    category2: Mapped[str] = mapped_column(String(50), nullable=False)
    consume_type: Mapped[str] = mapped_column(String(20), nullable=False)
    date: Mapped[str] = mapped_column(String(20), nullable=False)
    pay_type: Mapped[str] = mapped_column(String(20), nullable=False)
    owner: Mapped[str] = mapped_column(String(50), nullable=False)
    remark: Mapped[str] = mapped_column(String(200), default="")
    status: Mapped[int] = mapped_column(Integer, default=1)
    created_at: Mapped[datetime] = mapped_column(server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(server_default=func.now(), onupdate=func.now())

    device = relationship("Device", back_populates="bills")
