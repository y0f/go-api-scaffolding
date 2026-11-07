# gophling

<!-- forge:begin init -->
<img src="docs/assets/gophling.png" alt="gophling mascot" width="128" height="128" align="right">

Go REST API scaffold for parallel coding agents, with PostgreSQL,
OpenAPI, sqlc, and reproducible checks. Each resource is an ordinary Go package.

[![CI](https://github.com/y0f/gophling/actions/workflows/ci.yml/badge.svg)](https://github.com/y0f/gophling/actions/workflows/ci.yml)
[![MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

## Start a project

Use a patched supported Go release (minimum 1.26.10) and Git. Bootstrap uses
the pinned Go 1.27.3 toolchain, downloading it if needed. Docker runs the local
database.

```sh
go run github.com/y0f/gophling/cmd/forge@latest new myapp
cd myapp
go run ./cmd/dev bootstrap
```

The installer asks for a module path and the optional components to keep.
For automation, pass `-yes -module github.com/you/myapp`. Use `-ref <tag-or-SHA>`
to select the scaffold revision; `.forge.json` records the resolved revision.
For repeatable setup, pin the launcher version (`cmd/forge@<release>`) and use a
full commit SHA for `-ref` when selecting older or custom source.

If in-place initialization is interrupted while publishing files, recover from
the project root before making manual changes:

```sh
go run github.com/y0f/gophling/cmd/forge@<release> init -recover
```

Use the release that performed initialization. Recovery restores the saved
tracked files and preserves unrelated untracked files. Earlier tool or
generation failures leave the original project untouched.
<!-- forge:end init -->

## Run and verify

```sh
go run ./cmd/dev doctor
go run ./cmd/dev check
./bin/task up
```

The fast check runs without a database. `task up` starts the API and Postgres.
On PowerShell use `./bin/task.exe`; if Task is on PATH, `task` also works.

The API is at `http://localhost:8080`, the reference at `/docs`, and the
contract at `/openapi.yaml`. Read the development bearer token from the API log:

```sh
docker compose -f deployments/docker-compose.yml logs api
```

`task down` stops the stack and keeps data. `task reset` also deletes the
database volume.

## Add a feature

```sh
go run ./cmd/forge add resource Note
./bin/task generate
go run ./cmd/dev check
```

The generator adds a module, migration, SQL queries, OpenAPI operations, tests,
and route wiring. Reads are public; writes require `notes:write` or an explicit
role grant. The logged development token has the `admin` role, which gets no
automatic grant for new resources. Apply the migration before serving the routes.

Alternatively, ask your coding agent to perform the same change:

> Add a Note resource. Keep reads public and require notes:write for writes.
> Test invalid input and denied writes. Regenerate the bindings, then run the
> fast and integration checks.

[Agent setup](docs/agents.md) covers Claude Code, Codex, Gemini CLI, Copilot,
and Cursor instruction loading.
[Parallel work](docs/workflows/parallel.md) assigns modules to separate
worktrees and shared changes to one coordinator.
[The resource guide](docs/workflows/resource.md) gives the complete workflow.

## Included

| Area | Implementation |
| --- | --- |
| HTTP | `net/http`, chi, OpenAPI validation, generated interfaces, RFC 9457 errors |
| Data | PostgreSQL, pgx, sqlc, embedded goose migrations |
| Auth | OIDC/JWKS or RSA verification; service-level permissions |
| Operations | Structured logs, Prometheus metrics, health probes, graceful drain |
| Tests | Unit tests and isolated real-Postgres integration tests |
| Releases | Pinned tools and Actions, vulnerability checks, non-root image, signed checksums and SBOMs |

<!-- forge:begin outbox -->
Optional outbox: commit events with state changes and retry failed deliveries.
The example publisher only logs event metadata; replace it for external delivery.
<!-- forge:end outbox -->
<!-- forge:begin idempotency -->
Optional idempotency: replay create requests with the same caller, key, and
body; reject a changed body with 409.
<!-- forge:end idempotency -->
<!-- forge:begin otlp -->
Optional tracing: OTLP export with a local Collector, Tempo, and Grafana stack.
<!-- forge:end otlp -->
<!-- forge:begin admin -->
Optional admin listener: token-protected pprof and expvar on a separate port.
<!-- forge:end admin -->

Production mode requires a token verifier. The local Compose stack explicitly
enables development mode and its temporary signing key.

## Documentation

- [Setup and commands](docs/agents.md)
- [Parallel worktrees](docs/workflows/parallel.md)
- [Architecture and decisions](docs/architecture.md)
- [Schema changes](docs/workflows/schema.md)
- [Authorization](docs/workflows/authorization.md)
- [Testing](docs/workflows/testing.md)
<!-- forge:begin init -->
- [Fit and alternatives](docs/choosing.md)
- [Agent evaluation method](evals/README.md)
- [Recorded resource workflow](https://y0f.github.io/gophling/demo.html)
<!-- forge:end init -->
- [Contributing](.github/CONTRIBUTING.md)

## Verify a release

Replace `TAG` with the downloaded release tag (for example, `v1.3.0`) and
`OWNER/REPOSITORY` with the GitHub repository that published it.
<!-- forge:begin init -->
For gophling releases, the repository is `y0f/gophling`.
<!-- forge:end init -->

```sh
cosign verify-blob --bundle checksums.txt.bundle \
  --certificate-identity 'https://github.com/OWNER/REPOSITORY/.github/workflows/release.yml@refs/tags/TAG' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
```

Then compare the SHA-256 hash of your downloaded archive with its entry in the
verified `checksums.txt` before extracting it.

For public GitHub.com releases, also verify the archive's build provenance.
Replace `ARCHIVE` with the downloaded filename.

```sh
gh attestation verify ARCHIVE --repo OWNER/REPOSITORY \
  --signer-workflow OWNER/REPOSITORY/.github/workflows/release.yml \
  --source-ref refs/tags/TAG
```

This verifies the artifact's association with the release workflow and source
tag. The workflow skips GitHub attestations for private repositories;
enabling them there requires a
supported GitHub Enterprise Cloud plan. See GitHub's
[attestation guide](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations)
and [verification options](https://cli.github.com/manual/gh_attestation_verify).

When the release is marked **Immutable**, verify GitHub's release attestation
and the downloaded asset as well:

```sh
gh release verify TAG --repo OWNER/REPOSITORY
gh release verify-asset TAG ARCHIVE --repo OWNER/REPOSITORY
```

This applies to uploaded release assets, not GitHub's automatically generated
source ZIP/tarballs. Immutability must be enabled for each repository and applies
to future releases; it does not protect older releases or custom branch refs.
See [release operations](.github/RELEASING.md) and GitHub's
[release verification guide](https://docs.github.com/en/code-security/how-tos/secure-your-supply-chain/secure-your-dependencies/verify-release-integrity).

MIT. See [LICENSE](LICENSE).
