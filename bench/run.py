#!/usr/bin/env python3
"""Run one repeatable HTTP load-test sample and capture Docker statistics."""

import argparse
import concurrent.futures
import datetime as dt
import http.client
import json
import math
import platform
import random
import re
import shutil
import subprocess
import threading
import time
from pathlib import Path
from urllib.parse import urlsplit


DEFAULT_WORKLOADS = Path(__file__).with_name("workloads.json")
BYTE_UNITS = {
    "b": 1,
    "kb": 1000,
    "kib": 1024,
    "mb": 1000**2,
    "mib": 1024**2,
    "gb": 1000**3,
    "gib": 1024**3,
}


def parse_args():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--scenario", required=True)
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--concurrency", type=int, required=True)
    parser.add_argument("--warmup", type=float, default=30)
    parser.add_argument("--duration", type=float, default=120)
    parser.add_argument("--timeout", type=float, default=10)
    parser.add_argument("--seed", type=int, default=20260901)
    parser.add_argument("--profile", default="custom")
    parser.add_argument("--workloads", type=Path, default=DEFAULT_WORKLOADS)
    parser.add_argument("--container", action="append", default=[])
    parser.add_argument("--output", type=Path, required=True)
    return parser.parse_args()


def utc_now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def percentile(values, fraction):
    if not values:
        return None
    ordered = sorted(values)
    position = (len(ordered) - 1) * fraction
    lower = math.floor(position)
    upper = math.ceil(position)
    if lower == upper:
        return ordered[lower]
    return ordered[lower] + (ordered[upper] - ordered[lower]) * (position - lower)


def parse_bytes(value):
    match = re.fullmatch(r"\s*([0-9.]+)\s*([A-Za-z]+)\s*", value or "")
    if not match:
        return None
    number, unit = match.groups()
    multiplier = BYTE_UNITS.get(unit.lower())
    return float(number) * multiplier if multiplier else None


def parse_number(value):
    try:
        return float((value or "").strip().replace("%", ""))
    except ValueError:
        return None


def parse_memory(value):
    used, _, limit = (value or "").partition("/")
    return {
        "used_bytes": parse_bytes(used),
        "limit_bytes": parse_bytes(limit),
        "raw": value,
    }


def load_workload(path, scenario):
    with path.open(encoding="utf-8") as workload_file:
        workloads = json.load(workload_file)
    try:
        workload = workloads[scenario]
    except KeyError as error:
        available = ", ".join(sorted(workloads))
        raise SystemExit(f"Unknown scenario {scenario!r}; choose from {available}") from error
    if not workload.get("paths"):
        raise SystemExit(f"Scenario {scenario!r} has no request paths")
    return workload


