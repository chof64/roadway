# roadway geospatial services

Self-hosted routing, directions, and reverse-geocoding images for applications
that need a private geographic data stack.

The repository deliberately keeps the services as independently deployable
containers:

- `ghcr.io/chof64/roadway-osrm` — OSRM routing for the Philippines, built from
  a Geofabrik OSM extract.
- `ghcr.io/chof64/roadway-photon` — Photon reverse geocoding, built from the
  Philippines Photon dump.
- `ghcr.io/chof64/roadway-ors-compat` — temporary ORS-compatible facade for the
  roadway OSRM image.

The application should call these through an internal adapter or facade:

```text
Routing application -> /ors/v2/directions/{profile} -> roadway-ors-compat:8080
roadway-ors-compat -> /route -> roadway-osrm:5000
Routing application -> /reverse       -> roadway-photon:2322
```

## Service endpoints

When running through Compose, the service names are `osrm` and `photon`:

```text
OSRM base URL:   http://osrm:5000
ORS compatibility URL: http://ors-compat:8080
Photon base URL: http://photon:2322
```

### Temporary ORS compatibility sidecar

`ors-compat` preserves the ORS-shaped routing contract while the application
migrates away from the existing ORS engine. It accepts the driving-car and
cycling-electric profiles used by Xicar, mapping both to the one configured
driving OSRM instance. It supports POST directions requests from the backend:

```text
POST http://ors-compat:8080/ors/v2/directions/driving-car
POST http://ors-compat:8080/ors/v2/directions/cycling-electric
Content-Type: application/json

{"coordinates":[[<longitude>,<latitude>],[<longitude>,<latitude>]],"instructions":false}
```

The legacy GET form remains available for simple start/end requests:

```text
GET http://ors-compat:8080/ors/v2/directions/driving-car
    ?start=<longitude>,<latitude>
    &end=<longitude>,<latitude>
    &geometry_format=geojson
    &instructions=false
```

The sidecar has no routing-flavor selector. `OSRM_UPSTREAM_URL` is the only
upstream setting, so one selected OSRM instance can be placed behind it at a
time. The adapter preserves the ORS GeoJSON response shape for route geometry,
summary, segments, and waypoints. It also includes a legacy `routes[0]` view
with an encoded polyline for the ride-share path in the current backend. Matrix
and snap requests are translated to OSRM table and nearest requests as well.
Route values can still differ from the old service when the underlying graph or
routing engine differs. Both incoming profiles intentionally use the driving
OSRM graph during this migration.

The sidecar is a migration aid, not a permanent public API. Once clients use a
native internal facade or OSRM directly, remove the `ors-compat` service and
the old ORS-shaped route contract together.

These names resolve only inside the Docker network. To access the containers
from the host, publish ports explicitly, for example:

```sh
docker run --rm -p 5001:5000 ghcr.io/chof64/roadway-osrm:local
docker run --rm -p 23222:2322 ghcr.io/chof64/roadway-photon:local
docker run --rm -p 8080:8080 \
  -e OSRM_UPSTREAM_URL=http://host.docker.internal:5001 \
  ghcr.io/chof64/roadway-ors-compat:local
```

The corresponding host URLs are then `http://127.0.0.1:5001` and
`http://127.0.0.1:23222`.

### OSRM routing and directions

For a fixed-order route, use the `route` service. Coordinates are passed in
the required order and OSRM returns legs in that same order:

```text
route(A;B;C) = A -> B -> C
```

Example with route geometry:

```sh
curl 'http://osrm:5000/route/v1/driving/121.056,14.676;121.058,14.678;121.060,14.680?overview=full&geometries=polyline6&steps=false'
```

The response contains one leg for each consecutive pair, plus total `distance`
in metres, `duration` in seconds, and a `geometry` polyline. Use
`geometries=geojson` instead when the client wants GeoJSON coordinates.

For a distance/duration matrix between multiple points, use `table`:

```sh
curl 'http://osrm:5000/table/v1/driving/121.056,14.676;121.058,14.678;121.060,14.680?annotations=distance,duration'
```

