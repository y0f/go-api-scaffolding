"""Tests for evaluation bookkeeping and integrity checks; no model calls."""

import importlib.util
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest import mock


spec = importlib.util.spec_from_file_location("runner", Path(__file__).with_name("run.py"))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class EvaluationTests(unittest.TestCase):
    def test_deleted_or_changed_checks_fail_integrity(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            original = root / "service_test.go"
            original.write_text("original test", encoding="utf-8")
            instructions = root / "GEMINI.md"
            instructions.write_text("@./AGENTS.md", encoding="utf-8")
            migration = root / "migrations/00001_initial.sql"
            migration.parent.mkdir()
            migration.write_text("original migration", encoding="utf-8")
            before = runner.protected_files(root)
            original.write_text("weaker test", encoding="utf-8")
            instructions.unlink()
            migration.unlink()
            self.assertEqual(runner.integrity_changes(root, before),
                             ["GEMINI.md", "migrations/00001_initial.sql", "service_test.go"])

    def test_new_tests_and_migrations_are_allowed(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "existing_test.go").write_text("original", encoding="utf-8")
            before = runner.protected_files(root)
            (root / "regression_test.go").write_text("new test", encoding="utf-8")
            (root / "service.go").write_text("implementation", encoding="utf-8")
            (root / "migrations").mkdir()
            (root / "migrations/00002_feature.sql").write_text("new migration", encoding="utf-8")
            self.assertEqual(runner.integrity_changes(root, before), [])

    def test_added_verification_sources_and_replaced_tools_fail_integrity(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "bin").mkdir()
            (root / "bin/task").write_text("trusted executable", encoding="utf-8")
            before = runner.protected_files(root)
            for name in ("cmd/dev/extra.go", "internal/testutil/extra.go", ".codex/config.toml", "go.work"):
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("new verification override", encoding="utf-8")
            (root / "bin/task").write_text("replacement executable", encoding="utf-8")
            self.assertEqual(runner.integrity_changes(root, before),
                             [".codex/config.toml", "bin/task", "cmd/dev/extra.go", "go.work", "internal/testutil/extra.go"])

    def test_metadata_capture_has_a_deadline_and_preserves_output(self):
        with tempfile.TemporaryDirectory() as temporary:
            with self.assertRaises(subprocess.TimeoutExpired) as caught:
                runner.capture([sys.executable, "-c", "import time; print('started', flush=True); time.sleep(20)"],
                               temporary, timeout=0.3)
            self.assertIn("started", caught.exception.output)

    def test_metadata_capture_keeps_stderr_out_of_machine_output(self):
        output = runner.capture([sys.executable, "-c", "import sys; print('warning',file=sys.stderr); print(' value')"],
                                Path.cwd(), trim=False)
        self.assertEqual(output, " value\n")

    def test_timeout_stops_child_processes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            marker = root / "orphan.txt"
            child = "import pathlib,time; time.sleep(2); pathlib.Path('orphan.txt').write_text('orphan')"
            parent = f"import subprocess,sys,time; subprocess.Popen([sys.executable, '-c', {child!r}]); time.sleep(20)"
            result = runner.run([sys.executable, "-c", parent], root, root / "timeout.log", timeout=0.3)
            self.assertTrue(result["timeout"])
            time.sleep(2.1)
            self.assertFalse(marker.exists(), "a command descendant survived the timeout")

    def test_go_test_safety_timeout_follows_supervisor_deadline(self):
        for timeout in (1, 60, 600, 1200):
            for tags in (None, "integration"):
                command = runner.go_test_command(timeout, "./internal/evalverify", tags=tags)
                safety = int(command[command.index("-timeout") + 1].removesuffix("s"))
                self.assertGreater(safety, timeout)
                self.assertEqual(command[-1], "./internal/evalverify")

    def test_bootstrap_requires_agent_artifacts_and_success_before_grading(self):
        for outcome, expected in (("no_op", "task_failed"), ("permission", "permission_denied"),
                                  ("provider_error", "agent_error"), ("prepared", "passed")):
            with self.subTest(outcome=outcome), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                args = SimpleNamespace(output=root / "results", source=root, agent="claude", model="test-model",
                                       executable="test-client", timeout=1, verify_timeout=1)
                checks = []

                def capture(argv, cwd, timeout=runner.CAPTURE_TIMEOUT):
                    if argv[0:2] == ["git", "clone"]:
                        workspace = Path(argv[-1])
                        workspace.mkdir(parents=True)
                        (workspace / "migrations").mkdir()
                    if argv[0:2] == ["go", "env"]:
                        return str(root / "cache") + "\n" + str(root / "modules")
                    if argv[0:2] == ["git", "rev-parse"]:
                        return "fixture"
                    return ""

                def run(argv, cwd, log, timeout, prompt=None, env=None):
                    event = {}
                    if argv[-1] == "bootstrap" or (argv[0] == "test-client" and outcome == "prepared"):
                        (cwd / "bin").mkdir()
                        (cwd / "bin/task").write_text("trusted executable", encoding="utf-8")
                    if argv[0] == "test-client":
                        if outcome == "permission":
                            event = {"permission_denials": [{"tool_name": "Bash"}]}
                        if outcome == "provider_error":
                            event = {"is_error": True, "subtype": "error_max_turns"}
                    if argv[-1] == "check":
                        checks.append(argv)
                    log.write_text(json.dumps(event), encoding="utf-8")
                    return {"command": argv, "exit_code": 0, "timeout": False, "seconds": 0, "log": log.name}

                with mock.patch.object(runner, "capture", side_effect=capture), \
                        mock.patch.object(runner, "run", side_effect=run), \
                        mock.patch.object(runner, "commit_fixture", return_value="fixture"):
                    result = runner.evaluate(args, {"id": "bootstrap", "prompt": "Bootstrap."}, 1, "source", "", "test-version")
                self.assertEqual(result["status"], expected, result)
                self.assertEqual(len(checks), 1 if expected == "passed" else 0)
                if expected == "passed":
                    self.assertEqual(result["verification_scope"], "trusted coordinator after agent exit")
                    self.assertEqual(result["agent_session_checks"]["status"], "unreviewed")
                    prompt = (args.output / "claude-bootstrap-1/prompt.txt").read_text(encoding="utf-8")
                    self.assertEqual(result["prompt_sha256"], hashlib.sha256(prompt.encode()).hexdigest())

    def test_agent_commands_keep_permissions_enabled(self):
        for agent in ("claude", "codex"):
            command = runner.agent_command(agent, agent, "explicit-model", Path("workspace"))
            self.assertIn("explicit-model", command)
            self.assertFalse(any("bypass" in argument for argument in command))

    def test_claude_allows_named_native_powershell_commands(self):
        command = runner.agent_command("claude", "claude", "model", Path("workspace"))
        for rule in ("PowerShell(go *)", "PowerShell(./bin/task.exe *)", r"PowerShell(.\bin\task.exe *)",
                     "PowerShell(git status *)", "PowerShell(git diff *)"):
            self.assertIn(rule, command)
        self.assertNotIn("PowerShell", command)
        self.assertNotIn("PowerShell(*)", command)
        self.assertIn("--permission-prompts", command)
        self.assertIn("none", command)

    def test_codex_windows_override_preserves_workspace_sandbox(self):
        command = runner.agent_command("codex", "codex", "model", Path("workspace"), windows_sandbox="mxc")
        self.assertEqual(command[:3], ["codex", "-c", "windows.sandbox=mxc"])
        self.assertEqual(command[command.index("--sandbox") + 1], "workspace-write")
        standard = runner.agent_command("codex", "codex", "model", Path("workspace"))
        self.assertFalse(any(argument.startswith("windows.sandbox=") for argument in standard))

    def test_native_sandbox_preflight_is_bounded_and_does_not_call_a_model(self):
        with tempfile.TemporaryDirectory() as temporary:
            workspace = Path(temporary)
            log = workspace / "preflight.log"
            args = SimpleNamespace(agent="codex", executable="codex", codex_windows_sandbox="mxc", verify_timeout=600)

            def probe(command, cwd, output, timeout):
                self.assertIn("sandbox", command)
                self.assertNotIn("exec", command)
                self.assertNotIn("--model", command)
                self.assertIn("windows.sandbox=mxc", command)
                self.assertIn(":workspace", command)
                self.assertEqual(timeout, 30)
                output.write_text("FORGE_EVAL_SANDBOX_READY\n", encoding="utf-8")
                return {"exit_code": 0, "timeout": False}

            with mock.patch.object(runner.os, "name", "nt"), mock.patch.object(runner, "run", side_effect=probe):
                self.assertTrue(runner.executor_preflight(args, workspace, log)["ready"])
            with mock.patch.object(runner.os, "name", "posix"), mock.patch.object(runner, "run") as launch:
                self.assertIsNone(runner.executor_preflight(args, workspace, log))
                launch.assert_not_called()

    def test_local_test_profile_is_explicit_and_keeps_narrow_grants(self):
        scratch = Path("owned-scratch")
        name, settings = runner.local_profile(scratch, [Path("go-cache")])
        command = runner.agent_command("codex", "codex", "model", Path("workspace"), [Path("go-cache")], "mxc", scratch)
        self.assertNotIn("--sandbox", command, "legacy flags override named permissions")
        self.assertIn(f'default_permissions="{name}"', command)
        self.assertIn(f'permissions.{name}.extends=":workspace"', settings)
        self.assertIn("features.network_proxy=true", settings)
        self.assertIn(f'permissions.{name}.network.domains={{localhost="allow","127.0.0.1"="allow"}}', settings)
        self.assertFalse(any("bypass" in argument or "danger-full-access" in argument for argument in command))
        self.assertNotEqual(name, runner.local_profile(Path("other-worker-scratch"))[0])
        with self.assertRaises(ValueError):
            runner.agent_command("codex", "codex", "model", Path("workspace"), scratch=scratch)

    def test_local_test_option_requires_explicit_native_mxc(self):
        parser = argparse.ArgumentParser()
        runner.add_executor_option(parser)
        for agent, platform, implementation in (("claude", "nt", "mxc"), ("codex", "posix", "mxc"),
                                                 ("codex", "nt", None), ("codex", "nt", "unelevated")):
            args = SimpleNamespace(agent=agent, codex_windows_sandbox=implementation, codex_windows_local_tests=True)
            with mock.patch.object(runner.os, "name", platform), mock.patch.object(parser, "error", side_effect=ValueError):
                with self.assertRaises(ValueError):
                    runner.validate_executor_option(parser, args)

    def test_local_test_scratch_and_prompt_remain_outside_application(self):
        with tempfile.TemporaryDirectory(prefix="eval-'quoted-$()-") as temporary:
            output = Path(temporary)
            workspace = output / "workspace"
            workspace.mkdir()
            self.assertIsNone(runner.prepare_scratch(SimpleNamespace(), output))
            scratch = runner.prepare_scratch(SimpleNamespace(codex_windows_local_tests=True), output)
            self.assertFalse(scratch.is_relative_to(workspace))
            prompt = runner.task_prompt({"id": "bootstrap", "prompt": "Bootstrap."}, scratch)
            self.assertIn(str(scratch).replace("'", "''"), prompt)
            self.assertIn(runner.scratch_prefix(scratch), prompt)
            self.assertIn("only exception", prompt)
            self.assertNotEqual(prompt, runner.task_prompt({"id": "bootstrap", "prompt": "Bootstrap."}))

    def test_effective_permission_inspection_rejects_broader_or_unknown_scopes(self):
        with tempfile.TemporaryDirectory() as temporary:
            parent = Path(temporary)
            workspace, scratch, cache = (parent / name for name in ("workspace", "scratch", "cache"))
            entries = "".join(f'<entry access="write"><path>{path}</path></entry>' for path in (workspace, scratch, cache))

            def context(kind="restricted", rules=entries):
                return json.dumps([{"content": [{"text": '<environment_context><filesystem><permission_profile>'
                        f'<file_system type="{kind}">{rules}</file_system>'
                        '</permission_profile></filesystem></environment_context>'}]}])

            with mock.patch.object(runner, "capture", return_value=context()):
                result = runner.check_local_permissions("codex", workspace, [cache], scratch)
                self.assertEqual(result["filesystem"], "restricted")
            for output in (context("unrestricted"), context(rules=entries + '<entry access="write"><special>:root</special></entry>'),
                           context(rules=entries + f'<entry access="write"><path>{parent}</path></entry>'), "[]"):
                with mock.patch.object(runner, "capture", return_value=output):
                    with self.assertRaises(ValueError):
                        runner.check_local_permissions("codex", workspace, [cache], scratch)

    def test_local_preflight_runs_real_capability_tests_before_model_call(self):
        with tempfile.TemporaryDirectory() as temporary:
            workspace = Path(temporary)
            scratch = workspace / "scratch"
            args = SimpleNamespace(agent="codex", executable="codex", codex_windows_sandbox="mxc", verify_timeout=600)

            def probe(command, cwd, output, timeout):
                self.assertIn("sandbox", command)
                self.assertNotIn("exec", command)
                self.assertEqual(timeout, 90)
                self.assertIn(runner.scratch_prefix(scratch), command[-1])
                self.assertIn("./cmd/dev ./cmd/forge ./internal/server", command[-1])
                self.assertIn("-timeout 120s", command[-1])
                output.write_text("FORGE_EVAL_SANDBOX_READY\n", encoding="utf-8")
                return {"exit_code": 0, "timeout": False}

            with mock.patch.object(runner.os, "name", "nt"), \
                    mock.patch.object(runner, "check_local_permissions", return_value={"filesystem": "restricted"}) as inspect, \
                    mock.patch.object(runner, "run", side_effect=probe):
                result = runner.executor_preflight(args, workspace, workspace / "probe.log", (), scratch)
                self.assertTrue(result["ready"])
                inspect.assert_called_once()
            with mock.patch.object(runner.os, "name", "nt"), \
                    mock.patch.object(runner, "check_local_permissions", side_effect=ValueError("unexpected scope")), \
                    mock.patch.object(runner, "run") as launch:
                result = runner.executor_preflight(args, workspace, workspace / "blocked.log", (), scratch)
                self.assertFalse(result["ready"])
                self.assertEqual(result["stage"], "effective permission inspection")
                launch.assert_not_called()

    def test_bootstrap_snapshot_detects_added_application_code(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            before = runner.application_snapshot(root)
            (root / "new.go").write_text("package example", encoding="utf-8")
            self.assertNotEqual(runner.application_snapshot(root), before)

    def test_usage_is_reported_not_estimated(self):
        with tempfile.TemporaryDirectory() as temporary:
            log = Path(temporary) / "agent.log"
            log.write_text('noise\n{"type":"turn.completed","usage":{"input_tokens":42}}\n', encoding="utf-8")
            self.assertEqual(runner.reported_usage(log), {"usage": {"input_tokens": 42}})

    def test_seed_changes_only_update_permission(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            target = root / "internal/modules/widget/service.go"
            target.parent.mkdir(parents=True)
            target.write_text("func Create() { if !actor.HasPermission(PermissionWrite) {} }\n"
                              "func (s *Service) Update() { if !actor.HasPermission(PermissionWrite) {} }\n",
                              encoding="utf-8")
            runner.seed_repair(root)
            source = target.read_text(encoding="utf-8")
            self.assertEqual(source.count("!actor.HasPermission(PermissionWrite)"), 1)
            self.assertEqual(source.count('actor.Subject == "blocked-subject"'), 1)

    def test_seed_handles_shared_authorization_helper(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            target = root / "internal/modules/widget/service.go"
            target.parent.mkdir(parents=True)
            source = """func (s *Service) Create() {
    if err := s.authorizeWrite(actor); err != nil { return err }
}
func (s *Service) Update() {
    if err := s.authorizeWrite(actor); err != nil { return err }
    return s.repo.Update(ctx, id, in)
}
func (s *Service) Delete() {
    if err := s.authorizeWrite(actor); err != nil { return err }
}
"""
            target.write_text(source, encoding="utf-8")
            runner.seed_repair(root)
            mutated = target.read_text(encoding="utf-8")
            self.assertEqual(mutated.count("if err := s.authorizeWrite(actor); err != nil {"), 2)
            self.assertIn('if err := s.authorizeWrite(actor); err != nil && actor.Subject == "blocked-subject" {', mutated)
            self.assertEqual(mutated[:mutated.index("func (s *Service) Update(")],
                             source[:source.index("func (s *Service) Update(")])

    def test_seed_does_not_modify_a_later_method(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            target = root / "internal/modules/widget/service.go"
            target.parent.mkdir(parents=True)
            source = ("func (s *Service) Update() { return nil }\n"
                      "func (s *Service) Delete() { if !actor.HasPermission(PermissionWrite) {} }\n")
            target.write_text(source, encoding="utf-8")
            with self.assertRaises(ValueError):
                runner.seed_repair(root)
            self.assertEqual(target.read_text(encoding="utf-8"), source)

    def test_fixture_has_clean_local_commit_and_no_remote(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            subprocess.run(["git", "init", "--quiet", str(root)], check=True)
            subprocess.run(["git", "remote", "add", "origin", "https://example.invalid/upstream"], cwd=root, check=True)
            (root / "fixture.txt").write_text("prepared", encoding="utf-8")
            commit = runner.commit_fixture(root)
            self.assertEqual(len(commit), 40)
            self.assertEqual(runner.capture(["git", "status", "--short"], root), "")
            self.assertEqual(runner.capture(["git", "remote"], root), "")
            self.assertEqual(runner.capture(["git", "show", "HEAD:fixture.txt"], root), "prepared")
            author = runner.capture(["git", "log", "-1", "--format=%an <%ae>"], root)
            self.assertEqual(author, "Scaffold evaluation fixture <eval@example.invalid>")
            self.assertEqual(runner.repository_changes(root, commit), [])
            runner.capture(["git", "remote", "add", "unexpected", "https://example.invalid/remote"], root)
            self.assertEqual(runner.repository_changes(root, commit), ["agent added a remote"])
            runner.capture(["git", "remote", "remove", "unexpected"], root)
            runner.capture(["git", "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
                            "-c", "commit.gpgsign=false", "-c", "core.hooksPath=", "commit", "--allow-empty",
                            "--quiet", "-m", "unexpected agent commit"], root)
            self.assertEqual(runner.repository_changes(root, commit), ["agent committed changes"])

    def test_verifier_uses_frozen_migrations_and_current_module(self):
        with tempfile.TemporaryDirectory() as temporary:
            target = Path(temporary) / "evalverify"
            runner.write_verifier(target, 'import "github.com/y0f/gophling/internal/auth"',
                                  {"00001_widgets.sql": b"frozen before agent edits"})
            self.assertIn('"example.com/eval/app/internal/auth"', (target / "acceptance_test.go").read_text())
            self.assertEqual((target / "baseline/00001_widgets.sql").read_bytes(), b"frozen before agent edits")

    def test_prompt_includes_the_integrity_instructions(self):
        prompt = runner.task_prompt({"prompt": "Add the feature."})
        self.assertTrue(prompt.startswith("Add the feature."))
        self.assertIn("Do not commit or push.", prompt)
        self.assertIn("Do not edit or remove existing tests", prompt)
        bootstrap = runner.task_prompt({"id": "bootstrap", "prompt": "Set up the checkout."})
        self.assertNotIn("Add new tests", bootstrap)
        self.assertIn("Run each shell command separately", bootstrap)
        self.assertIn("Do not prepend cd", bootstrap)


if __name__ == "__main__":
    unittest.main()
