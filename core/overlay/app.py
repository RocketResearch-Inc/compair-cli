from __future__ import annotations

import importlib
import inspect
import os
from fastapi import FastAPI
from starlette.middleware.cors import CORSMiddleware

from .routers import device_auth, operator


def _import_base_app():
    # Allow override via env to specify app import path "pkg.module:app"
    # or factory "pkg.module:create_app".
    target = os.getenv(
        "COMPAIR_BACKEND_APP",
        "compair_core.server.app:create_app,compair_core.server.app:app,compair_backend.app:app,compair_master.api:app",
    )
    candidates = [s.strip() for s in target.split(",") if s.strip()]
    for cand in candidates:
        try:
            modpath, attr = cand.split(":", 1)
            mod = importlib.import_module(modpath)
            base = getattr(mod, attr)
            if isinstance(base, FastAPI):
                return base
            if callable(base):
                sig = inspect.signature(base)
                # Treat zero-arg callables as factories; otherwise assume ASGI app.
                if len(sig.parameters) == 0:
                    base = base()
            if base is not None:
                return base
        except Exception:
            continue
    return None


def create_overlay_app() -> FastAPI:
    app = FastAPI(title="Compair Core Overlay")
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    # Overlay routers
    app.include_router(device_auth.router, prefix="/auth/device", tags=["auth-device"])
    app.include_router(operator.router, prefix="/_operator", tags=["operator"])

    # Mount the base app last so overlay routes take precedence.
    base = _import_base_app()
    if base is not None:
        app.mount("/", base)

    return app


app = create_overlay_app()
