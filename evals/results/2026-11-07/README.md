# Agent evaluations: 7 November 2026

These development trials informed changes made before v1.3.0. They record every
attempt, including failures, against specific revisions. They do not score the
final release as a whole or establish a provider ranking.

Runs used Codex CLI **0.166.0** with `gpt-6-astra` and Claude Code **2.1.319**
with `claude-opus-5-5`. Requested and provider-reported model identities are in
the JSON records; model aliases may resolve differently later.

## Feature tasks

The six tasks cover bootstrap, a resource, a persisted field, authorization,
a seeded defect, and a second resource. An external bootstrap pass requires the
trusted coordinator's fast check. Feature passes also require integration tests
and independent HTTP acceptance against the compiled API and real PostgreSQL.

| Cohort | External passes | Other outcomes |
| --- | ---: | --- |
| Original Codex | 12/12 | Two field passes have a later contract finding |
| Original Claude | 8/12 | Four permission failures; two field passes have that finding |
| Claude permission follow-up | 3/4 | One permission failure |
| Corrected field, Codex | 2/2 | Passed the stronger criteria |
| Corrected field, Claude | 1/2 | One permission failure |

The [original cohort](baseline.json) has two attempts per task and client.
Its four field passes retain `accepted_with_contract_finding`: default injection
could accept explicit null despite the nonnullable contract. Codex field attempt
2 directly observed this; the other three findings are inferred from shared
configuration and mapping. The original external scores remain unchanged.

The [permission follow-up](permission-followup.json) keeps the original product
and acceptance criteria, with scoped dependency reads and Git inspection added
to the adapter. It covers authorization and a second resource. One attempt was
denied access to a Taskfile outside the workspace. The original failures also
include unsupported compound commands and `git stash`; all remain recorded.

The [corrected field cohort](null-field-followup.json) uses a repaired validator,
a more explicit prompt, and stronger acceptance checks. POST and PUT must reject
0, 6, and null without changing stored rows, timestamps, or counts; the served
schema must specify an optional, nonnullable integer from 1 to 5 with default 3.
The Claude failure involved unsupported Task/linter invocations and a prohibited
pipeline edit. These changed conditions prevent an identical-criteria comparison
with the original cohort.

External grading and agent-session checks are separate. Codex logs expose
observed command exits; Claude feature logs contain final self-reports.
Permission-denied attempts receive no external feature grade. Every completed
attempt also received a separate read-only coding-agent review; this is not a
claim of human review or a correctness certification.

## Serial and parallel repairs

The [predeclared plan](paired-plan.json) runs two repetitions per client and mode,
one scenario at a time. Each scenario prepares Note and Label, seeds the same
whitespace-validation defect, and assigns each module to a separate session and
worktree. Serial mode runs those two sessions sequentially; it does not measure
one agent doing both tasks in a shared conversation. Integration is automated.

All eight prepared trees were identical:
`af7c752d23fe45039d19e07232daf936ab55f899`. Final integrity checks found no
file-ownership or protected-test violations in any of the 16 workers. Fifteen
worker package checks passed; one was not run because of a permission denial.
Final acceptance requires the
integrated fast check, integration suite, and independent HTTP checks.

| Scenario | Preparation (s) | Worker phase (s) | Elapsed until exit (s) | Final outcome |
| --- | ---: | ---: | ---: | --- |
| Codex serial 1 | 76.4 | 240.0 | 342.6 | Passed |
| Codex parallel 1 | 83.2 | 134.0 | 240.2 | Passed |
| Claude parallel 1 | 36.6 | 52.6 | 112.4 | Passed |
| Claude serial 1 | 38.0 | 97.7 | 144.5 | Formatting failed |
| Codex parallel 2 | 107.5 | 125.0 | 258.8 | Passed |
| Codex serial 2 | 76.1 | 244.8 | 348.2 | Passed |
| Claude serial 2 | 40.0 | 98.7 | 147.8 | Formatting failed |
| Claude parallel 2 | 41.8 | 57.5 | 99.4 | Permission denied |

