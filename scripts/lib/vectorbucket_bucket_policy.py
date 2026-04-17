from __future__ import annotations

import json
from copy import deepcopy
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_CONFIG_PATH = REPO_ROOT / "configs" / "vectorbucket.json"


def _normalize_index_type(value: str | None) -> str:
    normalized = (value or "").strip().lower()
    if normalized in {"", "ivf", "ivf_sq8"}:
        return "ivf_sq8"
    if normalized in {"hnsw", "diskann"}:
        return normalized
    return "ivf_sq8"


def load_bucket_index_policies(config_path: Path | None = None) -> dict[str, dict]:
    path = config_path or DEFAULT_CONFIG_PATH
    if not path.exists():
        return {}
    content = json.loads(path.read_text())
    policies = content.get("bucketIndexPolicies", {})
    if not isinstance(policies, dict):
        return {}
    return policies


def bucket_index_model(bucket_name: str, config_path: Path | None = None) -> str:
    policies = load_bucket_index_policies(config_path)
    policy = policies.get(bucket_name, {})
    if not isinstance(policy, dict):
        policy = {}
    return _normalize_index_type(policy.get("indexType"))


def load_full_config(config_path: Path | None = None) -> dict:
    path = config_path or DEFAULT_CONFIG_PATH
    if not path.exists():
        return {}
    return json.loads(path.read_text())


def apply_bucket_index_model(bucket_name: str, index_model: str, config_path: Path | None = None) -> dict:
    path = config_path or DEFAULT_CONFIG_PATH
    previous = load_full_config(path)
    updated = deepcopy(previous)
    policies = updated.setdefault("bucketIndexPolicies", {})
    existing = policies.get(bucket_name, {})
    if not isinstance(existing, dict):
        existing = {}

    normalized = _normalize_index_type(index_model)
    policy = deepcopy(existing)
    policy["indexType"] = normalized

    if normalized in {"hnsw", "diskann"}:
        policy.setdefault("maxVectors", int(updated.get("performanceMaxVectors", 200000)))
    else:
        policy.pop("maxVectors", None)

    if normalized == "hnsw":
        policy.setdefault("hnswM", int(updated.get("hnswM", 16)))
        policy.setdefault("hnswEfConstruction", int(updated.get("hnswEfConstruction", 200)))
        policy.pop("diskannSearchList", None)
    elif normalized == "diskann":
        policy.setdefault("diskannSearchList", int(updated.get("diskannSearchList", 100)))
        policy.pop("hnswM", None)
        policy.pop("hnswEfConstruction", None)
    else:
        policy.pop("hnswM", None)
        policy.pop("hnswEfConstruction", None)
        policy.pop("diskannSearchList", None)

    policies[bucket_name] = policy
    path.write_text(json.dumps(updated, ensure_ascii=True, indent=2) + "\n")
    return previous


def restore_bucket_policy_config(previous: dict, config_path: Path | None = None) -> None:
    path = config_path or DEFAULT_CONFIG_PATH
    path.write_text(json.dumps(previous, ensure_ascii=True, indent=2) + "\n")
