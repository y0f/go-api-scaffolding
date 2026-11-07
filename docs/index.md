# Go API scaffold for parallel agents

Build a Go REST API with PostgreSQL, OpenAPI, and sqlc. Resource packages give
workers clear ownership; a coordinator integrates shared contracts and migrations.
The same commands check work locally and in CI.

Start with [setup](agents.md), then follow the guide for your change.

| Task | Guide |
| --- | --- |
| Add an endpoint | [Resources](workflows/resource.md) |
| Persist a field | [Schema changes](workflows/schema.md) |
| Protect an operation | [Authorization](workflows/authorization.md) |
| Run or debug tests | [Testing](workflows/testing.md) |
| Coordinate parallel agents | [Worktrees and ownership](workflows/parallel.md) |
| Understand the structure | [Architecture](architecture.md) |

<!-- forge:begin init -->
[Fit and alternatives](choosing.md) describes the scope of this starter.
[Agent evaluations](https://github.com/y0f/gophling/tree/main/evals)
explains how feature tasks are checked.
<!-- forge:end init -->
