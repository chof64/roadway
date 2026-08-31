# syntax=docker/dockerfile:1

# OSRM is distributed as a ready-to-run image. This repository only adds the
# data-build stage and produces a small runtime image containing the compiled
# routing graph, not the source PBF.
ARG OSRM_VERSION=26.7.3

FROM alpine:3.22 AS downloader

ARG PBF_URL=https://download.geofabrik.de/asia/philippines-latest.osm.pbf
ARG PBF_SHA256=

RUN apk add --no-cache ca-certificates curl \
  && mkdir -p /input \
  && curl -fL --retry 5 --retry-delay 3 "${PBF_URL}" -o /input/philippines.osm.pbf \
  && if [ -n "${PBF_SHA256}" ]; then \
       echo "${PBF_SHA256}  /input/philippines.osm.pbf" | sha256sum -c -; \
     fi

FROM ghcr.io/project-osrm/osrm-backend:${OSRM_VERSION}-debian AS builder

ARG OSRM_BUILD_THREADS=2

WORKDIR /data
COPY --from=downloader /input/philippines.osm.pbf /data/philippines.osm.pbf

# CH is a good first fit for this deployment: the graph is static between data
# refreshes and there is no live-traffic customization requirement. If that
# changes, replace this with osrm-partition + osrm-customize and run MLD.
RUN osrm-extract \
      --profile /opt/car.lua \
      --threads "${OSRM_BUILD_THREADS}" \
      /data/philippines.osm.pbf \
  && osrm-contract /data/philippines.osrm \
  && rm -f /data/philippines.osm.pbf

FROM ghcr.io/project-osrm/osrm-backend:${OSRM_VERSION}-debian AS runtime

COPY --from=builder /data/philippines.osrm* /data/

EXPOSE 5000

# mmap keeps the compiled graph file-backed instead of eagerly copying the
# complete graph into anonymous memory. ROUTE_GEOMETRY remains enabled because
# clients need route polylines; ROUTE_STEPS is disabled because it is not needed.
ENTRYPOINT ["osrm-routed"]
CMD ["/data/philippines.osrm", "--algorithm", "ch", "--mmap", "--disable-feature-dataset", "ROUTE_STEPS"]
