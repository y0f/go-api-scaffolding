# AGENTS.md

Guidance for coding agents (Claude Code, and other AI assistants) working in this
repository. Human contributors should read `CONTRIBUTING.md`.

## Commands

```bash
task setup             # install the pinned toolchain
task generate          # regenerate sqlc and OpenAPI code after editing SQL or api/openapi.yaml
task lint              # golangci-lint v2 (must pass)
task test              # unit tests, race detector, coverage
task test:integration  # integration tests against real Postgres (needs Docker)
task vuln              # govulncheck
```

## Conventions

- Edit `api/openapi.yaml` and the `*.sql` files, then run `task generate`. Never
  edit anything under `internal/gen`; it is generated and CI fails on drift.
- Each resource is one package under `internal/modules`. Add new ones with
  `forge add resource <Name>`, then follow the printed steps.
- Handlers are plain `http.Handler`. Authorization lives in the service layer.
- Commit messages follow Conventional Commits. Format with `task fmt`.
- Add a test with every change.

## Where things live

- Entry point and wiring: `cmd/api/main.go`.
- The contract: `api/openapi.yaml`. Architecture and decisions: `docs/`.
- The example slice to copy: `internal/modules/widget`.

## Exposing the API to LLM tools

The OpenAPI 3.0 document at `api/openapi.yaml` is the single source of truth, so
operations can be converted into tool or function schemas for an agent without
hand-writing them.
