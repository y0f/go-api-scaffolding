Follow [AGENTS.md](../AGENTS.md) for repository commands and task guides.

Use `go run ./cmd/dev bootstrap` for tools and `go run ./cmd/dev check` to verify
changes. Edit OpenAPI and SQL sources, then regenerate; do not edit `internal/gen`.
Keep authorization in services and add tests for changed behavior.
For parallel work, use separate worktrees and a coordinator for shared contracts,
migrations, and wiring. See [the workflow](../docs/workflows/parallel.md).

When opening or updating a pull request, complete the template's required yes/no
AI-assistance declaration. Name tools and scope when AI helped. Report only
checks actually run, including failures and unavailable prerequisites. Do not
name AI tools as authors or coauthors.

## Code review priorities

- Require service-layer authorization before writes; authentication alone does
  not grant permission. Test forbidden calls produce no persisted change.
- Check bounded pagination and request validation against the OpenAPI contract;
  keep SQL parameterized and honor request cancellation.
- Check transaction rollback, database error handling, and migration effects on
  existing data. Destructive changes need an explicit migration plan.
- Check graceful shutdown, deadline cancellation, and cleanup of connections and
  goroutines; do not accept success logs that mask returned errors.
- Keep credentials and tokens out of logs, including nested or inherited log
  groups. Trust forwarded headers only from configured proxies.
- Verify generated changes follow source changes and regression tests cover
  observable behavior. Do not claim a test ran without execution evidence.
<!-- forge:begin idempotency -->
- For idempotent replays, recheck current permissions before returning cached
  data; scope keys by subject and compare request fingerprints.
<!-- forge:end idempotency -->
<!-- forge:begin outbox -->
- Keep resource writes and outbox events in the same transaction. Check retry
  and shutdown paths for dropped events or leaked workers.
<!-- forge:end outbox -->
<!-- forge:begin init -->
- Preserve optional-slice markers and initialized preset behavior; test source
  changes in generated projects as well as in this scaffold.
<!-- forge:end init -->