The matrix entries correspond to the input order. `distances[i][j]` is metres
from point `i` to point `j`; `durations[i][j]` is seconds.

OSRM also provides `nearest` for snapping a coordinate to the road network and
`match` for matching a GPS trace to the road network:

```sh
curl 'http://osrm:5000/nearest/v1/driving/121.056,14.676?number=1'
curl 'http://osrm:5000/match/v1/driving/121.056,14.676;121.058,14.678?overview=full&geometries=polyline6'
```

Do not use OSRM’s `trip` service for a user-prescribed stop order. `trip`
solves a Traveling Salesman-style problem and may reorder intermediate stops.
It is appropriate only when the calling application explicitly wants OSRM to
choose the stop order. For a fixed-order round trip, pass the first point again
at the end of a normal `route` request: `A;B;C;A`.

### Photon reverse geocoding

This image is built in reverse-only mode, so the supported application endpoint
is `/reverse`:

```sh
curl 'http://photon:2322/reverse?lon=121.056&lat=14.676&limit=1'
```

The response is GeoJSON. `lon` and `lat` are required; `limit` is capped at one
by the container configuration. The health endpoint is:

```sh
curl 'http://photon:2322/status'
```

Forward-search endpoints such as `/api` and `/structured` are intentionally
unavailable in the default reverse-only image.

## Build

Build the images from the repository root:

```sh
docker build -f osrm/Dockerfile -t ghcr.io/chof64/roadway-osrm:local osrm
docker build -f photon/Dockerfile -t ghcr.io/chof64/roadway-photon:local photon
docker build -f compat/Dockerfile -t ghcr.io/chof64/roadway-ors-compat:local compat
```

The OSRM build downloads the current Philippines PBF, extracts a car profile,
and contracts a static CH graph. The final image does not contain the source
PBF. The Photon build downloads the current Philippines dump and creates a
reverse-only English index by default.

For a checksum-pinned build, pass a data version and checksum explicitly:

```sh
docker build \
  --build-arg PBF_URL=https://download.geofabrik.de/asia/philippines-latest.osm.pbf \
  --build-arg PBF_SHA256=<sha256> \
  -f osrm/Dockerfile \
  -t ghcr.io/chof64/roadway-osrm:<data-version> \
  osrm

docker build \
  --build-arg PHOTON_DATA_VERSION=<data-version> \
  --build-arg PHOTON_DUMP_SHA256=<sha256> \
  -f photon/Dockerfile \
  -t ghcr.io/chof64/roadway-photon:<data-version> \
  photon
```

The date tag is the reproducible deployment pin. The automated workflow also
publishes `latest` as a convenience pointer, so deployments can either pin a
CalVer or deliberately follow the most recent weekly build.

## GitHub Actions publishing

The `Build and publish roadway images` workflow runs every Monday at 02:17 UTC
and can also be started manually. Each run builds and publishes the OSRM,
Photon, and ORS compatibility images to GHCR using a UTC CalVer tag in the form
`YYYY.MM.DD`, plus `latest`:

```text
ghcr.io/<owner>/roadway-osrm:2026.09.01
ghcr.io/<owner>/roadway-osrm:latest
ghcr.io/<owner>/roadway-photon:2026.09.01
ghcr.io/<owner>/roadway-photon:latest
ghcr.io/<owner>/roadway-ors-compat:2026.09.01
ghcr.io/<owner>/roadway-ors-compat:latest
```

The workflow uses the GitHub-provided token, so the repository must have
Actions permission to write packages. A manual run may provide a CalVer
override when a date tag needs to be rebuilt. The Photon CalVer is passed as
`PHOTON_DATA_VERSION` so the scheduled run does not reuse the import layer for
the remote `latest` dump. The OSRM and Photon jobs use separate Buildx caches.

## Runtime notes

OSRM is configured for static CH routing with memory-mapped graph files. It
keeps route geometry for polylines and disables route steps. Photon is limited
to one reverse result and a two-second query timeout; CORS is not enabled by
default because the services should normally be private to the application
network.

Builds need substantially more temporary CPU and memory than the running
services. Run the scheduled build on a builder/CI worker, publish the images,
and keep the production host responsible only for running them.
