from __future__ import annotations

import os
import time
import uuid
import secrets
from dataclasses import dataclass
from typing import Optional

from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import JSONResponse, HTMLResponse
import jwt

try:
    import redis
except Exception:  # pragma: no cover
    redis = None  # type: ignore


router = APIRouter()


def _redis_client():
    url = os.getenv("REDIS_URL")
    if not url or not redis:
        return None
    return redis.Redis.from_url(url, decode_responses=True)


def _now() -> int:
    return int(time.time())


def _gen_user_code() -> str:
    # Simple 8-char code grouped as ABCD-EFGH
    raw = secrets.token_hex(4).upper()
    return f"{raw[:4]}-{raw[4:8]}"


def _sign_tokens(username: str) -> dict:
    secret = os.getenv("JWT_SECRET", "dev_secret_change_me")
    user_id = f"usr_{uuid.uuid4().hex[:8]}"
    now = _now()
    access_exp = now + 3600
    refresh_exp = now + 30 * 24 * 3600
    access = jwt.encode({"sub": user_id, "username": username, "exp": access_exp}, secret, algorithm="HS256")
    refresh = jwt.encode({"sub": user_id, "typ": "refresh", "exp": refresh_exp}, secret, algorithm="HS256")
    return {
        "access_token": access,
        "refresh_token": refresh,
        "expires_in": 3600,
        "user_id": user_id,
        "username": username,
    }


@dataclass
class DeviceRecord:
    device_code: str
    user_code: str
    status: str  # pending|approved|expired
    username: Optional[str] = None
    created_at: int = 0


def _save_device(rec: DeviceRecord, ttl: int = 900):
    r = _redis_client()
    key = f"device:{rec.device_code}"
    if r is None:
        # in-memory fallback
        _MEM[key] = {"device_code": rec.device_code, "user_code": rec.user_code, "status": rec.status, "username": rec.username, "created_at": rec.created_at}
        return
    r.hset(key, mapping={"user_code": rec.user_code, "status": rec.status, "username": rec.username or "", "created_at": str(rec.created_at)})
    r.expire(key, ttl)


def _load_device(device_code: str) -> Optional[DeviceRecord]:
    r = _redis_client()
    key = f"device:{device_code}"
    if r is None:
        v = _MEM.get(key)
        if not v:
            return None
        return DeviceRecord(device_code=device_code, user_code=v.get("user_code",""), status=v.get("status","pending"), username=v.get("username") or None, created_at=int(v.get("created_at",0)))
    if not r.exists(key):
        return None
    m = r.hgetall(key)
    return DeviceRecord(device_code=device_code, user_code=m.get("user_code",""), status=m.get("status","pending"), username=(m.get("username") or None), created_at=int(m.get("created_at") or 0))


def _load_by_user_code(user_code: str) -> Optional[DeviceRecord]:
    # naive scan acceptable for small pending set; Redis deployments can index user_code if desired
    r = _redis_client()
    pattern = "device:*"
    if r is None:
        for k, v in _MEM.items():
            if v.get("user_code") == user_code:
                return DeviceRecord(device_code=k.split(":",1)[1], user_code=user_code, status=v.get("status","pending"), username=v.get("username") or None, created_at=int(v.get("created_at",0)))
        return None
    for k in r.scan_iter(match=pattern):
        if r.hget(k, "user_code") == user_code:
            m = r.hgetall(k)
            return DeviceRecord(device_code=k.split(":",1)[1], user_code=user_code, status=m.get("status","pending"), username=(m.get("username") or None), created_at=int(m.get("created_at") or 0))
    return None


def _approve_device(rec: DeviceRecord, username: str):
    r = _redis_client()
    key = f"device:{rec.device_code}"
    if r is None:
        _MEM[key] = {"device_code": rec.device_code, "user_code": rec.user_code, "status": "approved", "username": username, "created_at": rec.created_at}
        return
    r.hset(key, mapping={"status": "approved", "username": username})


_MEM: dict[str, dict] = {}


@router.post("/start")
async def start_device(request: Request):
    data = await request.json() if request.headers.get("content-type","" ).startswith("application/json") else {}
    # client_id is optional; for Core we don't validate
    device_code = uuid.uuid4().hex
    user_code = _gen_user_code()
    rec = DeviceRecord(device_code=device_code, user_code=user_code, status="pending", created_at=_now())
    _save_device(rec)
    verification_uri = os.getenv("DEVICE_VERIFICATION_URI", "/activate")
    return JSONResponse({
        "device_code": device_code,
        "user_code": user_code,
        "verification_uri": verification_uri,
        "expires_in": 900,
        "interval": 5,
    })


@router.post("/poll")
async def poll_device(request: Request):
    data = await request.json()
    device_code = data.get("device_code")
    if not device_code:
        raise HTTPException(status_code=400, detail="device_code required")
    rec = _load_device(device_code)
    if rec is None:
        raise HTTPException(status_code=404, detail="unknown device_code")
    if rec.status != "approved" or not rec.username:
        return JSONResponse({"status":"pending"}, status_code=202)
    # Issue tokens
    tokens = _sign_tokens(rec.username)
    return JSONResponse(tokens)


@router.get("/activate", response_class=HTMLResponse)
async def activate_form():
    return """
    <html><body>
    <h3>Activate Device</h3>
    <form method='POST' action='/auth/device/activate'>
      User Code: <input name='user_code'/><br/>
      Username (email): <input name='username'/><br/>
      <button type='submit'>Approve</button>
    </form>
    </body></html>
    """


@router.post("/activate")
async def activate_submit(request: Request):
    form = await request.form()
    user_code = str(form.get("user_code","" )).strip().upper()
    username = str(form.get("username","" )).strip()
    if not user_code or not username:
        return HTMLResponse("Missing fields", status_code=400)
    rec = _load_by_user_code(user_code)
    if not rec:
        return HTMLResponse("Invalid user code", status_code=404)
    _approve_device(rec, username)
    return HTMLResponse("Device approved. You can close this window.")

