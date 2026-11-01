#!/usr/bin/env python3
# relationships:
#   references: [manifest, publish, stage]
"""Publish prepared release bytes, or verify and reuse the same published bytes."""

import base64
import hashlib
import json
import os
import re
import subprocess
import sys
import time
import shutil
import tarfile
import tempfile
from pathlib import Path

from git_env import git_env
from manifest import REPOSITORY, TARGETS, assets, sha256, verify_assets, version_value


def command(arguments):
    return subprocess.run(arguments, capture_output=True, text=True)


def require_success(result):
    if result.returncode:
        raise RuntimeError(f"publication command failed: {result.stderr.strip()}")
    return result.stdout


def github_release(version):
    result = command(["gh", "api", f"repos/{REPOSITORY}/releases/tags/{version}"])
    if result.returncode and "HTTP 404" in result.stderr:
        return None
    return json.loads(require_success(result))


def verify_github_assets(release, paths, version):
    if release["tag_name"] != version or release["draft"] or release["prerelease"]:
        raise ValueError("existing GitHub release must be final and match the version")
    published = [item["name"] for item in release["assets"]]
    if len(published) != len(set(published)) or set(published) - paths.keys():
        raise ValueError("existing GitHub release has duplicate or unexpected assets")
    # Check every existing asset before adding any missing one. Never clobber bytes.
    with tempfile.TemporaryDirectory(prefix="dcb-release-compare-") as directory:
        for name in published:
            require_success(
                command(
                    [
                        "gh",
                        "release",
                        "download",
                        version,
                        "--pattern",
                        name,
                        "--dir",
                        directory,
                    ]
                )
            )
            if sha256(Path(directory) / name) != sha256(paths[name]):
                raise ValueError(f"existing GitHub release asset differs: {name}")
    return [path for name, path in paths.items() if name not in published]


def publish_github(dist, version):
    verify_assets(dist, version)
    paths = {name: dist / name for *_, name in assets(version)}
    paths.update(
        {name: dist / name for name in ["checksums.txt", "release-manifest.json"]}
    )
    release = github_release(version)
    if release is None:
        require_success(
            command(
                [
                    "gh",
                    "release",
                    "create",
                    version,
                    "--verify-tag",
                    "--title",
                    version,
                    "--notes-file",
                    str(dist / "release-notes.md"),
                    *map(str, paths.values()),
                ]
            )
        )
    else:
        missing = verify_github_assets(release, paths, version)
        if missing:
            require_success(
                command(["gh", "release", "upload", version, *map(str, missing)])
            )
    # A partial upload or a concurrent writer must not be reported as complete.
    release = github_release(version)
    if release is None or verify_github_assets(release, paths, version):
        raise ValueError("GitHub release is still missing expected assets")


def npm_integrity(identity):
    result = command(
        [
            "npm",
            "view",
            identity,
            "dist.integrity",
            "--prefer-online",
            "--json",
            "--registry",
            "https://registry.npmjs.org",
            "--@wyrd-company:registry=https://registry.npmjs.org",
        ]
    )
    if result.returncode:
        try:
            missing = json.loads(result.stdout).get("error", {}).get("code") == "E404"
        except json.JSONDecodeError:
            missing = False
        if missing:
            return None
    return json.loads(require_success(result))


NPM_VISIBILITY_SECONDS = 600
NPM_POLL_SECONDS = 10


def wait_for_npm(packages):
    """Wait only for absent metadata; a different integrity is never transient."""
    deadline = time.monotonic() + NPM_VISIBILITY_SECONDS
    pending = {identity: local for _, identity, local, _ in packages}
    while pending:
        for identity, local in list(pending.items()):
            remote = npm_integrity(identity)
            if remote is None:
                continue
            if remote != local:
                raise ValueError(f"published npm version has different bytes: {identity}")
            del pending[identity]
            print(f"Verified npm bytes: {identity}", flush=True)
        if not pending:
            return
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise ValueError("npm accepted publication but metadata is still unavailable: " + ", ".join(pending))
        print("Waiting for npm registry visibility: " + ", ".join(pending), flush=True)
        time.sleep(min(NPM_POLL_SECONDS, remaining))


