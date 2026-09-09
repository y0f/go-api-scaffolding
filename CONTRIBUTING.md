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
- New resources are added with `go run ./cmd/forge add resource <Name>`, which
  follows the vertical-slice layout used by `internal/modules/widget`.
- Add a test with every change. Repository code is covered by integration tests
  against a real Postgres; business logic is covered by unit tests.

Install the git hooks with `pre-commit install` so formatting and linting run
before each commit.
