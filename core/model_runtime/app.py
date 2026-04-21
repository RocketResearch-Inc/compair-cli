from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from hashlib import sha256
import time

app = FastAPI(title="Compair Model Runtime (Local)")

class GenerateRequest(BaseModel):
    prompt: str | None = None
    messages: list[dict] | None = None
    document: str | None = None
    references: list[str] | None = None
    length_instruction: str | None = None
    focus_text: str | None = None
    change_context: str | None = None
    params: dict | None = None

class GenerateResponse(BaseModel):
    output: str
    text: str | None = None
    feedback: str | None = None
    tokens: int
    latency_ms: int


class EmbedRequest(BaseModel):
    text: str
    dimension: int = 384


class EmbedResponse(BaseModel):
    embedding: list[float]
    dimension: int
    latency_ms: int


def _hash_embedding(text: str, dimension: int) -> list[float]:
    """Generate a deterministic embedding suitable for local smoke tests."""
    safe_dimension = max(1, min(int(dimension or 384), 4096))
    values: list[float] = []
    seed = text.encode("utf-8", "ignore")
    salt = 0
    while len(values) < safe_dimension:
        digest = sha256(seed + salt.to_bytes(4, "big", signed=False)).digest()
        salt += 1
        for idx in range(0, len(digest), 4):
            chunk = digest[idx : idx + 4]
            if len(chunk) < 4:
                continue
            raw = int.from_bytes(chunk, "big", signed=False)
            values.append((raw / 0xFFFFFFFF) * 2.0 - 1.0)
            if len(values) >= safe_dimension:
                break
    return values

@app.post("/generate", response_model=GenerateResponse)
async def generate(req: GenerateRequest):
    t0 = time.time()
    text = req.prompt or ""
    if not text and req.messages:
        text = "\n".join([m.get("content","") for m in req.messages])
    if not text and req.document is not None:
        focus = (req.focus_text or "").strip()
        references = [ref.strip() for ref in (req.references or []) if ref and ref.strip()]
        if references:
            focus_hint = focus or req.document[:160]
            feedback = (
                f"[local-feedback] Compared the changed content against {len(references)} reference(s). "
                f"Focus area: {focus_hint[:220]}"
            )
        else:
            feedback = "NONE"
        dt = int((time.time()-t0)*1000)
        return GenerateResponse(
            output=feedback,
            text=feedback,
            feedback=feedback,
            tokens=len(feedback.split()),
            latency_ms=dt,
        )
    if not text:
        raise HTTPException(status_code=400, detail="prompt or messages required")
    # trivial echo-ish behavior; replace with actual model adapter later
    out = f"[local-model] {text[:2000]}"
    dt = int((time.time()-t0)*1000)
    return GenerateResponse(
        output=out,
        text=out,
        feedback=out,
        tokens=len(out.split()),
        latency_ms=dt,
    )


@app.post("/embed", response_model=EmbedResponse)
async def embed(req: EmbedRequest):
    t0 = time.time()
    text = (req.text or "").strip()
    if not text:
        raise HTTPException(status_code=400, detail="text required")
    embedding = _hash_embedding(text, req.dimension)
    dt = int((time.time() - t0) * 1000)
    return EmbedResponse(embedding=embedding, dimension=len(embedding), latency_ms=dt)