class ResourceSampler:
    def __init__(self, containers):
        self.containers = containers
        self.metadata = {}
        self.samples = []
        self.errors = []
        self.stop_event = threading.Event()
        self.thread = None

    def start(self):
        if not self.containers or not shutil.which("docker"):
            return
        self._capture_metadata()
        self.thread = threading.Thread(target=self._sample_loop, daemon=True)
        self.thread.start()

    def stop(self):
        self.stop_event.set()
        if self.thread:
            self.thread.join(timeout=5)

    def _sample_loop(self):
        while not self.stop_event.is_set():
            self._sample_once()
            self.stop_event.wait(1)

    def _capture_metadata(self):
        command_script = (
            "memory_current=$(cat /sys/fs/cgroup/memory.current 2>/dev/null || printf ''); "
            "rss_kb=$(awk '/^VmRSS:/ {print $2}' /proc/1/status 2>/dev/null || printf ''); "
            "data_bytes=$(du -sb /data /photon/data 2>/dev/null | awk '{sum += $1} END {print sum + 0}'); "
            "printf '%s %s %s\\n' \"$memory_current\" \"$rss_kb\" \"$data_bytes\""
        )
        for container in self.containers:
            try:
                completed = subprocess.run(
                    ["docker", "exec", container, "sh", "-c", command_script],
                    check=True,
                    capture_output=True,
                    text=True,
                    timeout=10,
                )
                memory_current, rss_kb, data_bytes = completed.stdout.split()
                self.metadata[container] = {
                    "initial_memory_current_bytes": int(memory_current) if memory_current else None,
                    "initial_process_rss_bytes": int(rss_kb) * 1024 if rss_kb else None,
                    "dataset_bytes": int(data_bytes),
                }
            except (OSError, ValueError, subprocess.SubprocessError) as error:
                self.errors.append(f"{container}: {error}")

    def _sample_once(self):
        command = [
            "docker",
            "stats",
            "--no-stream",
            "--format",
            "{{json .}}",
            *self.containers,
        ]
        try:
            completed = subprocess.run(
                command,
                check=True,
                capture_output=True,
                text=True,
                timeout=10,
            )
        except (OSError, subprocess.SubprocessError) as error:
            self.errors.append(str(error))
            return
        timestamp = time.time()
        for line in completed.stdout.splitlines():
            try:
                data = json.loads(line)
            except json.JSONDecodeError:
                continue
            memory = parse_memory(data.get("MemUsage"))
            self.samples.append(
                {
                    "timestamp": timestamp,
                    "container": data.get("Name") or data.get("Container"),
                    "cpu_percent": parse_number(data.get("CPUPerc")),
                    "memory": memory,
                    "memory_percent": parse_number(data.get("MemPerc")),
                    "net_io": data.get("NetIO"),
                    "block_io": data.get("BlockIO"),
                    "pids": data.get("PIDs"),
                }
            )


def validate_response(scenario, status, body):
    if status is None or status < 200 or status >= 300:
        return False, f"http_{status or 'connection'}"
    try:
        payload = json.loads(body)
    except json.JSONDecodeError:
        return False, "invalid_json"
    if scenario.startswith("osrm-") and payload.get("code") != "Ok":
        return False, f"osrm_{payload.get('code', 'missing_code')}"
    return True, None


def request_once(connection, path, scenario):
    started = time.perf_counter()
    status = None
    error = None
    try:
        connection.request("GET", path, headers={"Connection": "keep-alive"})
        response = connection.getresponse()
        status = response.status
        body = response.read()
        ok, error = validate_response(scenario, status, body)
    except (OSError, http.client.HTTPException) as request_error:
        ok = False
        error = type(request_error).__name__
        connection.close()
    elapsed_ms = (time.perf_counter() - started) * 1000
    return {
        "latency_ms": elapsed_ms,
        "ok": ok,
        "status": status,
        "error": error,
        "connection_open": connection.sock is not None,
        "transport_retries": 0,
    }


def run_phase(paths, scenario, host, port, concurrency, duration, timeout, seed):
    deadline = time.perf_counter() + duration

    def worker(worker_id):
        rng = random.Random(seed + worker_id)
        connection = http.client.HTTPConnection(host, port, timeout=timeout)
        results = []
        try:
            while time.perf_counter() < deadline:
                path = rng.choice(paths)
                retries = 0
                while True:
                    result = request_once(connection, path, scenario)
                    if result["status"] is not None or retries >= 1:
                        break
                    retries += 1
                    connection = http.client.HTTPConnection(host, port, timeout=timeout)
                result["transport_retries"] = retries
                results.append(result)
                if not result["connection_open"]:
                    connection = http.client.HTTPConnection(host, port, timeout=timeout)
        finally:
            connection.close()
        return results

    with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as executor:
        futures = [executor.submit(worker, worker_id) for worker_id in range(concurrency)]
        return [result for future in futures for result in future.result()]


