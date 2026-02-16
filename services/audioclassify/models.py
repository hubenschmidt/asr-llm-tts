import time

import numpy as np
import torch
from funasr import AutoModel

EMOTION_LABELS = ["neutral", "happy", "angry", "sad", "frustrated", "surprised"]


class EmotionClassifier:
    def __init__(self) -> None:
        self.model = AutoModel(model="iic/emotion2vec_base", model_revision="v2.0.4")

    def classify(self, samples: np.ndarray) -> dict:
        t0 = time.perf_counter()
        result = self.model.generate(samples.astype(np.float32), granularity="utterance", extract_embedding=False)
        entry = result[0] if result else {}
        labels = entry.get("labels", EMOTION_LABELS)
        probs = entry.get("scores", [])

        # handle single-label string result
        if isinstance(labels, str):
            labels = [labels]
        if isinstance(probs, (int, float)):
            probs = [probs]

        scores = {}
        for i, lbl in enumerate(labels):
            mapped = lbl if lbl in EMOTION_LABELS else EMOTION_LABELS[i] if i < len(EMOTION_LABELS) else lbl
            scores[mapped] = round(float(probs[i]), 4) if i < len(probs) else 0.0

        if not scores:
            scores = {"neutral": 1.0}

        label = max(scores, key=scores.get)
        latency_ms = (time.perf_counter() - t0) * 1000
        return {
            "label": label,
            "confidence": round(scores[label], 4),
            "scores": scores,
            "latency_ms": round(latency_ms, 2),
        }
