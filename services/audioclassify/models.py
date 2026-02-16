import time
import numpy as np
import tensorflow_hub as hub
import torch
from funasr import AutoModel

SCENE_LABELS = {
    "speech": {"Speech", "Narration, monologue", "Conversation", "Speech synthesizer"},
    "music": {"Music", "Musical instrument", "Singing", "Song"},
    "silence": {"Silence"},
    "noise": {
        "Noise", "White noise", "Static", "Hum", "Buzz",
        "Mechanical fan", "Air conditioning", "Engine",
    },
}

EMOTION_LABELS = ["neutral", "happy", "angry", "sad", "frustrated", "surprised"]


def _collapse_scene(scores_521: np.ndarray, class_names: list[str]) -> dict:
    buckets: dict[str, float] = {k: 0.0 for k in [*SCENE_LABELS, "other"]}
    for i, name in enumerate(class_names):
        placed = False
        for bucket, keywords in SCENE_LABELS.items():
            if name in keywords:
                buckets[bucket] += float(scores_521[i])
                placed = True
                break
        if not placed:
            buckets["other"] += float(scores_521[i])
    total = sum(buckets.values())
    if total > 0:
        buckets = {k: v / total for k, v in buckets.items()}
    return buckets


class SceneClassifier:
    def __init__(self) -> None:
        self.model = hub.load("https://tfhub.dev/google/yamnet/1")
        import csv, io, urllib.request
        url = "https://raw.githubusercontent.com/tensorflow/models/master/research/audioset/yamnet/yamnet_class_map.csv"
        resp = urllib.request.urlopen(url)
        reader = csv.reader(io.TextIOWrapper(resp))
        next(reader)  # skip header
        self.class_names = [row[2] for row in reader]

    def classify(self, samples: np.ndarray) -> dict:
        t0 = time.perf_counter()
        waveform = samples.astype(np.float32)
        scores, _, _ = self.model(waveform)
        avg_scores = scores.numpy().mean(axis=0)
        collapsed = _collapse_scene(avg_scores, self.class_names)
        label = max(collapsed, key=collapsed.get)
        latency_ms = (time.perf_counter() - t0) * 1000
        return {
            "label": label,
            "confidence": round(collapsed[label], 4),
            "scores": {k: round(v, 4) for k, v in collapsed.items()},
            "latency_ms": round(latency_ms, 2),
        }


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
