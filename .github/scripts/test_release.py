import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import release


SHA = "a" * 40


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.dist = Path(self.temporary.name) / "dist"
        self.dist.mkdir()
        self.files = {"custom-service_linux_amd64.tar.gz": b"archive",
                      "custom-service_linux_amd64.tar.gz.sbom.json": b"{}"}
        for name, content in self.files.items():
            (self.dist / name).write_bytes(content)
        lines = [f"{hashlib.sha256(content).hexdigest()}  {name}\n"
                 for name, content in self.files.items()]
        (self.dist / release.CHECKSUMS).write_text("".join(lines), encoding="utf-8")
        (self.dist / release.BUNDLE).write_bytes(b"signature test fixture")
        self.names = set(self.files) | {release.CHECKSUMS, release.BUNDLE}
        artifacts = [{"name": name, "type": "SBOM" if name.endswith("json") else "Archive"}
                     for name in self.files]
        artifacts += [{"name": release.CHECKSUMS, "type": "Checksum"},
                      {"name": release.BUNDLE, "type": "Signature"},
                      {"name": "api", "type": "Binary"}]
        (self.dist / "artifacts.json").write_text(json.dumps(artifacts), encoding="utf-8")
        self.assets = {name: {"name": name, "id": i, "state": "uploaded",
                             "size": (self.dist / name).stat().st_size,
                             "digest": "sha256:" + release.digest(self.dist / name)}
                       for i, name in enumerate(sorted(self.names), 1)}
        self.client = release.Release("example/custom-service", "v1.2.3", SHA)

    def test_local_manifest_accepts_custom_project_names(self):
        self.assertEqual(set(release.local_inventory(self.dist)), self.names)

    def test_tampered_missing_and_unchecksummed_payloads_fail(self):
        archive = next(iter(self.files))
        (self.dist / archive).write_bytes(b"tampered")
        with self.assertRaisesRegex(ValueError, "checksum mismatch"):
            release.local_inventory(self.dist)
        (self.dist / archive).unlink()
        with self.assertRaisesRegex(ValueError, "missing artifacts"):
            release.local_inventory(self.dist)
        (self.dist / archive).write_bytes(self.files[archive])
        with (self.dist / release.CHECKSUMS).open("a") as output:
            output.write(f"{'0' * 64}  surprise.zip\n")
        with self.assertRaisesRegex(ValueError, "exactly the release payload"):
            release.local_inventory(self.dist)

    def test_rejects_duplicate_checksums_and_unsafe_names(self):
        original = (self.dist / release.CHECKSUMS).read_text()
        (self.dist / release.CHECKSUMS).write_text(original + original)
        with self.assertRaisesRegex(ValueError, "duplicate checksum"):
            release.local_inventory(self.dist)
        for name in ("../escape", "sub/file", "sub\\file", "C:escape", "bad\nname"):
            with self.subTest(name=name), self.assertRaisesRegex(ValueError, "unsafe"):
                release.inventory([{"name": name}])
        with self.assertRaisesRegex(ValueError, "duplicate release asset"):
            release.inventory([{"name": "one"}, {"name": "one"}])

    def test_empty_release_payload_is_rejected(self):
        (self.dist / release.CHECKSUMS).write_text("")
        with self.assertRaisesRegex(ValueError, "release payload"):
            release.verify_content(self.dist, {release.CHECKSUMS, release.BUNDLE})

    def test_unsigned_snapshot_mode_does_not_require_a_fake_signature(self):
        artifacts = json.loads((self.dist / "artifacts.json").read_text())
        artifacts = [a for a in artifacts if a["type"] != "Signature"]
        (self.dist / "artifacts.json").write_text(json.dumps(artifacts))
        (self.dist / release.BUNDLE).unlink()
        self.assertEqual(set(release.local_inventory(self.dist, signed=False)),
                         self.names - {release.BUNDLE})
        with self.assertRaisesRegex(ValueError, "signature bundle missing"):
            release.local_inventory(self.dist)

    def test_unsigned_flag_cannot_bypass_publication_checks(self):
        with patch("sys.argv", ["release.py", "publish", "--unsigned-snapshot"]), \
                patch("sys.stderr"), patch("release.Release") as client:
            with self.assertRaises(SystemExit) as error:
                release.main()
            self.assertEqual(error.exception.code, 2)
            client.assert_not_called()

    def test_published_releases_are_never_rebuilt(self):
        with patch.object(self.client, "check_tag"), patch.object(self.client, "find") as find:
            for value in (None, {"draft": True}):
                find.return_value = value
                self.assertEqual(self.client.plan(), "publish")
            find.return_value = {"draft": False, "immutable": True}
            self.assertEqual(self.client.plan(), "verify")
            find.return_value = {"draft": False, "immutable": False}
            with self.assertRaisesRegex(ValueError, "do not overwrite"):
                self.client.plan()

    def test_moved_tag_is_rejected_and_annotated_tag_is_resolved(self):
        with patch("release.api") as api:
            api.return_value = {"object": {"type": "commit", "sha": "b" * 40}}
            with self.assertRaisesRegex(ValueError, "no longer matches"):
                self.client.check_tag()
            api.side_effect = [{"object": {"type": "tag", "sha": "c" * 40}},
                               {"object": {"type": "commit", "sha": SHA}}]
            self.client.check_tag()

    def test_remote_extra_and_changed_assets_fail_before_signing(self):
        local = release.local_inventory(self.dist)
        remote = dict(self.assets)
        remote["extra.zip"] = {"name": "extra.zip"}
        with patch.object(self.client, "assets", return_value=remote), patch("release.command") as command:
            with self.assertRaisesRegex(ValueError, "inventory differs"):
                self.client.verify_uploaded({}, Path(self.temporary.name), local, True)
            command.assert_not_called()

    def test_downloaded_bytes_and_signature_are_both_verified(self):
        directory = Path(self.temporary.name) / "download"
        directory.mkdir()

        def download(*args):
            self.assertEqual(args[:3], ("gh", "release", "download"))
            for name in self.names:
                shutil.copyfile(self.dist / name, directory / name)

        with patch.object(self.client, "assets", return_value=self.assets), \
                patch("release.command", side_effect=download), \
                patch.object(self.client, "verify_signatures") as verify:
            self.client.verify_uploaded({}, directory, release.local_inventory(self.dist), True)
            verify.assert_called_once()
        altered = {name: "0" * 64 for name in self.names}
        with patch.object(self.client, "assets", return_value=self.assets), \
                patch("release.command"), patch.object(self.client, "verify_signatures") as verify:
            with self.assertRaisesRegex(ValueError, "differs from local"):
                self.client.verify_uploaded({}, directory, altered, True)
            verify.assert_not_called()

    def finish_with(self, verified=True, changed=False, existing=False):
        draft = {"id": 42, "draft": not existing, "immutable": existing}
        published = {"id": 42, "draft": False, "immutable": True}
        after_download = {name: {**asset, "download_count": 1} for name, asset in self.assets.items()}
        if changed:
            after_download[next(iter(after_download))]["digest"] = "sha256:" + "0" * 64
        results = [published] if existing else [draft, draft, published]
        with patch.object(self.client, "check_tag"), \
                patch.object(self.client, "find", side_effect=results), \
                patch("release.api", return_value={"visibility": "public"}), \
                patch.object(self.client, "assets", return_value=after_download), \
                patch.object(self.client, "verify_uploaded", return_value=self.assets) as verify, \
                patch.object(self.client, "verify_immutable") as immutable, \
                patch("builtins.print"), \
                patch("release.command") as command:
            if not verified:
                verify.side_effect = ValueError("signature verification failed")
            if not verified or changed:
                with self.assertRaises(ValueError):
                    self.client.finish("publish", self.dist)
                command.assert_not_called()
                immutable.assert_not_called()
            else:
                self.client.finish("verify" if existing else "publish", self.dist)
                if existing:
                    command.assert_not_called()
                    self.assertIsNone(verify.call_args.args[2])
                else:
                    command.assert_called_once_with("gh", "release", "edit", "v1.2.3",
                                                    "--repo", "example/custom-service", "--draft=false")
                immutable.assert_called_once()

    def test_signature_failure_or_concurrent_asset_change_never_publishes(self):
        self.finish_with(verified=False)
        self.finish_with(changed=True)

    def test_verified_draft_publishes_then_verifies_release_attestation(self):
        self.finish_with()

    def test_immutable_rerun_verifies_without_any_remote_mutation(self):
        self.finish_with(existing=True)

    def test_crypto_commands_bind_workflow_tag_and_commit(self):
        with patch("release.command") as command:
            self.client.verify_signatures(self.dist, self.files, True)
            calls = [call.args for call in command.call_args_list]
            self.assertIn("https://github.com/example/custom-service/.github/workflows/release.yml@refs/tags/v1.2.3", calls[0])
            self.assertIn("--certificate-github-workflow-sha", calls[0])
            self.assertIn(SHA, calls[0])
            for call in calls[1:]:
                self.assertIn("--source-digest", call)
                self.assertIn(SHA, call)
                self.assertIn("refs/tags/v1.2.3", call)

    def test_immutable_verification_failure_is_not_skipped(self):
        with patch("release.command", side_effect=subprocess.CalledProcessError(1, ["gh"])), \
                patch("release.time.sleep") as sleep:
            with self.assertRaises(subprocess.CalledProcessError):
                self.client.verify_immutable(self.dist, self.assets)
            self.assertEqual(sleep.call_count, 5)


if __name__ == "__main__":
    unittest.main()
