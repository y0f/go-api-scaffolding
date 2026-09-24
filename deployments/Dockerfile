# syntax=docker/dockerfile:1
#
# Multi-stage build producing a small, static, non-root image. Base images are
# pinned by digest for reproducible builds; Dependabot keeps the digests current.

FROM golang:1.27-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195 AS build
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags "-s -w -X github.com/y0f/go-api-scaffolding/internal/config.version=${VERSION}" -o /out/api ./cmd/api && \
    go build -trimpath -ldflags "-s -w -X github.com/y0f/go-api-scaffolding/internal/config.version=${VERSION}" -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/api /api
COPY --from=build /out/migrate /migrate
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
