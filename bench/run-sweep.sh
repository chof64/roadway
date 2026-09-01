#!/bin/sh
set -eu

compose="docker compose -f docker-compose.yml -f docker-compose.bench.yml"
results_dir="${BENCH_RESULTS_DIR:-bench/results}"
concurrencies="${BENCH_CONCURRENCIES:-1 2 4 8 16 32 64}"
warmup="${BENCH_WARMUP:-30}"
duration="${BENCH_DURATION:-120}"
profiles="${BENCH_PROFILES:-1x 2x 4x}"

cleanup() {
  $compose down >/dev/null 2>&1 || true
}

trap cleanup EXIT INT TERM

wait_for_url() {
  url="$1"
  attempts=0
  while ! curl -fsS "$url" >/dev/null 2>&1; do
    attempts=$((attempts + 1))
    if [ "$attempts" -ge 90 ]; then
      echo "Timed out waiting for $url" >&2
      return 1
    fi
    sleep 2
  done
}

run_scenario() {
  profile="$1"
  scenario="$2"
  base_url="$3"
  container="$4"
  for concurrency in $concurrencies; do
    output="$results_dir/$profile/$scenario-c${concurrency}.json"
    python3 bench/run.py \
      --scenario "$scenario" \
      --base-url "$base_url" \
      --profile "$profile" \
      --concurrency "$concurrency" \
      --warmup "$warmup" \
      --duration "$duration" \
      --container "$container" \
      --output "$output"
  done
}

run_combined() {
  profile="$1"
  osrm_container="$2"
  photon_container="$3"
  for concurrency in $concurrencies; do
    osrm_output="$results_dir/$profile-combined/osrm-route-2-c${concurrency}.json"
    photon_output="$results_dir/$profile-combined/photon-reverse-c${concurrency}.json"
    python3 bench/run.py \
      --scenario osrm-route-2 \
      --base-url "http://127.0.0.1:${BENCH_OSRM_PORT:-5001}" \
      --profile "$profile-combined" \
      --concurrency "$concurrency" \
      --warmup "$warmup" \
      --duration "$duration" \
      --container "$osrm_container" \
      --output "$osrm_output" &
    osrm_pid=$!
    python3 bench/run.py \
      --scenario photon-reverse \
      --base-url "http://127.0.0.1:${BENCH_PHOTON_PORT:-23222}" \
      --profile "$profile-combined" \
      --concurrency "$concurrency" \
      --warmup "$warmup" \
      --duration "$duration" \
      --container "$photon_container" \
      --output "$photon_output" &
    photon_pid=$!
    set +e
    wait "$osrm_pid"
    osrm_status=$?
    wait "$photon_pid"
    photon_status=$?
    set -e
    if [ "$osrm_status" -ne 0 ] || [ "$photon_status" -ne 0 ]; then
      return 1
    fi
  done
}

if [ "${BENCH_BUILD:-1}" = "1" ]; then
  $compose build
fi

for profile in $profiles; do
  case "$profile" in
    1x) BENCH_CPUS=1 BENCH_MEMORY=1g;;
    2x) BENCH_CPUS=2 BENCH_MEMORY=2g;;
    4x) BENCH_CPUS=4 BENCH_MEMORY=4g;;
  esac
  export BENCH_CPUS BENCH_MEMORY

  $compose up -d --force-recreate
  wait_for_url "http://127.0.0.1:${BENCH_PHOTON_PORT:-23222}/status"
  wait_for_url "http://127.0.0.1:${BENCH_OSRM_PORT:-5001}/nearest/v1/driving/121.056,14.676?number=1"

  osrm_container="$($compose ps -q osrm)"
  photon_container="$($compose ps -q photon)"
  run_scenario "$profile" osrm-route-2 "http://127.0.0.1:${BENCH_OSRM_PORT:-5001}" "$osrm_container"
  run_scenario "$profile" osrm-route-5 "http://127.0.0.1:${BENCH_OSRM_PORT:-5001}" "$osrm_container"
  run_scenario "$profile" osrm-table-5 "http://127.0.0.1:${BENCH_OSRM_PORT:-5001}" "$osrm_container"
  run_scenario "$profile" photon-reverse "http://127.0.0.1:${BENCH_PHOTON_PORT:-23222}" "$photon_container"
  if [ "${BENCH_COMBINED:-0}" = "1" ]; then
    run_combined "$profile" "$osrm_container" "$photon_container"
  fi

  $compose down
done

python3 bench/report.py "$results_dir"
