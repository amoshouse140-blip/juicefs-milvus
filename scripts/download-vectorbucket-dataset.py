#!/usr/bin/env python3
"""
用途:
    下载 VectorBucket benchmark 使用的 DBPedia embedding 数据集，并保存到仓库内固定目录，
    供 benchmark/query benchmark 在离线环境中直接读取。

前置条件:
    - 当前 Python 环境已安装 datasets / pyarrow 等依赖
    - 当前机器可以访问 Hugging Face

用法:
    python3 scripts/download-vectorbucket-dataset.py [options]

示例:
    python3 scripts/download-vectorbucket-dataset.py
    python3 scripts/download-vectorbucket-dataset.py --force

说明:
    - 默认保存到 `datasets/dbpedia-openai-1m/`
    - 下载完成后，benchmark 脚本会只读本地数据，不再依赖联网
"""
from __future__ import annotations

import argparse
import json
import sys
import time
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from scripts.lib.vectorbucket_benchmark_dataset import DATASET_ID, dataset_dir, download_to_local


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--force", action="store_true")
    return parser.parse_args()


def log(level: str, message: str, payload=None) -> None:
    print(f"[{level}] {message}", flush=True)
    if payload is not None:
        print(json.dumps(payload, ensure_ascii=True, default=str, indent=2), flush=True)


def main() -> int:
    args = parse_args()
    target = dataset_dir(REPO_ROOT)
    log("信息", "开始准备本地 benchmark 数据集", {"dataset_id": DATASET_ID, "target_dir": str(target), "force": args.force})
    started = time.perf_counter()
    path = download_to_local(REPO_ROOT, force=args.force)
    elapsed = time.perf_counter() - started
    log("成功", f"本地 benchmark 数据集已准备完成，耗时 {elapsed:.2f}s", {"target_dir": str(path)})
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"[失败] 下载 benchmark 数据集失败: {exc}", file=sys.stderr, flush=True)
        raise SystemExit(1)
