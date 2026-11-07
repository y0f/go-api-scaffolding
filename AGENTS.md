# Repository guide

Go HTTP API backed by PostgreSQL. Resources live in `internal/modules/<name>`;
`cmd/api` wires them together.

## Commands

```sh
go run ./cmd/dev bootstrap  # pinned tools; network on first use
go run ./cmd/dev doctor     # available prerequisites
go run ./cmd/dev check      # lint, generation, unit tests, build; no database
go run ./cmd/dev ci         # full local gate; Docker and a C compiler
```

Use a focused package test while editing. Run the fast check before handing off
code; use integration tests for SQL, migrations, transactions, and HTTP behavior.
Report checks that could not run.

## Boundaries

- Edit `api/openapi.yaml`, migrations, and query SQL; run `task generate`.
  Never edit `internal/gen` by hand. Use the tools pinned in `tools/go.mod`.
- Keep authorization in services, before persistence. Authentication alone
  does not grant a resource permission. Generated reads are public; writes
  require an explicit permission. Choose a different policy when needed.
- Add a resource with `go run ./cmd/forge add resource <Name>`.
- Parallel agents use separate worktrees and own separate modules. One
  coordinator integrates shared contracts, migrations, wiring, and generation.
- Add regression tests for changed behavior. Keep existing checks intact.
- `task down` preserves data; `task reset` deletes the local database volume.
- Follow the [contribution policy](.github/CONTRIBUTING.md) for pull requests:
  complete its required yes/no AI-assistance declaration. Do not name AI tools
  as authors or coauthors.

## Task guides

| Change | Read |
| --- | --- |
| Resource or endpoint | [Resource workflow](docs/workflows/resource.md) |
| Persisted field | [Schema changes](docs/workflows/schema.md) |
| Access rules | [Authorization](docs/workflows/authorization.md) |
| Failing check | [Testing](docs/workflows/testing.md) |
| Hosted agent | [Agent setup](docs/agents.md) |
| Parallel feature work | [Worktree workflow](docs/workflows/parallel.md) |
| Package boundaries | [Architecture](docs/architecture.md) |

<!-- forge:begin example -->
The working reference is `internal/modules/widget`.
<!-- forge:end example -->
<!-- forge:begin init -->
Scaffold maintainers: preserve slice markers and test initialized projects.
<!-- forge:end init -->
