from __future__ import annotations

import tempfile
import unittest
from pathlib import Path
from unittest import mock

from datasets import Dataset

from scripts.lib import vectorbucket_benchmark_dataset as dsmod


class LocalDatasetTest(unittest.TestCase):
    def make_repo_with_dataset(self) -> Path:
        repo_root = Path(tempfile.mkdtemp())
        target = dsmod.dataset_dir(repo_root)
        target.parent.mkdir(parents=True, exist_ok=True)
        dataset = Dataset.from_dict(
            {
                "_id": ["a", "b", "c"],
                "embedding": [[0.1, 0.2], [0.3, 0.4], [0.5, 0.6]],
                "title": ["t1", "t2", "t3"],
            }
        )
        dataset.save_to_disk(str(target))
        return repo_root

    def test_require_local_dataset_raises_clear_error_when_missing(self) -> None:
        repo_root = Path(tempfile.mkdtemp())
        with self.assertRaises(RuntimeError) as ctx:
            dsmod.require_local_dataset(repo_root)
        self.assertIn("download-vectorbucket-dataset.py", str(ctx.exception))

    def test_load_profile_reads_saved_local_dataset(self) -> None:
        repo_root = self.make_repo_with_dataset()
        with mock.patch.object(dsmod, "PROFILE_SIZES", {"tiny": 2}):
            dataset = dsmod.load_profile("tiny", repo_root)
        self.assertEqual(2, len(dataset))
        self.assertEqual("a", dataset[0]["_id"])


if __name__ == "__main__":
    unittest.main()
