"""Run bounded coding tasks in disposable clones; grade them outside the agent run."""

from __future__ import annotations

import argparse
from contextlib import nullcontext
import hashlib
import json
import os
from pathlib import Path, PurePosixPath, PureWindowsPath
import platform
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import xml.etree.ElementTree as ET


HERE = Path(__file__).resolve().parent
CHECK_FILES = {"Taskfile.yml", ".golangci.yml", "go.mod", "go.sum", "go.work", "go.work.sum",
               "AGENTS.md", "CLAUDE.md", "GEMINI.md", ".gitignore", ".gitattributes", ".forge.json"}
CHECK_PREFIXES = (".github/", ".claude/", ".codex/", ".cursor/", ".gemini/", "tools/", "evals/",
                  "cmd/dev/", "internal/testutil/", "internal/evalverify/")
CAPTURE_TIMEOUT = 60
SHELL_GUIDANCE = ("Run each shell command separately from the current workspace root. "
                  "Do not prepend cd, wrap commands in another shell, or use pipelines/redirection. "
                  "Use the native PowerShell tool on Windows when available. ")


def capture(argv, cwd, timeout=CAPTURE_TIMEOUT, trim=True):
    """Bound metadata and Git commands with the same process cleanup as grading."""
    with tempfile.TemporaryDirectory(prefix="forge-eval-command-") as temporary:
        log = Path(temporary) / "command.log"
        errors = Path(temporary) / "command.stderr.log"
        result = run(argv, cwd, log, timeout, stderr_log=errors)
        output = log.read_text(encoding="utf-8", errors="replace")
        error_output = errors.read_text(encoding="utf-8", errors="replace")
        if result["timeout"]:
            raise subprocess.TimeoutExpired(argv, timeout, output=output, stderr=error_output)
        if result["exit_code"] != 0:
            raise subprocess.CalledProcessError(result["exit_code"], argv, output=output, stderr=error_output)
        return output.strip() if trim else output


