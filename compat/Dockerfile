# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN gofmt -w *.go \
  && go test ./... \
  && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/ors-compat .

FROM alpine:3.22

RUN addgroup -S compat \
  && adduser -S -G compat compat

COPY --from=builder /out/ors-compat /usr/local/bin/ors-compat

USER compat
EXPOSE 80

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:80/healthz >/dev/null

ENTRYPOINT ["/usr/local/bin/ors-compat"]
