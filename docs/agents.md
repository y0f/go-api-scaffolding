# Coding agent setup

`AGENTS.md` holds shared repository instructions. Adapters keep that guidance
available across clients; the application has no model or agent dependency.

## Instruction loading

Checked against official documentation on November 7, 2026. These are
instruction-loading paths, not claims that every client passed the evaluations.

| Client | Repository guidance |
| --- | --- |
| Codex | Reads `AGENTS.md`; see [instruction discovery](https://learn.chatgpt.com/docs/agent-configuration/agents-md) |
| Claude Code | `CLAUDE.md` imports `AGENTS.md`; see [project memory](https://code.claude.com/docs/en/memory) |
| Gemini CLI | `GEMINI.md` imports `@./AGENTS.md`; use `/memory show` to inspect [loaded context](https://geminicli.com/docs/cli/gemini-md/) |
| Copilot | `AGENTS.md` for supported agents; `.github/copilot-instructions.md` for other [supported surfaces](https://docs.github.com/en/copilot/reference/custom-instructions-support) |
| Cursor | Uses root `AGENTS.md` as [project instructions](https://cursor.com/docs/rules#agentsmd) |

For another client, load `AGENTS.md` through its repository-instruction feature.
Confirm the file was read before delegating work. Provider settings still control
tools, network access, permissions, and available models.

## Local setup

Use a patched supported Go release (minimum 1.26.10) and Git, then run:

```sh
go run ./cmd/dev bootstrap
go run ./cmd/dev doctor
go run ./cmd/dev check
```

Bootstrap builds the tools pinned in `tools/go.mod` into `bin` with Go 1.27.3,
downloading that toolchain if needed. The first run needs network access.
Use `./bin/task` on Linux/macOS and `./bin/task.exe` on
Windows if Task is not on PATH. Start your coding agent in the repository root
and ask it to summarize the instructions to confirm they loaded.

| Check | Extra requirement |
| --- | --- |
| `go run ./cmd/dev check` | None after dependency download and bootstrap |
| `task test:integration` | Docker or `FORGE_TEST_DATABASE_URL` |
| `task test:race` | A C compiler |
| `go run ./cmd/dev ci` | Docker, C compiler, network for vulnerability data |

## Hosted environments

Run `go run ./cmd/dev bootstrap`, `go mod download`, and
`go -C tools mod download` during environment setup. Cache Go's module and
build directories. Run the fast check before assigning a feature.

Without Docker, set `FORGE_TEST_DATABASE_URL` to a dedicated Postgres test
server. Its user needs database-creation permission; tests create and remove
isolated databases. Never use a production connection. Store credentials in
the environment's secret settings. If neither test backend is available,
report integration tests as unrun and use CI for that gate.

Plain chat sessions need a checkout and execution tools to run these checks.

## Parallel work

Use [the worktree workflow](workflows/parallel.md): workers own independent
modules, while one coordinator integrates shared contracts, migrations, SQL
registration, and wiring. Each worktree keeps its own `bin`; Go's caches can
be shared. Run focused checks in workers and the full gate on the integrated
tree.

A handoff should state the behavior changed, checks run, and remaining limits.
