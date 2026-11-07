# Parallel feature work

Use one coordinator and one worktree per worker. Assign independent resource
changes together; keep dependent steps in order. A second worker helps when
there is a separate module, test task, or review to complete.

## Assign ownership

| Owner | Files and decisions |
| --- | --- |
| Resource worker | Its `internal/modules/<name>` source, query SQL, and tests |
| Coordinator | `api/openapi.yaml`, `sqlc.yaml`, `cmd/api/resources.go`, migration versions, final `internal/gen` output |
| Reviewer | The integrated diff and evidence against the requested behavior |

The task brief names the branch, owned paths, expected behavior, test command,
and dependencies. Workers send shared-file changes to the coordinator instead
of editing another worker's checkout.
Give each worker the relevant task guide and acceptance criteria; it need not
read every guide or inherit unrelated conversation history.

## Prepare a common base

The coordinator generates new resource skeletons serially, resolves their
contract and migration entries, regenerates, and checks that baseline. Review
and commit it before starting independent feature edits.

```sh
go run ./cmd/forge add resource Note
go run ./cmd/forge add resource Label
go run ./cmd/dev generate
go run ./cmd/dev check
```

Create worker branches from that reviewed commit:

```sh
git worktree add -b feat/notes ../api-notes <base-commit>
git worktree add -b feat/labels ../api-labels <base-commit>
```

Bootstrap each worktree once. Keep tools in its own `bin`; Go's module and build
caches can remain shared. Workers can now implement the two modules in parallel.
If contract changes are needed, the coordinator updates the common branch and
workers incorporate that commit before continuing dependent work.

## Local stacks

By default, initialization derives a stable Compose project name from the full
Go module path. Moving a checkout keeps that name; worktrees of the same app share it.
Prefer one coordinator-owned development stack while workers use isolated test
databases. A simultaneous worker stack needs a unique `COMPOSE_PROJECT_NAME` and
different `FORGE_DEV_API_PORT` and `FORGE_DEV_DB_PORT` values in its untracked
`.env`; Task loads them. If running `task migrate` on the host, also match
`FORGE_DB_URL` to that database port.
<!-- forge:begin otlp -->
For simultaneous observability stacks, also choose different
`FORGE_DEV_OTLP_PORT`, `FORGE_DEV_COLLECTOR_METRICS_PORT`,
`FORGE_DEV_PROMETHEUS_PORT`, and `FORGE_DEV_GRAFANA_PORT` values.
<!-- forge:end otlp -->
Keep each project's name stable to retain its local database. Changing the name
selects a different volume; it does not move or delete existing data.

## Integrate and verify

1. Each worker runs `go fmt ./internal/modules/<name>` and its package tests,
   then hands off the diff, exact test outcomes, shared-file requests, and
   unresolved decisions. The coordinator's full lint still checks gofumpt and
   goimports formatting.
2. The coordinator reviews each change and integrates sources one at a time.
   Only renumber migrations that have never been applied. Resolve duplicate
   versions before running migrations or generating SQL bindings.
3. Regenerate on the combined tree. Workers may regenerate locally for tests,
   but generated-file conflicts are resolved from sources, never by hand.
4. Run `go run ./cmd/dev check` and the relevant integration tests. Run
   `go run ./cmd/dev ci` before merging the finished change.
5. Review the final diff against acceptance criteria, including both modules
   running together. Keep changes on the integration branch until it passes.

Avoid repeated full-suite runs in every worker when a focused check answers the
question. Re-run broader checks after integration or a change that affects their
result. Measure elapsed time and interventions before claiming a speedup.
