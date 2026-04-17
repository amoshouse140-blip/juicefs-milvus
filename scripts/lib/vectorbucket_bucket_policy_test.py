from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from scripts.lib.vectorbucket_bucket_policy import (
    apply_bucket_index_model,
    bucket_index_model,
    load_full_config,
    restore_bucket_policy_config,
)


class BucketPolicyTest(unittest.TestCase):
    def write_config(self, body: str) -> Path:
        tmpdir = Path(tempfile.mkdtemp())
        path = tmpdir / "vectorbucket.json"
        path.write_text(body)
        return path

    def test_returns_hnsw_for_bucket(self) -> None:
        path = self.write_config(
            '{"bucketIndexPolicies":{"bench-hnsw":{"indexType":"hnsw"}}}'
        )
        self.assertEqual("hnsw", bucket_index_model("bench-hnsw", path))

    def test_returns_diskann_for_bucket(self) -> None:
        path = self.write_config(
            '{"bucketIndexPolicies":{"bench-diskann":{"indexType":"diskann"}}}'
        )
        self.assertEqual("diskann", bucket_index_model("bench-diskann", path))

    def test_returns_ivf_sq8_for_bucket(self) -> None:
        path = self.write_config(
            '{"bucketIndexPolicies":{"bench-ivf":{"indexType":"ivf_sq8"}}}'
        )
        self.assertEqual("ivf_sq8", bucket_index_model("bench-ivf", path))

    def test_defaults_to_ivf_sq8_when_bucket_missing(self) -> None:
        path = self.write_config('{"bucketIndexPolicies":{}}')
        self.assertEqual("ivf_sq8", bucket_index_model("bench-missing", path))

    def test_apply_and_restore_bucket_index_model(self) -> None:
        path = self.write_config(
            '{"bucketIndexPolicies":{"bench-ivf":{"indexType":"ivf_sq8"}},"performanceMaxVectors":200000,"hnswM":16,"hnswEfConstruction":200,"diskannSearchList":100}'
        )
        previous = apply_bucket_index_model("bench-hnsw", "hnsw", path)
        self.assertEqual("hnsw", bucket_index_model("bench-hnsw", path))
        config = load_full_config(path)
        self.assertEqual(200000, config["bucketIndexPolicies"]["bench-hnsw"]["maxVectors"])
        restore_bucket_policy_config(previous, path)
        restored = load_full_config(path)
        self.assertNotIn("bench-hnsw", restored.get("bucketIndexPolicies", {}))
