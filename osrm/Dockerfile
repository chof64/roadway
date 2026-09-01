# syntax=docker/dockerfile:1

# OSRM is distributed as a ready-to-run image. This repository only adds the
# data-build stage and produces a small runtime image containing the compiled
# routing graph, not the source PBF.
ARG OSRM_VERSION=26.7.3
ARG OSRM_PROFILE=restrictive
ARG OSRM_DATA_VERSION=latest

FROM alpine:3.22 AS downloader

ARG PBF_URL=https://download.geofabrik.de/asia/philippines-latest.osm.pbf
ARG PBF_SHA256=
ARG PBF_FILE=philippines.osm.pbf
ARG OSRM_DATA_VERSION=latest

COPY . /build-context/

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
COPY profiles/ /profiles/

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

LABEL org.opencontainers.image.variant="${OSRM_PROFILE}" \
      org.opencontainers.image.version="${OSRM_DATA_VERSION}"

EXPOSE 5000

# mmap keeps the compiled graph file-backed instead of eagerly copying the
# complete graph into anonymous memory. Route steps remain enabled so clients
# can inspect access classes emitted by the permissive profile.
ENTRYPOINT ["osrm-routed"]
CMD ["/data/philippines.osrm", "--algorithm", "ch", "--mmap"]
