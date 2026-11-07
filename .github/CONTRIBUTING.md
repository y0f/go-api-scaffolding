# Contributing

## Getting started

```bash
go run ./cmd/dev bootstrap
go run ./cmd/dev doctor
go run ./cmd/dev check
```

The generators are built on the Go release pinned by `toolchain` in
`tools/go.mod`, because the embedded OpenAPI spec is compressed by the release
that built the generator. `task setup` handles it; never run the generators off
`$PATH`.

## Before opening a pull request

Run the full local gate when Docker and a C compiler are available:

```bash
go run ./cmd/dev ci
```

This includes the fast check, vulnerability scan, race and integration tests,
generated-project tests, and container checks. CI also runs its platform and
Go-version matrices, initialized presets, and release-config validation.
See [testing](../docs/workflows/testing.md) for prerequisites and focused checks.

If you change SQL or the OpenAPI spec, run `task generate` and commit the
output. CI fails if `internal/gen` is out of date with its sources.

## Conventions

- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org)
  (`feat:`, `fix:`, `docs:`, ...). Release notes are generated from them.
- Code is formatted with gofumpt and goimports (`task fmt`).
- New resources are added with `go run ./cmd/forge add resource <Name>`, one
  package per resource.
- Edit `api/openapi.yaml` and the `*.sql` files, then run `task generate`. Never
  edit anything under `internal/gen`; it is generated and CI fails on drift.
- Handlers are plain `http.Handler`. Authorization lives in the service layer.
- Add tests for changed behavior. Persistence uses real-Postgres integration
  tests; service rules use unit tests.

Install the git hooks with `pre-commit install` so formatting and linting run
before each commit. The hooks run the task pinned in `tools/go.mod` through
`go tool`, so no global task install is needed.

<!-- forge:begin init -->
## Optional slices

`forge init` lets a new project drop the example resource, outbox, idempotency
keys, OTLP export and the admin listener. Each lives in its own package where
it can, and where it must touch a shared file its lines sit between
`forge:begin <slice>` and `forge:end <slice>` markers. When you change a slice,
keep its markers whole: `go test ./cmd/forge` fails on an unbalanced marker,
and the `init` CI job runs every preset through lint, build and the test
suites.

Before changing agent-facing workflows, run the relevant tasks in
[`evals`](../evals/README.md). Record failed attempts as well as successful ones.
Review generated projects: their instructions and documentation must reference
only the components they retain.
<!-- forge:end init -->
