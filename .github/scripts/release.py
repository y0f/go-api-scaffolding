"""Verify a draft's uploaded artifacts before publication; never replace a release."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time
from urllib.parse import quote


CHECKSUMS = "checksums.txt"
BUNDLE = CHECKSUMS + ".bundle"
# These are the upload types emitted by this repository's GoReleaser config.
# Fail closed if the config adds another type that needs verification support.
UPLOAD_TYPES = {"Archive", "SBOM", "Checksum", "Signature"}


def command(*args):
    result = subprocess.run(args, check=True, capture_output=True, text=True,
                            encoding="utf-8", timeout=300)
    return result.stdout


def api(path, *args):
    return json.loads(command("gh", "api", path, *args))


def filename(name):
    if (not isinstance(name, str) or not name or name in {".", ".."}
            or name.startswith("-") or any(c in name for c in '/\\:')
            or any(ord(c) < 32 for c in name)):
        raise ValueError(f"unsafe artifact name: {name!r}")
    return name


def digest(path):
    checksum = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            checksum.update(block)
    return checksum.hexdigest()


def inventory(artifacts):
    files = {}
    for artifact in artifacts:
        name = filename(artifact["name"])
        if name in files:
            raise ValueError(f"duplicate release asset: {name}")
        files[name] = artifact
    if not files:
        raise ValueError("release has no assets")
    return files


def asset_fingerprint(assets):
    # Downloads change download_count; only publication-relevant fields matter.
    return {name: {key: asset.get(key) for key in ("id", "name", "state", "size", "digest")}
            for name, asset in assets.items()}


def local_inventory(dist, signed=True):
    artifacts = json.loads((dist / "artifacts.json").read_text(encoding="utf-8"))
    selected = []
    for artifact in artifacts:
        kind = artifact["type"]
        if kind in UPLOAD_TYPES:
            selected.append(artifact)
        elif kind not in {"Binary", "Metadata"}:
            raise ValueError(f"unsupported release artifact type: {kind}")
    files = inventory(selected)
    verify_content(dist, files, signed=signed)
    return {name: digest(dist / name) for name in files}


def verify_content(directory, expected, signed=True):
    actual = {path.name for path in directory.iterdir()}
    # Local dist also contains build directories and internal GoReleaser metadata.
    missing = set(expected) - actual
    if missing:
        raise ValueError(f"missing artifacts: {sorted(missing)}")
    for name in expected:
        filename(name)
        path = directory / name
        if path.is_symlink() or not path.is_file():
            raise ValueError(f"artifact is not a regular file: {name}")
    required = {CHECKSUMS, BUNDLE} if signed else {CHECKSUMS}
    if not required <= set(expected):
        raise ValueError("checksum manifest or signature bundle missing")
    checksums = {}
    for line in (directory / CHECKSUMS).read_text(encoding="utf-8").splitlines():
        match = re.fullmatch(r"([0-9a-f]{64}) [ *](.+)", line)
        if not match:
            raise ValueError("invalid SHA-256 checksum line")
        checksum, name = match.groups()
        filename(name)
        if name in checksums:
            raise ValueError(f"duplicate checksum: {name}")
        checksums[name] = checksum
    if not checksums or set(checksums) != set(expected) - required:
        raise ValueError("checksum manifest does not cover exactly the release payload")
    for name, checksum in checksums.items():
        if digest(directory / name) != checksum:
            raise ValueError(f"checksum mismatch: {name}")
    return checksums


class Release:
    def __init__(self, repo, tag, sha, server="https://github.com"):
        if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repo):
            raise ValueError("expected OWNER/REPOSITORY")
        if not re.fullmatch(r"v[A-Za-z0-9._+-]+", tag):
            raise ValueError("expected a version tag beginning with v")
        if not re.fullmatch(r"[0-9a-f]{40,64}", sha):
            raise ValueError("expected full source commit SHA")
        self.repo, self.tag, self.sha = repo, tag, sha
        self.server = server.rstrip("/")

    def find(self):
        pages = api(f"repos/{self.repo}/releases?per_page=100", "--paginate", "--slurp")
        matches = [release for page in pages for release in page
                   if release["tag_name"] == self.tag]
        if len(matches) > 1:
            raise ValueError("multiple releases use the requested tag")
        return matches[0] if matches else None

    def assets(self, release):
        pages = api(f"repos/{self.repo}/releases/{release['id']}/assets?per_page=100",
                    "--paginate", "--slurp")
        return inventory([asset for page in pages for asset in page])

    def check_tag(self):
        obj = api(f"repos/{self.repo}/git/ref/tags/{quote(self.tag, safe='')}")["object"]
        for _ in range(10):
            if obj["type"] == "commit":
                if obj["sha"] != self.sha:
                    raise ValueError("release tag no longer matches the checked-out source")
                return
            if obj["type"] != "tag":
                break
            obj = api(f"repos/{self.repo}/git/tags/{obj['sha']}")["object"]
        raise ValueError("release tag does not resolve to a commit")

    def plan(self):
        self.check_tag()
        release = self.find()
        if release and not release["draft"]:
            if not release.get("immutable"):
                raise ValueError("published mutable release exists; use a new version, do not overwrite it")
            return "verify"
        return "publish"

    def verify_signatures(self, directory, checksums, provenance):
        identity = f"{self.server}/{self.repo}/.github/workflows/release.yml@refs/tags/{self.tag}"
        command("cosign", "verify-blob", "--bundle", str(directory / BUNDLE),
                "--certificate-identity", identity,
                "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com",
                "--certificate-github-workflow-sha", self.sha, str(directory / CHECKSUMS))
        if provenance:
            for name in sorted(checksums):
                command("gh", "attestation", "verify", str(directory / name),
                        "--repo", self.repo, "--signer-workflow",
                        f"{self.repo}/.github/workflows/release.yml",
                        "--source-ref", f"refs/tags/{self.tag}", "--source-digest", self.sha)

    def verify_uploaded(self, release, directory, local, provenance):
        assets = self.assets(release)
        if local is not None and set(assets) != set(local):
            raise ValueError("uploaded asset inventory differs from the local build")
        command("gh", "release", "download", self.tag, "--repo", self.repo,
                "--dir", str(directory))
        if {path.name for path in directory.iterdir()} != set(assets):
            raise ValueError("downloaded asset inventory differs from the release")
        checksums = verify_content(directory, assets)
        for name, asset in assets.items():
            checksum = digest(directory / name)
            if (asset.get("state") != "uploaded"
                    or asset.get("size") != (directory / name).stat().st_size
                    or asset.get("digest") != "sha256:" + checksum):
                raise ValueError(f"uploaded metadata or digest mismatch: {name}")
            if local is not None and local[name] != checksum:
                raise ValueError(f"uploaded artifact differs from local build: {name}")
        self.verify_signatures(directory, checksums, provenance)
        return assets

    def verify_immutable(self, directory, assets):
        # GitHub creates its release attestation after publication. Allow bounded
        # propagation time; verification failures never become successful skips.
        for attempt in range(6):
            try:
                command("gh", "release", "verify", self.tag, "--repo", self.repo)
                break
            except subprocess.CalledProcessError:
                if attempt == 5:
                    raise
                time.sleep(5)
        for name in sorted(assets):
            command("gh", "release", "verify-asset", self.tag,
                    str(directory / name), "--repo", self.repo)

    def finish(self, mode, dist):
        self.check_tag()
        release = self.find()
        if release is None:
            raise ValueError("release does not exist")
        if mode == "publish" and not release["draft"]:
            raise ValueError("refusing to publish or replace an already-published release")
        if mode == "verify" and (release["draft"] or not release.get("immutable")):
            raise ValueError("verify-only mode requires an already-published immutable release")
        local = local_inventory(dist) if mode == "publish" else None
        repository = api(f"repos/{self.repo}")
        provenance = self.server == "https://github.com" and repository["visibility"] == "public"
        with tempfile.TemporaryDirectory(prefix="release-verify-") as temporary:
            directory = Path(temporary)
            assets = self.verify_uploaded(release, directory, local, provenance)
            if mode == "publish":
                self.check_tag()
                current = self.find()
                if (current is None or current["id"] != release["id"] or not current["draft"]
                        or asset_fingerprint(self.assets(current)) != asset_fingerprint(assets)):
                    raise ValueError("draft changed during verification; refusing publication")
                command("gh", "release", "edit", self.tag, "--repo", self.repo, "--draft=false")
                release = self.find()
                if release is None or release["draft"]:
                    raise ValueError("release did not become published")
            if release.get("immutable"):
                self.verify_immutable(directory, assets)
                print("Verified published immutable release and every uploaded asset.")
            else:
                print("Verified and published release; repository release immutability is not enabled.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("plan", "publish", "verify", "local"))
    parser.add_argument("--dist", type=Path, default=Path("dist"))
    parser.add_argument("--unsigned-snapshot", action="store_true",
                        help="local mode only: validate an unsigned GoReleaser snapshot")
    args = parser.parse_args()
    if args.unsigned_snapshot and args.mode != "local":
        parser.error("--unsigned-snapshot is only allowed with local mode")
    if args.mode == "local":
        files = local_inventory(args.dist, signed=not args.unsigned_snapshot)
        print(f"Validated {len(files)} local artifact files; signatures/provenance were not checked.")
        return
    release = Release(os.environ["GITHUB_REPOSITORY"], os.environ["GITHUB_REF_NAME"],
                      command("git", "rev-parse", "HEAD").strip(),
                      os.environ.get("GITHUB_SERVER_URL", "https://github.com"))
    if args.mode == "plan":
        mode = release.plan()
        with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
            output.write(f"mode={mode}\n")
        print(f"Release mode: {mode}")
    else:
        release.finish(args.mode, args.dist)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, subprocess.SubprocessError) as error:
        if isinstance(error, subprocess.CalledProcessError) and error.stderr:
            print(error.stderr, file=sys.stderr)
        raise SystemExit(f"release verification failed: {error}") from error
