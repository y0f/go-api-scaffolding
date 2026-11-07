# Contributing

## Getting started

```bash
go run ./cmd/dev bootstrap
go run ./cmd/dev doctor
go run ./cmd/dev check
```

The generators are built on the Go release pinned by `toolchain` in
`tools/go.mod`, because the embedded OpenAPI spec is compressed by the release
that built the generator. `task setup` handles it; never run the generators off
`$PATH`.

## Before opening a pull request

Run the full local gate when Docker and a C compiler are available:

```bash
go run ./cmd/dev ci
```

This includes the fast check, vulnerability scan, race and integration tests,
and container checks. CI also runs its platform and Go-version matrices and
release-config validation.
<!-- forge:begin init -->
The scaffold also tests initialized presets and generated resources.
<!-- forge:end init -->
See [testing](../docs/workflows/testing.md) for prerequisites and focused checks.

If you change SQL or the OpenAPI spec, run `task generate` and commit the
output. CI fails if `internal/gen` is out of date with its sources.

## AI-assisted contributions

Every pull request must answer **yes or no** to AI assistance in the PR
template. This includes AI help with design, code, tests, documentation, or
review. If yes, name the tools used and describe which parts they helped with;
include model names when known. Update the declaration if that changes during
review. This is this repository's contribution policy.

The contributor remains responsible for the whole change. Review the complete
diff, verify behavior and security assumptions, and check the origin and license
of incorporated code. Record the commands actually run and their results,
including failed or skipped checks and the reason. Do not report suggested
commands as completed tests. AI review is additional feedback; it does not
certify correctness or transfer the contributor's review responsibility.

Do not include private prompts, conversation transcripts, credentials, or other
sensitive data in the declaration. Tool names and a brief scope description are
enough. Do not add AI tools or models as commit authors or `Co-authored-by`
trailers; disclose assistance in the PR. Ordinary deterministic generators,
formatters, and linters do not count as AI assistance.

See GitHub's [responsible-use guidance for agents](https://docs.github.com/en/copilot/responsible-use/agents)
for the limits of generated code and automated review.

## GitHub Copilot setup and review

[Copilot instructions](copilot-instructions.md) contain the review checks for
this repository. Keep them specific and short, following GitHub's
[custom review guidance](https://docs.github.com/en/copilot/tutorials/customize-code-review).
CI and maintainer review enforce requirements; instructions alone do not.

The [setup workflow](workflows/copilot-setup-steps.yml) installs pinned Go tools,
downloads module dependencies, and reports available prerequisites. CI calls
the same workflow to validate it. It needs no application or model secrets.
GitHub uses this file for agent sessions that run on Actions; cloud sandboxes
need their own environment configuration. Inspect setup logs: a failed step
can leave an agent running with an incomplete environment. See GitHub's
[environment setup documentation](https://docs.github.com/en/copilot/how-tos/copilot-on-github/customize-copilot/customize-cloud-agent/customize-the-agent-environment).

## Conventions

- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org)
  (`feat:`, `fix:`, `docs:`, ...). Release notes are generated from them.
- Code is formatted with gofumpt and goimports (`task fmt`).
- New resources are added with `go run ./cmd/forge add resource <Name>`, one
  package per resource.
- Edit `api/openapi.yaml` and the `*.sql` files, then run `task generate`. Never
  edit anything under `internal/gen`; it is generated and CI fails on drift.
- Handlers are plain `http.Handler`. Authorization lives in the service layer.
- Add tests for changed behavior. Persistence uses real-Postgres integration
  tests; service rules use unit tests.

Install the git hooks with `pre-commit install` so formatting and linting run
before each commit. The hooks run the task pinned in `tools/go.mod` through
`go tool`, so no global task install is needed.

<!-- forge:begin init -->
## Optional slices

`forge init` lets a new project drop the example resource, outbox, idempotency
keys, OTLP export and the admin listener. Each lives in its own package where
it can, and where it must touch a shared file its lines sit between
`forge:begin <slice>` and `forge:end <slice>` markers. When you change a slice,
keep its markers whole: `go test ./cmd/forge` fails on an unbalanced marker,
and the `init` CI job runs representative component combinations through lint,
build and the test suites.

Before changing agent-facing workflows, run the relevant tasks in
[`evals`](../evals/README.md). Record failed attempts as well as successful ones.
Review generated projects: their instructions and documentation must reference
only the components they retain.
<!-- forge:end init -->
