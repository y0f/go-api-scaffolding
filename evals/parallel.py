"""Compare serial and concurrent module repairs from the same prepared fixture."""

from concurrent.futures import ThreadPoolExecutor
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import time

import run as runner


RESOURCES = ("note", "label")


def run_jobs(jobs, serial=False):
    def guarded(job):
        name, work = job
        started = time.monotonic()
        try:
            return work()
        except Exception as error:  # Preserve both worker outcomes when one fails.
            return {"resource": name, "status": "error", "error": str(error),
                    "wall_seconds": round(time.monotonic() - started, 3), "human_interventions": 0}

    if serial:
        return [guarded(job) for job in jobs]
    with ThreadPoolExecutor(max_workers=2) as executor:
        return list(executor.map(guarded, jobs))


def changed_paths(workspace):
    paths = set()
    for arguments in (["git", "diff", "--name-only", "-z", "HEAD"],
                      ["git", "ls-files", "--others", "--exclude-standard", "-z"]):
        output = runner.capture(arguments, workspace, trim=False)
        paths.update(path for path in output.split("\0") if path)
    return sorted(paths)


def review_changes(workspace, resource, frozen, fixture):
    paths = changed_paths(workspace)
    prefix = f"internal/modules/{resource}/"
    violations = [path for path in paths if not path.startswith(prefix) or
                  (workspace / path).is_symlink() or
                  not (workspace / path).resolve().is_relative_to(workspace.resolve())]
    violations.extend(runner.integrity_changes(workspace, frozen))
    violations.extend(runner.repository_changes(workspace, fixture))
    added_tests = [path for path in paths if path.endswith("_test.go") and path not in frozen]
    return paths, sorted(set(violations)), added_tests


def integrate_changes(workspace, coordinator, paths):
    for relative in paths:
        source, target = workspace / relative, coordinator / relative
        if (not source.resolve().is_relative_to(workspace.resolve()) or
                not target.resolve().is_relative_to(coordinator.resolve()) or source.is_symlink()):
            raise ValueError(f"unsafe integration path: {relative}")
        if source.exists():
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, target)
        else:
            target.unlink()


def seed_validation(workspace, resource):
    path = workspace / "internal/modules" / resource / f"{resource}.go"
    source = path.read_text(encoding="utf-8")
    needle = 'strings.TrimSpace(in.Name) == ""'
    if source.count(needle) != 1:
        raise ValueError(f"unsupported {resource} validation seed")
    path.write_text(source.replace(needle, f'({needle} && in.Name == "")'), encoding="utf-8", newline="\n")


def worker(args, resource, workspace, fixture, caches):
    started = time.monotonic()
    frozen = runner.protected_files(workspace)
    prompt = (f"Fix the whitespace-only name validation regression in {resource}. "
              f"You own only internal/modules/{resource}/. Reject all-whitespace names in create and update; "
              "preserve every nonblank name exactly as supplied. Add new tests for tabs/newlines and ordinary names. "
              "Do not edit existing tests or any other paths, contracts, migrations, generated bindings, or instructions. "
              "Another worker owns the other broken module; do not repair it or run the whole suite. "
              f"Read AGENTS.md and run go test ./internal/modules/{resource}. Do not commit or push. "
              "Report the files changed and check outcomes. ") + runner.SHELL_GUIDANCE
    (args.output / f"{resource}-prompt.txt").write_text(prompt, encoding="utf-8")
    record = {"resource": resource, "status": "error", "human_interventions": 0,
              "prompt_sha256": hashlib.sha256(prompt.encode()).hexdigest()}
    command = runner.agent_command(args.agent, args.executable, args.model, workspace, caches,
                                   getattr(args, "codex_windows_sandbox", None))
    agent = runner.run(command, workspace, args.output / f"{resource}-agent.log", args.timeout, prompt=prompt)
    record["agent"] = agent
    record["reported_usage"] = runner.reported_usage(args.output / f"{resource}-agent.log")
    paths, violations, added_tests = review_changes(workspace, resource, frozen, fixture)
    record.update(changed_files=paths, integrity_changes=violations, new_test_files=added_tests)
    if agent["timeout"]:
        record["status"] = "timeout"
    elif agent["exit_code"] != 0 or record["reported_usage"].get("is_error"):
        record["status"] = "agent_error"
    elif record["reported_usage"].get("permission_denials_count", 0):
        record["status"] = "permission_denied"
    elif violations:
        record["status"] = "integrity_failed"
    elif not added_tests:
        record.update(status="task_failed", error="worker did not add tests")
    else:
        check = runner.run(runner.go_test_command(args.verify_timeout, f"./internal/modules/{resource}", tags=None), workspace,
                           args.output / f"{resource}-check.log", args.verify_timeout)
        record["check"] = check
        record["status"] = "passed" if check["exit_code"] == 0 else "verification_failed"
    record["wall_seconds"] = round(time.monotonic() - started, 3)
    return record


