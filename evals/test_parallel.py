"""Exercise concurrency and worktree ownership without calling a provider."""

from pathlib import Path
import subprocess
import tempfile
import threading
import unittest

import parallel


class ParallelTests(unittest.TestCase):
    def fixture(self, root):
        subprocess.run(["git", "init", "--quiet", str(root)], check=True)
        subprocess.run(["git", "remote", "add", "origin", "https://example.invalid/source"], cwd=root, check=True)
        module = root / "internal/modules/note"
        module.mkdir(parents=True)
        (module / "note.go").write_text("original implementation\n", encoding="utf-8")
        (module / "service_test.go").write_text("original test\n", encoding="utf-8")
        sha = parallel.runner.commit_fixture(root)
        return sha, parallel.runner.protected_files(root)

    def test_parallel_workers_overlap(self):
        rendezvous = threading.Barrier(2)

        def work(name):
            rendezvous.wait(timeout=5)
            return {"resource": name, "status": "passed"}

        results = parallel.run_jobs([(name, lambda name=name: work(name)) for name in ("note", "label")])
        self.assertEqual([result["status"] for result in results], ["passed", "passed"])

    def test_failure_preserves_other_worker_in_both_modes(self):
        def fail():
            raise OSError("client could not start")

        for serial in (False, True):
            results = parallel.run_jobs([
                ("note", fail), ("label", lambda: {"resource": "label", "status": "passed"})], serial=serial)
            self.assertEqual([result["status"] for result in results], ["error", "passed"])
            self.assertIn("could not start", results[0]["error"])

    def test_owned_changes_and_new_tests_integrate(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "worker"
            root.mkdir()
            sha, frozen = self.fixture(root)
            module = root / "internal/modules/note"
            (module / "note.go").write_text("repaired implementation\n", encoding="utf-8")
            (module / "validation_test.go").write_text("new regression tests\n", encoding="utf-8")
            paths, violations, added = parallel.review_changes(root, "note", frozen, sha)
            self.assertEqual(violations, [])
            self.assertEqual(added, ["internal/modules/note/validation_test.go"])
            target = Path(temporary) / "coordinator"
            target.mkdir()
            parallel.integrate_changes(root, target, paths)
            self.assertEqual((target / "internal/modules/note/note.go").read_text(), "repaired implementation\n")
            self.assertFalse((target / "internal/modules/note/service_test.go").exists())

    def test_rejects_foreign_staged_files_and_modified_existing_tests(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            sha, frozen = self.fixture(root)
            test = root / "internal/modules/note/service_test.go"
            test.unlink()
            foreign = root / "internal/modules/label/label.go"
            foreign.parent.mkdir()
            foreign.write_text("other worker's code", encoding="utf-8")
            subprocess.run(["git", "add", "--all"], cwd=root, check=True)
            _, violations, _ = parallel.review_changes(root, "note", frozen, sha)
            self.assertIn("internal/modules/label/label.go", violations)
            self.assertIn("internal/modules/note/service_test.go", violations)

    def test_integration_rejects_paths_outside_workspace(self):
        with tempfile.TemporaryDirectory() as temporary:
            parent = Path(temporary)
            worker, coordinator = parent / "worker", parent / "coordinator"
            worker.mkdir()
            coordinator.mkdir()
            secret = parent / "outside.txt"
            secret.write_text("outside", encoding="utf-8")
            with self.assertRaises(ValueError):
                parallel.integrate_changes(worker, coordinator, ["../outside.txt"])
            self.assertEqual(secret.read_text(), "outside")

    def test_seed_keeps_empty_name_check_and_fails_on_unknown_template(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            domain = root / "internal/modules/note/note.go"
            domain.parent.mkdir(parents=True)
            domain.write_text('if strings.TrimSpace(in.Name) == "" || tooLong { return invalid }', encoding="utf-8")
            parallel.seed_validation(root, "note")
            self.assertIn('(strings.TrimSpace(in.Name) == "" && in.Name == "")', domain.read_text())
            domain.write_text("unsupported implementation", encoding="utf-8")
            with self.assertRaises(ValueError):
                parallel.seed_validation(root, "note")
            self.assertEqual(domain.read_text(), "unsupported implementation")


if __name__ == "__main__":
    unittest.main()
