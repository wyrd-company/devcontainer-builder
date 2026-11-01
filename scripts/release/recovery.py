#!/usr/bin/env python3
# relationships:
#   references: [manifest, stage, channels, recover-release]
"""Recover package publication from a final GitHub release without compiling."""
import argparse
import json
import subprocess
from pathlib import Path

import channels
import manifest
import stage
from git_env import git_env


def download(dist, version):
    manifest.version_value(version)
    release = channels.github_release(version)
    if release is None or release["draft"] or release["prerelease"] or release["tag_name"] != version:
        raise ValueError("recovery requires the final GitHub release for this version")
    expected = {name for *_, name in manifest.assets(version)} | {"checksums.txt", "release-manifest.json"}
    actual = [a["name"] for a in release["assets"]]
    if len(actual) != len(expected) or set(actual) != expected:
        raise ValueError("GitHub release assets are incomplete or unexpected")
    dist.mkdir(parents=True, exist_ok=True)
    subprocess.run(["gh", "release", "download", version, "-R", manifest.REPOSITORY,
                    "--dir", str(dist)], check=True)
    handoff = json.loads((dist / "release-manifest.json").read_text())
    commit = handoff["source"]["commit"]
    manifest.verify_handoff(dist / "release-manifest.json", commit, version)
    manifest.verify_assets(dist, version)
    return commit


def prepare(dist, version, source):
    commit = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True, env=git_env()).strip()
    tag = subprocess.check_output(["git", "-C", str(source), "rev-parse", f"refs/tags/{version}^{{}}"], text=True, env=git_env()).strip()
    if tag != commit:
        raise ValueError("recovery source must be the requested release tag")
    if subprocess.check_output(["git", "-C", str(source), "status", "--porcelain"], text=True, env=git_env()):
        raise ValueError("recovery packaging source must be clean")
    recorded = json.loads((dist / "release-manifest.json").read_text())
    manifest.verify_handoff(dist / "release-manifest.json", commit, version)
    expected = manifest.create(dist, version, commit)
    if recorded != expected:
        raise ValueError("release assets disagree with the original handoff")
    # Packaging metadata and launcher sources come from the original release,
    # even when the recovery workflow runs from a newer tooling commit.
    previous = stage.ROOT
    try:
        stage.ROOT = source.resolve()
        stage.stage(dist, version, commit)
    finally:
        stage.ROOT = previous
    if json.loads((dist / "release-manifest.json").read_text()) != recorded:
        raise ValueError("recovery changed the original handoff")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=["download", "prepare", "npm", "homebrew"])
    parser.add_argument("--version", required=True)
    parser.add_argument("--dist", type=Path, default=Path("dist"))
    parser.add_argument("--source", type=Path)
    parser.add_argument("--tap", type=Path)
    args = parser.parse_args()
    manifest.version_value(args.version)
    if args.operation == "download":
        download(args.dist, args.version)
    else:
        if args.source is None:
            parser.error("--source is required")
        if args.operation == "prepare":
            prepare(args.dist, args.version, args.source)
        else:
            commit = subprocess.check_output(["git", "-C", str(args.source), "rev-parse", "HEAD"], text=True, env=git_env()).strip()
            manifest.verify_handoff(args.dist / "release-manifest.json", commit, args.version)
            manifest.verify_assets(args.dist, args.version)
            if args.operation == "npm":
                channels.publish_npm(args.dist, args.version)
            else:
                if args.tap is None:
                    parser.error("--tap is required")
                channels.publish_homebrew(args.dist, args.version, args.tap)
