from __future__ import annotations

import re
from dataclasses import dataclass


@dataclass(frozen=True)
class MemorySummary:
    peak_mb: float
    steady_mb: float


_MEM_RE = re.compile(r"^\s*([0-9]+(?:\.[0-9]+)?)\s*([KMGTP]i?B)\s*$", re.IGNORECASE)


def _unit_to_mb(unit: str) -> float:
    normalized = unit.upper()
    table = {
        "KB": 0.001 / 1.048576,
        "MB": 1 / 1.048576,
        "GB": 1000 / 1.048576,
        "TB": 1000 * 1000 / 1.048576,
        "KIB": 1 / 1024,
        "MIB": 1.0,
        "GIB": 1024.0,
        "TIB": 1024.0 * 1024.0,
    }
    if normalized not in table:
        raise ValueError(f"unsupported memory unit: {unit}")
    return table[normalized]


def parse_docker_mem_usage_mb(mem_usage: str) -> float:
    current = mem_usage.split("/", 1)[0].strip()
    match = _MEM_RE.match(current)
    if not match:
        raise ValueError(f"cannot parse docker memory usage: {mem_usage}")
    value = float(match.group(1))
    unit = match.group(2)
    return value * _unit_to_mb(unit)


def summarize_memory_samples(samples_mb: list[float], tail_size: int = 5) -> MemorySummary:
    if not samples_mb:
        return MemorySummary(peak_mb=0.0, steady_mb=0.0)
    peak = max(samples_mb)
    tail = samples_mb[-max(1, tail_size) :]
    steady = sum(tail) / len(tail)
    return MemorySummary(peak_mb=float(peak), steady_mb=float(steady))
