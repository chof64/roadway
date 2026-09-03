#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
image="${OSRM_TEST_IMAGE:-ghcr.io/project-osrm/osrm-backend:26.7.3-debian}"
work_dir="$(mktemp -d)"
container_name="roadway-osrm-smoke-$$"

cleanup() {
  docker rm -f "${container_name}" >/dev/null 2>&1 || true
  rm -rf "${work_dir}"
}

trap cleanup EXIT

for profile in restrictive permissive; do
  mkdir -p "${work_dir}/${profile}"

  docker run --rm \
    -v "${repo_root}:/workspace:ro" \
    -v "${work_dir}/${profile}:/data" \
    --entrypoint sh \
    "${image}" \
    -ec "
      osrm-extract \
        --profile /workspace/osrm/profiles/${profile}.lua \
        --output /data/philippines.osrm \
        /workspace/osrm/tests/fixtures/access.osm
      osrm-contract /data/philippines.osrm
    "

  docker run -d --rm \
    --name "${container_name}" \
    -p 127.0.0.1:5500:80 \
    -v "${work_dir}/${profile}:/data:ro" \
    --entrypoint osrm-routed \
    "${image}" \
    /data/philippines.osrm --algorithm ch --mmap --port 80 >/dev/null

  ready=false
  for attempt in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:5500/route/v1/driving/121.000000,14.000000;121.003000,14.000000?overview=false" >/dev/null; then
      ready=true
      break
    fi
    sleep 1
  done

  if [[ "${ready}" != true ]]; then
    echo "${profile} OSRM server did not become ready" >&2
    docker logs "${container_name}" >&2 || true
    exit 1
  fi

  curl -fsS \
    'http://127.0.0.1:5500/route/v1/driving/121.000000,14.000000;121.003000,14.000000?overview=false&steps=true&annotations=true' \
    > "${work_dir}/${profile}/direct.json"

  curl -sS \
    'http://127.0.0.1:5500/route/v1/driving/121.009000,14.010000;121.012000,14.010000?overview=false' \
    > "${work_dir}/${profile}/blocked.json"

  docker rm -f "${container_name}" >/dev/null

  jq -e '.code == "Ok"' "${work_dir}/${profile}/direct.json" >/dev/null
  jq -e '.code == "NoRoute"' "${work_dir}/${profile}/blocked.json" >/dev/null

  if [[ "${profile}" == permissive ]]; then
    jq -e '[.routes[0].legs[].steps[].intersections[].classes[]?] | index("restricted") != null' \
      "${work_dir}/${profile}/direct.json" >/dev/null
  fi
done

restrictive_distance="$(jq -r '.routes[0].distance' "${work_dir}/restrictive/direct.json")"
permissive_distance="$(jq -r '.routes[0].distance' "${work_dir}/permissive/direct.json")"

awk -v restrictive="${restrictive_distance}" -v permissive="${permissive_distance}" \
  'BEGIN { if (permissive >= restrictive) exit 1 }'

echo "OSRM profile smoke test passed: permissive route is shorter and restricted access remains classified."
