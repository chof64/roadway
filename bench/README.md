# Roadway benchmark

This benchmark measures the two runtime services at three per-container resource tiers. It reports throughput, latency, validation failures, and Docker CPU/memory samples. It is intended for relative sizing on the M4 host, not for predicting exact VPS capacity.

## Resource tiers

| Profile | `BENCH_CPUS` | `BENCH_MEMORY` |
| --- | ---: | ---: |
| `1x` | `1` | `1g` |
| `2x` | `2` | `2g` |
| `4x` | `4` | `4g` |

The limits apply to each service container. In the combined test, a `1x` run therefore allows 1 vCPU and 1 GB to OSRM, the compatibility sidecar, and Photon.

Photon uses `-XX:MaxRAMPercentage=60` in the benchmark Compose override so its JVM heap leaves space for native memory and the Lucene index. This is a benchmark runtime setting; production tuning should be decided separately.

## Start a tier

Build or pull fixed image versions before testing. Do not rebuild between concurrency levels. The repository's default image names can be used for a local build:

```sh
BENCH_CPUS=1 BENCH_MEMORY=1g \
  docker compose -f docker-compose.yml -f docker-compose.bench.yml up -d --build
```

Wait for readiness before running the load test:

```sh
until curl -fsS http://127.0.0.1:23222/status >/dev/null; do sleep 2; done
until curl -fsS http://127.0.0.1:5001/nearest/v1/driving/121.056,14.676?number=1 >/dev/null; do sleep 2; done
```

Find container IDs for resource sampling:

```sh
OSRM_CONTAINER="$(docker compose -f docker-compose.yml -f docker-compose.bench.yml ps -q osrm)"
PHOTON_CONTAINER="$(docker compose -f docker-compose.yml -f docker-compose.bench.yml ps -q photon)"
```

## Run one sample

The runner uses persistent HTTP connections and a closed-loop concurrency model. It validates OSRM's application-level `code` in addition to HTTP status. It validates that Photon returns a successful JSON response. If a service closes a reused connection before sending a response, the runner makes one reconnect attempt and reports those reconnects separately. Each result also records the service dataset size, which is especially important for OSRM because its graph is memory-mapped.

```sh
python3 bench/run.py \
  --scenario osrm-route-2 \
  --base-url http://127.0.0.1:5001 \
  --profile 1x \
  --concurrency 8 \
  --warmup 30 \
  --duration 120 \
  --container "$OSRM_CONTAINER" \
  --output bench/results/1x/osrm-route-2-c8.json

python3 bench/run.py \
  --scenario photon-reverse \
  --base-url http://127.0.0.1:23222 \
  --profile 1x \
  --concurrency 8 \
  --warmup 30 \
  --duration 120 \
  --container "$PHOTON_CONTAINER" \
  --output bench/results/1x/photon-reverse-c8.json
```

Run the same scenarios at concurrency `1,2,4,8,16,32,64` for profiles `1x`, `2x`, and `4x`. Repeat with both `--container` values to measure contention. Keep the scenario, seed, warmup, duration, and image digest unchanged across tiers.

## Run the complete sweep

After the images are built, the sweep command recreates both containers at each tier, waits for readiness, runs all scenarios at all concurrency levels, shuts down the tier, and prints a scaling CSV:

```sh
BENCH_WARMUP=30 BENCH_DURATION=120 sh bench/run-sweep.sh
```

The default run is substantial: 3 tiers × 4 scenarios × 7 concurrency levels, with warmup and measurement time for every sample. For a quick validation, use shorter durations and fewer concurrency levels:

```sh
BENCH_WARMUP=2 BENCH_DURATION=5 BENCH_CONCURRENCIES="1 2" sh bench/run-sweep.sh
```

Set `BENCH_COMBINED=1` to additionally run OSRM route and Photon reverse requests concurrently at every tier and concurrency level. Combined results use profiles such as `1x-combined` and show contention in each service's resource samples:

```sh
BENCH_COMBINED=1 BENCH_WARMUP=30 BENCH_DURATION=120 sh bench/run-sweep.sh
```

Set `BENCH_BUILD=0` when fixed images have already been built or pulled. Results are written under `bench/results/`, which is intentionally ignored by Git. To regenerate the report later:

```sh
python3 bench/report.py bench/results
```

Use `BENCH_PROFILES="1x"` or `BENCH_PROFILES="2x 4x"` to select only specific tiers while iterating.

## Interpret results

Use the highest operating point that remains within the chosen latency and error budget. Compare tiers using:

```text
2x scaling factor = throughput_2x / throughput_1x
4x scaling factor = throughput_4x / throughput_1x
scaling efficiency = scaling factor / resource multiplier
```

The useful output is the throughput/latency curve for each scenario, not a single maximum number. A curve that flattens while CPU is saturated suggests CPU-bound behavior; a curve that improves with memory while CPU remains lower suggests cache or index pressure.

## Scenarios

- `osrm-route-2`: short two-point routes with production-like geometry output.
- `osrm-route-5`: five-point fixed-order routes.
- `osrm-table-5`: five-by-five distance/duration matrices.
- `photon-reverse`: reverse geocoding with `limit=1`.

The coordinate corpus is deliberately fixed and seeded for repeatability. It is a representative sample, not a statistically complete map of production traffic.

To benchmark the migration path, send route requests to `http://127.0.0.1:8080` and include the `ors-compat` container ID in resource sampling. Direct OSRM benchmarks remain useful for measuring the routing engine without adapter overhead.