def run(argv, cwd, log, timeout, prompt=None, env=None, stderr_log=None):
    """Preserve logs and terminate descendants if a command exceeds its budget."""
    started = time.monotonic()
    options = {"creationflags": subprocess.CREATE_NEW_PROCESS_GROUP} if os.name == "nt" else {"start_new_session": True}
    errors = stderr_log.open("w", encoding="utf-8") if stderr_log else nullcontext(None)
    with log.open("w", encoding="utf-8") as output, errors as error_output:
        process = subprocess.Popen(argv, cwd=cwd, stdin=subprocess.PIPE if prompt is not None else subprocess.DEVNULL,
                                   stdout=output, stderr=error_output or subprocess.STDOUT, text=True, encoding="utf-8",
                                   env=env, **options)
        timed_out = False
        try:
            process.communicate(prompt, timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True
            if os.name == "nt":
                try:
                    subprocess.run(["taskkill", "/PID", str(process.pid), "/T", "/F"], timeout=10,
                                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
                except (OSError, subprocess.TimeoutExpired):
                    pass  # Still stop the direct process if tree cleanup failed.
                finally:
                    if process.poll() is None:
                        process.kill()
            else:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass  # The process may exit between the deadline and kill.
            process.wait(timeout=10)
    return {"command": argv, "exit_code": process.returncode, "timeout": timed_out,
            "seconds": round(time.monotonic() - started, 3), "log": log.name}


def file_digest(path):
    if path.is_symlink():
        return "symlink:" + str(path.readlink())
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def verification_path(name):
    return name in CHECK_FILES or name.startswith(CHECK_PREFIXES)


def protected_files(root):
    """Freeze existing tests, migrations, instructions and verification machinery."""
    result = {}
    for path in root.rglob("*"):
        if not path.is_file() and not path.is_symlink():
            continue
        relative = path.relative_to(root).as_posix()
        if relative.startswith((".git/", ".task/")):
            continue
        if (verification_path(relative) or relative.endswith("_test.go") or
                relative.startswith(("migrations/", "bin/"))):
            result[relative] = file_digest(path)
    return result


def integrity_changes(root, before):
    after = protected_files(root)
    changes = {name for name, digest in before.items() if after.get(name) != digest}
    changes.update(name for name in after if name not in before and
                   (verification_path(name) or (name.startswith("migrations/") and not name.endswith(".sql"))))
    return sorted(changes)


def repository_changes(root, fixture):
    changes = []
    if capture(["git", "rev-parse", "HEAD"], root) != fixture:
        changes.append("agent committed changes")
    if capture(["git", "remote"], root):
        changes.append("agent added a remote")
    return changes


def bootstrap_changes(root, expected):
    actual = protected_files(root)
    return sorted(name for name, digest in expected.items() if actual.get(name) != digest)


def write_verifier(target, verifier, baseline_migrations=None):
    target.mkdir(exist_ok=False)
    (target / "acceptance_test.go").write_text(
        verifier.replace("github.com/y0f/gophling", "example.com/eval/app"), encoding="utf-8", newline="\n")
    if baseline_migrations is not None:
        baseline = target / "baseline"
        baseline.mkdir()
        for name, content in baseline_migrations.items():
            (baseline / name).write_bytes(content)


def go_test_command(timeout, package="./...", tags="integration"):
    # The supervisor must stop the whole process tree before Go can abort the
    # test binary without running cleanups for its API subprocess.
    command = ["go", "test", "-count=1", "-timeout", f"{timeout + 30}s"]
    if tags:
        command.extend(["-tags", tags])
    return command + [package]


def application_snapshot(root):
    result = {}
    for path in root.rglob("*"):
        relative = path.relative_to(root).as_posix()
        if (path.is_file() and path.suffix in (".go", ".sql", ".yaml", ".yml") and
                not relative.startswith((".git/", "bin/", ".task/"))):
            result[relative] = hashlib.sha256(path.read_bytes()).hexdigest()
    return result


def local_profile(scratch, writable_caches=()):
    """Use a per-attempt name so unrelated saved profiles cannot add grants."""
    name = "gophling-eval-" + hashlib.sha256(str(scratch).encode()).hexdigest()[:12]
    roots = "{" + ",".join(json.dumps(str(path)) + "=true" for path in writable_caches) + "}"
    settings = [f'default_permissions="{name}"', "features.network_proxy=true",
                f'permissions.{name}.extends=":workspace"',
                f"permissions.{name}.workspace_roots={roots}",
                f'permissions.{name}.filesystem={{{json.dumps(str(scratch))}="write"}}',
                f"permissions.{name}.network.enabled=true",
                f"permissions.{name}.network.allow_local_binding=true",
                f'permissions.{name}.network.domains={{localhost="allow","127.0.0.1"="allow"}}']
    return name, [argument for setting in settings for argument in ("-c", setting)]


def scratch_prefix(scratch):
    # PowerShell single-quoted literals do not evaluate $() or backticks.
    literal = "'" + str(scratch).replace("'", "''") + "'"
    return f"$env:TEMP={literal}; $env:TMP=$env:TEMP; "


def shell_guidance(scratch=None):
    if scratch is None:
        return SHELL_GUIDANCE
    return SHELL_GUIDANCE + (
        "For this Windows MXC run only, prefix each Go or Task command in the same PowerShell call with "
        f"`{scratch_prefix(scratch)}`. This temp assignment is the only exception to separate commands; "
        "MXC replaces inherited TEMP/TMP. The runner owns this scratch directory outside the application. "
        "Only localhost networking is enabled; report unavailable external dependencies or Docker access. ")


def prepare_scratch(args, directory, name="sandbox-temp"):
    if not getattr(args, "codex_windows_local_tests", False):
        return None
    scratch = (directory / name).resolve()
    scratch.mkdir(exist_ok=False)
    return scratch


def check_local_permissions(executable, workspace, caches, scratch):
    """Fail closed if saved legacy settings override the requested boundary."""
    _, settings = local_profile(scratch, caches)
    output = capture([executable, "-c", "windows.sandbox=mxc", *settings, "debug", "prompt-input"],
                     workspace, timeout=30)
    messages = json.loads(output)
    contexts = [item.get("text", "") for message in messages if isinstance(message, dict)
                for item in message.get("content", []) if isinstance(item, dict)
                and item.get("text", "").startswith("<environment_context>")]
    if len(contexts) != 1:
        raise ValueError("cannot verify the effective Codex workspace boundary; no model call made")
    try:
        filesystem = ET.fromstring(contexts[0]).find("filesystem/permission_profile/file_system")
    except ET.ParseError as error:
        raise ValueError("cannot parse effective Codex permissions; no model call made") from error
    if filesystem is None or filesystem.get("type") != "restricted":
        raise ValueError("Codex resolved unrestricted filesystem permissions; no model call made")
    canonical = lambda path: os.path.normcase(os.path.realpath(path))
    allowed = {canonical(path) for path in (workspace, scratch, *caches)}
    actual = set()
    for entry in filesystem.findall("entry"):
        if entry.get("access") != "write":
            continue
        if entry.find("special") is not None:
            if entry.findtext("special") not in (":slash_tmp", ":tmpdir"):
                raise ValueError("unexpected Codex writable filesystem scope; no model call made")
        elif entry.find("path") is not None:
            actual.add(canonical(entry.findtext("path")))
        else:
            raise ValueError("unknown Codex writable filesystem rule; no model call made")
    if actual != allowed:
        raise ValueError("Codex effective write roots differ from workspace, caches and owned scratch; no model call made")
    return {"filesystem": "restricted", "write_roots": sorted(actual), "inspection": "no-model prompt-input"}


def read_only_caches(agent, caches):
    # Both callers collect `go env GOCACHE GOMODCACHE`, in that order. Only the
    # second directory contains dependency sources Claude needs to inspect.
    return list(caches[1:2]) if agent == "claude" else []


def claude_read_rule(directory):
    """Anchor a Read rule to one local directory, without adding a worktree."""
    raw = str(directory)
    if any(character in raw for character in "*?[]{}!,\n\r"):
        raise ValueError("module cache path cannot contain permission pattern characters")
    windows = PureWindowsPath(raw)
    if windows.drive:
        if (len(windows.drive) != 2 or windows.drive[1] != ":" or
                not windows.drive[0].isascii() or not windows.drive[0].isalpha() or
                not windows.is_absolute() or len(windows.parts) <= 1 or ".." in windows.parts):
            raise ValueError("module cache must be a local absolute directory below a drive root")
        normalized = "/" + windows.drive[0].lower() + "/" + "/".join(windows.parts[1:])
    else:
        posix = PurePosixPath(raw)
        if (not posix.is_absolute() or raw.startswith("//") or len(posix.parts) <= 1 or
                ".." in posix.parts or "\\" in raw):
            raise ValueError("module cache must be a local absolute directory below the filesystem root")
        normalized = str(posix)
    # Claude normalizes C:\\Users to /c/Users, and // anchors at filesystem root.
    return "Read(/" + normalized + "/**)"


def cache_read_guidance(agent, caches):
    directories = read_only_caches(agent, caches)
    if not directories:
        return ""
    for directory in directories:
        claude_read_rule(directory)  # Reject ambiguous grants before any model call.
    return ("Use git ls-files from the current workspace root to inspect tracked files. "
            "Dependency source reads and searches are allowed in the Go module cache: " +
            ", ".join(str(directory) for directory in directories) +
            ". This is a read-only file-tool grant; do not modify cached dependencies. ")


def claude_git_read_rules(workspace):
    """Permit explicit-cwd inspection only when -C names this exact worktree."""
    resolved = workspace.resolve()
    commands = []
    for path in sorted({str(resolved), resolved.as_posix()}):
        if any(character in path for character in "*?[]{}!,\n\r\"'$`"):
            raise ValueError("workspace path cannot be represented by a literal Git permission rule")
        operands = [f'"{path}"', f"'{path}'"]
        if all(character.isalnum() or character in "/\\:._-" for character in path):
            operands.append(path)
        commands.extend(f"git -C {operand} ls-files *" for operand in operands)
    return [f"{shell}({command})" for shell in ("Bash", "PowerShell") for command in commands]


def agent_command(agent, executable, model, workspace, writable_caches=(), windows_sandbox=None, scratch=None):
    if agent == "codex":
        command = [executable]
        if windows_sandbox:
            command.extend(["-c", "windows.sandbox=" + windows_sandbox])
        if scratch is not None:
            if windows_sandbox != "mxc":
                raise ValueError("local test permissions require the MXC executor")
            _, settings = local_profile(scratch, writable_caches)
            command.extend(settings)
        command.extend(["exec", "--model", model, "--json", "--ephemeral", "--cd", str(workspace)])
        if scratch is None:
            command.extend(["--sandbox", "workspace-write"])
            for directory in writable_caches:
                command.extend(["--add-dir", str(directory)])
        return command + ["-"]
    commands = ("go *", "./bin/task *", "./bin/task.exe *", "git status *", "git diff *", "git ls-files *")
    permissions = [f"{shell}({command})" for shell in ("Bash", "PowerShell") for command in commands]
    permissions.append(r"PowerShell(.\bin\task.exe *)")
    permissions.extend(claude_git_read_rules(workspace))
    permissions.extend(claude_read_rule(path) for path in read_only_caches(agent, writable_caches))
    return [executable, "--print", "--model", model, "--permission-mode", "acceptEdits",
            "--permission-prompts", "none", "--output-format", "json", "--no-session-persistence",
            "--allowedTools", *permissions]


def executor_name(args):
    if args.agent == "codex":
        if os.name == "nt":
            implementation = getattr(args, "codex_windows_sandbox", None) or "host-config"
            if getattr(args, "codex_windows_local_tests", False):
                return "codex :workspace profile / Windows MXC / localhost and owned temp"
            return "codex workspace-write / Windows " + implementation
        return "codex workspace-write"
    return "claude acceptEdits / named shell permissions"


def executor_preflight(args, workspace, log, writable_caches=(), scratch=None):
    """Check native Windows sandbox startup before spending a model call."""
    if args.agent != "codex" or os.name != "nt":
        return None
    command = [args.executable]
    implementation = getattr(args, "codex_windows_sandbox", None)
    if implementation:
        command.extend(["-c", "windows.sandbox=" + implementation])
    marker = "FORGE_EVAL_SANDBOX_READY"
    profile, script, limit = ":workspace", "Write-Output '" + marker + "'", 30
    if scratch is not None:
        try:
            permissions = check_local_permissions(args.executable, workspace, writable_caches, scratch)
        except (OSError, ValueError, subprocess.SubprocessError) as error:
            log.write_text(f"Effective permission inspection failed: {error}\n", encoding="utf-8")
            return {"ready": False, "exit_code": None, "timeout": isinstance(error, subprocess.TimeoutExpired),
                    "log": log.name, "error": str(error), "stage": "effective permission inspection"}
        profile, settings = local_profile(scratch, writable_caches)
        command.extend(settings)
        limit = 90
        # Exercise real Git fixtures, file replacement and localhost shutdown
        # tests before a model call. No task modules or test outcomes are changed.
        safety_timeout = min(args.verify_timeout, limit) + 30
        script = (scratch_prefix(scratch) +
                  f"go test -count=1 -timeout {safety_timeout}s ./cmd/dev ./cmd/forge ./internal/server; "
                  f"if ($LASTEXITCODE -ne 0) {{ exit $LASTEXITCODE }}; Write-Output '{marker}'")
    command.extend(["sandbox", "--include-managed-config", "--permission-profile", profile,
                    "-C", str(workspace), "--", "powershell.exe", "-NoProfile", "-Command", script])
    result = run(command, workspace, log, min(args.verify_timeout, limit))
    if scratch is not None:
        result["effective_permissions"] = permissions
    result["ready"] = (not result["timeout"] and result["exit_code"] == 0 and
                       marker in log.read_text(encoding="utf-8", errors="replace").splitlines())
    return result


def add_executor_option(parser):
    parser.add_argument("--codex-windows-sandbox", choices=("mxc", "elevated", "unelevated"),
                        help="Override only this evaluation's native Windows sandbox implementation; keep workspace-write")
    parser.add_argument("--codex-windows-local-tests", action="store_true",
                        help="Opt in to an MXC workspace profile with localhost access and an owned temp directory")


def validate_executor_option(parser, args):
    if args.codex_windows_sandbox and (args.agent != "codex" or os.name != "nt"):
        parser.error("--codex-windows-sandbox requires Codex on native Windows")
    if args.codex_windows_local_tests and (args.agent != "codex" or os.name != "nt" or args.codex_windows_sandbox != "mxc"):
        parser.error("--codex-windows-local-tests requires Codex on native Windows with --codex-windows-sandbox mxc")


def reported_usage(log):
    """Keep provider-reported accounting without inferring unavailable cost."""
    result = {}
    for line in log.read_text(encoding="utf-8", errors="replace").splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not isinstance(event, dict):
            continue
        for key in ("usage", "total_cost_usd", "modelUsage", "num_turns", "model", "is_error", "subtype"):
            if key in event:
                result[key] = event[key]
        if isinstance(event.get("permission_denials"), list):
            result["permission_denials_count"] = len(event["permission_denials"])
    return result


def seed_repair(workspace):
    path = workspace / "internal/modules/widget/service.go"
    source = path.read_text(encoding="utf-8")
    start = source.index("func (s *Service) Update(")
    end = source.find("\nfunc ", start + 1)
    end = len(source) if end == -1 else end
    method = source[start:end]
    replacements = {
        "if !actor.HasPermission(PermissionWrite) {": 'if actor.Subject == "blocked-subject" {',
        "if err := s.authorizeWrite(actor); err != nil {":
            'if err := s.authorizeWrite(actor); err != nil && actor.Subject == "blocked-subject" {',
    }
    for needle, replacement in replacements.items():
        if method.count(needle) == 1:
            method = method.replace(needle, replacement, 1)
            break
    else:
        raise ValueError("cannot locate the supported Update permission check")
    source = source[:start] + method + source[end:]
    path.write_text(source, encoding="utf-8", newline="\n")


def commit_fixture(workspace):
    """Give the agent a clean local baseline without an upstream remote."""
    capture(["git", "remote", "remove", "origin"], workspace)
    capture(["git", "add", "--all"], workspace)
    capture(["git", "-c", "user.name=Scaffold evaluation fixture",
                    "-c", "user.email=eval@example.invalid", "-c", "commit.gpgsign=false",
                    "-c", "core.hooksPath=", "commit", "--quiet", "--no-verify",
             "-m", "test: prepare isolated evaluation fixture"], workspace)
    return capture(["git", "rev-parse", "HEAD"], workspace)


def task_prompt(task, scratch=None):
    tests = "Add new tests. " if task.get("id") != "bootstrap" else ""
    return task["prompt"] + "\nUse AGENTS.md. " + tests + ("Do not edit or remove existing tests, migrations, "
            "dependency files, check configuration, pinned tool binaries, or agent instructions. "
            "Do not add verification tooling or configuration. Do not commit or push. "
            "Run go run ./cmd/dev check and the relevant integration tests. Report failed, blocked and unrun checks. ") + shell_guidance(scratch)


def evaluate(args, task, attempt, sha, verifier, version):
    directory = args.output / f"{args.agent}-{task['id']}-{attempt}"
    directory.mkdir(parents=True, exist_ok=False)
    workspace = directory / "workspace"
    record = {"task": task["id"], "attempt": attempt, "agent": args.agent, "model_requested": args.model,
              "client_version": version, "source_commit": sha, "status": "error",
              "human_interventions": 0, "environment": platform.platform(),
              "executor": executor_name(args),
              "verifier_sha256": hashlib.sha256(verifier.encode()).hexdigest(),
              "runner_sha256": file_digest(HERE / "run.py"),
              "verification_scope": "trusted coordinator after agent exit",
              "agent_session_checks": {"status": "unreviewed", "log": "agent.log"},
              "fixture": "initialized full preset", "cache": "shared host Go caches", "steps": []}
    try:
        scratch = prepare_scratch(args, directory)
        record["sandbox_temp"] = str(scratch) if scratch else None
        record["go_version"] = capture(["go", "version"], args.source)
        capture(["git", "clone", "--quiet", "--no-hardlinks", str(args.source), str(workspace)],
                args.source, args.verify_timeout)
        capture(["git", "checkout", "--quiet", "--detach", sha], workspace, args.verify_timeout)
        initialize = run(["go", "run", "./cmd/forge", "init", "-yes", "-module", "example.com/eval/app"],
                         workspace, directory / "initialize.log", args.verify_timeout)
        record["steps"].append(initialize)
        if initialize["exit_code"] != 0:
            record["status"] = "preparation_failed"
            return record
        prep = run(["go", "run", "./cmd/dev", "bootstrap"], workspace, directory / "prepare.log", args.verify_timeout)
        record["steps"].append(prep)
        if prep["exit_code"] != 0:
            record["status"] = "preparation_failed"
            return record
        expected_tools = None
        if task["id"] == "bootstrap":
            expected_tools = {name: digest for name, digest in protected_files(workspace).items()
                              if name.startswith("bin/")}
            if not expected_tools:
                raise ValueError("trusted bootstrap produced no tools")
            # Leave the agent a checkout without tools, while retaining a trusted
            # reference so the grader cannot silently bootstrap on its behalf.
            (workspace / "bin").rename(directory / "bootstrap-reference")
        if task["id"] == "second_resource":
            for number, command in enumerate((["go", "run", "./cmd/forge", "add", "resource", "Note"],
                                              [str(workspace / "bin" / ("task.exe" if os.name == "nt" else "task")), "generate"])):
                step = run(command, workspace, directory / f"seed-{number}.log", args.verify_timeout)
                record["steps"].append(step)
                if step["exit_code"] != 0:
                    record["status"] = "preparation_failed"
                    return record
        if task["id"] == "repair":
            seed_repair(workspace)
        record["fixture_commit"] = commit_fixture(workspace)
        record["fixture_tree"] = capture(["git", "rev-parse", "HEAD^{tree}"], workspace)
        baseline_migrations = {path.name: path.read_bytes() for path in (workspace / "migrations").glob("*.sql")}
        record["baseline_migrations_sha256"] = {name: hashlib.sha256(data).hexdigest()
                                                for name, data in baseline_migrations.items()}
        frozen = protected_files(workspace)
        application_before = application_snapshot(workspace) if task["id"] == "bootstrap" else None
        caches = [Path(value).resolve() for value in capture(["go", "env", "GOCACHE", "GOMODCACHE"], workspace).splitlines()]
        record["writable_caches"] = [str(path) for path in caches] if args.agent == "codex" else []
        record["read_only_caches"] = [str(path) for path in read_only_caches(args.agent, caches)]
        prompt = task_prompt(task, scratch) + cache_read_guidance(args.agent, caches)
        record["prompt_sha256"] = hashlib.sha256(prompt.encode()).hexdigest()
        (directory / "prompt.txt").write_text(prompt, encoding="utf-8")
        if scratch is not None:
            record["permission_profile"] = local_profile(scratch, caches)[0]
            record["network_allowlist"] = ["localhost", "127.0.0.1"]
        preflight = executor_preflight(args, workspace, directory / "sandbox-preflight.log", caches, scratch)
        if preflight is not None:
            record["steps"].append(preflight)
            if not preflight["ready"]:
                record["status"] = "preparation_failed"
                record["error"] = "native Windows sandbox preflight failed; review sandbox-preflight.log before retrying"
                return record
        argv = agent_command(args.agent, args.executable, args.model, workspace, caches,
                             getattr(args, "codex_windows_sandbox", None), scratch)
        agent = run(argv, workspace, directory / "agent.log", args.timeout, prompt=prompt)
        record["steps"].append(agent)
        record["reported_usage"] = reported_usage(directory / "agent.log")
        record["integrity_changes"] = integrity_changes(workspace, frozen)
        record["integrity_changes"].extend(repository_changes(workspace, record["fixture_commit"]))
        record["new_test_files"] = sorted(name for name in protected_files(workspace)
                                          if name.endswith("_test.go") and name not in frozen)
        record["changed_files"] = capture(["git", "status", "--short"], workspace).splitlines()
        if agent["timeout"]:
            record["status"] = "timeout"
            return record
        if agent["exit_code"] != 0 or record["reported_usage"].get("is_error"):
            record["status"] = "agent_error"
            return record
        if record["reported_usage"].get("permission_denials_count", 0):
            record["status"] = "permission_denied"
            return record
        if record["integrity_changes"]:
            record["status"] = "integrity_failed"
            return record
        if application_before is not None and application_snapshot(workspace) != application_before:
            record["status"] = "integrity_failed"
            record["error"] = "bootstrap changed application sources"
            return record
        if expected_tools is not None:
            record["bootstrap_artifact_changes"] = bootstrap_changes(workspace, expected_tools)
            if record["bootstrap_artifact_changes"]:
                record["status"] = "task_failed"
                record["error"] = "agent did not reproduce the pinned bootstrap artifacts before verification"
                return record
        if task["id"] != "bootstrap" and not record["new_test_files"]:
            record["status"] = "task_failed"
            record["error"] = "task requires an added regression test"
            return record
        check = run(["go", "run", "./cmd/dev", "check"], workspace, directory / "check.log", args.verify_timeout)
        record["steps"].append(check)
        if check["exit_code"] != 0:
            record["status"] = "verification_failed"
            return record
        if task["id"] != "bootstrap":
            integration = run(go_test_command(args.verify_timeout),
                              workspace, directory / "integration.log", args.verify_timeout)
            record["steps"].append(integration)
            if integration["exit_code"] != 0:
                record["status"] = "verification_failed"
                return record
            target = workspace / "internal/evalverify"
            write_verifier(target, verifier, baseline_migrations if task["id"] == "field" else None)
            environment = dict(os.environ, FORGE_EVAL_TASK=task["id"])
            acceptance = run(go_test_command(args.verify_timeout, "./internal/evalverify"),
                             workspace, directory / "acceptance.log", args.verify_timeout, env=environment)
            record["steps"].append(acceptance)
            if acceptance["exit_code"] != 0:
                record["status"] = "verification_failed"
                return record
        record["status"] = "passed"
        return record
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        record["error"] = str(error)
        if getattr(error, "stderr", None):
            record["error_output"] = error.stderr
        return record
    finally:
        (directory / "result.json").write_text(json.dumps(record, indent=2) + "\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, default=HERE.parent)
    parser.add_argument("--ref", default="HEAD")
    parser.add_argument("--agent", required=True, choices=("claude", "codex"))
    parser.add_argument("--model", required=True, help="Record an explicit model identifier, preferably a dated version")
    parser.add_argument("--executable", help="Path to the selected agent CLI")
    add_executor_option(parser)
    parser.add_argument("--task", default="all")
    parser.add_argument("--attempts", type=int, default=1)
    parser.add_argument("--timeout", type=int, default=900, help="Agent wall-clock limit in seconds")
    parser.add_argument("--verify-timeout", type=int, default=600)
    parser.add_argument("--output", type=Path, required=True, help="New results directory outside the source tree")
    args = parser.parse_args()
    validate_executor_option(parser, args)
    args.source, args.output = args.source.resolve(), args.output.resolve()
    if args.output == args.source or args.source in args.output.parents:
        parser.error("output must be outside the source tree")
    if min(args.attempts, args.timeout, args.verify_timeout) < 1:
        parser.error("attempts and timeouts must be positive")
    args.executable = args.executable or shutil.which(args.agent)
    if not args.executable:
        parser.error(f"{args.agent} is not on PATH")
    tasks = json.loads((HERE / "tasks.json").read_text(encoding="utf-8"))
    selected = [task for task in tasks if args.task in ("all", task["id"])]
    if not selected:
        parser.error("unknown task")
    sha = capture(["git", "rev-parse", f"{args.ref}^{{commit}}"], args.source)
    version = capture([args.executable, "--version"], args.source)
    verifier = (HERE / "acceptance_test.go.txt").read_text(encoding="utf-8")
    args.output.mkdir(parents=True, exist_ok=False)
    records = []
    for task in selected:
        for attempt in range(1, args.attempts + 1):
            result = evaluate(args, task, attempt, sha, verifier, version)
            records.append(result)
            (args.output / "results.json").write_text(json.dumps(records, indent=2) + "\n", encoding="utf-8")
            print(f"{task['id']} attempt {attempt}: {result['status']}", flush=True)
    return 0 if all(record["status"] == "passed" for record in records) else 1


if __name__ == "__main__":
    sys.exit(main())
