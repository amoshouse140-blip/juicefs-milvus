from __future__ import annotations

from dataclasses import dataclass
from itertools import islice
from pathlib import Path
from typing import Iterable, Iterator

from datasets import load_dataset, load_from_disk

DATASET_ID = "filipecosta90/dbpedia-openai-1M-text-embedding-3-large-2048d"
PROFILE_SIZES = {"10k": 10_000, "100k": 100_000, "1m": 1_000_000}


@dataclass(frozen=True)
class VectorRecord:
    key: str
    vector: list[float]
    metadata: dict[str, str]


def dataset_dir(repo_root: Path) -> Path:
    return repo_root / "datasets" / "dbpedia-openai-1m"


def require_local_dataset(repo_root: Path) -> Path:
    path = dataset_dir(repo_root)
    if not path.exists():
        raise RuntimeError(
            f"local dataset not found: {path}. "
            f"run `python3 scripts/download-vectorbucket-dataset.py` first"
        )
    return path


def download_to_local(repo_root: Path, force: bool = False):
    target = dataset_dir(repo_root)
    if target.exists() and not force:
        return target
    target.parent.mkdir(parents=True, exist_ok=True)
    dataset = load_dataset(DATASET_ID, split="train")
    if target.exists():
        import shutil

        shutil.rmtree(target)
    dataset.save_to_disk(str(target))
    return target


def load_profile(profile: str, repo_root: Path | None = None):
    if profile not in PROFILE_SIZES:
        raise ValueError(f"unknown profile: {profile}")
    if repo_root is None:
        raise ValueError("repo_root is required for local dataset loading")
    dataset = load_from_disk(str(require_local_dataset(repo_root)))
    return dataset.select(range(PROFILE_SIZES[profile]))


def stream_profile(profile: str, repo_root: Path | None = None) -> Iterable[dict]:
    return load_profile(profile, repo_root)


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
