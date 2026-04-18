from __future__ import annotations

import unittest

from scripts.lib.vectorbucket_memory_benchmark import (
    MemorySummary,
    parse_docker_mem_usage_mb,
    summarize_memory_samples,
)


class MemoryBenchmarkHelpersTest(unittest.TestCase):
    def test_parse_docker_mem_usage_mb_for_gib(self) -> None:
        self.assertAlmostEqual(1048.576, parse_docker_mem_usage_mb("1.024GiB / 7.75GiB"), places=3)

    def test_parse_docker_mem_usage_mb_for_mib(self) -> None:
        self.assertAlmostEqual(815.4, parse_docker_mem_usage_mb("815.4MiB / 7.75GiB"), places=3)

    def test_parse_docker_mem_usage_mb_for_mb(self) -> None:
        self.assertAlmostEqual(953.674, parse_docker_mem_usage_mb("1.0GB / 8.0GB"), places=3)

    def test_summarize_memory_samples_uses_peak_and_tail_average(self) -> None:
        summary = summarize_memory_samples([100.0, 120.0, 130.0, 90.0, 110.0], tail_size=2)
        self.assertEqual(MemorySummary(peak_mb=130.0, steady_mb=100.0), summary)

    def test_summarize_memory_samples_handles_empty(self) -> None:
        self.assertEqual(MemorySummary(peak_mb=0.0, steady_mb=0.0), summarize_memory_samples([]))


if __name__ == "__main__":
    unittest.main()