def publish_npm(dist, version, *, provenance=True):
    packages = []
    receipt_path = dist / "npm-publication-receipts.json"
    receipts = {"version": version, "accepted": {}}
    if receipt_path.exists():
        receipts = json.loads(receipt_path.read_text())
        if receipts.get("version") != version or not isinstance(receipts.get("accepted"), dict):
            raise ValueError("npm publication receipts disagree with release")
    # Preflight every existing version before publishing any native dependency.
    names = [f"dcb-{osname}-{nodearch}" for osname, _, _, nodearch in TARGETS] + ["dcb"]
    for name in names:
        archive = dist / "npm/tarballs" / f"wyrd-company-{name}-{version}.tgz"
        with tarfile.open(archive) as stream:
            metadata = json.load(stream.extractfile("package/package.json"))
        if (
            metadata["name"] != f"@wyrd-company/{name}"
            or metadata["version"] != version
        ):
            raise ValueError(f"npm tarball identity disagrees: {archive}")
        with archive.open("rb") as stream:
            local = (
                "sha512-"
                + base64.b64encode(
                    hashlib.file_digest(stream, "sha512").digest()
                ).decode()
            )
        identity = f"{metadata['name']}@{version}"
        if identity in receipts["accepted"] and receipts["accepted"][identity] != local:
            raise ValueError(f"npm publication receipt has different bytes: {identity}")
        remote = npm_integrity(identity)
        if remote is not None and remote != local:
            raise ValueError(f"existing npm version has different bytes: {identity}")
        packages.append((archive, identity, local, remote is not None))
    if set(receipts["accepted"]) - {p[1] for p in packages}:
        raise ValueError("npm publication receipt has unexpected packages")
    for _, identity, local, exists in packages:
        if exists:
            receipts["accepted"][identity] = local
    if receipts["accepted"]:
        temporary = receipt_path.with_suffix(".tmp")
        temporary.write_text(json.dumps(receipts, sort_keys=True) + "\n")
        temporary.replace(receipt_path)
    for archive, identity, local, exists in packages:
        if exists or identity in receipts["accepted"]:
            continue
        require_success(
            command(
                ["npm", "publish", str(archive), "--@wyrd-company:registry=https://registry.npmjs.org", "--access", "public"]
                + (["--provenance"] if provenance else [])
            )
        )
        receipts["accepted"][identity] = local
        temporary = receipt_path.with_suffix(".tmp")
        temporary.write_text(json.dumps(receipts, sort_keys=True) + "\n")
        temporary.replace(receipt_path)
        print(f"npm accepted publication: {identity}", flush=True)
    wait_for_npm(packages)


def verify_homebrew(dist, version, tap):
    existing = tap / "Formula/dcb.rb"
    if not existing.exists():
        return
    match = re.search(r'^\s*version "([^"]+)"\s*$', existing.read_text(), re.MULTILINE)
    if match is None:
        raise ValueError("existing Homebrew formula has no explicit version")
    current = version_value(match[1])
    requested = version_value(version)
    if tuple(map(int, current.split("."))) > tuple(map(int, requested.split("."))):
        raise ValueError(f"refusing to downgrade Homebrew from {current} to {requested}")
    if current == requested:
        if existing.read_bytes() != (dist / "dcb.rb").read_bytes():
            raise ValueError(
                "existing Homebrew formula for this version has different bytes"
            )


def publish_homebrew(dist, version, tap):
    verify_homebrew(dist, version, tap)
    formula = tap / "Formula/dcb.rb"
    formula.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(dist / "dcb.rb", formula)
    def git(args):
        return subprocess.run(["git", "-C", str(tap), *args], capture_output=True, text=True, env=git_env())
    for args in [["config", "user.name", "dcb release bot"],
                 ["config", "user.email", "41898282+github-actions[bot]@users.noreply.github.com"],
                 ["add", "Formula/dcb.rb"]]:
        require_success(git(args))
    diff = git(["diff", "--cached", "--quiet"])
    if diff.returncode == 1:
        require_success(git(["commit", "-m", f"Brew formula update for dcb version {version}"]))
    elif diff.returncode:
        require_success(diff)
    require_success(git(["push", "origin", "HEAD:main"]))


if __name__ == "__main__":
    version = version_value(os.environ["RELEASE_VERSION"])
    channel = sys.argv[1]
    if channel == "github":
        publish_github(Path("dist"), version)
    elif channel == "npm":
        publish_npm(Path("dist"), version)
    elif channel == "homebrew-publish":
        publish_homebrew(Path("dist"), version, Path(sys.argv[2]))
    elif channel == "homebrew":
        verify_homebrew(Path("dist"), version, Path(sys.argv[2]))
    else:
        raise SystemExit("unknown publication channel")
