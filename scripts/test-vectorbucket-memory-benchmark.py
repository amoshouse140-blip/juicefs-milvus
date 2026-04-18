#!/usr/bin/env python3
"""
用途:
    依次对 ivf_sq8 / hnsw / diskann 三种索引模型执行完整部署 + 写入/查询 benchmark，
    并采样 Milvus、JuiceFS gateway、etcd 的内存占用，输出以下结果：
    - 写入阶段峰值内存
    - 写完但未热查询时的平稳内存
    - 跑完查询后的平稳内存

前置条件:
    - 已安装 Docker / docker compose
    - 已安装 Go 工具链
    - 当前 Python 环境已安装 boto3 和 benchmark 依赖
    - `scripts/start-vectorbucket-standalone.sh` 和 benchmark 脚本可正常运行

用法:
    python3 scripts/test-vectorbucket-memory-benchmark.py [options]

示例:
    python3 scripts/test-vectorbucket-memory-benchmark.py --profile 10k
    python3 scripts/test-vectorbucket-memory-benchmark.py --profile 100k --models ivf_sq8,hnsw
    python3 scripts/test-vectorbucket-memory-benchmark.py --profile 10k --sample-interval 2 --steady-wait-seconds 30

说明:
    - 每个模型都会先执行 `./scripts/start-vectorbucket-standalone.sh --fresh`，彻底清空旧环境。
    - 每轮都会先执行“纯写入 benchmark”，再执行“纯查询 benchmark”。
    - 默认不 cleanup，方便观察写完后和查询后的平稳内存。
    - 报告会写入 `.runtime/benchmarks/`。
"""
from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import threading
import time
from dataclasses import asdict, dataclass
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from scripts.lib.vectorbucket_benchmark_report import write_report
from scripts.lib.vectorbucket_memory_benchmark import (
    MemorySummary,
    parse_docker_mem_usage_mb,
    summarize_memory_samples,
)


@dataclass(frozen=True)
class ComponentMemoryResult:
    peak_mb: float
    steady_mb: float
    sample_count: int


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--profile", choices=["10k", "100k", "1m"], default="10k")
    parser.add_argument("--models", default="ivf_sq8,hnsw,diskann")
    parser.add_argument("--sample-interval", type=float, default=2.0)
    parser.add_argument("--steady-wait-seconds", type=int, default=30)
    parser.add_argument("--batch-size", type=int, default=100)
    parser.add_argument("--query-count", type=int, default=200)
    parser.add_argument("--query-workers", type=int, default=8)
    parser.add_argument("--top-k", type=int, default=10)
    parser.add_argument("--query-warmup-count", type=int, default=20)
    parser.add_argument("--output", default="")
    parser.add_argument("--keep-running", action="store_true")
    return parser.parse_args()


def log(level: str, message: str, payload=None) -> None:
    print(f"[{level}] {message}", flush=True)
    if payload is not None:
        print(json.dumps(payload, ensure_ascii=True, default=str, indent=2), flush=True)


def run_step(step_name: str, func):
    started = time.perf_counter()
    log("开始", step_name)
    try:
        result = func()
    except Exception as exc:
        elapsed = time.perf_counter() - started
        log("失败", f"{step_name}，耗时 {elapsed:.2f}s", {"error": str(exc)})
        raise
    elapsed = time.perf_counter() - started
    log("成功", f"{step_name}，耗时 {elapsed:.2f}s")
    return result


def run_command(command: list[str], cwd: Path | None = None) -> None:
    process = subprocess.Popen(
        command,
        cwd=str(cwd or REPO_ROOT),
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        bufsize=1,
    )
    assert process.stdout is not None
    for line in process.stdout:
        print(line.rstrip(), flush=True)
    rc = process.wait()
    if rc != 0:
        raise RuntimeError(f"command failed with exit code {rc}: {' '.join(command)}")


