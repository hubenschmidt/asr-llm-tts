import asyncio
import struct
from contextlib import asynccontextmanager

import numpy as np
from fastapi import FastAPI, Request

from models import EmotionClassifier

emotion_model: EmotionClassifier | None = None


@asynccontextmanager
async def lifespan(_app: FastAPI):
    global emotion_model
    emotion_model = EmotionClassifier()
    yield


app = FastAPI(lifespan=lifespan)


def _parse_float32(body: bytes) -> np.ndarray:
    n = len(body) // 4
    return np.array(struct.unpack(f"<{n}f", body[:n * 4]), dtype=np.float32)


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.post("/emotion")
async def classify_emotion(request: Request):
    body = await request.body()
    samples = _parse_float32(body)
    result = await asyncio.get_event_loop().run_in_executor(
        None, emotion_model.classify, samples,
    )
    return result
