#!/usr/bin/env python3
# relationships:
#   references: [goreleaser, taskfile]
"""Build the six-Linux-artifact repo.wyrd.foo release handoff."""

import hashlib
import json
import re
from pathlib import Path

VERSION = re.compile(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)")
COMMIT = re.compile(r"[0-9a-f]{40}")
REPOSITORY = "wyrd-company/devcontainer-builder"
PACKAGE = {
    "name": "dcb",
    "binary": "dcb",
    "description": "Build and run development containers",
    "homepage": f"https://github.com/{REPOSITORY}",
    "license": "MIT",
    "maintainer": "Wyrd Company <support@wyrd.company>",
}
PUBLISH = {
    "apt": {"suite": "stable", "component": "main"},
    "rpm": {"channel": "stable"},
    "aur": {"package": "dcb-bin"},
}
TARGETS = [
    ("linux", "amd64", "x86_64", "x64"),
    ("linux", "arm64", "aarch64", "arm64"),
    ("darwin", "amd64", "x86_64", "x64"),
    ("darwin", "arm64", "aarch64", "arm64"),
]


def version_value(value):
    if not VERSION.fullmatch(value):
        raise ValueError("release version must be bare stable SemVer")
    return value


def sha256(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def assets(version):
    return [
        (kind, fmt, osname, arch, f"dcb_{version}_{osname}_{suffix}.{fmt}")
        for osname, arch, suffix, _ in TARGETS
        for kind, fmt in [("archive", "tar.gz"), ("package", "deb"), ("package", "rpm")]
        if osname == "linux" or kind == "archive"
    ]


def verify_assets(root, version):
    expected = {item[4] for item in assets(version)}
    actual = {
        p.name
        for p in root.iterdir()
        if p.is_file() and p.name.endswith((".tar.gz", ".deb", ".rpm"))
    }
    if actual != expected:
        raise ValueError(
            f"release asset matrix disagrees: missing {expected - actual}; extra {actual - expected}"
        )
    checksums = {}
    for line in (root / "checksums.txt").read_text().splitlines():
        digest, name = line.split()
        if name in checksums:
            raise ValueError(f"duplicate checksum for {name}")
        checksums[name] = digest
    if set(checksums) != expected:
        raise ValueError("checksums must cover exactly the release asset matrix")
    for name, digest in checksums.items():
        if sha256(root / name) != digest:
            raise ValueError(f"checksum mismatch for {name}")
    return checksums


def create(root, version, commit):
    version_value(version)
    if not COMMIT.fullmatch(commit):
        raise ValueError("source commit must be a full lowercase Git object ID")
    checksums = verify_assets(root, version)
    return {
        "schema_version": 1,
        "product": "dcb",
        "version": version,
        "tag": version,
        "source": {"repository": REPOSITORY, "commit": commit},
        "package": PACKAGE,
        "publish": PUBLISH,
        "artifacts": [
            dict(
                kind=kind,
                format=fmt,
                os=osname,
                arch=arch,
                filename=name,
                sha256=checksums[name],
                url=f"https://github.com/{REPOSITORY}/releases/download/{version}/{name}",
            )
            for kind, fmt, osname, arch, name in assets(version)
            if osname == "linux"
        ],
    }


def verify_handoff(path, commit, expected_version=None):
    handoff = json.loads(Path(path).read_text())
    version = version_value(handoff["version"])
    if (
        handoff["tag"] != version
        or handoff["product"] != "dcb"
        or handoff["source"] != {"repository": REPOSITORY, "commit": commit}
        or not COMMIT.fullmatch(commit)
        or (expected_version is not None and version != expected_version)
    ):
        raise ValueError("retained handoff does not match the release identity")
    return version
