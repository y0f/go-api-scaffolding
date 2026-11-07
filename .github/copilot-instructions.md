Follow [AGENTS.md](../AGENTS.md) for repository commands and task guides.

Use `go run ./cmd/dev bootstrap` for tools and `go run ./cmd/dev check` to verify
changes. Edit OpenAPI and SQL sources, then regenerate; do not edit `internal/gen`.
Keep authorization in services and add tests for changed behavior.
For parallel work, use separate worktrees and a coordinator for shared contracts,
migrations, and wiring. See [the workflow](../docs/workflows/parallel.md).
