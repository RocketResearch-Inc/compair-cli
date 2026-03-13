from fastapi import APIRouter

router = APIRouter()

@router.get("/healthz")
async def healthz():
    return {"status":"ok"}

@router.get("/ready")
async def ready():
    return {"status":"ready"}

