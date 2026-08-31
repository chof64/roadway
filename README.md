# roadway geospatial services

Self-hosted routing, directions, and reverse-geocoding images for applications
that need a private geographic data stack.

The repository deliberately keeps the services as two independently deployable
containers:

- `ghcr.io/chof64/roadway-osrm` — OSRM routing for the Philippines, built from
  a Geofabrik OSM extract.
- `ghcr.io/chof64/roadway-photon` — Photon reverse geocoding, built from the
  Philippines Photon dump.

The application should call these through an internal adapter or facade:

```text
Routing application -> /route, /table -> roadway-osrm:5000
Routing application -> /reverse       -> roadway-photon:2322
```

## Service endpoints

When running through Compose, the service names are `osrm` and `photon`:

```text
OSRM base URL:   http://osrm:5000
Photon base URL: http://photon:2322
```

These names resolve only inside the Docker network. To access the containers
from the host, publish ports explicitly, for example:

```sh
docker run --rm -p 5001:5000 ghcr.io/chof64/roadway-osrm:local
docker run --rm -p 23222:2322 ghcr.io/chof64/roadway-photon:local
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
```

The OSRM build downloads the current Philippines PBF, extracts a car profile,
and contracts a static CH graph. The final image does not contain the source
PBF. The Photon build downloads the current Philippines dump and creates a
reverse-only English index by default.

For reproducible scheduled builds, pass a data version and checksum from the
workflow:

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

Do not use `latest` as the deployed tag. Publish an immutable date or source
timestamp tag, smoke-test both containers, then deploy that tag to the calling
application.

## Runtime notes

OSRM is configured for static CH routing with memory-mapped graph files. It
keeps route geometry for polylines and disables route steps. Photon is limited
to one reverse result and a two-second query timeout; CORS is not enabled by
default because the services should normally be private to the application
network.

Builds need substantially more temporary CPU and memory than the running
services. Run the scheduled build on a builder/CI worker, publish the images,
and keep the production host responsible only for running them.
