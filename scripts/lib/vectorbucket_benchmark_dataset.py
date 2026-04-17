from __future__ import annotations

from dataclasses import dataclass
from itertools import islice
from pathlib import Path
from typing import Iterable, Iterator

from datasets import load_dataset

DATASET_ID = "filipecosta90/dbpedia-openai-1M-text-embedding-3-large-2048d"
PROFILE_SIZES = {"10k": 10_000, "100k": 100_000, "1m": 1_000_000}


@dataclass(frozen=True)
class VectorRecord:
    key: str
    vector: list[float]
    metadata: dict[str, str]


def cache_dir(repo_root: Path) -> Path:
    return repo_root / ".runtime" / "datasets" / "dbpedia-openai-1m"


def load_profile(profile: str, repo_root: Path | None = None):
    if profile not in PROFILE_SIZES:
        raise ValueError(f"unknown profile: {profile}")
    kwargs = {}
    if repo_root is not None:
        kwargs["cache_dir"] = str(cache_dir(repo_root))
    dataset = load_dataset(DATASET_ID, split="train", **kwargs)
    return dataset.select(range(PROFILE_SIZES[profile]))


def stream_profile(profile: str) -> Iterable[dict]:
    if profile not in PROFILE_SIZES:
        raise ValueError(f"unknown profile: {profile}")
    dataset = load_dataset(DATASET_ID, split="train", streaming=True)
    return dataset.take(PROFILE_SIZES[profile])


def iter_records(dataset) -> Iterator[VectorRecord]:
    for row in dataset:
        yield VectorRecord(
            key=row["_id"],
            vector=[float(x) for x in row["embedding"]],
            metadata={"title": row["title"]},
        )


def sample_queries(dataset, limit: int):
    if limit <= 0:
        return []
    step = max(len(dataset) // limit, 1)
    rows = []
    for idx in range(0, len(dataset), step):
        rows.append(dataset[idx])
        if len(rows) == limit:
            break
    return rows


def sample_stream_queries(rows: Iterable[dict], limit: int) -> list[dict]:
    if limit <= 0:
        return []
    return list(islice(rows, limit))
