# syntax=docker/dockerfile:1
# Multi-stage build: Flutter web UI → embedded into a static Go binary → scratch image.
FROM ghcr.io/cirruslabs/flutter:stable AS ui
WORKDIR /src
COPY app/ app/
RUN cd app && flutter pub get && flutter build web --release --base-href /

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/app/build/web/ internal/webui/web/
ARG VERSION=docker
RUN mkdir -p /tmp/fundus-data && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/fundus-app/fundus/internal/api.Version=${VERSION}" -o /fundus ./cmd/fundus

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /fundus /fundus
# /data must be writable by the nonroot user (uid 65532) before the volume is created.
COPY --from=build --chown=65532:65532 /tmp/fundus-data /data
VOLUME ["/data"]
EXPOSE 7433
ENV FUNDUS_DATA_DIR=/data FUNDUS_LISTEN=0.0.0.0:7433 FUNDUS_CONFIG=/data/config.toml
# Set FUNDUS_TOKEN (or mount a config) before exposing the port beyond localhost.
ENTRYPOINT ["/fundus"]
CMD ["serve"]
