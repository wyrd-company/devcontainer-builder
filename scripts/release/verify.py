#!/usr/bin/env python3
# relationships:
#   references: [manifest, stage]
"""Inspect produced packages and run the native launcher from local npm tarballs."""

import argparse
import json
import platform
import subprocess
import struct
import tarfile
import tempfile
from pathlib import Path

import manifest


def verify(dist, version):
    handoff = json.loads((dist / "release-manifest.json").read_text())
    assert handoff == manifest.create(dist, version, handoff["source"]["commit"])
    for _, fmt, osname, arch, name in manifest.assets(version):
        artifact = dist / name
        if fmt == "tar.gz":
            with tarfile.open(artifact) as stream:
                names = stream.getnames()
                assert all(
                    names.count(entry) == 1 for entry in ["dcb", "LICENSE", "README.md"]
                )
                binary = stream.extractfile("dcb").read(32)
                assert binary[:4] == (
                    b"\x7fELF" if osname == "linux" else b"\xcf\xfa\xed\xfe"
                ), name
                machine = struct.unpack_from(
                    "<H" if osname == "linux" else "<I",
                    binary,
                    18 if osname == "linux" else 4,
                )[0]
                assert (
                    machine
                    == (
                        {"amd64": 62, "arm64": 183}
                        if osname == "linux"
                        else {"amd64": 0x01000007, "arm64": 0x0100000C}
                    )[arch]
                ), name
        elif fmt == "deb":
            fields = subprocess.check_output(
                ["dpkg-deb", "-f", str(artifact), "Package", "Version", "Architecture"],
                text=True,
            )
            assert (
                fields == f"Package: dcb\nVersion: {version}\nArchitecture: {arch}\n"
            ), fields
            listing = subprocess.check_output(
                ["dpkg-deb", "-c", str(artifact)], text=True
            )
            assert "./usr/bin/dcb" in listing
        else:
            fields = subprocess.check_output(
                ["rpm", "-qp", "--qf", "%{NAME}\n%{VERSION}\n%{ARCH}\n", str(artifact)],
                text=True,
            )
            suffix = "x86_64" if arch == "amd64" else "aarch64"
            assert fields == f"dcb\n{version}\n{suffix}\n", fields
            listing = subprocess.check_output(["rpm", "-qpl", str(artifact)], text=True)
            assert "/usr/bin/dcb" in listing
    packs = dist / "npm/tarballs"
    archives = sorted(packs.glob("*.tgz"))
    assert len(archives) == 5, archives
    for archive in archives:
        with tarfile.open(archive) as stream:
            meta = json.load(stream.extractfile("package/package.json"))
            assert meta["version"] == version
            assert "package/LICENSE" in stream.getnames()
            assert "package/README.md" in stream.getnames()
            if meta["name"] == "@wyrd-company/dcb":
                assert meta["bin"] == {"dcb": "bin/dcb.js"}
                assert meta["optionalDependencies"] == {
                    f"@wyrd-company/dcb-{osname}-{nodearch}": version
                    for osname, _, _, nodearch in manifest.TARGETS
                }
            else:
                assert stream.getmember("package/bin/dcb").mode & 0o111
    osname = platform.system().lower()
    nodearch = {"x86_64": "x64", "aarch64": "arm64", "arm64": "arm64"}[
        platform.machine()
    ]
    with tempfile.TemporaryDirectory(prefix="dcb-npm-smoke-") as directory:
        root = Path(directory)
        (root / "package.json").write_text(
            '{"name":"example-consumer","version":"1.0.0","private":true}'
        )
        subprocess.run(
            [
                "npm",
                "install",
                "--offline",
                "--ignore-scripts",
                "--no-audit",
                "--no-fund",
                "--omit=optional",
                "--userconfig=/dev/null",
                "--cache",
                str(root / "cache"),
                str((packs / f"wyrd-company-dcb-{version}.tgz").resolve()),
                str(
                    (
                        packs / f"wyrd-company-dcb-{osname}-{nodearch}-{version}.tgz"
                    ).resolve()
                ),
            ],
            cwd=root,
            check=True,
        )
        output = subprocess.check_output(
            [str(root / "node_modules/.bin/dcb"), "version"], text=True
        )
        assert json.loads(output)["version"] == version, output
    print(
        "Verified four archives, four Linux packages, five npm tarballs and offline native npm launcher."
    )


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dist", type=Path, default=Path("dist"))
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    verify(args.dist, args.version)
