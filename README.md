<!-- forge:begin init -->
<p align="center">
  <img src="docs/assets/logo.webp" alt="Go" width="160">
</p>
<!-- forge:end init -->

<h1 align="center">go-api-scaffolding</h1>

<!-- forge:begin init -->
<p align="center">
  A Go API service to clone, rename and build on. The production concerns are
  wired and tested; none of it is a framework.
</p>
<!-- forge:end init -->

<p align="center">
  <a href="https://github.com/y0f/go-api-scaffolding/actions/workflows/ci.yml"><img src="https://github.com/y0f/go-api-scaffolding/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/y0f/go-api-scaffolding"><img src="https://pkg.go.dev/badge/github.com/y0f/go-api-scaffolding.svg" alt="Go Reference"></a>
  <img src="https://img.shields.io/badge/go-1.26+-00ADD8?logo=go&logoColor=white" alt="Go 1.26+">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT"></a>
</p>

## What you get

- **HTTP.** `net/http` and chi. Requests are validated against `api/openapi.yaml`,
  the typed server interface is generated from it, and CI fails on drift. Errors
  are RFC 9457 `problem+json` carrying the trace ID. Per-client rate limiting
  that keys on the forwarded client behind the proxies in
  `FORGE_HTTP_TRUSTED_PROXIES`. Readiness-first graceful drain.
- **Data.** pgx/v5, sqlc-generated queries checked against the real schema,
  goose migrations embedded in the binary and applied as a deploy step.
- **Auth.** Bearer tokens verified against JWKS (OIDC) or an RSA key. RBAC in
  the service layer. In development a token is minted and logged at startup.
- **Docs.** The spec served verbatim at `/openapi.yaml` and an interactive
  reference at `/docs`.
- **Observability.** Prometheus `/metrics`; slog with `trace_id` and `span_id`
  on every line and secret redaction.
<!-- forge:begin otlp -->
- **Tracing.** OpenTelemetry spans exported over OTLP, with a Collector, Tempo,
  Prometheus and Grafana compose profile.
<!-- forge:end otlp -->
<!-- forge:begin outbox -->
- **Outbox.** Events written in the same transaction as the state change,
  relayed at-least-once by a poller.
<!-- forge:end outbox -->
<!-- forge:begin idempotency -->
- **Idempotency.** `Idempotency-Key` on unsafe requests: the response is stored
  with the write and replayed on retry, scoped to the caller.
<!-- forge:end idempotency -->
<!-- forge:begin admin -->
- **Admin.** pprof and expvar on a separate token-gated port.
<!-- forge:end admin -->
- **Testing.** Unit tests without a database. Integration tests on a real
  Postgres via testcontainers, each test on its own clone of a migrated
  template database.
- **Supply chain.** SHA-pinned Actions, govulncheck and CodeQL, distroless
  non-root image, cosign-signed releases with an SBOM.
- **Growth.** `forge add resource <Name>` stamps a new vertical slice.

<!-- forge:begin init -->
## Start a project

Needs Go, git, Docker and [Task](https://taskfile.dev).

```bash
go run github.com/y0f/go-api-scaffolding/cmd/forge@latest new myapp
cd myapp
task up
```

`new` clones the scaffold into `myapp`, asks for your module path and which
optional slices to keep (example resource, outbox, idempotency keys, OTLP
export with the Grafana stack, admin listener), deletes the rest, removes the
installer, and regenerates and builds the result. In a clone you already have,
run `go run ./cmd/forge init` instead. Non-interactive:

```bash
go run ./cmd/forge init -yes -module github.com/you/app -drop outbox,admin
```

The `FORGE_` environment prefix and the `forge` command name stay; both are
independent of the module path. The rest of this file describes the scaffold
with everything kept.
<!-- forge:end init -->

## Quickstart

Needs Docker and [Task](https://taskfile.dev).

```bash
git clone https://github.com/y0f/go-api-scaffolding
cd go-api-scaffolding
task up          # Postgres, migrations, API on :8080
```

The API log prints a development bearer token at startup. The API reference
is at `http://localhost:8080/docs`.
<!-- forge:begin example -->

```bash
export TOKEN="<token from the api log>"

curl -s localhost:8080/v1/widgets

curl -s -X POST localhost:8080/v1/widgets \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"first"}'
```
<!-- forge:begin idempotency -->

Add `-H "Idempotency-Key: $(uuidgen)"` to the POST and a retry replays the
stored response instead of creating a second widget.
<!-- forge:end idempotency -->
<!-- forge:end example -->

`task down` stops the stack and deletes the database volume.

To run the binary outside Docker against any Postgres, copy `.env.example` to
`.env` (Task loads it), then:

```bash
task setup       # build the pinned toolchain from tools/go.mod into ./bin
task migrate
task run
```

## Add a resource

```bash
go run ./cmd/forge add resource Order
```

This writes `internal/modules/order` (domain, store, service, handler, test,
queries) and a migration, registers the queries with sqlc, and prints the
remaining steps: `task generate`, add the handler to `RouterDeps.Mounts`, grant
the write permission, `task migrate`.

Generated modules are plain chi handlers with authentication, authorization,
validation and `problem+json` errors. Make one spec-first by adding its paths
to `api/openapi.yaml` and implementing the generated interface.
<!-- forge:begin example -->
The `widget` module is that spec-first form of the same slice.
<!-- forge:end example -->

## Tasks

```bash
task generate          # regenerate sqlc and OpenAPI code
task lint              # golangci-lint
task test              # unit tests with coverage
task test:race         # unit tests under the race detector (needs a C toolchain)
task test:integration  # integration tests on real Postgres (needs Docker)
task vuln              # govulncheck
task ci                # every gate CI runs
task build             # api, migrate and forge into ./bin
```
<!-- forge:begin otlp -->

`task observe` starts the full stack with the OpenTelemetry Collector, Tempo,
Prometheus and Grafana. Grafana is at `http://localhost:3000` with both data
sources provisioned; traces flow from the API through the Collector into Tempo.
<!-- forge:end otlp -->

The toolchain (linter, generators, scanner, live reload) is pinned in
`tools/go.mod`, a module of its own so none of it enters the application's
dependency graph. CI builds the same versions from the same file.

## Layout

```
api/           the OpenAPI contract
cmd/           api (composition root), migrate, forge
internal/      config, server, auth, observability, platform, modules, gen
migrations/    versioned SQL, embedded into the binaries
deployments/   Dockerfile, docker compose
docs/          architecture and ADRs
tools/         pinned developer toolchain
```

The package-level tree, the request flow and the reasoning behind the design
are in [`docs/architecture.md`](docs/architecture.md) and
[`docs/adr`](docs/adr). Conventions: [`.github/CONTRIBUTING.md`](.github/CONTRIBUTING.md).

## Verify a release

```bash
cosign verify-blob \
  --bundle checksums.txt.bundle \
  --certificate-identity-regexp 'https://github.com/y0f/go-api-scaffolding' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

## License

MIT. See [LICENSE](LICENSE).
