#!/usr/bin/env python3
"""Print benchmark JSON results as a scaling comparison table."""

import argparse
import json
from pathlib import Path


def parse_args():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("results_dir", type=Path)
    return parser.parse_args()


def result_files(results_dir):
    return sorted(results_dir.glob("*/*.json"))


def report_row(path):
    with path.open(encoding="utf-8") as result_file:
        report = json.load(result_file)
    summary = report["summary"]
    resources = report["resource_summary"]
    metadata = report.get("resource_metadata", {})
    cpu = [value["average_cpu_percent"] for value in resources.values() if value["average_cpu_percent"] is not None]
    memory = [value["peak_memory_bytes"] for value in resources.values() if value["peak_memory_bytes"] is not None]
    dataset = [value["dataset_bytes"] for value in metadata.values() if value.get("dataset_bytes") is not None]
    return {
        "profile": report["profile"],
        "scenario": report["scenario"],
        "concurrency": report["concurrency"],
        "rps": summary["successful_requests_per_second"],
        "p95_ms": summary["latency_ms"]["p95"],
        "success": summary["success_rate"],
        "transport_retries": summary["transport_retries"],
        "avg_cpu_percent": sum(cpu) / len(cpu) if cpu else None,
        "peak_memory_mib": max(memory) / 1024**2 if memory else None,
        "dataset_gib": max(dataset) / 1024**3 if dataset else None,
    }


def main():
    args = parse_args()
    rows = [report_row(path) for path in result_files(args.results_dir)]
    if not rows:
        raise SystemExit(f"No result files found under {args.results_dir}")
    rows.sort(key=lambda row: (row["scenario"], row["concurrency"], row["profile"]))
    baseline = {
        (row["scenario"], row["concurrency"]): row["rps"]
        for row in rows
        if row["profile"] == "1x"
    }
    columns = [
        "profile",
        "scenario",
        "concurrency",
        "rps",
        "speedup_vs_1x",
        "efficiency",
        "p95_ms",
        "success",
        "transport_retries",
        "avg_cpu_percent",
        "peak_memory_mib",
        "dataset_gib",
    ]
    print(",".join(columns))
    for row in rows:
        base = baseline.get((row["scenario"], row["concurrency"]))
        multiplier = {"1x": 1, "2x": 2, "4x": 4}.get(row["profile"])
        speedup = row["rps"] / base if base else None
        efficiency = speedup / multiplier if speedup is not None and multiplier else None
        values = [
            row["profile"],
            row["scenario"],
            row["concurrency"],
            f"{row['rps']:.3f}",
            f"{speedup:.3f}" if speedup is not None else "",
            f"{efficiency:.3f}" if efficiency is not None else "",
            f"{row['p95_ms']:.1f}" if row["p95_ms"] is not None else "",
            f"{row['success']:.4f}",
            row["transport_retries"],
            f"{row['avg_cpu_percent']:.1f}" if row["avg_cpu_percent"] is not None else "",
            f"{row['peak_memory_mib']:.1f}" if row["peak_memory_mib"] is not None else "",
            f"{row['dataset_gib']:.2f}" if row["dataset_gib"] is not None else "",
        ]
        print(",".join(str(value) for value in values))


if __name__ == "__main__":
    main()
