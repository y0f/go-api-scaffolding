# Contributing

## Getting started

```bash
task setup     # build the pinned toolchain from tools/go.mod into ./bin
task up        # start Postgres and the service via docker compose
task test      # unit tests
```

The OpenAPI spec is embedded in `internal/gen/api/api.gen.go` in compressed
form, and the compressor lives in the Go release that built the generator, so
regenerate with the Go version CI uses (see `.github/workflows/ci.yml`) or the
drift gate will flag bytes that carry no real change.

## Before opening a pull request

Run the same gates CI runs:

```bash
task lint
task vuln
task test
task test:integration   # requires Docker
```

If you change SQL or the OpenAPI spec, regenerate and commit the output:

```bash
task generate
```

CI fails if `internal/gen` is out of date with its sources.

## Conventions

- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org)
  (`feat:`, `fix:`, `docs:`, ...). Release notes are generated from them.
- Code is formatted with gofumpt and goimports (`task fmt`).
- New resources are added with `forge add resource <Name>`, which follows the
  vertical-slice layout used by `internal/modules/widget`.
- Add a test with every change. Repository code is covered by integration tests
  against a real Postgres; business logic is covered by unit tests.

Install the git hooks with `pre-commit install` so formatting and linting run
before each commit.
