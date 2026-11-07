# Changelog

## v1.3.0

### Upgrade notes

- The project is now named `gophling`. New installations use
  `go run github.com/y0f/gophling/cmd/forge@v1.3.0 new myapp`.
  Existing initialized projects do not upgrade automatically; review the
  relevant changes before applying them to your application.
- `forge new` selects the launcher's release by default and records the exact
  scaffold revision in `.forge.json`. `-ref` selects another tag or commit.
  Initialization verifies a staged copy before publishing. If publication is
  interrupted, use the same launcher's `init -recover` command before editing.
- Generated resources now extend OpenAPI, SQL, migrations, tests, and explicit
  application wiring together. Run `go run ./cmd/dev generate` and apply the
  migration before serving a new resource.
- The minimum Go version is 1.26.10; development tools use Go 1.27.3. Bootstrap
  downloads the preferred toolchain when needed. `go run ./cmd/dev check` runs
  the fast checks without Docker; `ci` adds integration, race, vulnerability,
  generated-project, and container checks.
- Idempotent write replays recheck current permissions. Shutdown deadlines now
  cancel unfinished HTTP requests. Sensitive parent log groups are redacted.
- `task down` retains the development database; use `task reset` to delete it.
  Development ports bind to loopback by default.
- Shared agent instructions, worktree guidance, evaluation runners, and a
  recorded resource workflow document the development process. Compatibility
  guidance and measured client results are reported separately.

## v1.2.0

### Upgrade notes

- `GET /v1/widgets` now rejects `offset` above 10000 with a 400, because the
  database reads every skipped row. Clients that page to the end with
  `offset += limit` get a 400 once they pass 10000, and widgets past
  offset 10000 plus `limit` can no longer be listed, although `total` still
  counts them. If you need deeper pages, raise `maximum` on the `Offset`
  parameter in `api/openapi.yaml` and run `task generate`.
- `FORGE_ENV` now defaults to `production` (v1.1.0 defaulted to
  `development`). Set it explicitly. A deployment that left it unset now
  refuses to start without `FORGE_AUTH_JWKS_URL` or
  `FORGE_AUTH_PUBLIC_KEY_PATH`, and one that has a verifier switches to
  production behaviour: CORS no longer reflects any origin, so list browser
  origins in `FORGE_HTTP_CORS_ALLOWED_ORIGINS`.
- `FORGE_LOG_LEVEL` must be `debug`, `info`, `warn`/`warning` or `error`, and
  `FORGE_LOG_FORMAT` must be `json` or `text` (case-insensitive, surrounding
  whitespace ignored). Other values, such as `trace`, used to fall back to
  `info` or `json` and now stop startup.
- A create with an `Idempotency-Key` from a token without a `sub` claim now
  returns 400 instead of sharing one key namespace with every other
  subject-less caller. Send such requests without the header, or issue tokens
  with a subject.
- The outbox relay no longer stops a batch at the first message the publisher
  rejects: that message records its error, backs off (2s doubling up to one
  hour) and is retried later while the messages behind it are published. So
  delivery order is no longer guaranteed; consumers that relied on it must
  order by their own data.
- Migration `00005_widgets_list_index.sql` replaces the widgets list index
  with `CREATE INDEX CONCURRENTLY` outside a transaction, so reads and writes
  on `widgets` continue while it builds. If it fails part-way, run
  `migrate up` again: it drops the half-built index and starts over.
- Idempotency keys are now stored under an escaped subject, so subjects that
  contain `:` can no longer collide with other subjects' keys. For a subject
  that contains `:` or `%`, a key stored by v1.1.0 is not found after the
  upgrade, so a retry of such a request within the key's TTL creates the
  resource again. Keys of all other subjects are unchanged.
- `migrate` now fails with `FORGE_DB_URL is required` when the variable is
  unset or empty. v1.1.0 fell back to the development default
  `postgres://forge:forge@localhost:5432/forge`; set `FORGE_DB_URL` (or copy
  `.env.example` to `.env` for `task migrate`) wherever migrations run.
- Migration `00006_outbox_published_at_idx.sql` builds its index with
  `CREATE INDEX CONCURRENTLY` outside a transaction, so outbox writes are not
  blocked while it runs. If it fails part-way, run `migrate up` again: it drops
  the invalid index and rebuilds it.