def run_command_with_sampling(command: list[str], sampler, cwd: Path | None = None) -> None:
    stop_event = threading.Event()

    def sample_loop():
        while not stop_event.is_set():
            sampler()
            stop_event.wait(sampler.interval)

    thread = threading.Thread(target=sample_loop, daemon=True)
    process = subprocess.Popen(
        command,
        cwd=str(cwd or REPO_ROOT),
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        bufsize=1,
    )
    thread.start()
    try:
        assert process.stdout is not None
        for line in process.stdout:
            print(line.rstrip(), flush=True)
        rc = process.wait()
        sampler()
        if rc != 0:
            raise RuntimeError(f"command failed with exit code {rc}: {' '.join(command)}")
    finally:
        stop_event.set()
        thread.join(timeout=max(1.0, sampler.interval * 2))


class MemorySampler:
    def __init__(self, interval: float):
        self.interval = interval
        self.milvus_samples_mb: list[float] = []
        self.etcd_samples_mb: list[float] = []
        self.juicefs_samples_mb: list[float] = []

    def __call__(self) -> None:
        docker_cmd = [
            "docker",
            "stats",
            "--no-stream",
            "--format",
            "{{.Name}}\t{{.MemUsage}}",
            "milvus-standalone",
            "milvus-etcd",
        ]
        try:
            output = subprocess.check_output(docker_cmd, cwd=str(REPO_ROOT), text=True)
        except subprocess.CalledProcessError:
            output = ""
        for line in output.strip().splitlines():
            if not line.strip():
                continue
            try:
                name, mem_usage = line.split("\t", 1)
            except ValueError:
                continue
            try:
                value_mb = parse_docker_mem_usage_mb(mem_usage)
            except ValueError:
                continue
            if name == "milvus-standalone":
                self.milvus_samples_mb.append(value_mb)
            elif name == "milvus-etcd":
                self.etcd_samples_mb.append(value_mb)

        try:
            pid_output = subprocess.check_output(
                ["pgrep", "-f", str(REPO_ROOT / ".runtime" / "bin" / "juicefs-milvus-integration") + " gateway"],
                cwd=str(REPO_ROOT),
                text=True,
            ).strip()
        except subprocess.CalledProcessError:
            pid_output = ""
        if pid_output:
            pid = pid_output.splitlines()[0].strip()
            try:
                rss_kb = subprocess.check_output(
                    ["ps", "-o", "rss=", "-p", pid],
                    cwd=str(REPO_ROOT),
                    text=True,
                ).strip()
                self.juicefs_samples_mb.append(float(rss_kb) / 1024.0)
            except (subprocess.CalledProcessError, ValueError):
                pass


def load_newest_report(before: set[Path], pattern: str) -> tuple[Path, dict]:
    after = set((REPO_ROOT / ".runtime" / "benchmarks").glob(pattern))
    created = sorted(after - before, key=lambda path: path.stat().st_mtime)
    if not created:
        raise RuntimeError(f"no new benchmark report matched {pattern}")
    path = created[-1]
    return path, json.loads(path.read_text())


def sample_steady_state(interval: float, wait_seconds: int) -> MemorySampler:
    sampler = MemorySampler(interval)
    deadline = time.time() + wait_seconds
    while time.time() < deadline:
        sampler()
        remaining = deadline - time.time()
        if remaining <= 0:
            break
        time.sleep(min(interval, remaining))
    return sampler


def component_result(run_samples: list[float], steady_samples: list[float]) -> ComponentMemoryResult:
    peak = summarize_memory_samples(run_samples + steady_samples)
    steady = summarize_memory_samples(steady_samples or run_samples)
    return ComponentMemoryResult(
        peak_mb=round(peak.peak_mb, 2),
        steady_mb=round(steady.steady_mb, 2),
        sample_count=len(run_samples) + len(steady_samples),
    )


def benchmark_command(args, bucket_name: str, model: str) -> list[str]:
    return [
        "python3",
        "scripts/test-vectorbucket-benchmark.py",
        "--profile",
        args.profile,
        "--vector-bucket-name",
        bucket_name,
        "--index-model",
        model,
        "--batch-size",
        str(args.batch_size),
        "--query-count",
        str(args.query_count),
        "--query-workers",
        str(args.query_workers),
        "--top-k",
        str(args.top_k),
    ]


