"""Record a real resource workflow from a committed source in a disposable clone."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import signal
import subprocess
import sys
import threading
import time


def redact(text, paths=()):
    for path, label in sorted(paths, key=lambda item: len(str(item[0])), reverse=True):
        for spelling in {str(path), str(path).replace("\\", "/")}:
            text = re.sub(re.escape(spelling), lambda _: label, text, flags=re.IGNORECASE)
    text = re.sub(r"[A-Za-z]:[\\/](?:Users|Documents and Settings)[\\/][^\\/\s]+", "<home>", text)
    text = re.sub(r"/(?:home|Users)/[^/\s]+", "<home>", text)
    for name, value in os.environ.items():
        if len(value) >= 6 and re.search(r"TOKEN|PASSWORD|SECRET|API_?KEY|ACCESS_?KEY|PRIVATE_KEY|CREDENTIAL", name, re.IGNORECASE):
            text = text.replace(value, "<redacted>")
    text = re.sub(r"(https?://)[^/\s@]+@", r"\1<redacted>@", text)
    return re.sub(r"(?i)(Bearer\s+)[A-Za-z0-9._~+/-]+=*", r"\1<redacted>", text)


class WindowsJob:
    """Own a process tree, including descendants whose parent has exited."""

    def __init__(self):
        import ctypes
        from ctypes import wintypes

        class BasicLimits(ctypes.Structure):
            _fields_ = [("process_time", ctypes.c_longlong), ("job_time", ctypes.c_longlong),
                        ("flags", wintypes.DWORD), ("min_working_set", ctypes.c_size_t),
                        ("max_working_set", ctypes.c_size_t), ("active_processes", wintypes.DWORD),
                        ("affinity", ctypes.c_size_t), ("priority", wintypes.DWORD),
                        ("scheduling", wintypes.DWORD)]

        class ExtendedLimits(ctypes.Structure):
            _fields_ = [("basic", BasicLimits), ("io_counters", ctypes.c_ulonglong * 6),
                        ("process_memory", ctypes.c_size_t), ("job_memory", ctypes.c_size_t),
                        ("peak_process_memory", ctypes.c_size_t), ("peak_job_memory", ctypes.c_size_t)]

        self.api = ctypes.WinDLL("kernel32", use_last_error=True)
        self.api.CreateJobObjectW.argtypes = [ctypes.c_void_p, wintypes.LPCWSTR]
        self.api.CreateJobObjectW.restype = wintypes.HANDLE
        self.api.SetInformationJobObject.argtypes = [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD]
        self.api.SetInformationJobObject.restype = wintypes.BOOL
        self.api.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
        self.api.OpenProcess.restype = wintypes.HANDLE
        self.api.AssignProcessToJobObject.argtypes = [wintypes.HANDLE, wintypes.HANDLE]
        self.api.AssignProcessToJobObject.restype = wintypes.BOOL
        self.api.TerminateJobObject.argtypes = [wintypes.HANDLE, wintypes.UINT]
        self.api.TerminateJobObject.restype = wintypes.BOOL
        self.api.CloseHandle.argtypes = [wintypes.HANDLE]
        self.api.CloseHandle.restype = wintypes.BOOL
        self.handle = self.api.CreateJobObjectW(None, None)
        if not self.handle:
            raise ctypes.WinError(ctypes.get_last_error())
        limits = ExtendedLimits()
        limits.basic.flags = 0x2000  # JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
        if not self.api.SetInformationJobObject(self.handle, 9, ctypes.byref(limits), ctypes.sizeof(limits)):
            error = ctypes.WinError(ctypes.get_last_error())
            self.close()
            raise error

    def assign(self, pid):
        import ctypes

        process = self.api.OpenProcess(0x0101, False, pid)  # PROCESS_SET_QUOTA | PROCESS_TERMINATE
        if not process:
            raise ctypes.WinError(ctypes.get_last_error())
        try:
            if not self.api.AssignProcessToJobObject(self.handle, process):
                raise ctypes.WinError(ctypes.get_last_error())
        finally:
            self.api.CloseHandle(process)

    def close(self):
        if self.handle:
            self.api.CloseHandle(self.handle)
            self.handle = None

    def terminate(self):
        import ctypes

        if self.handle and not self.api.TerminateJobObject(self.handle, 124):
            raise ctypes.WinError(ctypes.get_last_error())


def start_process(command, workspace):
    options = dict(cwd=workspace, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                   text=True, encoding="utf-8", errors="replace",
                   env=dict(os.environ, NO_COLOR="1", TERM="dumb"))
    if os.name != "nt":
        return subprocess.Popen(command, stdin=subprocess.DEVNULL, start_new_session=True, **options), None

    # The launcher waits for argv on stdin. Assign it to the job before giving
    # it any command, so even a fast command cannot spawn outside the job.
    launcher = "import json,subprocess,sys; sys.exit(subprocess.call(json.load(sys.stdin), stdin=subprocess.DEVNULL))"
    job = WindowsJob()
    process = None
    try:
        process = subprocess.Popen([sys.executable, "-c", launcher], stdin=subprocess.PIPE,
                                   creationflags=subprocess.CREATE_NO_WINDOW, **options)
        job.assign(process.pid)
        process.stdin.write(json.dumps(command))
        process.stdin.close()
        return process, job
    except BaseException:
        job.close()
        if process is not None:
            if process.poll() is None:
                process.kill()
            process.wait(timeout=10)
            process.stdin.close()
            process.stdout.close()
        raise


class Recorder:
    def __init__(self, workspace, output, paths, timeout):
        self.workspace, self.output, self.paths, self.timeout = workspace, output, paths, timeout
        self.events, self.steps, self.preparation = [], [], []
        self.started, self.timestamp = None, None

    def emit(self, text, visible):
        clean = redact(text, self.paths)
        if visible:
            self.events.append([round(time.monotonic() - self.started, 3), "o", clean.replace("\n", "\r\n")])
        else:
            self.preparation.append(clean)

    def run(self, command, visible=False, timeout=None):
        if visible and self.started is None:
            self.started, self.timestamp = time.monotonic(), int(time.time())
        if (self.events if visible else self.preparation):
            self.emit("\n", visible)
        self.emit("$ " + " ".join(command) + "\n", visible)
        started = time.monotonic()
        process, job = start_process(command, self.workspace)
        timed_out = threading.Event()
        output = []

        def kill_tree():
            if job is not None:
                try:
                    job.terminate()
                finally:
                    job.close()
            else:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass

        def terminate():
            # The initial process may have exited while a descendant still
            # holds stdout open; its whole tree remains subject to the deadline.
            timed_out.set()
            kill_tree()

        timer = threading.Timer(self.timeout if timeout is None else timeout, terminate)
        timer.start()
        try:
            for line in process.stdout:
                self.emit(line, visible)
                output.append(redact(line, self.paths))
            process.wait(timeout=10)
        finally:
            timer.cancel()
            timer.join()  # Do not race job-handle cleanup with the timer.
            kill_tree()
            process.wait(timeout=10)
            process.stdout.close()
        elapsed = round(time.monotonic() - started, 3)
        self.steps.append({"command": [redact(part, self.paths) for part in command], "seconds": elapsed,
                           "exit_code": process.returncode, "timeout": timed_out.is_set(), "recorded": visible})
        self.emit(f"[exit {process.returncode}; {elapsed:.3f}s]\n", visible)
        if process.returncode != 0 or timed_out.is_set():
            raise RuntimeError("command failed; see the saved transcript and metadata")
        return "".join(output).strip()

    def save(self, metadata):
        duration = round(time.monotonic() - self.started, 3) if self.started else 0
        header = {"version": 2, "width": 100, "height": 30, "duration": duration,
                  "title": "gophling: add and check a Note resource"}
        if self.timestamp is not None:
            header["timestamp"] = self.timestamp
        lines = [header, *self.events]
        (self.output / "demo.cast").write_text("".join(json.dumps(line) + "\n" for line in lines), encoding="utf-8")
        transcript = "".join(event[2] for event in self.events).replace("\r\n", "\n")
        (self.output / "demo.txt").write_text(transcript, encoding="utf-8", newline="\n")
        (self.output / "preparation.txt").write_text("".join(self.preparation), encoding="utf-8", newline="\n")
        metadata.update(duration_seconds=duration, steps=self.steps)
        (self.output / "demo.json").write_text(json.dumps(metadata, indent=2) + "\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument("--ref", required=True, help="Committed source revision to record")
    parser.add_argument("--output", type=Path, required=True, help="New directory outside the source tree")
    parser.add_argument("--timeout", type=int, default=600, help="Seconds allowed per command")
    args = parser.parse_args()
    source, output = args.source.resolve(), args.output.resolve()
    if output == source or source in output.parents or args.timeout < 1:
        parser.error("output must be outside the source tree and timeout must be positive")
    output.mkdir(parents=True, exist_ok=False)
    workspace = output / "workspace"
    paths = [(source, "<source>"), (output, "<demo>"), (Path.home(), "<home>")]
    recorder = Recorder(source, output, paths, args.timeout)
    metadata = {"status": "error", "python": platform.python_version(), "os": platform.system(),
                "architecture": platform.machine(), "agent": None,
                "cache": "Shared host Go caches; initialization and bootstrap excluded from playback",
                "redactions": "Local source, output and home paths; credential-shaped environment values and URLs",
                "recorder_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}

    def capture(command):
        return recorder.run(command, timeout=min(args.timeout, 60))

    try:
        metadata["source_commit"] = capture(["git", "rev-parse", f"{args.ref}^{{commit}}"])
        metadata["git"] = capture(["git", "--version"])
        recorder.run(["git", "clone", "--quiet", "--no-hardlinks", str(source), str(workspace)])
        recorder.workspace = workspace
        recorder.run(["git", "checkout", "--quiet", "--detach", metadata["source_commit"]])
        recorder.run(["go", "run", "./cmd/forge", "init", "-yes", "-module", "example.com/demo/app"])
        recorder.run(["go", "run", "./cmd/dev", "bootstrap"])
        metadata["go"] = capture(["go", "version"])
        metadata["tools_go_mod_sha256"] = hashlib.sha256((workspace / "tools/go.mod").read_bytes()).hexdigest()
        recorder.run(["git", "remote", "remove", "origin"])
        recorder.run(["git", "add", "--all"])
        recorder.run(["git", "-c", "user.name=Scaffold demonstration fixture", "-c", "user.email=demo@example.invalid",
                      "-c", "commit.gpgsign=false", "-c", "core.hooksPath=", "commit", "--quiet", "--no-verify",
                      "-m", "test: prepare local demonstration fixture"])
        metadata["fixture_commit"] = capture(["git", "rev-parse", "HEAD"])
        for command in (["go", "run", "./cmd/forge", "add", "resource", "Note"],
                        ["go", "run", "./cmd/dev", "generate"], ["go", "run", "./cmd/dev", "check"],
                        ["git", "add", "-N", "."], ["git", "diff", "--stat"]):
            recorder.run(command, visible=True)
        metadata["status"] = "passed"
    except (OSError, subprocess.SubprocessError, RuntimeError) as error:
        metadata["error"] = redact(str(error), paths)
    finally:
        recorder.save(metadata)
    print(f"Recording {metadata['status']}; demo.cast, demo.txt and demo.json saved in the output directory.")
    return 0 if metadata["status"] == "passed" else 1


if __name__ == "__main__":
    sys.exit(main())
