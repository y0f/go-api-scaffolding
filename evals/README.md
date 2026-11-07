# Agent feature evaluations

Six tasks check setup, resource generation, a persisted field, authorization,
a seeded defect, and a second resource. The runner uses fresh clones initialized
with the full preset. Initialization and tool preparation are reported separately
from agent time; host Go caches are shared. This is not a cold-install benchmark.

The repository's instruction adapters cover more clients than the evaluation
runner. Claude Code and Codex have runner integrations; Gemini CLI, Copilot,
and Cursor instruction loading is documented separately. Only completed result
records establish which client/model combinations were measured. Compatibility
guidance is not a successful evaluation result.

## Run

Use Python 3.10+, Go, Git, Docker (or `FORGE_TEST_DATABASE_URL`), and an
authenticated Claude Code or Codex CLI. Claude Code 2.1.259 or later is needed
for [`--permission-prompts none`](https://code.claude.com/docs/en/cli-reference); the runner flags were checked with Claude Code
2.1.319 and Codex CLI 0.166.0. Model use can consume your provider
quota. Pick an explicit model identifier and a committed source revision:

```sh
python evals/run.py --agent codex --model MODEL --ref COMMIT --task all --attempts 2 --output ../codex-results
python evals/run.py --agent claude --model MODEL --ref COMMIT --task all --attempts 2 --output ../claude-results
```

The output directory must be new and outside the source repository. Each agent
attempt has a 15-minute limit; `--timeout` changes it. Each setup/check command
has a separate 10-minute limit controlled by `--verify-timeout`. Workspaces and
logs are retained for review. Metadata and local Git bookkeeping have a
60-second limit. For bootstrap, preparation builds a trusted tool reference,
then moves it out of the agent workspace. The agent must recreate matching
artifacts before the grader runs, so a no-op agent cannot receive credit for
the grader's own bootstrap. Host caches remain warm.

After preparation, the runner removes the clone's
origin remote and commits its local fixture with the synthetic author
`Scaffold evaluation fixture <eval@example.invalid>`. This separates the agent's
diff from initialization and seeded changes. The agent must not commit, and
neither the runner nor the agent pushes anything.

Agent sessions retain normal permission checks. Claude permits edits and the
documented Go/Task and read-only Git commands through Bash or native PowerShell;
`git ls-files` can inspect the current workspace, including an explicit `-C`
that names that exact workspace. Exact `gofmt -l` commands can inspect safe local
module directories present when the session starts. These rules grant no `-w`,
`-r`, extra operands, root-wide paths, or shell pipelines. Claude receives a path-scoped
[`Read` rule](https://code.claude.com/docs/en/permissions#read-and-edit) for Go's
module cache, so file reads and searches can inspect dependency sources without
granting edits there. This changes no saved settings and does not override
managed denials or outside-directory restrictions. The resolved grant is recorded
as `read_only_caches` and disclosed in the saved prompt. Prompts request individual
commands without `cd` or pipelines. Codex uses its workspace sandbox with write access
to Go's build and module cache directories. Dependencies are warmed during
preparation. Permission denials or unavailable dependencies are unsuccessful
attempts, not successful skips or evidence of model competence.
Use an isolated host or container and its normal permissions for automated runs.

On native Windows, a bounded non-model probe checks Codex sandbox startup before
each attempt. To select [MXC](https://learn.chatgpt.com/docs/windows/windows-sandbox)
for the evaluation, add `--codex-windows-sandbox mxc` to either runner. This keeps
`workspace-write`, honors managed requirements, and leaves the saved user config
unchanged. If the probe fails, resolve the host sandbox setup before retrying;
the failure is recorded as preparation, with no agent call. Linux behavior is
unchanged. Keep interrupted or permission-blocked attempts in reports when
repairing an adapter, and identify the repair before reporting fresh attempts.

For native Windows tests that need localhost and reliable temporary files, opt in:

```sh
python evals/run.py --agent codex --model MODEL --ref COMMIT --codex-windows-sandbox mxc --codex-windows-local-tests --output ../codex-local-results
```

Both runners support this option. It selects a per-attempt [permission profile](https://learn.chatgpt.com/docs/permissions)
extending `:workspace`, enables the managed network proxy with only `localhost`
and `127.0.0.1` allowed, and grants an owned scratch directory outside the app.
Each parallel worker gets separate scratch. The prompt supplies a quoted
PowerShell `TEMP`/`TMP` prefix for Go/Task calls because MXC replaces inherited
values. Exact guidance is included in the saved prompt and its hash.
Saved configuration stays unchanged; other runs keep their existing defaults.
Legacy `sandbox_mode` settings can override named profiles. A bounded non-model
inspection rejects unexpected write scopes, then a 90-second preflight runs the
actual Git-fixture, file-replacement, and localhost tests before calling a model.
Docker access and uncached dependency downloads remain separate requirements.
For integration tests, a coordinator may provide `FORGE_TEST_DATABASE_URL` for a
disposable local Postgres instance; never point evaluations at production data.

## What counts as success

- The agent exits before the time limit and adds tests for feature tasks.
- Existing tests, migrations, dependency files, instructions, and check tooling
  remain unchanged, including pinned tool binaries. New feature migrations/tests
  are allowed; new verification machinery is rejected.
- The agent neither commits changes nor adds a Git remote. Reported permission
  denials and provider errors fail the attempt even if the CLI exits with zero.
- The coordinator's repository fast check passes after the agent exits.
- The existing and added integration tests pass for feature tasks.
- The independent HTTP verifier passes against a fresh Postgres database.

The verifier is copied into the result workspace after the agent exits. It
starts the compiled application with its own RSA key and checks the served
contract, persistence, request validation, and allowed/denied token scopes.
The source is public; this is a reproducibility tool, not an adversarially secure
benchmark. Review the resulting diff for hardcoded behavior or weakened policy.
`status: passed` describes this external grading. It does not establish that the
agent's own checks succeeded. `agent_session_checks` starts as `unreviewed`;
inspect the agent log and report its checks as passed, failed, blocked, or unrun
separately. An external pass with agent-side checks blocked is not a complete
end-to-end session success.

The bootstrap task uses the fast check only. Other tasks also require their
specific behavior: CRUD and pagination; priority create/update/default/bounds
and list responses, including a row inserted before the new migration;
protected count; denied update preserving data; or both resources together.

The field verifier now also rejects explicit `null` on POST and PUT because
priority's integer schema is nonnullable. After each invalid request it compares
the complete widget and list responses, including the row count, with the prior
state. The field prompt states this requirement explicitly. This extends the
earlier 0/6 range checks. Reports must identify the new prompt and verifier
hashes and keep runs using the earlier criteria separate; an earlier
external pass does not prove null rejection.

## Parallel worktrees

The paired scenario prepares Note and Label on a common initialized baseline,
then seeds the same whitespace-validation regression in both modules. Existing
tests stay intact. Two agents repair one module each and add tests, using separate
Git worktrees. The coordinator checks file ownership and test integrity, copies
accepted changes into a third worktree, and runs the fast check, integration suite,
and independent HTTP checks for both resources. The HTTP checks reject spaces,
tabs, newlines, and Unicode whitespace while preserving nonblank names exactly.

```sh
python evals/parallel.py --agent codex --model MODEL --ref COMMIT --output ../parallel-results
python evals/parallel.py --agent codex --model MODEL --ref COMMIT --serial --output ../serial-results
```

Use `--agent claude` for Claude Code. Both modes use the same tasks and acceptance
criteria; each worker has a 10-minute default limit. `result.json` separates
preparation, worker wall time, individual calls, and total time, and records the
fixture tree hash so the prepared content can be compared. Logs contain reported
usage when available. No human intervenes during a run. Automated ownership and
integrity checks do not replace reviewing the resulting code.

Repeat both modes with the same source, client, model, and host before comparing
successful completion time and usage. Shared caches, provider load, and rate
limits affect measurements. This scenario is a bounded two-module repair, not a
claim about speedup on every feature or provider.

## Report

`results.json` includes each task, attempt, source and fixture SHAs, client
version, requested model, environment, Go version, harness, full prompt and verifier
hashes, elapsed times, exit codes, and
provider-reported usage when available. A model alias may resolve differently
later; keep the provider's resolved model data when it is returned.

Publish all attempts, including failures. The status identifies preparation,
agent execution, integrity, task, or verification failures; logs establish the
cause. A failed command alone does not distinguish an environment failure from
incorrect code. Report infrastructure failures separately from feature results.
Report task success as a
fraction with the sample size, plus elapsed time and interventions. Do not
equate a single successful demonstration with a benchmark result.

Review logs for credentials, private paths, and provider account data before
publishing. Keep raw result workspaces out of the source repository. Repeat
runs after workflow changes using the same prompts and verification criteria.

Harness tests run without either provider:

```sh
python -m unittest discover -s evals -p "test_*.py"
```
