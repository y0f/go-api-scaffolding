# Add a persisted field

1. Add a new migration. Keep applied migrations unchanged. Define the default
   or backfill for existing rows before making the field required.
2. Update the resource's query SQL, domain input, and repository mapping.
3. Update the OpenAPI input/output schemas, including bounds and optionality.
4. Run `task generate`, then map the generated types in the handler.
5. Test create, read, update, invalid values, and migration of existing rows.
6. Run `go run ./cmd/dev check` and `task test:integration`.

Migrations use increasing numeric versions. Renumber an unapplied migration
if parallel branches chose the same version. Review rollback behavior before
deployment, especially when it removes data.

A passing build does not prove persistence: write the field and read it back
from the real test database.
