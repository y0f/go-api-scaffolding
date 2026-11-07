"""Require native Linux AMD64 and ARM64 images for a Compose service."""

import json
import subprocess
import sys


REQUIRED = {"linux/amd64", "linux/arm64"}


def docker_json(*args):
    result = subprocess.run(
        ["docker", *args], check=True, capture_output=True, text=True, timeout=90
    )
    return json.loads(result.stdout)


def check(compose_file, service):
    config = docker_json("compose", "-f", compose_file, "--profile", "*",
                         "config", "--format", "json")
    image = config["services"][service]["image"]
    manifest = docker_json("buildx", "imagetools", "inspect", "--raw", image)
    platforms = {
        f"{item['platform']['os']}/{item['platform']['architecture']}"
        for item in manifest.get("manifests", [])
        if "os" in item.get("platform", {})
        and "architecture" in item["platform"]
    }
    missing = REQUIRED - platforms
    if missing:
        found = ", ".join(sorted(platforms)) or "no multi-platform manifest list"
        raise ValueError(
            f"{service} ({image}) is missing {', '.join(sorted(missing))}; "
            f"registry reports {found}"
        )
    print(f"{service} ({image}) supports {', '.join(sorted(REQUIRED))}")


def main(args):
    if len(args) != 2:
        print("usage: check_image_platforms.py COMPOSE_FILE SERVICE", file=sys.stderr)
        return 2
    try:
        check(*args)
    except (OSError, subprocess.SubprocessError, ValueError, KeyError, TypeError) as error:
        print(f"image platform check failed: {error}", file=sys.stderr)
        if isinstance(error, subprocess.CalledProcessError) and error.stderr:
            print(error.stderr.strip(), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