def ingest_only_benchmark_command(args, bucket_name: str, model: str) -> list[str]:
    return [
        "python3",
        "scripts/test-vectorbucket-benchmark.py",
        "--profile",
        args.profile,
        "--vector-bucket-name",
        bucket_name,
        "--index-model",
        model,
        "--batch-size",
        str(args.batch_size),
        "--query-count",
        "0",
        "--skip-delete-check",
    ]


def query_only_benchmark_command(args, bucket_name: str, model: str) -> list[str]:
    return [
        "python3",
        "scripts/test-vectorbucket-query-benchmark.py",
        "--profile",
        args.profile,
        "--vector-bucket-name",
        bucket_name,
        "--index-model",
        model,
        "--query-count",
        str(args.query_count),
        "--query-workers",
        str(args.query_workers),
        "--top-k",
        str(args.top_k),
        "--warmup-count",
        str(args.query_warmup_count),
    ]


def main() -> int:
    args = parse_args()
    models = [item.strip() for item in args.models.split(",") if item.strip()]
    if not models:
        raise RuntimeError("no index models selected")

    started = time.strftime("%Y%m%d-%H%M%S")
    report_path = Path(args.output) if args.output else REPO_ROOT / ".runtime" / "benchmarks" / f"vectorbucket-memory-{args.profile}-{started}.json"
    overall: dict[str, object] = {
        "profile": args.profile,
        "models": {},
        "sample_interval_seconds": args.sample_interval,
        "steady_wait_seconds": args.steady_wait_seconds,
        "query_warmup_count": args.query_warmup_count,
    }

    log("信息", "开始执行三种索引模型内存对比测试", {
        "profile": args.profile,
        "models": models,
        "sample_interval_seconds": args.sample_interval,
        "steady_wait_seconds": args.steady_wait_seconds,
    })

    for model in models:
        bucket_name = f"bench-{model}"
        model_started = time.perf_counter()
        log("信息", "开始测试索引模型", {"index_model": model, "vector_bucket_name": bucket_name})
        result: dict[str, object] = {
            "index_model": model,
            "vector_bucket_name": bucket_name,
            "status": "ok",
        }
        try:
            run_step(f"启动全新部署环境（{model}）", lambda: run_command(["./scripts/start-vectorbucket-standalone.sh", "--fresh"]))

            before_ingest_reports = set((REPO_ROOT / ".runtime" / "benchmarks").glob(f"vectorbucket-{args.profile}-*.json"))
            ingest_sampler = MemorySampler(args.sample_interval)
            run_step(
                f"执行纯写入 benchmark（{model}）",
                lambda: run_command_with_sampling(ingest_only_benchmark_command(args, bucket_name, model), ingest_sampler),
            )
            ingest_report_path, ingest_report = load_newest_report(before_ingest_reports, f"vectorbucket-{args.profile}-*.json")

            post_ingest_sampler = run_step(
                f"采样写完未热查询平稳内存（{model}）",
                lambda: sample_steady_state(args.sample_interval, args.steady_wait_seconds),
            )

            before_query_reports = set((REPO_ROOT / ".runtime" / "benchmarks").glob(f"vectorbucket-query-{args.profile}-*.json"))
            query_sampler = MemorySampler(args.sample_interval)
            run_step(
                f"执行纯查询 benchmark（{model}）",
                lambda: run_command_with_sampling(query_only_benchmark_command(args, bucket_name, model), query_sampler),
            )
            query_report_path, query_report = load_newest_report(before_query_reports, f"vectorbucket-query-{args.profile}-*.json")

            post_query_sampler = run_step(
                f"采样查询后平稳内存（{model}）",
                lambda: sample_steady_state(args.sample_interval, args.steady_wait_seconds),
            )

            memory = {
                "milvus": {
                    "ingest_peak_mb": round(max(ingest_sampler.milvus_samples_mb or [0.0]), 2),
                    "post_ingest_steady_mb": round(summarize_memory_samples(post_ingest_sampler.milvus_samples_mb).steady_mb, 2),
                    "post_query_peak_mb": round(max(query_sampler.milvus_samples_mb or [0.0]), 2),
                    "post_query_steady_mb": round(summarize_memory_samples(post_query_sampler.milvus_samples_mb).steady_mb, 2),
                    "sample_count": len(ingest_sampler.milvus_samples_mb) + len(post_ingest_sampler.milvus_samples_mb) + len(query_sampler.milvus_samples_mb) + len(post_query_sampler.milvus_samples_mb),
                },
                "etcd": {
                    "ingest_peak_mb": round(max(ingest_sampler.etcd_samples_mb or [0.0]), 2),
                    "post_ingest_steady_mb": round(summarize_memory_samples(post_ingest_sampler.etcd_samples_mb).steady_mb, 2),
                    "post_query_peak_mb": round(max(query_sampler.etcd_samples_mb or [0.0]), 2),
                    "post_query_steady_mb": round(summarize_memory_samples(post_query_sampler.etcd_samples_mb).steady_mb, 2),
                    "sample_count": len(ingest_sampler.etcd_samples_mb) + len(post_ingest_sampler.etcd_samples_mb) + len(query_sampler.etcd_samples_mb) + len(post_query_sampler.etcd_samples_mb),
                },
                "juicefs": {
                    "ingest_peak_mb": round(max(ingest_sampler.juicefs_samples_mb or [0.0]), 2),
                    "post_ingest_steady_mb": round(summarize_memory_samples(post_ingest_sampler.juicefs_samples_mb).steady_mb, 2),
                    "post_query_peak_mb": round(max(query_sampler.juicefs_samples_mb or [0.0]), 2),
                    "post_query_steady_mb": round(summarize_memory_samples(post_query_sampler.juicefs_samples_mb).steady_mb, 2),
                    "sample_count": len(ingest_sampler.juicefs_samples_mb) + len(post_ingest_sampler.juicefs_samples_mb) + len(query_sampler.juicefs_samples_mb) + len(post_query_sampler.juicefs_samples_mb),
                },
            }
            result["memory"] = memory
            result["ingest_benchmark_report_path"] = str(ingest_report_path)
            result["query_benchmark_report_path"] = str(query_report_path)
            result["ingest_benchmark"] = ingest_report
            result["query_benchmark"] = query_report
            result["elapsed_sec"] = round(time.perf_counter() - model_started, 2)
            log("成功", "索引模型测试完成", {
                "index_model": model,
                "milvus_ingest_peak_mb": memory["milvus"]["ingest_peak_mb"],
                "milvus_post_ingest_steady_mb": memory["milvus"]["post_ingest_steady_mb"],
                "milvus_post_query_steady_mb": memory["milvus"]["post_query_steady_mb"],
                "juicefs_ingest_peak_mb": memory["juicefs"]["ingest_peak_mb"],
                "juicefs_post_query_steady_mb": memory["juicefs"]["post_query_steady_mb"],
            })
        except Exception as exc:
            result["status"] = "failed"
            result["error"] = str(exc)
            result["elapsed_sec"] = round(time.perf_counter() - model_started, 2)
            log("失败", f"索引模型测试失败（{model}）", {"error": str(exc)})
        finally:
            if not args.keep_running:
                try:
                    run_step(f"停止部署环境（{model}）", lambda: run_command(["./scripts/stop-vectorbucket-standalone.sh"]))
                except Exception as exc:
                    log("失败", f"停止部署环境失败（{model}）", {"error": str(exc)})
        overall["models"][model] = result

    write_report(report_path, overall)
    log("成功", "内存对比报告已写出", {"report_path": str(report_path)})
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"[失败] 内存 benchmark 脚本异常退出: {exc}", file=sys.stderr, flush=True)
        raise SystemExit(1)
