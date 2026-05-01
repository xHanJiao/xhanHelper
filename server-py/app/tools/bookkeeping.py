import json
from datetime import date as today_date
from sqlalchemy.ext.asyncio import AsyncSession

from .base import BaseTool, ToolResult, ExecuteContext
from app.models.bill import (
    Bill, CATEGORY1_LIST, CATEGORY2_LIST, CONSUME_TYPE_LIST, PAY_TYPE_LIST,
    BILL_STATUS_NORMAL, BILL_STATUS_CANCEL,
)
from app.models.base import uuid


def _category1_field():
    return {
        "type": "string",
        "enum": CATEGORY1_LIST,
        "description": "一级分类，必须从列表中选",
    }


def _category2_field():
    return {
        "type": "string",
        "enum": CATEGORY2_LIST,
        "description": "二级分类，必须从列表中选",
    }


def _consume_type_field():
    return {
        "type": "string",
        "enum": CONSUME_TYPE_LIST,
        "description": "消费类型",
    }


def _pay_type_field():
    return {
        "type": "string",
        "enum": PAY_TYPE_LIST,
        "description": "支付方式，默认现金",
    }


class BookkeepingTool(BaseTool):
    name = "bookkeeping"
    description = "记账工具，可以新增、修改、删除、查询账单"

    def __init__(self, session_factory):
        self._sf = session_factory

    def functions(self) -> list[dict]:
        return [
            {
                "name": "create_bill",
                "description": "新增一笔账单",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "item": {"type": "string", "description": "项目名称"},
                        "amount": {"type": "number", "description": "金额"},
                        "category1": _category1_field(),
                        "category2": _category2_field(),
                        "consume_type": _consume_type_field(),
                        "date": {"type": "string", "description": "日期 YYYY-MM-DD，默认今天"},
                        "pay_type": _pay_type_field(),
                        "owner": {"type": "string", "description": "归属人"},
                        "remark": {"type": "string", "description": "备注"},
                    },
                    "required": ["item", "amount", "category1", "category2", "consume_type"],
                },
            },
            {
                "name": "update_bill",
                "description": "修改已有账单，id 必填",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "id": {"type": "string", "description": "账单ID (必填)"},
                        "item": {"type": "string", "description": "项目名称"},
                        "amount": {"type": "number", "description": "金额"},
                        "category1": _category1_field(),
                        "category2": _category2_field(),
                        "consume_type": _consume_type_field(),
                        "date": {"type": "string", "description": "日期"},
                        "pay_type": _pay_type_field(),
                        "owner": {"type": "string", "description": "归属人"},
                        "remark": {"type": "string", "description": "备注"},
                    },
                    "required": ["id"],
                },
            },
            {
                "name": "delete_bill",
                "description": "删除一笔账单（软删除），id 必填",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "id": {"type": "string", "description": "账单ID (必填)"},
                    },
                    "required": ["id"],
                },
            },
            {
                "name": "query_bills",
                "description": "查询当前所有正常状态账单",
                "parameters": {"type": "object", "properties": {}},
            },
        ]

    async def execute(self, ctx: ExecuteContext, func_name: str, args: dict) -> ToolResult:
        async with self._sf() as db:
            match func_name:
                case "create_bill": return await self._create(db, ctx.device_id, args)
                case "update_bill": return await self._update(db, args)
                case "delete_bill": return await self._delete(db, args)
                case "query_bills": return await self._query(db, ctx.device_id)
                case _: return ToolResult(success=False, error=f"unknown function: {func_name}")

    async def _create(self, db: AsyncSession, device_id: str, args: dict) -> ToolResult:
        if not args.get("item") or not args.get("amount"):
            return ToolResult(success=False, error="item and amount are required")

        bill = Bill(
            device_id=device_id,
            item=args["item"],
            amount=float(args["amount"]),
            category1=args.get("category1", ""),
            category2=args.get("category2", ""),
            consume_type=args.get("consume_type", ""),
            date=args.get("date") or today_date.today().isoformat(),
            pay_type=args.get("pay_type") or "现金",
            owner=args.get("owner", ""),
            remark=args.get("remark", ""),
            status=BILL_STATUS_NORMAL,
        )
        db.add(bill)
        await db.commit()
        await db.refresh(bill)
        return ToolResult(success=True, data={
            "id": bill.id, "item": bill.item, "amount": bill.amount,
            "category1": bill.category1, "category2": bill.category2,
        })

    async def _update(self, db: AsyncSession, args: dict) -> ToolResult:
        bill_id = args.get("id")
        if not bill_id:
            return ToolResult(success=False, error="id is required")

        from sqlalchemy import select
        result = await db.execute(select(Bill).where(Bill.id == bill_id, Bill.status == BILL_STATUS_NORMAL))
        bill = result.scalar_one_or_none()
        if bill is None:
            return ToolResult(success=False, error=f"bill not found: {bill_id}")

        for field in ["item", "amount", "category1", "category2", "consume_type",
                       "date", "pay_type", "owner", "remark"]:
            if field in args:
                setattr(bill, field, args[field])
        await db.commit()
        await db.refresh(bill)
        return ToolResult(success=True, data={"id": bill.id, "amount": bill.amount})

    async def _delete(self, db: AsyncSession, args: dict) -> ToolResult:
        bill_id = args.get("id")
        if not bill_id:
            return ToolResult(success=False, error="id is required")
        from sqlalchemy import update
        await db.execute(
            update(Bill).where(Bill.id == bill_id).values(status=BILL_STATUS_CANCEL)
        )
        await db.commit()
        return ToolResult(success=True, data={"deleted_id": bill_id})

    async def _query(self, db: AsyncSession, device_id: str) -> ToolResult:
        from sqlalchemy import select
        result = await db.execute(
            select(Bill)
            .where(Bill.device_id == device_id, Bill.status == BILL_STATUS_NORMAL)
            .order_by(Bill.date.desc())
        )
        bills = result.scalars().all()
        return ToolResult(success=True, data={
            "bills": [{"id": b.id, "item": b.item, "amount": b.amount,
                        "category1": b.category1, "date": b.date} for b in bills]
        })
