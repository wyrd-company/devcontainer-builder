#!/usr/bin/env python3
# relationships:
#   validates: [goreleaser, stage, manifest]
"""Compare the actual published files from two local snapshot builds."""

import os
import subprocess
from pathlib import Path

from manifest import TARGETS, assets, sha256, version_value


def digests(version):
    paths = [Path("dist") / name for *_, name in assets(version)]
    paths += [
        Path("dist/checksums.txt"),
        Path("dist/release-manifest.json"),
        Path("dist/dcb.rb"),
    ]
    names = [f"dcb-{osname}-{nodearch}" for osname, _, _, nodearch in TARGETS] + ["dcb"]
    paths += [
        Path("dist/npm/tarballs") / f"wyrd-company-{name}-{version}.tgz"
        for name in names
    ]
    return {str(path): sha256(path) for path in paths}


def verify():
    version = version_value(os.environ["RELEASE_VERSION"])
    subprocess.run(["task", "release:snapshot"], check=True)
    first = digests(version)
    subprocess.run(["task", "release:snapshot"], check=True)
    second = digests(version)
    different = [name for name in first if first[name] != second[name]]
    if different:
        raise ValueError(
            "snapshot rebuild changed published bytes: " + ", ".join(different)
        )
    print(
        f"Two snapshots match for {len(first)} release assets, formula and npm tarballs."
    )


if __name__ == "__main__":
    verify()
