# Contributing

## Getting started

```bash
task setup     # build the pinned toolchain from tools/go.mod into ./bin
task up        # start Postgres and the service via docker compose
task test      # unit tests
```

The generators are built on the Go release pinned by `toolchain` in
`tools/go.mod`, because the embedded OpenAPI spec is compressed by the release
that built the generator. `task setup` handles it; never run the generators off
`$PATH`.

## Before opening a pull request

Run the same gates CI runs:

```bash
task ci
```

That is lint, govulncheck, the generated-code drift gate, unit tests,
integration tests (requires Docker) and a build. CI also runs the unit tests
under the race detector; run `task test:race` yourself if you have a C
toolchain.

If you change SQL or the OpenAPI spec, run `task generate` and commit the
output. CI fails if `internal/gen` is out of date with its sources.

## Conventions

- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org)
  (`feat:`, `fix:`, `docs:`, ...). Release notes are generated from them.
- Code is formatted with gofumpt and goimports (`task fmt`).
- New resources are added with `go run ./cmd/forge add resource <Name>`, one
  package per resource.
- Add a test with every change. Repository code is covered by integration tests
  against a real Postgres; business logic is covered by unit tests.

Install the git hooks with `pre-commit install` so formatting and linting run
before each commit.

<!-- forge:begin init -->
## Optional slices

`forge init` lets a new project drop the example resource, outbox, idempotency
keys, OTLP export and the admin listener. Each lives in its own package where
it can, and where it must touch a shared file its lines sit between
`forge:begin <slice>` and `forge:end <slice>` markers. When you change a slice,
keep its markers whole: `go test ./cmd/forge` fails on an unbalanced marker,
and the `init` CI job runs every preset through lint, build and the test
suites.
<!-- forge:end init -->
