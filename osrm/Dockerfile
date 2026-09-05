# syntax=docker/dockerfile:1

# OSRM is distributed as a ready-to-run image. This repository only adds the
# data-build stage and produces a small runtime image containing the compiled
# routing graph, not the source PBF.
ARG OSRM_VERSION=26.7.3
ARG OSRM_PROFILE=restrictive
ARG OSRM_DATA_VERSION=latest

FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS health-proxy-builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY health-proxy/go.mod ./go.mod
COPY health-proxy/main.go ./
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
  go build -trimpath -ldflags='-s -w' -o /out/health-proxy .

FROM alpine:3.22 AS downloader

ARG PBF_URL=https://download.geofabrik.de/asia/philippines-latest.osm.pbf
ARG PBF_SHA256=
ARG PBF_FILE=philippines.osm.pbf
ARG OSRM_DATA_VERSION=latest

COPY osrm/ /build-context/

RUN apk add --no-cache ca-certificates curl \
  && mkdir -p /input \
  && echo "Building OSRM data version: ${OSRM_DATA_VERSION}" \
  && if [ -f "/build-context/${PBF_FILE}" ]; then \
       cp "/build-context/${PBF_FILE}" /input/philippines.osm.pbf; \
     else \
       curl -fL --retry 5 --retry-delay 3 "${PBF_URL}" -o /input/philippines.osm.pbf; \
     fi \
  && if [ -n "${PBF_SHA256}" ]; then \
       echo "${PBF_SHA256}  /input/philippines.osm.pbf" | sha256sum -c -; \
     fi

FROM ghcr.io/project-osrm/osrm-backend:${OSRM_VERSION}-debian AS builder

ARG OSRM_BUILD_THREADS=2
ARG OSRM_PROFILE=restrictive

WORKDIR /data
COPY --from=downloader /input/philippines.osm.pbf /data/philippines.osm.pbf
COPY osrm/profiles/ /profiles/

# CH is a good first fit for this deployment: the graph is static between data
# refreshes and there is no live-traffic customization requirement. If that
# changes, replace this with osrm-partition + osrm-customize and run MLD.
RUN case "${OSRM_PROFILE}" in \
      restrictive|permissive) ;; \
      *) echo "Unsupported OSRM_PROFILE: ${OSRM_PROFILE}" >&2; exit 2 ;; \
    esac \
  && mkdir -p /profiles/lib \
  && cp -a /opt/lib/. /profiles/lib/ \
  && osrm-extract \
      --profile "/profiles/${OSRM_PROFILE}.lua" \
      --threads "${OSRM_BUILD_THREADS}" \
      /data/philippines.osm.pbf \
  && osrm-contract /data/philippines.osrm \
  && rm -f /data/philippines.osm.pbf

FROM ghcr.io/project-osrm/osrm-backend:${OSRM_VERSION}-debian AS runtime

ARG OSRM_PROFILE=restrictive
ARG OSRM_DATA_VERSION=latest

COPY --from=builder /data/philippines.osrm* /data/
COPY --from=health-proxy-builder /out/health-proxy /usr/local/bin/health-proxy

LABEL org.opencontainers.image.variant="${OSRM_PROFILE}" \
      org.opencontainers.image.version="${OSRM_DATA_VERSION}"

EXPOSE 80

ENV UPSTREAM_UP_PATH="/route/v1/driving/121.056,14.676;121.058,14.678?overview=false"

# Probe a real route so Docker reports the service unhealthy if the HTTP
# listener is up but the compiled graph is not available.
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
  CMD ["bash", "-ec", "exec 3<>/dev/tcp/127.0.0.1/80; printf 'GET /up HTTP/1.1\\r\\nHost: localhost\\r\\nConnection: close\\r\\n\\r\\n' >&3; IFS= read -r status <&3; case \"$status\" in HTTP/*\\ 200\\ *) exit 0 ;; *) exit 1 ;; esac"]

# mmap keeps the compiled graph file-backed instead of eagerly copying the
# complete graph into anonymous memory. Route steps remain enabled so clients
# can inspect access classes emitted by the permissive profile.
ENTRYPOINT ["/usr/local/bin/health-proxy", "--"]
CMD ["osrm-routed", "/data/philippines.osrm", "--algorithm", "ch", "--mmap", "--port", "8080"]
