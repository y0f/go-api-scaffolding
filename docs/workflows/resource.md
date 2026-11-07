# Add a resource

```sh
go run ./cmd/forge add resource Note
./bin/task generate
```

The generator adds `internal/modules/note`, a migration, SQL registration,
OpenAPI operations, tests, and composition wiring. List, create, get, update,
and delete use the same contract and pagination conventions as the API.
Add `-dry-run` to inspect the planned files before writing them.

Concurrent generation in one checkout is locked. After an interrupted command,
inspect `.forge-resource.lock` and confirm its process has stopped before
removing the lock. Review the diff and restore unfinished generated source
before retrying. Separate worktrees avoid sharing that lock.

1. Edit the module's domain and service for its business rules.
2. Update query SQL, migrations, and OpenAPI schemas for the data model.
3. Choose access rules. Reads are public; writes require `notes:write`.
   Issue that scope or grant it to selected roles in `internal/auth/principal.go`.
4. Run `task generate` after SQL or contract edits.
5. Test successful requests, invalid input, and denied access.
6. Run `go run ./cmd/dev check`, `task test:integration`, and `task migrate`.

`task migrate` requires `FORGE_DB_URL`. For local development, copy `.env.example`
to `.env` and set the database URL there; Task loads this file.

Do not edit `internal/gen`. Module files outside that directory are ordinary
application code. The route must appear in `/openapi.yaml` and work through
the public router before the feature is complete.

For an operation on an existing resource, add its contract path and operation
ID, regenerate, implement the typed handler, and delegate it in
`cmd/api/resources.go`. Keep policy decisions in the service.

<!-- forge:begin example -->
`internal/modules/widget` includes a complete persistence and HTTP example.
<!-- forge:end example -->
