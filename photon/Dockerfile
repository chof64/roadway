# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS health-proxy-builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY health-proxy/go.mod ./go.mod
COPY health-proxy/main.go ./
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
  go build -trimpath -ldflags='-s -w' -o /out/health-proxy .

# Build-time image: download a Photon dump and create the Lucene/OpenSearch
# index. The runtime stage contains only the Photon JAR and the index.
FROM eclipse-temurin:21-jre-jammy AS builder

ARG PHOTON_VERSION=1.2.1
ARG PHOTON_DUMP_URL=https://download1.graphhopper.com/public/asia/philippines/photon-dump-philippines-1.0-latest.jsonl.zst
ARG PHOTON_DUMP_SHA256=
ARG PHOTON_DATA_VERSION=latest
ARG PHOTON_LANGUAGES=en
ARG PHOTON_COUNTRY_CODES=ph
ARG PHOTON_REVERSE_ONLY=true
ARG PHOTON_BUILD_JAVA_OPTS=-Xmx2G

ENV PHOTON_HOME=/photon \
    PHOTON_DATA_DIR=/photon/data

RUN apt-get update \
  && apt-get install -y --no-install-recommends curl zstd ca-certificates \
  && rm -rf /var/lib/apt/lists/* \
  && mkdir -p "${PHOTON_HOME}" "${PHOTON_DATA_DIR}" \
  && curl -fsSL \
    "https://github.com/komoot/photon/releases/download/${PHOTON_VERSION}/photon-${PHOTON_VERSION}.jar" \
    -o "${PHOTON_HOME}/photon.jar"

# PHOTON_DATA_VERSION is intentionally referenced here. Pass a new value from
# the update workflow when the remote `latest` dump changes, otherwise Docker
# can reuse a cached import layer indefinitely.
RUN set -eux; \
  dump=/tmp/photon-dump.jsonl.zst; \
  echo "Building Photon data version: ${PHOTON_DATA_VERSION}"; \
  curl -fsSL "${PHOTON_DUMP_URL}" -o "${dump}"; \
  if [ -n "${PHOTON_DUMP_SHA256}" ]; then \
    echo "${PHOTON_DUMP_SHA256}  ${dump}" | sha256sum -c -; \
  fi; \
  args="-data-dir ${PHOTON_DATA_DIR} -import-file - -languages ${PHOTON_LANGUAGES}"; \
  if [ -n "${PHOTON_COUNTRY_CODES}" ]; then args="${args} -country-codes ${PHOTON_COUNTRY_CODES}"; fi; \
  if [ "${PHOTON_REVERSE_ONLY}" = "true" ]; then args="${args} -reverse-only"; fi; \
  zstd -dc "${dump}" | java ${PHOTON_BUILD_JAVA_OPTS} -jar "${PHOTON_HOME}/photon.jar" import ${args}; \
  find "${PHOTON_DATA_DIR}" -type f -name '*.lock' -delete; \
  rm -f "${dump}"

FROM eclipse-temurin:21-jre-jammy AS runtime

ARG PHOTON_VERSION=1.2.1

ENV PHOTON_HOME=/photon \
    PHOTON_DATA_DIR=/photon/data \
    PHOTON_LISTEN_PORT=8080 \
    JAVA_OPTS=-Xmx1G

RUN apt-get update \
  && apt-get install -y --no-install-recommends curl ca-certificates \
  && rm -rf /var/lib/apt/lists/* \
  && useradd --system --home-dir /photon --shell /usr/sbin/nologin photon \
  && mkdir -p "${PHOTON_HOME}" "${PHOTON_DATA_DIR}"

COPY --from=builder /photon/photon.jar /photon/photon.jar
COPY --from=builder /photon/data/ /photon/data/
COPY --from=health-proxy-builder /out/health-proxy /usr/local/bin/health-proxy

RUN chown -R photon:photon "${PHOTON_HOME}"

WORKDIR /photon
USER photon

EXPOSE 80

ENV UPSTREAM_UP_PATH=/status

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
  CMD curl -fsS http://127.0.0.1:80/up | grep -q '"status":"up"'

# CORS is deliberately not enabled by default. Keep Photon behind an
# application service or set an explicit CORS option in the deployment if
# browser clients need to call it directly.
ENTRYPOINT ["/usr/local/bin/health-proxy", "--"]
CMD ["sh", "-c", "exec java $JAVA_OPTS -jar $PHOTON_HOME/photon.jar serve -data-dir $PHOTON_DATA_DIR -listen-ip 127.0.0.1 -listen-port $PHOTON_LISTEN_PORT -max-reverse-results 1 -query-timeout 2"]