def summarize(results, duration):
    latencies = [result["latency_ms"] for result in results]
    successful = sum(result["ok"] for result in results)
    failures = len(results) - successful
    errors = {}
    transport_retries = sum(result["transport_retries"] for result in results)
    for result in results:
        if result["error"]:
            errors[result["error"]] = errors.get(result["error"], 0) + 1
    return {
        "requests": len(results),
        "successful_requests": successful,
        "failed_requests": failures,
        "success_rate": successful / len(results) if results else 0,
        "successful_requests_per_second": successful / duration if duration else 0,
        "all_requests_per_second": len(results) / duration if duration else 0,
        "transport_retries": transport_retries,
        "latency_ms": {
            "p50": percentile(latencies, 0.50),
            "p95": percentile(latencies, 0.95),
            "p99": percentile(latencies, 0.99),
            "max": max(latencies) if latencies else None,
        },
        "errors": errors,
    }


def summarize_resources(samples, start_timestamp):
    grouped = {}
    for sample in samples:
        if sample["timestamp"] < start_timestamp:
            continue
        grouped.setdefault(sample["container"], []).append(sample)
    summary = {}
    for container, container_samples in grouped.items():
        cpu = [sample["cpu_percent"] for sample in container_samples if sample["cpu_percent"] is not None]
        memory = [
            sample["memory"]["used_bytes"]
            for sample in container_samples
            if sample["memory"]["used_bytes"] is not None
        ]
        summary[container] = {
            "samples": len(container_samples),
            "average_cpu_percent": sum(cpu) / len(cpu) if cpu else None,
            "peak_cpu_percent": max(cpu) if cpu else None,
            "average_memory_bytes": sum(memory) / len(memory) if memory else None,
            "peak_memory_bytes": max(memory) if memory else None,
        }
    return summary


def main():
    args = parse_args()
    if args.concurrency < 1 or args.warmup < 0 or args.duration <= 0:
        raise SystemExit("concurrency must be positive, warmup non-negative, and duration positive")
    workload = load_workload(args.workloads, args.scenario)
    if workload["service"] == "osrm" and not args.base_url:
        raise SystemExit("OSRM scenarios require an OSRM base URL")

    parsed_url = urlsplit(args.base_url)
    if parsed_url.scheme != "http" or not parsed_url.hostname:
        raise SystemExit("base URL must be an http URL")
    port = parsed_url.port or 80
    prefix = parsed_url.path.rstrip("/")
    paths = [prefix + path for path in workload["paths"]]

    started_at = utc_now()
    sampler = ResourceSampler(args.container)
    sampler.start()
    if args.warmup:
        run_phase(paths, args.scenario, parsed_url.hostname, port, args.concurrency, args.warmup, args.timeout, args.seed)
    measurement_start = time.time()
    results = run_phase(
        paths,
        args.scenario,
        parsed_url.hostname,
        port,
        args.concurrency,
        args.duration,
        args.timeout,
        args.seed + 100000,
    )
    sampler.stop()

    report = {
        "schema_version": 1,
        "started_at": started_at,
        "host_architecture": platform.machine(),
        "profile": args.profile,
        "scenario": args.scenario,
        "service": workload["service"],
        "base_url": args.base_url,
        "concurrency": args.concurrency,
        "warmup_seconds": args.warmup,
        "measurement_seconds": args.duration,
        "timeout_seconds": args.timeout,
        "seed": args.seed,
        "workload_paths": len(paths),
        "summary": summarize(results, args.duration),
        "resource_summary": summarize_resources(sampler.samples, measurement_start),
        "resource_metadata": sampler.metadata,
        "resource_samples": sampler.samples,
        "resource_sampler_errors": sorted(set(sampler.errors)),
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    summary = report["summary"]
    print(
        f"{args.scenario} profile={args.profile} concurrency={args.concurrency} "
        f"rps={summary['successful_requests_per_second']:.2f} "
        f"p95_ms={summary['latency_ms']['p95'] or 0:.1f} "
        f"success={summary['success_rate']:.3f} output={args.output}"
    )


if __name__ == "__main__":
    main()
