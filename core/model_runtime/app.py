from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
import time

app = FastAPI(title="Compair Model Runtime (Local)")

class GenerateRequest(BaseModel):
    prompt: str | None = None
    messages: list[dict] | None = None
    params: dict | None = None

class GenerateResponse(BaseModel):
    output: str
    tokens: int
    latency_ms: int

@app.post("/generate", response_model=GenerateResponse)
async def generate(req: GenerateRequest):
    t0 = time.time()
    text = req.prompt or ""
    if not text and req.messages:
        text = "\n".join([m.get("content","") for m in req.messages])
    if not text:
        raise HTTPException(status_code=400, detail="prompt or messages required")
    # trivial echo-ish behavior; replace with actual model adapter later
    out = f"[local-model] {text[:2000]}"
    dt = int((time.time()-t0)*1000)
    return GenerateResponse(output=out, tokens=len(out.split()), latency_ms=dt)

