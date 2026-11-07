"""Verify recording output and publication redaction without running Go."""

import json
import os
from pathlib import Path
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

from record_demo import Recorder, redact


class RecordingTests(unittest.TestCase):
    def test_paths_and_credentials_are_redacted(self):
        with patch.dict(os.environ, {"DEMO_API_TOKEN": "secret-test-token"}):
            value = redact("C:\\Users\\private\\go /home/private/go secret-test-token "
                           "https://person:password@example.com Bearer eyJ.abc.def")
        for private in ("private", "secret-test-token", "person:password", "eyJ.abc.def"):
            self.assertNotIn(private, value)
        self.assertIn("https://<redacted>@example.com", value)

    def test_real_output_and_failure_are_saved_with_elapsed_events(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            recorder = Recorder(root, root, [(root, "<demo>")], 5)
            with self.assertRaises(RuntimeError):
                recorder.run([sys.executable, "-c", "print('actual output'); raise SystemExit(3)"], visible=True)
            recorder.save({"status": "error"})
            cast = [json.loads(line) for line in (root / "demo.cast").read_text().splitlines()]
            self.assertEqual(cast[0]["version"], 2)
            times = [event[0] for event in cast[1:]]
            self.assertEqual(times, sorted(times))
            self.assertGreater(times[-1], 0)
            transcript = (root / "demo.txt").read_text()
            self.assertIn("actual output", transcript)
            self.assertIn("[exit 3;", transcript)
            self.assertFalse(transcript.endswith("\n\n"))
            self.assertEqual(transcript, "".join(event[2] for event in cast[1:]).replace("\r\n", "\n"))
            self.assertNotIn(temporary, transcript)
            self.assertEqual(recorder.steps[0]["exit_code"], 3)

    def test_preparation_is_saved_outside_playback(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            recorder = Recorder(root, root, [], 5)
            captured = recorder.run([sys.executable, "-c", "print('preparation only')"], timeout=5)
            self.assertEqual(captured, "preparation only")
            recorder.save({"status": "passed"})
            self.assertEqual((root / "demo.txt").read_text(), "")
            self.assertIn("preparation only", (root / "preparation.txt").read_text())

    def test_real_command_timeout_records_failure_and_returns(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            recorder = Recorder(root, root, [], 5)
            started = time.monotonic()
            with self.assertRaises(RuntimeError):
                recorder.run([sys.executable, "-c", "import time; print('started', flush=True); time.sleep(60)"],
                             visible=True, timeout=0.2)
            self.assertLess(time.monotonic() - started, 15)
            self.assertTrue(recorder.steps[0]["timeout"])
            self.assertNotEqual(recorder.steps[0]["exit_code"], 0)
            recorder.save({"status": "error"})
            self.assertIn("started", (root / "demo.txt").read_text())

    def test_timeout_cleans_descendant_after_parent_exits(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            marker = root / "escaped-child.txt"
            child = "import pathlib,time; print('child started',flush=True); time.sleep(1.5); " \
                    f"pathlib.Path({str(marker)!r}).write_text('escaped')"
            parent = f"import subprocess,sys; subprocess.Popen([sys.executable,'-c',{child!r}]); " \
                     "print('parent exiting',flush=True)"
            recorder = Recorder(root, root, [], 5)
            started = time.monotonic()
            with self.assertRaises(RuntimeError):
                recorder.run([sys.executable, "-c", parent], visible=True, timeout=0.4)
            self.assertLess(time.monotonic() - started, 1.5)
            self.assertTrue(recorder.steps[0]["timeout"])
            # Killing a descendant must not falsify the original parent's exit.
            self.assertEqual(recorder.steps[0]["exit_code"], 0)
            recorder.save({"status": "error"})
            self.assertIn("child started", (root / "demo.txt").read_text())
            time.sleep(1.6)
            self.assertFalse(marker.exists(), "descendant survived timeout cleanup")


if __name__ == "__main__":
    unittest.main()