Worker time includes the model calls and coordinator-run package checks. Total
elapsed time includes preparation and any final checks that ran. Failed trials
stopped before integrated acceptance; their shorter elapsed times are not
successful completion times. Both serial Claude failures left a missing space
that gofumpt rejected. The last parallel trial attempted an ungranted read-only
`gofmt -l` command. No failed trial was repaired or replaced in these scores.

Codex completed both modes twice, with lower parallel completion times in these
runs. Claude has no completed serial trial for a completion-time comparison.
Two repetitions of one bounded repair do not establish a general speedup or
quality difference. See the [phase summary](paired-summary.json) and
[complete paired metadata](paired.json) for exact clocks, usage, and reviews.

## Earlier trials and adapter probes

[Historical adapter trials](historical-inventory.json) retain seven attempt
directories: five completed records (two host bootstrap passes and three
permission failures) and two interrupted attempts. Both coordinator stop records
remain. Agent-side checks were blocked or initially failed in the bootstrap
trials despite their eventual host-grader passes.

Two separate, single-invocation probes are excluded from every feature and paired
denominator. The [read-permission probe](capability-probe.json) observed a scoped
dependency read and the exact quoted-workspace Bash Git command succeed. The
[formatting probe](formatting-capability-probe.json) observed the formerly denied
PowerShell `gofmt -l internal/modules/label` command succeed after a narrow adapter
fix. Neither changed the fixture or saved client settings; each had zero denials
and no retry. Only those exact tool forms have runtime evidence.

Completed runs record zero in-run interventions. Later fixes, follow-up cohorts,
and post-run coding-agent reviews are separate. The legacy `human_interventions`
field does not imply that a human performed the coordinator actions or reviews.

## Reproduce and inspect

Use the [runner instructions](../../README.md) and both the recorded source and
harness revision. Selecting `--ref` alone does not select a historical harness.

| Cohort | Source revision | Harness revision |
| --- | --- | --- |
| Original | `46800b1` | `46800b1` |
| Permission follow-up | `46800b1` | `9619269` |
| Corrected field | `2717bce` | `2717bce` |
| Serial/parallel | `d38d2e3` | `d38d2e3` |

For example, from a checkout containing the recorded commits:

```sh
git worktree add --detach ../gophling-eval d38d2e39f4369e64fab5a58b4c4f1d527e9f1909
cd ../gophling-eval
python evals/parallel.py --agent codex --model gpt-6-astra --ref d38d2e39f4369e64fab5a58b4c4f1d527e9f1909 --codex-windows-sandbox mxc --codex-windows-local-tests --output ../paired-results
```

The Windows flags reproduce this host's selected executor; omit them on Linux.
Use a new output directory for each run and `--serial` for the other mode.
The individual cohorts used 900-second agent and 600-second verification limits;
paired runs used 600 seconds for both. Plans preserve the exact task order and
counts. Provider authentication, quotas, prerequisites, and normal permission
checks still apply.

The host used Windows 11, Go 1.27.3, Docker 29.1.3, and a disposable PostgreSQL
server. [Environment context](coordinator-context.json) records the platform,
Docker version, and launch plan. Exact hardware details and Windows build numbers
are intentionally omitted from public metadata.
Go caches were shared and warm. Original client batches overlapped; paired
scenarios ran sequentially with no competing local test batches from this
session. Other host activity, provider load, and cache order were uncontrolled.
Default client settings were not reset. These conditions do not support a
cross-provider speed or cost ranking.

The [publication index](publication-index.json) links the plans, exact versions,
source/harness/prompt/verifier hashes, phase results, usage, reviews, and evidence
digests. It reconciles 32 individual records and eight paired scenarios with their
original manifests. The scaffold, tasks, and verifier sources are public at the
recorded revisions. Raw logs, prompts, workspaces, and full review artifacts
remain private. Sanitized review summaries are included; public metadata excludes
credentials, account/session IDs, and local paths. Prompt hashes use UTF-8/LF
text; saved-file byte hashes are separate.
