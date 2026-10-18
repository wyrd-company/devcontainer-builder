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
import tarfile
import tempfile
from pathlib import Path

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
            "--json",
            "--registry",
            "https://registry.npmjs.org",
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


def publish_npm(dist, version):
    packages = []
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
        remote = npm_integrity(identity)
        if remote is not None and remote != local:
            raise ValueError(f"existing npm version has different bytes: {identity}")
        packages.append((archive, identity, local, remote is not None))
    for archive, identity, local, exists in packages:
        if exists:
            continue
        require_success(
            command(
                ["npm", "publish", str(archive), "--access", "public", "--provenance"]
            )
        )
        if npm_integrity(identity) != local:
            raise ValueError(
                f"published npm version does not yet match local bytes: {identity}; rerun to verify"
            )


def verify_homebrew(dist, version, tap):
    existing = tap / "Formula/dcb.rb"
    if existing.exists() and re.search(
        r'^\s*version "' + re.escape(version) + r'"\s*$',
        existing.read_text(),
        re.MULTILINE,
    ):
        if existing.read_bytes() != (dist / "dcb.rb").read_bytes():
            raise ValueError(
                "existing Homebrew formula for this version has different bytes"
            )


if __name__ == "__main__":
    version = version_value(os.environ["RELEASE_VERSION"])
    channel = sys.argv[1]
    if channel == "github":
        publish_github(Path("dist"), version)
    elif channel == "npm":
        publish_npm(Path("dist"), version)
    elif channel == "homebrew":
        verify_homebrew(Path("dist"), version, Path(sys.argv[2]))
    else:
        raise SystemExit("unknown publication channel")
