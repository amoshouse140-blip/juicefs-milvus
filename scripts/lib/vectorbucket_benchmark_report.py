from __future__ import annotations

import json
from dataclasses import asdict, dataclass
from pathlib import Path

import numpy as np


@dataclass(frozen=True)
class LatencySummary:
    count: int
    p50_ms: float
    p95_ms: float
    p99_ms: float


def summarize_latencies(samples_ms: list[float]) -> LatencySummary:
    if not samples_ms:
        return LatencySummary(count=0, p50_ms=0.0, p95_ms=0.0, p99_ms=0.0)
    arr = np.array(samples_ms, dtype=float)
    return LatencySummary(
        count=int(arr.size),
        p50_ms=float(np.percentile(arr, 50)),
        p95_ms=float(np.percentile(arr, 95)),
        p99_ms=float(np.percentile(arr, 99)),
    )


def write_report(path: Path, payload: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, ensure_ascii=True, indent=2) + "\n")


def dataclass_payload(value) -> dict:
    return asdict(value)