def scenario(args):
    started = time.monotonic()
    fixture = args.output / "fixture"
    record = {"scenario": "parallel_validation_repair", "mode": "serial" if args.serial else "parallel",
              "agent": args.agent, "model_requested": args.model, "human_interventions": 0,
              "environment": platform.platform(),
              "executor": runner.executor_name(args),
              "runner_sha256": runner.file_digest(runner.HERE / "run.py"),
              "scenario_sha256": runner.file_digest(Path(__file__)),
              "cache": "shared host Go caches; per-worktree tool binaries", "status": "error", "steps": []}
    try:
        record["client_version"] = runner.capture([args.executable, "--version"], args.source)
        record["source_commit"] = runner.capture(["git", "rev-parse", f"{args.ref}^{{commit}}"], args.source)
        record["go_version"] = runner.capture(["go", "version"], args.source)
        verifier = (runner.HERE / "acceptance_test.go.txt").read_text(encoding="utf-8")
        record["verifier_sha256"] = hashlib.sha256(verifier.encode()).hexdigest()
        runner.capture(["git", "clone", "--quiet", "--no-hardlinks", str(args.source), str(fixture)],
                       args.source, args.verify_timeout)
        runner.capture(["git", "checkout", "--quiet", "--detach", record["source_commit"]], fixture, args.verify_timeout)
        commands = [
            ["go", "run", "./cmd/forge", "init", "-yes", "-module", "example.com/eval/app"],
            ["go", "run", "./cmd/dev", "bootstrap"],
            ["go", "run", "./cmd/forge", "add", "resource", "Note"],
            ["go", "run", "./cmd/forge", "add", "resource", "Label"],
            ["go", "run", "./cmd/dev", "generate"],
            ["go", "run", "./cmd/dev", "check"],
        ]
        for index, command in enumerate(commands):
            step = runner.run(command, fixture, args.output / f"prepare-{index}.log", args.verify_timeout)
            record["steps"].append(step)
            if step["exit_code"] != 0:
                record["status"] = "preparation_failed"
                return record
        for resource in RESOURCES:
            seed_validation(fixture, resource)
        record["seed"] = "Both generated Input.Validate methods accept whitespace-only names; existing tests are retained."
        record["fixture_commit"] = runner.commit_fixture(fixture)
        record["fixture_tree"] = runner.capture(["git", "rev-parse", "HEAD^{tree}"], fixture)
        caches = [Path(value).resolve() for value in runner.capture(["go", "env", "GOCACHE", "GOMODCACHE"], fixture).splitlines()]
        record["writable_caches"] = [str(path) for path in caches] if args.agent == "codex" else []
        workspaces = {}
        for name in (*RESOURCES, "coordinator"):
            path = args.output / name
            runner.capture(["git", "worktree", "add", "--quiet", "-b", f"eval-{name}", str(path), record["fixture_commit"]],
                           fixture, args.verify_timeout)
            shutil.copytree(fixture / "bin", path / "bin")
            workspaces[name] = path
        preflight = runner.executor_preflight(args, workspaces["coordinator"], args.output / "sandbox-preflight.log")
        if preflight is not None:
            record["steps"].append(preflight)
            if not preflight["ready"]:
                record["status"] = "preparation_failed"
                record["error"] = "native Windows sandbox preflight failed; review sandbox-preflight.log before retrying"
                return record
        record["preparation_seconds"] = round(time.monotonic() - started, 3)
        phase_start = time.monotonic()
        jobs = [(name, lambda name=name: worker(args, name, workspaces[name], record["fixture_commit"], caches)) for name in RESOURCES]
        record["workers"] = run_jobs(jobs, serial=args.serial)
        record["workers_wall_seconds"] = round(time.monotonic() - phase_start, 3)
        if any(result["status"] != "passed" for result in record["workers"]):
            record["status"] = "workers_failed"
            return record
        coordinator = workspaces["coordinator"]
        for result in record["workers"]:
            integrate_changes(workspaces[result["resource"]], coordinator, result["changed_files"])
        record["integrated_files"] = changed_paths(coordinator)
        commands = [["go", "run", "./cmd/dev", "check"],
                    runner.go_test_command(args.verify_timeout)]
        for index, command in enumerate(commands):
            step = runner.run(command, coordinator, args.output / f"integrated-{index}.log", args.verify_timeout)
            record["steps"].append(step)
            if step["exit_code"] != 0:
                record["status"] = "verification_failed"
                return record
        target = coordinator / "internal/evalverify"
        runner.write_verifier(target, verifier)
        acceptance = runner.run(runner.go_test_command(args.verify_timeout, "./internal/evalverify"),
                                coordinator, args.output / "acceptance.log", args.verify_timeout,
                                env=dict(os.environ, FORGE_EVAL_TASK="parallel_validation"))
        record["steps"].append(acceptance)
        record["status"] = "passed" if acceptance["exit_code"] == 0 else "verification_failed"
        return record
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        record["error"] = str(error)
        if getattr(error, "stderr", None):
            record["error_output"] = error.stderr
        return record
    finally:
        record["total_wall_seconds"] = round(time.monotonic() - started, 3)
        (args.output / "result.json").write_text(json.dumps(record, indent=2) + "\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, default=runner.HERE.parent)
    parser.add_argument("--ref", default="HEAD")
    parser.add_argument("--agent", choices=("claude", "codex"), required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--executable")
    runner.add_executor_option(parser)
    parser.add_argument("--serial", action="store_true", help="Run the same two worker tasks sequentially")
    parser.add_argument("--timeout", type=int, default=600)
    parser.add_argument("--verify-timeout", type=int, default=600)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    runner.validate_executor_option(parser, args)
    args.source, args.output = args.source.resolve(), args.output.resolve()
    if args.output == args.source or args.source in args.output.parents:
        parser.error("output must be outside the source tree")
    if min(args.timeout, args.verify_timeout) < 1:
        parser.error("timeouts must be positive")
    args.executable = args.executable or shutil.which(args.agent)
    if not args.executable:
        parser.error(f"{args.agent} is not on PATH")
    args.output.mkdir(parents=True, exist_ok=False)
    result = scenario(args)
    print(f"{result['mode']}: {result['status']}", flush=True)
    return 0 if result["status"] == "passed" else 1


if __name__ == "__main__":
    sys.exit(main())
