import contextlib
import io
import json
import subprocess
import unittest
from unittest.mock import patch

import check_image_platforms as guard


class ImagePlatformTests(unittest.TestCase):
    def check_manifest(self, manifest):
        config = {"services": {"collector": {"image": "example/collector:1.2.3"}}}
        with patch.object(guard, "docker_json", side_effect=[config, manifest]) as docker:
            guard.check("compose.yml", "collector")
            self.assertEqual(docker.call_args.args,
                             ("buildx", "imagetools", "inspect", "--raw", "example/collector:1.2.3"))

    def test_multiarch_accepts_arm_variant_and_attestation(self):
        manifest = {"manifests": [
            {"platform": {"os": "linux", "architecture": "amd64"}},
            {"platform": {"os": "linux", "architecture": "arm64", "variant": "v8"}},
            {"platform": {"os": "unknown", "architecture": "unknown"}},
            {"annotations": {"vnd.docker.reference.type": "attestation-manifest"}},
        ]}
        with contextlib.redirect_stdout(io.StringIO()):
            self.check_manifest(manifest)

    def test_single_arch_and_missing_arm_fail(self):
        for manifest in [
            {"config": {"digest": "sha256:example"}},
            {"manifests": [{"platform": {"os": "linux", "architecture": "amd64"}}]},
        ]:
            with self.subTest(manifest=manifest), self.assertRaisesRegex(ValueError, "missing.*linux/arm64"):
                self.check_manifest(manifest)

    def test_cli_failure_is_not_a_pass(self):
        error = subprocess.CalledProcessError(1, ["docker"], stderr="registry unavailable")
        with patch.object(guard, "docker_json", side_effect=error):
            with contextlib.redirect_stderr(io.StringIO()) as stderr:
                self.assertEqual(guard.main(["compose.yml", "collector"]), 1)
        self.assertIn("registry unavailable", stderr.getvalue())

    def test_docker_command_is_bounded_and_json_parsed(self):
        with patch.object(guard.subprocess, "run") as run:
            run.return_value.stdout = json.dumps({"schemaVersion": 2})
            self.assertEqual(guard.docker_json("buildx", "version"), {"schemaVersion": 2})
            self.assertEqual(run.call_args.kwargs["timeout"], 90)
            self.assertTrue(run.call_args.kwargs["check"])


if __name__ == "__main__":
    unittest.main()
