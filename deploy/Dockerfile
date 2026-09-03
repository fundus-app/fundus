# syntax=docker/dockerfile:1
# Multi-stage build: Flutter web UI → embedded into a static Go binary → scratch image.
# Stage 1: the web UI. The Flutter version is read from app/pubspec.yaml
# (environment.flutter), the single source of truth shared with GitHub Actions.
FROM debian:bookworm-slim AS ui
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends curl ca-certificates git unzip xz-utils zip >/dev/null && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY app/pubspec.yaml app/pubspec.yaml
RUN set -e; FV="$(grep -E '^[[:space:]]*flutter:' app/pubspec.yaml | head -1 | awk '{print $2}' | tr -d '"')"; \
    [ -n "$FV" ] || { echo "app/pubspec.yaml must pin environment.flutter"; exit 1; }; \
    curl -sSL "https://storage.googleapis.com/flutter_infra_release/releases/stable/linux/flutter_linux_${FV}-stable.tar.xz" | tar -xJ -C /opt; \
    git config --global --add safe.directory /opt/flutter
ENV PATH="/opt/flutter/bin:${PATH}" FLUTTER_SUPPRESS_ANALYTICS=true CI=true
RUN flutter config --no-analytics --enable-web >/dev/null && flutter --version
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

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /fundus /fundus
# /data must be writable by the nonroot user (uid 65532) before the volume is created.
COPY --from=build --chown=65532:65532 /tmp/fundus-data /data
VOLUME ["/data"]
EXPOSE 7433
ENV FUNDUS_DATA_DIR=/data FUNDUS_LISTEN=0.0.0.0:7433 FUNDUS_CONFIG=/data/config.toml
# Set FUNDUS_TOKEN (or mount a config) before exposing the port beyond localhost.
ENTRYPOINT ["/fundus"]
CMD ["serve"]
