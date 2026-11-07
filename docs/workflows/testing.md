# Run and diagnose checks

```sh
go test ./...
go run ./cmd/dev check
./bin/task test:integration
```

| Failure | Check first |
| --- | --- |
| Missing/wrong tool | Bootstrap; use tools from `bin` |
| Generated drift | Fix the SQL/OpenAPI source, then `task generate` |
| Postgres unavailable | `go run ./cmd/dev doctor -integration`; Docker or test database URL |
| Migration failure | Ordering, duplicate versions, and SQL against a fresh database |
| HTTP 401 | Token signature, issuer, audience, expiry |
| HTTP 403 | Service permission and caller roles/scopes |
| New route 404/501 | Contract operation, implementation, composition wiring |
| Race detector unavailable | Install a C compiler; report the check as unrun |

Integration tests use isolated databases. They use Docker unless
`FORGE_TEST_DATABASE_URL` points to a dedicated test account with permission
to create databases.

Keep a regression test for the observed failure. Do not disable checks or edit
generated code to make a test pass. Investigate blocked I/O and missing cleanup
before extending timeouts.

`go run ./cmd/dev ci` is the full local gate. CI also exercises its Go/OS matrix
and release configuration.
<!-- forge:begin init -->
Scaffold CI additionally tests initialized presets and generated resources.
<!-- forge:end init -->
Workflow files define the checks run on a pull request.
