# syntax=docker/dockerfile:1
#
# Multi-stage build producing a small, static, non-root image. Pin the base
# images by digest in your fork for fully reproducible builds.

FROM golang:1.26-bookworm AS build
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags "-s -w -X github.com/forge/forge/internal/config.version=${VERSION}" -o /out/api ./cmd/api && \
    go build -trimpath -ldflags "-s -w -X github.com/forge/forge/internal/config.version=${VERSION}" -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
COPY --from=build /out/migrate /migrate
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
