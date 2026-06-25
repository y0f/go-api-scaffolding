# Contributing

## Getting started

```bash
task setup     # install the pinned toolchain
task up        # start Postgres and the service via docker compose
task test      # unit tests
```

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
