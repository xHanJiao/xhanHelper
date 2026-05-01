from fastapi import APIRouter, Depends, HTTPException, Request
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy import select
from app.db.session import get_db
from app.api.deps import get_device
from app.models.device import Device
from app.models.bill import Bill, BILL_STATUS_NORMAL, CATEGORY1_LIST, CATEGORY2_LIST
from app.schemas import BillOut, BillImportResult
from app.models.base import uuid

router = APIRouter()


@router.get("/bills", response_model=list[BillOut])
async def list_bills(
    device: Device = Depends(get_device),
    db: AsyncSession = Depends(get_db),
):
    result = await db.execute(
        select(Bill)
        .where(Bill.device_id == device.id, Bill.status == BILL_STATUS_NORMAL)
        .order_by(Bill.date.desc(), Bill.created_at.desc())
    )
    return result.scalars().all()


@router.post("/bills/import", response_model=BillImportResult)
async def import_bills(
    request: Request,
    device: Device = Depends(get_device),
    db: AsyncSession = Depends(get_db),
):
    body = (await request.body()).decode("utf-8")
    lines = body.strip().split("\n")

    imported = 0
    skipped = 0
    errors = []

    for i, line in enumerate(lines):
        if i == 0 and "项目" in line:
            continue
        if not line.strip():
            continue

        fields = [f.strip() for f in line.split(",")]
        if len(fields) < 8:
            skipped += 1
            errors.append(f"第{i+1}行: 字段不足")
            continue

        try:
            amount = float(fields[1])
        except ValueError:
            skipped += 1
            errors.append(f"第{i+1}行: 金额解析失败 '{fields[1]}'")
            continue

        item = fields[0]
        category1 = fields[2]
        category2 = fields[3]
        consume_type = fields[4]
        date = fields[5].replace("/", "-")
        pay_type = fields[6]
        owner = fields[7]
        remark = fields[8] if len(fields) > 8 else ""

        if category1 not in CATEGORY1_LIST:
            skipped += 1
            errors.append(f"第{i+1}行: 无效一级分类 '{category1}'")
            continue
        if category2 not in CATEGORY2_LIST:
            skipped += 1
            errors.append(f"第{i+1}行: 无效二级分类 '{category2}'")
            continue

        bill = Bill(
            device_id=device.id,
            item=item,
            amount=amount,
            category1=category1,
            category2=category2,
            consume_type=consume_type,
            date=date,
            pay_type=pay_type,
            owner=owner,
            remark=remark,
            status=BILL_STATUS_NORMAL,
        )
        db.add(bill)
        imported += 1

    await db.commit()
    return BillImportResult(imported=imported, skipped=skipped, total=imported + skipped, errors=errors)
